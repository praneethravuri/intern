package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/praneethravuri/tether/internal/proc"
	"github.com/praneethravuri/tether/internal/store"
	"github.com/praneethravuri/tether/pkg/protocol"
)

// Defaults for Config. Anything non-positive in a caller-supplied Config falls
// back to these, so a zero Config is a working Config.
const (
	defaultStaleAfter      = 5 * time.Minute
	defaultDeadAfter       = 24 * time.Hour
	defaultRetainMessages  = 7 * 24 * time.Hour
	defaultSweepInterval   = 5 * time.Minute
	defaultShutdownTimeout = 5 * time.Second
	defaultRequestTimeout  = 30 * time.Second
	defaultMaxRequestBytes = 1 << 20 // 1 MiB
	defaultWaitTimeout     = 60 * time.Second

	// maxWaitPerRequest bounds one held-open wait round trip; WaitResult.Capped
	// tells the CLI to transparently re-issue wait past this internal ceiling.
	maxWaitPerRequest = 5 * time.Minute

	defaultInboxLimit = 50
	maxInboxLimit     = 500
	maxClientMsgLen   = 256
)

// errRequestTooLarge is returned by limitedReader once a single request has
// exceeded Config.MaxRequestBytes.
var errRequestTooLarge = errors.New("request too large")

// Config tunes the daemon. The zero value is valid and yields the defaults.
type Config struct {
	// StaleAfter is how long an agent may go without a heartbeat before its
	// name can be claimed by a different session.
	StaleAfter time.Duration
	// DeadAfter is how old an unacked message may get before the sweeper
	// retires it.
	DeadAfter time.Duration
	// RetainMessages is how long read or dead mail stays in the database
	// before the sweeper deletes it outright, so the file plateaus instead
	// of growing forever. Separate from DeadAfter: unread mail is marked
	// dead first, then deleted once it's also past this window.
	RetainMessages time.Duration
	// SweepInterval is how often the sweeper runs.
	SweepInterval time.Duration
	// ShutdownTimeout bounds how long Serve waits for in-flight connections.
	ShutdownTimeout time.Duration
	// RequestTimeout bounds a single non-wait request. Wait is exempt: it is
	// a long poll by design.
	RequestTimeout time.Duration
	// MaxRequestBytes is the largest single request accepted on a connection.
	MaxRequestBytes int64
	// DefaultWait is the wait timeout used when the caller does not ask for
	// one; MaxWait caps what a caller may ask for.
	DefaultWait time.Duration
	MaxWait     time.Duration
	// Logger receives daemon logs. Nil means log.Default().
	Logger *log.Logger
}

// DefaultConfig returns the production configuration.
func DefaultConfig() Config {
	return Config{
		StaleAfter:      defaultStaleAfter,
		DeadAfter:       defaultDeadAfter,
		RetainMessages:  defaultRetainMessages,
		SweepInterval:   defaultSweepInterval,
		ShutdownTimeout: defaultShutdownTimeout,
		RequestTimeout:  defaultRequestTimeout,
		MaxRequestBytes: defaultMaxRequestBytes,
		DefaultWait:     defaultWaitTimeout,
		MaxWait:         maxWaitPerRequest,
	}
}

// withDefaults fills in every non-positive field.
func (c Config) withDefaults() Config {
	d := DefaultConfig()
	if c.StaleAfter <= 0 {
		c.StaleAfter = d.StaleAfter
	}
	if c.DeadAfter <= 0 {
		c.DeadAfter = d.DeadAfter
	}
	if c.RetainMessages <= 0 {
		c.RetainMessages = d.RetainMessages
	}
	if c.SweepInterval <= 0 {
		c.SweepInterval = d.SweepInterval
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = d.ShutdownTimeout
	}
	if c.RequestTimeout <= 0 {
		c.RequestTimeout = d.RequestTimeout
	}
	if c.MaxRequestBytes <= 0 {
		c.MaxRequestBytes = d.MaxRequestBytes
	}
	if c.DefaultWait <= 0 {
		c.DefaultWait = d.DefaultWait
	}
	if c.MaxWait <= 0 {
		c.MaxWait = d.MaxWait
	}
	if c.DefaultWait > c.MaxWait {
		c.DefaultWait = c.MaxWait
	}
	if c.Logger == nil {
		c.Logger = log.Default()
	}
	return c
}

// Server serves the tether protocol over a unix socket, one goroutine per
// reusable connection. Panics are recovered per request; internal failures
// are logged in full but reported to clients as an opaque reference.
type Server struct {
	store   *store.Store
	waiters *Waiters
	cfg     Config
	log     *log.Logger

	// wg tracks live connection goroutines.
	wg sync.WaitGroup

	// mu guards conns/closed. Tracking connections is what lets shutdown
	// unblock handlers parked in Decode or in a long wait.
	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool

	// errSeq numbers internal-error references so a user-visible 500 can be
	// matched to a log line without leaking the underlying error text.
	errSeq atomic.Uint64
}

