// Package proc checks whether a pid is still the same live process it used
// to be, guarding against the OS recycling pids.
package proc

// AliveAt reports whether pid is alive and was started at start. start == 0
// degrades to a bare liveness check.
func AliveAt(pid int, start int64) bool {
	if !Alive(pid) {
		return false
	}
	if start == 0 {
		return true
	}
	got, err := StartTime(pid)
	if err != nil {
		return true // cannot disprove liveness from a failed lookup
	}
	return got == start
}
