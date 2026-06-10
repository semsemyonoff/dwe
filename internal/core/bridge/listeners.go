package bridge

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeproto"
)

// Bridge-dir file names and modes (design D8 on-disk layout). The shim reads
// the same names through the read-only container mount —
// internal/shared/bridgeclient keeps its own copies to stay a leaf.
const (
	sockFileName  = "host.sock"
	portFileName  = "port"
	tokenFileName = "token"

	bridgeDirPerm = 0o700
	sockPerm      = 0o660
	portFilePerm  = 0o644 // not a secret, unlike the 0600 token
)

// ParseBindOverride splits a BindOverrideEnv value into bind addresses:
// comma- or whitespace-separated, empty entries dropped. An empty result
// means "no override" — the default 127.0.0.1 + docker gateway set applies.
func ParseBindOverride(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
}

// ensureTokenFile reads the project token, generating and persisting a fresh
// one (mode 0600) when the file is absent or empty.
func ensureTokenFile(path string) (string, error) {
	token, err := bridgeproto.ReadTokenFile(path)
	if err == nil && token != "" {
		return token, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	token, err = bridgeproto.GenerateToken()
	if err != nil {
		return "", err
	}
	if err := bridgeproto.WriteTokenFile(path, token); err != nil {
		return "", err
	}
	return token, nil
}

// openUnixListener binds host.sock (socket mode 0660 inside the 0700 bridge
// dir — design D3). A leftover socket file from a dead daemon is removed
// first: single-instance is guaranteed by the caller's flock on daemon.pid,
// so an existing file can only be stale.
func (d *Daemon) openUnixListener() (net.Listener, error) {
	sock := d.SocketPath()
	if err := os.Remove(sock); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("bridge: removing stale socket: %w", err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return nil, fmt.Errorf("bridge: binding unix socket: %w", err)
	}
	if err := os.Chmod(sock, sockPerm); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("bridge: setting socket mode: %w", err)
	}
	return ln, nil
}

// openTCPListeners binds the TCP transport per design D3: the first address
// gets an ephemeral OS-assigned port (collisions cannot exist), every other
// address is a best-effort bind on that same port — needed for the docker
// gateway IP on native Linux, silently skipped where the interface does not
// exist (macOS). Never 0.0.0.0.
func (d *Daemon) openTCPListeners() ([]net.Listener, int, error) {
	addrs := ParseBindOverride(d.cfg.BindOverride)
	if len(addrs) == 0 {
		addrs = []string{"127.0.0.1"}
		if d.cfg.GatewayIP != nil {
			gw, err := d.cfg.GatewayIP()
			switch {
			case err != nil:
				d.logf("bridge: resolving docker gateway IP: %v", err)
			case gw != "":
				addrs = append(addrs, gw)
			}
		}
	}

	primary, err := net.Listen("tcp", net.JoinHostPort(addrs[0], "0"))
	if err != nil {
		return nil, 0, fmt.Errorf("bridge: binding tcp on %s: %w", addrs[0], err)
	}
	port := primary.Addr().(*net.TCPAddr).Port

	listeners := []net.Listener{primary}
	for _, addr := range addrs[1:] {
		ln, err := net.Listen("tcp", net.JoinHostPort(addr, strconv.Itoa(port)))
		if err != nil {
			d.logf("bridge: skipping extra bind on %s:%d: %v", addr, port, err)
			continue
		}
		listeners = append(listeners, ln)
	}
	return listeners, port, nil
}