// NewServer builds a Server. st must be non-nil and stays owned by the caller:
// Serve never closes it.
func NewServer(st *store.Store, cfg Config) *Server {
	c := cfg.withDefaults()
	return &Server{
		store:   st,
		waiters: NewWaiters(),
		cfg:     c,
		log:     c.Logger,
		conns:   make(map[net.Conn]struct{}),
	}
}

// Config returns the effective configuration, defaults applied.
func (s *Server) Config() Config { return s.cfg }

// Serve accepts connections until ctx is cancelled or the listener fails. On
// cancellation it closes the listener and every connection, waits up to
// ShutdownTimeout, then returns once all goroutines have stopped.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if ln == nil {
		return errors.New("tether: nil listener")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var bg sync.WaitGroup

	bg.Add(1)
	go func() {
		defer bg.Done()
		s.sweepLoop(ctx)
	}()

	// Closing ln unblocks Accept; closing every conn unblocks parked Decode calls.
	stopped := make(chan struct{})
	bg.Add(1)
	go func() {
		defer bg.Done()
		select {
		case <-ctx.Done():
		case <-stopped:
		}
		_ = ln.Close()
		s.closeConns()
	}()

	err := s.acceptLoop(ctx, ln)

	close(stopped)
	cancel()
	s.awaitConns()
	bg.Wait()
	return err
}

// acceptLoop runs until ctx is cancelled or the listener stops working.
func (s *Server) acceptLoop(ctx context.Context, ln net.Listener) error {
	const maxConsecutiveErrs = 5

	fails := 0
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil // orderly shutdown
			}
			fails++
			s.log.Printf("accept failed (%d/%d): %v", fails, maxConsecutiveErrs, err)
			if fails >= maxConsecutiveErrs {
				return fmt.Errorf("accept: %w", err)
			}
			select { // back off so a wedged listener can't spin a core
			case <-ctx.Done():
				return nil
			case <-time.After(time.Duration(fails) * 50 * time.Millisecond):
			}
			continue
		}
		fails = 0

		if !s.trackConn(conn) {
			_ = conn.Close() // shutting down
			continue
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

// awaitConns waits for connection goroutines, bounded by ShutdownTimeout so a
// wedged client can never hang the daemon's exit.
func (s *Server) awaitConns() {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	t := time.NewTimer(s.cfg.ShutdownTimeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		s.log.Printf("shutdown timeout: abandoning connections still in flight")
	}
}

// sweepLoop retires unacked mail, old observations, and dead agent rows that
// nobody ever came back for. It sweeps once immediately, before the first
// tick, so a daemon that restarts often still prunes something.
func (s *Server) sweepLoop(ctx context.Context) {
	s.sweepOnce(ctx)

	t := time.NewTicker(s.cfg.SweepInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepOnce(ctx)
		}
	}
}

func (s *Server) sweepOnce(ctx context.Context) {
	if n, err := s.store.SweepDead(ctx, s.cfg.DeadAfter); err != nil {
		if ctx.Err() == nil {
			s.log.Printf("sweep dead messages failed: %v", err)
		}
	} else if n > 0 {
		s.log.Printf("swept %d dead message(s)", n)
	}

	if n, err := s.store.PurgeMessages(ctx, s.cfg.RetainMessages); err != nil {
		if ctx.Err() == nil {
			s.log.Printf("purge retired messages failed: %v", err)
		}
	} else if n > 0 {
		s.log.Printf("purged %d message(s)", n)
	}

	if n, err := s.store.SweepObservations(ctx, s.cfg.DeadAfter); err != nil {
		if ctx.Err() == nil {
			s.log.Printf("sweep observations failed: %v", err)
		}
	} else if n > 0 {
		s.log.Printf("swept %d observation(s)", n)
	}

	s.sweepDeadAgents(ctx)
}

// sweepDeadAgents deletes agent rows both stale past DeadAfter and provably
// dead by pid, replacing the old explicit unregister RPC.
func (s *Server) sweepDeadAgents(ctx context.Context) {
	agents, err := s.store.ListAgents(ctx, "", 0) // every agent; cutoff applied below
	if err != nil {
		if ctx.Err() == nil {
			s.log.Printf("sweep dead agents: list: %v", err)
		}
		return
	}

	cutoff := time.Now().Add(-s.cfg.DeadAfter)
	var dead []store.AgentKey
	for _, a := range agents {
		if a.LastSeen.After(cutoff) {
			continue
		}
		if proc.AliveAt(a.PID, a.PIDStart) {
			continue
		}
		dead = append(dead, store.AgentKey{Workspace: a.Workspace, Name: a.Name})
	}
	if len(dead) == 0 {
		return
	}

	n, err := s.store.DeleteAgents(ctx, dead)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Printf("sweep dead agents: delete: %v", err)
		}
		return
	}
	if n > 0 {
		s.log.Printf("swept %d dead agent(s)", n)
	}
}

