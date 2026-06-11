package bridgeclient

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeproto"
)

// Bridge-dir file names shared with the daemon (design D8 on-disk layout).
const (
	sockFileName  = "host.sock"
	portFileName  = "port"
	tokenFileName = "token"
)

// portTokenRetries is how many extra read attempts the shim makes when the
// port or token file is momentarily absent (daemon restart window, D2).
const portTokenRetries = 2

// dialTransport implements the D2 transport selection: try the unix socket
// first (native Linux; on Docker Desktop the dead socket inode refuses
// instantly), then fall back to TCP host.docker.internal using the port and
// token files. The returned token is non-empty only on the TCP path — unix
// connections authenticate via peercred and never send one.
func dialTransport(opts Options) (conn net.Conn, token string, err error) {
	sock := filepath.Join(opts.BridgeDir, sockFileName)
	if _, statErr := os.Stat(sock); statErr == nil {
		if c, dialErr := net.DialTimeout("unix", sock, opts.UnixDialTimeout); dialErr == nil {
			return c, "", nil
		}
		// Refused or timed out: fall through to TCP (D2 step 2).
	}

	port, token, err := readPortToken(opts.BridgeDir, opts.RetryDelay)
	if err != nil {
		return nil, "", err
	}
	addr := net.JoinHostPort(opts.TCPHost, strconv.Itoa(port))
	c, err := net.DialTimeout("tcp", addr, opts.TCPDialTimeout)
	if err != nil {
		return nil, "", fmt.Errorf("dialing %s: %w", addr, err)
	}
	return c, token, nil
}

// readPortToken reads the daemon's port and token files, retrying
// portTokenRetries times with retryDelay sleeps when a file does not exist
// yet — the daemon writes them right after bind, so a restart can leave a
// short window where one is missing.
func readPortToken(bridgeDir string, retryDelay time.Duration) (port int, token string, err error) {
	for attempt := 0; ; attempt++ {
		port, token, err = readPortTokenOnce(bridgeDir)
		if err == nil || !errors.Is(err, os.ErrNotExist) || attempt == portTokenRetries {
			return port, token, err
		}
		time.Sleep(retryDelay)
	}
}

func readPortTokenOnce(bridgeDir string) (int, string, error) {
	portRaw, err := os.ReadFile(filepath.Join(bridgeDir, portFileName))
	if err != nil {
		return 0, "", fmt.Errorf("reading port file: %w", err)
	}
	portStr := string(bytes.TrimSpace(portRaw))
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, "", fmt.Errorf("invalid port file content %q", portStr)
	}
	token, err := bridgeproto.ReadTokenFile(filepath.Join(bridgeDir, tokenFileName))
	if err != nil {
		return 0, "", err
	}
	return port, token, nil
}
