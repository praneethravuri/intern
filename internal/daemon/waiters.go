package daemon

import "sync"

// Waiters is the long-poll registry behind the "wait" method. Zero value is
// usable; every Wait must be paired with one Release, entries are reference
// counted and removed when the last waiter leaves.
type Waiters struct {
	mu sync.Mutex
	m  map[string]*waitEntry
}

// waitEntry is the per-address broadcast channel plus its waiter count. The
// channel is only ever closed (never sent on), so every waiter wakes exactly once.
type waitEntry struct {
	ch chan struct{}
	n  int
}

// NewWaiters returns an empty registry.
func NewWaiters() *Waiters {
	return &Waiters{m: make(map[string]*waitEntry)}
}

// Wait subscribes to mail for addr and returns a channel closed on the next
// Notify(addr). Callers must call Release(addr) when done; subscribe before
// checking for existing mail to avoid missing a message committed in between.
func (w *Waiters) Wait(addr string) <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.m == nil {
		w.m = make(map[string]*waitEntry)
	}
	e := w.m[addr]
	if e == nil {
		e = &waitEntry{ch: make(chan struct{})}
		w.m[addr] = e
	}
	e.n++
	return e.ch
}

// Release drops one subscription for addr; a no-op if none remain.
func (w *Waiters) Release(addr string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	e := w.m[addr]
	if e == nil {
		return
	}
	e.n--
	if e.n <= 0 {
		delete(w.m, addr)
	}
}

// Notify wakes every caller parked on addr and installs a fresh channel.
func (w *Waiters) Notify(addr string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	e := w.m[addr]
	if e == nil {
		return // nobody listening; mail is durable, so a later caller sees it via pending count
	}
	close(e.ch)
	e.ch = make(chan struct{})
}

// Len reports how many addresses currently have at least one waiter. It exists
// for tests and diagnostics; it is not part of the wire protocol.
func (w *Waiters) Len() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.m)
}

// Count reports how many callers are currently parked on addr.
func (w *Waiters) Count(addr string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	e := w.m[addr]
	if e == nil {
		return 0
	}
	return e.n
}