func (s *Server) trackConn(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if s.conns == nil {
		s.conns = make(map[net.Conn]struct{})
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrackConn(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

func (s *Server) closeConns() {
	s.mu.Lock()
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.closed = true
	s.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

// handleConn serves one connection until EOF, an unrecoverable protocol
// error, or shutdown. No read deadline: wait is a long poll; shutdown closes
// the connection instead.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("panic in connection handler: %v\n%s", r, debug.Stack())
		}
		s.untrackConn(conn)
		_ = conn.Close()
	}()

	// Peer pid is advisory logging only, not authentication (the socket
	// directory is); losing it must never lose the connection.
	pid, err := getPeerPID(conn)
	if err != nil {
		s.log.Printf("peer pid unavailable, continuing without it: %v", err)
		pid = 0
	}
	s.log.Printf("connection open (pid %d)", pid)
	defer s.log.Printf("connection closed (pid %d)", pid)

	lr := &limitedReader{r: conn}
	dec := json.NewDecoder(lr)
	enc := json.NewEncoder(conn)

	for {
		lr.reset(s.cfg.MaxRequestBytes) // budget is per request, not per connection

		var req protocol.Request
		if err := dec.Decode(&req); err != nil {
			s.reportDecodeError(conn, enc, err, pid)
			return
		}

		resp := s.dispatch(ctx, req, pid)
		if err := enc.Encode(resp); err != nil {
			if !isDisconnect(err) {
				s.log.Printf("write to pid %d failed: %v", pid, err)
			}
			return
		}
	}
}

// reportDecodeError answers a failed Decode as best it can and always ends the
// connection: a JSON stream cannot be resynchronised after a framing error.
func (s *Server) reportDecodeError(conn net.Conn, enc *json.Encoder, err error, pid int) {
	switch {
	case errors.Is(err, io.EOF), isDisconnect(err):
		return
	case errors.Is(err, errRequestTooLarge):
		s.log.Printf("request from pid %d exceeded %d bytes", pid, s.cfg.MaxRequestBytes)
		_ = enc.Encode(protocol.Fail("", protocol.CodeTooLarge, "request too large"))
		drainPeer(conn)
	default:
		s.log.Printf("malformed request from pid %d: %v", pid, err)
		_ = enc.Encode(protocol.Fail("", protocol.CodeBadRequest, "malformed request"))
	}
}

// drainPeer discards leftover bytes from a rejected oversized request so the
// peer (blocked mid-write on a small AF_UNIX buffer) can still read our error
// response instead of getting EPIPE.
func drainPeer(conn net.Conn) {
	const drainCap = 64 << 10
	_ = conn.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	_, _ = io.CopyN(io.Discard, conn, drainCap)
}

// isDisconnect reports whether err is an ordinary peer hangup rather than a
// fault worth logging.
func isDisconnect(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET)
}

// dispatch routes one request. It never panics: a handler panic becomes a 500.
func (s *Server) dispatch(ctx context.Context, req protocol.Request, pid int) (resp protocol.Response) {
	defer func() {
		if r := recover(); r != nil {
			ref := s.nextRef()
			s.log.Printf("panic handling %q [%s]: %v\n%s", clip(req.Method), ref, r, debug.Stack())
			resp = protocol.Fail(req.ID, protocol.CodeInternal, "internal error (ref "+ref+")")
		}
	}()

	// Wait is a long poll and must not inherit the per-request deadline.
	if req.Method != protocol.MethodWait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.RequestTimeout)
		defer cancel()
	}

	switch req.Method {
	case protocol.MethodRegister:
		return s.handleRegister(ctx, req, pid)
	case protocol.MethodSend:
		return s.handleSend(ctx, req)
	case protocol.MethodInbox:
		return s.handleInbox(ctx, req)
	case protocol.MethodWait:
		return s.handleWait(ctx, req)
	case protocol.MethodLs:
		return s.handleLs(ctx, req)
	case protocol.MethodExplain:
		return s.handleExplain(ctx, req)
	case protocol.MethodStats:
		return s.handleStats(ctx, req)
	default:
		return protocol.Fail(req.ID, protocol.CodeBadRequest, "unknown method: "+clip(req.Method))
	}
}

// -- handlers ---------------------------------------------------------------

