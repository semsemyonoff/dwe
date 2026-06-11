//go:build linux

package bridgeproto

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID reads the peer uid via SO_PEERCRED.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("bridgeproto: raw conn: %w", err)
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("bridgeproto: reading peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("bridgeproto: SO_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
