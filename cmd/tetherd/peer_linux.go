//go:build linux

package main

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
)

func getPeerPID(conn net.Conn) (int, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("not a unix socket")
	}

	raw, err := unixConn.SyscallConn()
	if err != nil {
		return 0, err
	}

	var pid int
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if err == nil {
			pid = int(cred.Pid)
		}
	})

	return pid, err
}