// handleRegister claims or refreshes a workspace/name for the caller.
func (s *Server) handleRegister(ctx context.Context, req protocol.Request, peerPID int) protocol.Response {
	var p protocol.RegisterParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "register")
	}

	ws, err := requireWorkspace(p.Workspace)
	if err != nil {
		return s.fail(req.ID, err, "register")
	}

	harness := stripControl(strings.TrimSpace(p.Harness))
	if harness == "" {
		harness = "unknown"
	}
	session := stripControl(strings.TrimSpace(p.SessionID))

	// A session that already holds a name in ws: resolving an empty Name
	// falls back to it, and an explicit different Name renames it in place.
	var existingName string
	if session != "" {
		if n, findErr := s.store.FindNameBySession(ctx, ws, session); findErr == nil {
			existingName = n
		} else if !errors.Is(findErr, store.ErrNoSuchAgent) {
			return s.fail(req.ID, findErr, "register")
		}
	}

	name := strings.TrimSpace(p.Name)
	switch {
	case name == "" && existingName != "":
		name = existingName
	case name == "":
		name, err = s.mintFreeName(ctx, ws, harness, session)
		if err != nil {
			return s.fail(req.ID, err, "register")
		}
	default:
		name, err = requireName(name)
		if err != nil {
			return s.fail(req.ID, err, "register")
		}
	}

	// Peer pid is a fallback for when the client didn't supply a session pid.
	sessionPID := p.PID
	if sessionPID <= 0 {
		sessionPID = peerPID
	}

	if sessionPID > 0 && !proc.Alive(sessionPID) {
		return s.fail(req.ID, badRequest("session pid %d is not alive", sessionPID), "register")
	}
	var pidStart int64
	if sessionPID > 0 {
		pidStart, _ = proc.StartTime(sessionPID) // 0 on failure is proc.AliveAt's "unknown"
	}

	a := store.Agent{
		Workspace: ws,
		Name:      name,
		Harness:   harness,
		SessionID: session,
		Cwd:       stripControl(p.Cwd),
		PID:       sessionPID,
		PIDStart:  pidStart,
	}

	var created, renamed bool
	if existingName != "" && existingName != name {
		if _, err := s.store.Rename(ctx, a); err != nil {
			if errors.Is(err, store.ErrNameTaken) {
				err = s.withNameSuggestion(ctx, err, ws, name)
			}
			return s.fail(req.ID, err, "register")
		}
		renamed = true
	} else {
		created, err = s.registerOrReclaim(ctx, a)
		if err != nil {
			if errors.Is(err, store.ErrNameTaken) {
				err = s.withNameSuggestion(ctx, err, ws, name)
			}
			return s.fail(req.ID, err, "register")
		}
	}

	s.touch(ctx, ws, name, "register", harness)
	s.log.Printf("registered %s (harness=%s pid=%d created=%v renamed=%v)",
		a.Address(), harness, sessionPID, created, renamed)
	return protocol.OK(req.ID, protocol.RegisterResult{
		Address: a.Address(),
		Name:    name,
		Harness: harness,
		Created: created,
		Renamed: renamed,
	})
}

// mintFreeName synthesises a name from harness+session and finds a free
// variant, for a register with no chosen name and no existing registration
// to resolve to.
func (s *Server) mintFreeName(ctx context.Context, ws, harness, session string) (string, error) {
	base := mintName(harness, session)
	if free, err := s.isNameFree(ctx, ws, base); err != nil {
		return "", err
	} else if free {
		return base, nil
	}
	if alt := s.firstFreeSuffixed(ctx, ws, base); alt != "" {
		return alt, nil
	}
	return "", fmt.Errorf("could not find a free name derived from %s", base)
}

// withNameSuggestion enriches a name-conflict error with a free alternative
// (target-2, target-3, ...), so a rejected register has something concrete
// to try next.
func (s *Server) withNameSuggestion(ctx context.Context, err error, ws, target string) error {
	if alt := s.firstFreeSuffixed(ctx, ws, target); alt != "" {
		return fmt.Errorf("%w — try %s@%s", err, alt, ws)
	}
	return err
}

// isNameFree reports whether no agent in ws is named name.
func (s *Server) isNameFree(ctx context.Context, ws, name string) (bool, error) {
	if _, err := s.store.GetAgent(ctx, ws, name); errors.Is(err, store.ErrNoSuchAgent) {
		return true, nil
	} else if err != nil {
		return false, err
	}
	return false, nil
}

// firstFreeSuffixed returns base-2, base-3, ... up to a small bound --
// whichever names no agent in ws yet -- or "" if none of them do.
func (s *Server) firstFreeSuffixed(ctx context.Context, ws, base string) string {
	for i := 2; i <= 20; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if free, err := s.isNameFree(ctx, ws, candidate); err == nil && free {
			return candidate
		}
	}
	return ""
}

// registerOrReclaim performs the guarded register, and when the name is held
// by a provably dead session, retries immediately with a forced stale cutoff
// instead of waiting out StaleAfter.
//
// The "did this row already exist" check is a separate read before the
// guarded upsert, so Created can race and misreport under a same-instant
// collision; it is cosmetic (only affects what the CLI prints), so this is
// an accepted ceiling — the claim itself stays a single guarded statement.
func (s *Server) registerOrReclaim(ctx context.Context, a store.Agent) (created bool, err error) {
	existedBefore := true
	if _, getErr := s.store.GetAgent(ctx, a.Workspace, a.Name); errors.Is(getErr, store.ErrNoSuchAgent) {
		existedBefore = false
	}

	staleCutoff := time.Now().Add(-s.cfg.StaleAfter)
	err = s.store.Register(ctx, a, staleCutoff)
	if err == nil {
		return !existedBefore, nil
	}
	if !errors.Is(err, store.ErrNameTaken) {
		return false, err
	}

	incumbent, getErr := s.store.GetAgent(ctx, a.Workspace, a.Name)
	if getErr != nil {
		return false, err // could not confirm the incumbent; report the original conflict
	}
	if proc.AliveAt(incumbent.PID, incumbent.PIDStart) {
		return false, err // still alive: today's behaviour, a genuine conflict
	}

	forceCutoff := time.Now().Add(time.Millisecond) // incumbent is provably dead
	if err := s.store.Register(ctx, a, forceCutoff); err != nil {
		return false, err
	}
	return false, nil // reclaiming an existing row, not creating one
}

