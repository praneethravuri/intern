package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// closed reports whether ch is already closed, without blocking.
func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestWaiters_NotifyWakesEveryCurrentWaiter(t *testing.T) {
	w := NewWaiters()

	const n = 8
	chans := make([]<-chan struct{}, n)
	for i := range chans {
		chans[i] = w.Wait("a@ws")
	}

	for i, ch := range chans {
		if closed(ch) {
			t.Fatalf("waiter %d woke before Notify", i)
		}
	}

	w.Notify("a@ws")

	for i, ch := range chans {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatalf("waiter %d did not wake", i)
		}
	}

	for range chans {
		w.Release("a@ws")
	}
	if got := w.Len(); got != 0 {
		t.Fatalf("Len after all releases = %d, want 0", got)
	}
}

func TestWaiters_WaiterAfterNotifyBlocks(t *testing.T) {
	w := NewWaiters()

	first := w.Wait("a@ws")
	w.Notify("a@ws")
	if !closed(first) {
		t.Fatal("first waiter did not wake")
	}

	// A caller arriving after the broadcast must get the fresh channel and
	// block, otherwise every subsequent wait would return instantly forever.
	second := w.Wait("a@ws")
	if closed(second) {
		t.Fatal("waiter registered after Notify did not block")
	}
	if first == second {
		t.Fatal("Notify did not install a fresh channel")
	}

	w.Release("a@ws")
	w.Release("a@ws")
}

func TestWaiters_NotifyWithoutWaitersIsNoop(t *testing.T) {
	w := NewWaiters()

	// A busy daemon notifies far more often than anyone waits; that must not
	// allocate a permanent map entry per address.
	for i := 0; i < 1000; i++ {
		w.Notify(fmt.Sprintf("agent-%d@ws", i))
	}
	if got := w.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}

	// And a waiter arriving afterwards still blocks.
	if closed(w.Wait("agent-0@ws")) {
		t.Fatal("waiter saw a stale wakeup")
	}
	w.Release("agent-0@ws")
}

func TestWaiters_MapDoesNotGrowUnbounded(t *testing.T) {
	w := NewWaiters()

	for i := 0; i < 5000; i++ {
		addr := fmt.Sprintf("agent-%d@ws", i)
		ch := w.Wait(addr)
		w.Notify(addr)
		if !closed(ch) {
			t.Fatalf("%s did not wake", addr)
		}
		w.Release(addr)
		if got := w.Len(); got != 0 {
			t.Fatalf("iteration %d: Len = %d, want 0", i, got)
		}
	}
}

func TestWaiters_ReleaseIsCountedNotAbsolute(t *testing.T) {
	w := NewWaiters()

	a := w.Wait("a@ws")
	b := w.Wait("a@ws")
	if a != b {
		t.Fatal("two waiters on one address must share a channel")
	}

	w.Release("a@ws")
	if got := w.Len(); got != 1 {
		t.Fatalf("Len after one of two releases = %d, want 1", got)
	}
	if closed(b) {
		t.Fatal("remaining waiter was woken by a Release")
	}

	w.Release("a@ws")
	if got := w.Len(); got != 0 {
		t.Fatalf("Len after both releases = %d, want 0", got)
	}

	// Extra releases must not panic or corrupt the count.
	w.Release("a@ws")
	w.Release("nobody@ws")
}

func TestWaiters_Count(t *testing.T) {
	w := NewWaiters()

	if got := w.Count("a@ws"); got != 0 {
		t.Fatalf("Count on an address nobody is waiting on = %d, want 0", got)
	}

	w.Wait("a@ws")
	w.Wait("a@ws")
	if got := w.Count("a@ws"); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	// A different address is unaffected.
	if got := w.Count("b@ws"); got != 0 {
		t.Fatalf("Count(b@ws) = %d, want 0", got)
	}

	w.Release("a@ws")
	if got := w.Count("a@ws"); got != 1 {
		t.Fatalf("Count after one release = %d, want 1", got)
	}
	w.Release("a@ws")
	if got := w.Count("a@ws"); got != 0 {
		t.Fatalf("Count after last release = %d, want 0", got)
	}
}

func TestWaiters_ZeroValueUsable(t *testing.T) {
	var w Waiters

	w.Notify("a@ws") // must not panic on a nil map
	w.Release("a@ws")

	ch := w.Wait("a@ws")
	w.Notify("a@ws")
	if !closed(ch) {
		t.Fatal("zero-value Waiters did not broadcast")
	}
	w.Release("a@ws")
}

// TestWaiters_HundredConcurrentWaitersOnOneAddressAllWake is a stronger form
// of TestWaiters_NotifyWakesEveryCurrentWaiter: a hundred goroutines, not
// eight, all parked on the SAME address, subscribing concurrently rather
// than sequentially before Notify runs, run under -race so any data race
// in the mutex-guarded map or the broadcast channel swap would show up.
func TestWaiters_HundredConcurrentWaitersOnOneAddressAllWake(t *testing.T) {
	w := NewWaiters()
	const n = 100
	const addr = "hot@ws"

	var ready sync.WaitGroup
	ready.Add(n)
	done := make(chan struct{}, n)

	for i := 0; i < n; i++ {
		go func() {
			ch := w.Wait(addr)
			ready.Done()
			<-ch
			w.Release(addr)
			done <- struct{}{}
		}()
	}

	ready.Wait()
	w.Notify(addr)

	for i := 0; i < n; i++ {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d waiters woke", i, n)
		}
	}
	if got := w.Len(); got != 0 {
		t.Fatalf("Len after all %d releases = %d, want 0", n, got)
	}
}

func TestWaiters_ConcurrentWaitNotifyRelease(t *testing.T) {
	w := NewWaiters()

	const (
		addrs   = 16
		waiters = 8
		rounds  = 50
	)

	var notifiers, parked sync.WaitGroup
	stop := make(chan struct{})
	starved := make(chan string, 1)

	// Notifiers hammer every address until the waiters are done.
	for i := 0; i < 4; i++ {
		notifiers.Add(1)
		go func() {
			defer notifiers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for a := 0; a < addrs; a++ {
					w.Notify(fmt.Sprintf("agent-%d@ws", a))
				}
			}
		}()
	}

	for a := 0; a < addrs; a++ {
		for k := 0; k < waiters; k++ {
			parked.Add(1)
			go func(addr string) {
				defer parked.Done()
				for r := 0; r < rounds; r++ {
					ch := w.Wait(addr)
					select {
					case <-ch:
					case <-time.After(10 * time.Second):
						select {
						case starved <- addr:
						default:
						}
						w.Release(addr)
						return
					}
					w.Release(addr)
				}
			}(fmt.Sprintf("agent-%d@ws", a))
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		parked.Wait()
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("waiters did not finish")
	}
	close(stop)
	notifiers.Wait()

	select {
	case addr := <-starved:
		t.Fatalf("waiter on %s never woke", addr)
	default:
	}

	if got := w.Len(); got != 0 {
		t.Fatalf("Len after all waiters left = %d, want 0", got)
	}
}
