package proc

import (
	"os"
	"testing"
)

func TestAlive_SelfIsAlive(t *testing.T) {
	if !Alive(os.Getpid()) {
		t.Fatal("Alive(self) = false, want true")
	}
}

// 1<<30 exceeds every supported platform's pid_max, so it never flakes against a real pid.
func TestAlive_ImplausiblePIDIsNotAlive(t *testing.T) {
	if Alive(1 << 30) {
		t.Fatal("Alive on an implausible pid = true, want false")
	}
	if Alive(0) || Alive(-1) {
		t.Fatal("Alive accepted a non-positive pid")
	}
}

func TestAliveAt_SelfWithRealStartTime(t *testing.T) {
	start, err := StartTime(os.Getpid())
	if err != nil {
		t.Skipf("StartTime unsupported in this environment: %v", err)
	}
	if !AliveAt(os.Getpid(), start) {
		t.Fatal("AliveAt(self, real start time) = false, want true")
	}
}

func TestAliveAt_WrongStartTimeReadsAsDead(t *testing.T) {
	if _, err := StartTime(os.Getpid()); err != nil {
		t.Skipf("StartTime unsupported in this environment: %v", err)
	}
	if AliveAt(os.Getpid(), 1) {
		t.Fatal("AliveAt(self, wrong start time) = true, want false (recycled-pid protection)")
	}
}

func TestAliveAt_ZeroStartDegradesToBareLiveness(t *testing.T) {
	live := os.Getpid()
	if AliveAt(live, 0) != Alive(live) {
		t.Fatalf("AliveAt(%d, 0) = %v, want it to match Alive(%d) = %v",
			live, AliveAt(live, 0), live, Alive(live))
	}

	const dead = 1 << 30
	if AliveAt(dead, 0) != Alive(dead) {
		t.Fatalf("AliveAt(%d, 0) = %v, want it to match Alive(%d) = %v",
			dead, AliveAt(dead, 0), dead, Alive(dead))
	}
}

func TestStartTimeOfSelf(t *testing.T) {
	start, err := StartTime(os.Getpid())
	if err != nil {
		t.Skipf("StartTime unsupported in this environment: %v", err)
	}
	if start <= 0 {
		t.Fatalf("StartTime(self) = %d, want a positive value", start)
	}

	// Stable: reading it twice for the same still-running process must agree.
	again, err := StartTime(os.Getpid())
	if err != nil {
		t.Fatalf("StartTime(self) failed on the second call: %v", err)
	}
	if again != start {
		t.Fatalf("StartTime(self) = %d then %d, want it stable", start, again)
	}
}

// An implausible pid must fail the OS lookup, not fabricate a start time.
func TestStartTimeOfImplausiblePIDErrors(t *testing.T) {
	if _, err := StartTime(1 << 30); err == nil {
		t.Fatal("StartTime(implausible pid): want error, got nil")
	}
}
