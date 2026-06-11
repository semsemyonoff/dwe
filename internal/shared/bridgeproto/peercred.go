package bridgeproto

import (
	"net"
	"os"
)

// PeerUID returns the uid of the process on the other end of a unix-socket
// connection (SO_PEERCRED on linux, LOCAL_PEERCRED on darwin). Unsupported
// platforms return an error, so peercred auth fails closed.
func PeerUID(conn *net.UnixConn) (uint32, error) {
	return peerUID(conn)
}

// PeerIsSameUser reports whether the unix-socket peer runs as the same uid as
// the current process — the auth predicate for the bridge unix transport.
// Note: under userns-remap / rootless Docker the peer uid is shifted and this
// predicate fails; those setups use the TCP+token transport (design D12).
func PeerIsSameUser(conn *net.UnixConn) (bool, error) {
	uid, err := peerUID(conn)
	if err != nil {
		return false, err
	}
	return int64(uid) == int64(os.Getuid()), nil
}
