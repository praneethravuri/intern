package store

import "time"

// Message kinds.
const (
	KindNote     = "note"
	KindHandoff  = "handoff"
	KindQuestion = "question"
	KindAnswer   = "answer"
)

// Agent is a registered participant on the bus, uniquely identified by
// (Workspace, Name).
type Agent struct {
	Workspace string
	Name      string
	Harness   string
	SessionID string
	Cwd       string
	PID       int
	PIDStart  int64
	Dropped   int
	LastKind  string
	LastNote  string

	RegisteredAt time.Time
	LastSeen     time.Time
}

// Address returns the canonical "name@workspace" form.
func (a Agent) Address() string { return a.Name + "@" + a.Workspace }

// AgentKey identifies one agent row by its natural key. Used by DeleteAgents
// so a caller (the daemon's periodic sweep) can name a batch of rows to
// remove without round-tripping full Agent values.
type AgentKey struct {
	Workspace, Name string
}

// Message is a single piece of mail. Messages are never deleted by reads;
// DeliveredAt records the first time the recipient saw it and AckedAt records
// when the recipient explicitly retired it.
type Message struct {
	ID       string
	ThreadID string
	ReplyTo  string

	FromName string
	FromWS   string
	ToName   string
	ToWS     string

	Kind string
	Body string

	CreatedAt   time.Time
	DeliveredAt *time.Time
	AckedAt     *time.Time
}

// From returns the sender as "name@workspace".
func (m Message) From() string { return m.FromName + "@" + m.FromWS }

// To returns the recipient as "name@workspace".
func (m Message) To() string { return m.ToName + "@" + m.ToWS }

// ValidKind reports whether k is a known message kind.
func ValidKind(k string) bool {
	switch k {
	case KindNote, KindHandoff, KindQuestion, KindAnswer:
		return true
	}
	return false
}

// fromMS converts Unix milliseconds back to a local time.Time.
func fromMS(ms int64) time.Time { return time.UnixMilli(ms) }

// msPtr converts a nullable millisecond column into a *time.Time.
func msPtr(n int64, valid bool) *time.Time {
	if !valid {
		return nil
	}
	t := fromMS(n)
	return &t
}
