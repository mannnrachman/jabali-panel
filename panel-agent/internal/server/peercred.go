package server

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID extracts the connecting process's UID from a Unix socket
// connection using SO_PEERCRED. Returns an error if the connection is not
// a Unix socket or the syscall fails.
func peerUID(conn net.Conn) (uint32, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peercred: not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peercred: syscall conn: %w", err)
	}

	var cred *unix.Ucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err != nil {
		return 0, fmt.Errorf("peercred: control: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("peercred: getsockopt: %w", credErr)
	}
	return cred.Uid, nil
}