// authenticate checks that session is allowed to act as ws/name: a missing
// agent or an empty stored/claimed session passes; a mismatch between two
// non-empty sessions means someone else's session holds the name.
//
// Same-uid forgery is still possible (any local process can read another
// session's id); the 0700 socket directory is the real trust boundary, not
// this comparison.
func (s *Server) authenticate(ctx context.Context, ws, name, session string) error {
	a, err := s.store.GetAgent(ctx, ws, name)
	if err != nil {
		if errors.Is(err, store.ErrNoSuchAgent) {
			return nil
		}
		return err
	}
	if a.SessionID == "" || session == "" || a.SessionID == session {
		return nil
	}
	return fmt.Errorf("%w: acting as %s but a different session holds that name",
		store.ErrNameTaken, addr(name, ws))
}

// addr renders the canonical "name@workspace" form from separate strings.
func addr(name, ws string) string { return name + "@" + ws }

// touch records an observation and refreshes last_seen; errors are logged,
// not propagated, since the handler's real work already succeeded.
func (s *Server) touch(ctx context.Context, ws, name, kind, detail string) {
	if err := s.store.Observe(ctx, store.Observation{
		Workspace: ws, Name: name, Kind: kind, Detail: detail,
	}); err != nil {
		s.log.Printf("touch: observe %s@%s: %v", name, ws, err)
	}
	if _, err := s.store.Heartbeat(ctx, ws, name); err != nil {
		s.log.Printf("touch: refresh last_seen %s@%s: %v", name, ws, err)
	}
}

// broadcastStar and broadcastAll mean "every other agent in the workspace".
// Both exist because a bare "*" glob-expands in the caller's shell; requireName
// refuses to let a real agent register under either.
const (
	broadcastStar = "*"
	broadcastAll  = "all"
)

func (s *Server) handleSend(ctx context.Context, req protocol.Request) protocol.Response {
	var p protocol.SendParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "send")
	}

	fromName, fromWS, err := requireAddress(p.FromName, p.FromWorkspace)
	if err != nil {
		return s.fail(req.ID, err, "send")
	}

	// Authenticate the sender, not the recipient: the recipient only receives.
	if err := s.authenticate(ctx, fromWS, fromName, p.FromSession); err != nil {
		return s.fail(req.ID, err, "send")
	}

	kind := strings.TrimSpace(p.Kind)
	if kind == "" {
		kind = store.KindNote
	}
	if !store.ValidKind(kind) {
		return protocol.Fail(req.ID, protocol.CodeBadRequest, "unknown kind: "+clip(kind))
	}
	replyTo := strings.TrimSpace(p.ReplyTo)

	toWS, err := requireWorkspace(p.ToWorkspace)
	if err != nil {
		return s.fail(req.ID, err, "send")
	}

	toNameRaw := strings.TrimSpace(p.ToName)
	if toNameRaw == broadcastStar || toNameRaw == broadcastAll {
		return s.handleBroadcastSend(ctx, req, fromName, fromWS, toWS, kind, p.Body, replyTo)
	}

	toName, err := requireName(p.ToName)
	if err != nil {
		return s.fail(req.ID, err, "send")
	}

	id, err := s.sendOne(ctx, fromName, fromWS, toName, toWS, kind, p.Body, replyTo)
	if err != nil {
		if errors.Is(err, store.ErrNoSuchAgent) {
			err = s.withSuggestion(ctx, err, toWS, toName)
		}
		return s.fail(req.ID, err, "send")
	}

	s.touch(ctx, fromWS, fromName, "send", addr(toName, toWS))
	s.log.Printf("send %s -> %s (%s, id=%s)", addr(fromName, fromWS), addr(toName, toWS), kind, id)
	return protocol.OK(req.ID, protocol.SendResult{MessageID: id})
}

// sendOne delivers one message and wakes any waiter parked on the recipient;
// broadcast is a loop over this, not a second implementation.
func (s *Server) sendOne(ctx context.Context, fromName, fromWS, toName, toWS, kind, body, replyTo string) (string, error) {
	m := store.Message{
		ReplyTo:  replyTo,
		FromName: fromName,
		FromWS:   fromWS,
		ToName:   toName,
		ToWS:     toWS,
		Kind:     kind,
		Body:     body,
	}
	id, err := s.store.Send(ctx, m)
	if err != nil {
		return "", err
	}
	s.waiters.Notify(m.To()) // only after a durable commit
	return id, nil
}

