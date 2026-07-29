//go:build windows

package proc

import "syscall"

// StartTime returns pid's creation time as a Windows FILETIME (100ns
// intervals since 1601-01-01).
func StartTime(pid int) (int64, error) {
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, err
	}
	defer func() { _ = syscall.CloseHandle(h) }()

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, err
	}
	return int64(creation.HighDateTime)<<32 | int64(creation.LowDateTime), nil
}
