package main

import (
	"fmt"
	"sync"
	"time"

	"github.com/praneethravuri/helios/pkg/protocol"
)

// staleAfter is how long an agent can go unseen before "who" stops listing it.
const staleAfter = 30 * time.Minute

type mailboxAgent struct {
	lastSeen time.Time
	inbox    []protocol.MailboxMessage
}

// Mailbox is an in-memory, mutex-guarded agent directory + message store, in the
// same shape as SessionManager -- register/send/inbox/who all take the lock,
// mutate the map, and return.
//
// ponytail: in-memory only, lost on daemon restart (matches how sessions already
// behave). Add persistence once the messaging model itself is validated.
type Mailbox struct {
	mu     sync.Mutex
	agents map[string]*mailboxAgent
}

func NewMailbox() *Mailbox {
	return &Mailbox{agents: make(map[string]*mailboxAgent)}
}

// Register upserts an agent's last-seen time.
func (m *Mailbox) Register(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.agents[name]
	if !ok {
		a = &mailboxAgent{}
		m.agents[name] = a
	}
	a.lastSeen = time.Now()
}

// Send delivers body into to's inbox. If to is "*", it fans out to every
// registered agent except from -- the whole loop guard for v1 (see
// [[Messaging Bus Pivot]] in the vault for the stronger process-ancestry guard
// deferred to v1.x).
func (m *Mailbox) Send(from, to, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	msg := protocol.MailboxMessage{From: from, Body: body, At: time.Now()}

	if to == protocol.BroadcastAll {
		for name, a := range m.agents {
			if name == from {
				continue
			}
			a.inbox = append(a.inbox, msg)
		}
		return nil
	}

	a, ok := m.agents[to]
	if !ok {
		return fmt.Errorf("agent %q is not registered", to)
	}
	a.inbox = append(a.inbox, msg)
	return nil
}

// Inbox returns and clears as's pending messages -- read-once, no separate ack.
func (m *Mailbox) Inbox(as string) ([]protocol.MailboxMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	a, ok := m.agents[as]
	if !ok {
		return nil, fmt.Errorf("agent %q is not registered", as)
	}
	msgs := a.inbox
	a.inbox = nil
	return msgs, nil
}

// Who lists agents seen within staleAfter.
func (m *Mailbox) Who() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := time.Now().Add(-staleAfter)
	names := make([]string, 0, len(m.agents))
	for name, a := range m.agents {
		if a.lastSeen.After(cutoff) {
			names = append(names, name)
		}
	}
	return names
}