// handleBroadcastSend delivers to every other agent registered in toWS,
// dropped by name+workspace comparison. Zero recipients is success, not an error.
func (s *Server) handleBroadcastSend(ctx context.Context, req protocol.Request, fromName, fromWS, toWS, kind, body, replyTo string) protocol.Response {
	agents, err := s.store.ListAgents(ctx, toWS, s.cfg.StaleAfter)
	if err != nil {
		return s.fail(req.ID, err, "send")
	}

	addrs := make([]string, 0, len(agents))
	for _, a := range agents {
		if a.Name == fromName && a.Workspace == fromWS {
			continue // never deliver to the sender
		}
		if _, err := s.sendOne(ctx, fromName, fromWS, a.Name, toWS, kind, body, replyTo); err != nil {
			s.log.Printf("broadcast send %s@%s -> %s@%s failed: %v", fromName, fromWS, a.Name, toWS, err)
			continue
		}
		addrs = append(addrs, addr(a.Name, toWS))
	}

	s.touch(ctx, fromWS, fromName, "send", addr(broadcastStar, toWS))
	s.log.Printf("broadcast send %s@%s -> */%s (%s, delivered=%d)", fromName, fromWS, toWS, kind, len(addrs))
	return protocol.OK(req.ID, protocol.SendResult{Recipients: addrs, Delivered: len(addrs)})
}

// withSuggestion enriches a "no such agent" error with a did-you-mean hint.
func (s *Server) withSuggestion(ctx context.Context, err error, ws, target string) error {
	agents, listErr := s.store.ListAgents(ctx, ws, 0)
	if listErr != nil || len(agents) == 0 {
		return err
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	if hint := suggest(target, names); hint != "" {
		return fmt.Errorf("%w — did you mean %s@%s?", err, hint, ws)
	}
	return err
}

// handleInbox serves Replay, Peek, and the default drain over one method.
func (s *Server) handleInbox(ctx context.Context, req protocol.Request) protocol.Response {
	var p protocol.InboxParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "inbox")
	}
	name, ws, err := requireAddress(p.Name, p.Workspace)
	if err != nil {
		return s.fail(req.ID, err, "inbox")
	}
	if err := s.authenticate(ctx, ws, name, p.Session); err != nil {
		return s.fail(req.ID, err, "inbox")
	}
	limit, err := clampLimit(p.Limit)
	if err != nil {
		return s.fail(req.ID, err, "inbox")
	}

	var (
		msgs    []store.Message
		cleared int
		dropped int
	)
	switch {
	case p.Replay:
		msgs, err = s.store.Replay(ctx, ws, name, limit)
	case p.Peek:
		msgs, err = s.store.Inbox(ctx, ws, name, limit)
	default:
		msgs, dropped, err = s.store.Drain(ctx, ws, name, limit)
		cleared = len(msgs)
	}
	if err != nil {
		return s.fail(req.ID, err, "inbox")
	}

	pending, err := s.store.PendingCount(ctx, ws, name)
	if err != nil {
		return s.fail(req.ID, err, "inbox")
	}

	views := make([]protocol.MessageView, 0, len(msgs))
	for _, m := range msgs {
		views = append(views, messageView(m))
	}
	s.touch(ctx, ws, name, "inbox", "")
	return protocol.OK(req.ID, protocol.InboxResult{
		Messages: views,
		Cleared:  cleared,
		Pending:  pending,
		Dropped:  dropped,
	})
}

// handleWait subscribes before counting pending mail, closing the race where
// a send commits between the two.
func (s *Server) handleWait(ctx context.Context, req protocol.Request) protocol.Response {
	var p protocol.WaitParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "wait")
	}
	name, ws, err := requireAddress(p.Name, p.Workspace)
	if err != nil {
		return s.fail(req.ID, err, "wait")
	}
	if err := s.authenticate(ctx, ws, name, p.Session); err != nil {
		return s.fail(req.ID, err, "wait")
	}
	s.touch(ctx, ws, name, "wait", "")

	requested := s.cfg.DefaultWait
	if p.TimeoutMS > 0 {
		requested = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	// capped marks a timeout bounded by the daemon's own ceiling, not a real
	// "nothing arrived" answer; the CLI re-issues wait when it sees this.
	timeout := requested
	capped := false
	if timeout > s.cfg.MaxWait {
		timeout = s.cfg.MaxWait
		capped = true
	}

	address := addr(name, ws)
	ch := s.waiters.Wait(address)
	defer s.waiters.Release(address)

	n, err := s.store.PendingCount(ctx, ws, name)
	if err != nil {
		return s.fail(req.ID, err, "wait")
	}
	if n > 0 {
		return protocol.OK(req.ID, protocol.WaitResult{Pending: n, TimedOut: false})
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ch:
		n, err = s.store.PendingCount(ctx, ws, name) // re-read; another connection may have acked it
		if err != nil {
			return s.fail(req.ID, err, "wait")
		}
		return protocol.OK(req.ID, protocol.WaitResult{Pending: n, TimedOut: false})
	case <-timer.C:
		return protocol.OK(req.ID, protocol.WaitResult{Pending: 0, TimedOut: true, Capped: capped})
	case <-ctx.Done():
		return protocol.OK(req.ID, protocol.WaitResult{Pending: 0, TimedOut: true, Capped: capped})
	}
}

