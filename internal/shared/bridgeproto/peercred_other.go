//go:build !linux && !darwin

package bridgeproto

import (
	"errors"
	"net"
)

// peerUID is unavailable on this platform; peercred auth fails closed.
func peerUID(_ *net.UnixConn) (uint32, error) {
	return 0, errors.New("bridgeproto: peer credentials unsupported on this platform")
}
