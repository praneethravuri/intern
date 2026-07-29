//go:build darwin

package proc

import "golang.org/x/sys/unix"

// StartTime returns pid's start time in microseconds via the kern.proc.pid sysctl.
func StartTime(pid int) (int64, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	tv := kp.Proc.P_starttime
	return tv.Sec*1_000_000 + int64(tv.Usec), nil
}