// handleLs lists agents plus their computed state. ws is used exactly as
// sent; --all and --workspace compose rather than one overriding the other.
func (s *Server) handleLs(ctx context.Context, req protocol.Request) protocol.Response {
	var p protocol.WhoParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "who")
	}

	ws := strings.TrimSpace(p.Workspace)

	agents, err := s.store.ListAgents(ctx, ws, s.cfg.StaleAfter)
	if err != nil {
		return s.fail(req.ID, err, "who")
	}

	obs, pending, err := s.fleetSignals(ctx, agents)
	if err != nil {
		return s.fail(req.ID, err, "who")
	}

	now := time.Now()
	views := make([]protocol.AgentView, 0, len(agents))
	for _, a := range agents {
		addr := a.Address()
		blocked := s.waiters.Count(addr) > 0
		sr := computeState(a, obs[addr], blocked, now)
		views = append(views, agentView(a, sr, pending[addr]))
	}
	return protocol.OK(req.ID, protocol.WhoResult{Agents: views})
}

// fleetSignals gathers the latest observation and pending count for agents,
// one query pair per distinct workspace, keyed by full address.
func (s *Server) fleetSignals(ctx context.Context, agents []store.Agent) (map[string]store.Observation, map[string]int, error) {
	obs := make(map[string]store.Observation, len(agents))
	pending := make(map[string]int, len(agents))

	seen := make(map[string]bool)
	for _, a := range agents {
		if seen[a.Workspace] {
			continue
		}
		seen[a.Workspace] = true

		wsObs, err := s.store.LastObservations(ctx, a.Workspace)
		if err != nil {
			return nil, nil, err
		}
		for name, o := range wsObs {
			obs[addr(name, a.Workspace)] = o
		}

		wsPending, err := s.store.PendingByWorkspace(ctx, a.Workspace)
		if err != nil {
			return nil, nil, err
		}
		for name, n := range wsPending {
			pending[addr(name, a.Workspace)] = n
		}
	}
	return obs, pending, nil
}

func (s *Server) handleExplain(ctx context.Context, req protocol.Request) protocol.Response {
	var p protocol.StatusParams
	if err := decodeParams(req, &p); err != nil {
		return s.fail(req.ID, err, "status")
	}
	name, ws, err := requireAddress(p.Name, p.Workspace)
	if err != nil {
		return s.fail(req.ID, err, "status")
	}

	a, err := s.store.GetAgent(ctx, ws, name)
	if err != nil {
		return s.fail(req.ID, err, "status")
	}
	pending, err := s.store.PendingCount(ctx, ws, name)
	if err != nil {
		return s.fail(req.ID, err, "status")
	}
	last, err := s.store.LastObservation(ctx, ws, name)
	if err != nil {
		return s.fail(req.ID, err, "status")
	}
	blocked := s.waiters.Count(a.Address()) > 0
	sr := computeState(a, last, blocked, time.Now())
	return protocol.OK(req.ID, protocol.StatusResult{Agent: agentView(a, sr, pending)})
}

// handleStats reports row counts for doctor's database health line.
func (s *Server) handleStats(ctx context.Context, req protocol.Request) protocol.Response {
	st, err := s.store.Stats(ctx)
	if err != nil {
		return s.fail(req.ID, err, "stats")
	}
	return protocol.OK(req.ID, protocol.StatsResult{
		Messages:     st.Messages,
		Agents:       st.Agents,
		Observations: st.Observations,
	})
}

// -- errors -----------------------------------------------------------------

// fail turns any error into a Response. Store sentinels map to protocol
// codes; anything else is logged in full and reported as an opaque error.
func (s *Server) fail(id string, err error, op string) protocol.Response {
	var pe *protocol.Error
	switch {
	case errors.As(err, &pe):
		return protocol.Fail(id, pe.Code, clip(pe.Message))
	case errors.Is(err, store.ErrNameTaken):
		return protocol.Fail(id, protocol.CodeConflict, publicMessage(err))
	case errors.Is(err, store.ErrNoSuchAgent), errors.Is(err, store.ErrNoSuchMessage):
		return protocol.Fail(id, protocol.CodeNotFound, publicMessage(err))
	case errors.Is(err, store.ErrBodyTooLarge):
		return protocol.Fail(id, protocol.CodeTooLarge, publicMessage(err))
	case errors.Is(err, store.ErrEmptyBody), errors.Is(err, store.ErrBadAddress):
		return protocol.Fail(id, protocol.CodeBadRequest, publicMessage(err))
	default:
		ref := s.nextRef()
		s.log.Printf("%s failed [%s]: %v", op, ref, err)
		return protocol.Fail(id, protocol.CodeInternal, "internal error (ref "+ref+")")
	}
}

// nextRef mints a short reference shared between the log line and the client.
func (s *Server) nextRef() string {
	return fmt.Sprintf("e%04d", s.errSeq.Add(1))
}

// publicMessage renders a sentinel error for a client, stripping the "store: " prefix.
func publicMessage(err error) string {
	return clip(strings.TrimPrefix(err.Error(), "store: "))
}

