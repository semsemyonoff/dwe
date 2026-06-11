//go:build darwin

package bridgeproto

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID reads the peer uid via LOCAL_PEERCRED.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("bridgeproto: raw conn: %w", err)
	}
	var (
		cred    *unix.Xucred
		credErr error
	)
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("bridgeproto: reading peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("bridgeproto: LOCAL_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