// stripControl replaces C0 control bytes and DEL with a space, so
// client-controlled metadata can't carry a terminal escape into another
// agent's screen. Deliberately unbounded in length, unlike clip.
func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
}

// clip bounds and sanitises a client-visible string.
func clip(s string) string {
	s = stripControl(s)
	if len(s) > maxClientMsgLen {
		return s[:maxClientMsgLen] + "..."
	}
	return s
}

// hasControlBytes reports whether s contains a C0 control code or DEL.
func hasControlBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// badRequest builds a 400 as an error value so handlers can return it through
// the same fail() path as store errors.
func badRequest(format string, args ...any) error {
	return &protocol.Error{Code: protocol.CodeBadRequest, Message: fmt.Sprintf(format, args...)}
}

// -- request helpers --------------------------------------------------------

// decodeParams decodes req.Params into dst. An absent params block is valid and
// leaves dst at its zero value; the handler's own validation then rejects it.
func decodeParams(req protocol.Request, dst any) error {
	if len(req.Params) == 0 {
		return nil
	}
	if err := json.Unmarshal(req.Params, dst); err != nil {
		return badRequest("invalid params for %s", clip(req.Method))
	}
	return nil
}

// requireAddress validates and normalises a name/workspace pair.
func requireAddress(name, ws string) (string, string, error) {
	name, err := requireName(name)
	if err != nil {
		return "", "", err
	}
	ws, err = requireWorkspace(ws)
	if err != nil {
		return "", "", err
	}
	return name, ws, nil
}

// MaxNameLength keeps a name short enough to stay readable in the ls table
// and in a name@workspace address.
const MaxNameLength = 32

// requireName validates and normalises one agent name: no "@" (ambiguous
// with "name@workspace"), no control bytes, not too long, and "*"/"all" are
// reserved for broadcast addressing.
func requireName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", badRequest("name is required")
	}
	if strings.ContainsRune(name, '@') {
		return "", badRequest("name must not contain '@'")
	}
	if hasControlBytes(name) {
		return "", badRequest("name must not contain control characters")
	}
	if len(name) > MaxNameLength {
		return "", badRequest("name must be at most %d characters", MaxNameLength)
	}
	if name == broadcastStar || name == broadcastAll {
		return "", badRequest("%q is a reserved name and cannot be registered", name)
	}
	return name, nil
}

// requireWorkspace validates and normalises one workspace name.
func requireWorkspace(ws string) (string, error) {
	ws = strings.TrimSpace(ws)
	if ws == "" {
		return "", badRequest("workspace is required")
	}
	if strings.ContainsRune(ws, '@') {
		return "", badRequest("workspace must not contain '@'")
	}
	if hasControlBytes(ws) {
		return "", badRequest("workspace must not contain control characters")
	}
	return ws, nil
}

// clampLimit turns a non-positive limit into the default and rejects
// anything past maxInboxLimit with a 400, rather than silently truncating.
func clampLimit(limit int) (int, error) {
	if limit <= 0 {
		return defaultInboxLimit, nil
	}
	if limit > maxInboxLimit {
		return 0, badRequest("limit %d exceeds the maximum of %d", limit, maxInboxLimit)
	}
	return limit, nil
}

// -- views ------------------------------------------------------------------

// formatTime renders a store time as RFC3339 in UTC. The zero time renders as
// the empty string rather than year 1.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)
	return &s
}

func messageView(m store.Message) protocol.MessageView {
	return protocol.MessageView{
		ID:          m.ID,
		ThreadID:    m.ThreadID,
		ReplyTo:     m.ReplyTo,
		From:        m.From(),
		To:          m.To(),
		Kind:        m.Kind,
		Body:        m.Body,
		CreatedAt:   formatTime(m.CreatedAt),
		DeliveredAt: formatTimePtr(m.DeliveredAt),
		AckedAt:     formatTimePtr(m.AckedAt),
	}
}

// agentView renders one agent plus its freshly computed state for the wire.
func agentView(a store.Agent, sr stateReport, pending int) protocol.AgentView {
	return protocol.AgentView{
		Address:      a.Address(),
		Name:         a.Name,
		Workspace:    a.Workspace,
		Harness:      a.Harness,
		State:        sr.State,
		StateSource:  sr.Source,
		StateAgeMS:   sr.Age.Milliseconds(),
		StateDetail:  sr.Detail,
		Cwd:          a.Cwd,
		PID:          a.PID,
		Pending:      pending,
		Dropped:      a.Dropped,
		RegisteredAt: formatTime(a.RegisteredAt),
		LastSeen:     formatTime(a.LastSeen),
	}
}

// -- bounded reading --------------------------------------------------------

// limitedReader caps bytes per request; resettable since one connection
// carries many requests. Used by one goroutine only, so needs no locking.
type limitedReader struct {
	r io.Reader
	n int64
}

func (l *limitedReader) reset(n int64) { l.n = n }

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n <= 0 {
		return 0, errRequestTooLarge
	}
	if int64(len(p)) > l.n {
		p = p[:l.n]
	}
	n, err := l.r.Read(p)
	l.n -= int64(n)
	return n, err
}
