// Package bridge implements the host side of the dwe host bridge: a daemon
// that listens on the project's `.dwe/bridge` transports (unix socket plus
// loopback/gateway TCP), authenticates in-container shim connections, and
// forks `dwe <argv>` per connection, pumping stdio frames between the shim
// and the subprocess.
//
// The daemon is a stateless forwarder: no in-memory cache, no business
// logic, no project model. Policy enforcement happens inside the forked dwe
// via the daemon-controlled DWE_INVOKED_FROM=container; the daemon itself
// only authenticates, validates the HELLO, and proxies bytes.
package bridge

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// Environment variables of the bridge env contract (design D7). They are
// host-controlled: the daemon strips any client-sent value and force-sets
// its own, so a container can never spoof them. The canonical definitions
// live in shared/bridgeclient (the leaf both core/ and cli/ can import);
// these aliases keep the daemon-side call sites reading naturally.
const (
	// EnvInvokedFrom marks a dwe process as bridge-forked; the CLI command
	// policy keys off InvokedFromContainer.
	EnvInvokedFrom = bridgeclient.EnvInvokedFrom
	// InvokedFromContainer is the EnvInvokedFrom value set by the daemon.
	InvokedFromContainer = bridgeclient.InvokedFromContainer
	// EnvNonInteractive forces the existing non-interactive contract (as in
	// CI) on every bridged invocation — the bridge never allocates a pty.
	EnvNonInteractive = bridgeclient.EnvNonInteractive
)

// BindOverrideEnv lists override TCP bind addresses for exotic setups
// (design D3); comma- or whitespace-separated. The caller (the `dwe bridge
// daemon` command) reads it into Config.BindOverride.
const BindOverrideEnv = "DWE_BRIDGE_BIND"

// defaultGrace is the SIGTERM → SIGKILL window after the shim connection is
// lost (container shutdown mid-command, design D5).
const defaultGrace = 5 * time.Second

// Config configures a Daemon. ProjectRoot is required; everything else has
// production defaults and exists as a seam for tests.
type Config struct {
	// ProjectRoot is the absolute project root (`--project-root`). HELLO
	// cwds must resolve inside it (realpath containment, design D5).
	ProjectRoot string
	// BridgeDir overrides the runtime directory; empty means
	// DefaultBridgeDir(ProjectRoot).
	BridgeDir string
	// ExecPath is the dwe binary forked per connection; empty means the
	// daemon's own executable (resolved lazily, so tests that inject Launch
	// never touch os.Executable — see the test-recursion hazard note there).
	ExecPath string
	// Launch overrides subprocess creation; nil means the production
	// os/exec-backed launcher.
	Launch LaunchFunc
	// GatewayIP resolves the docker bridge gateway IP for the best-effort
	// extra TCP bind needed on native Linux (design D3); nil or a failing
	// resolver skips that bind silently.
	GatewayIP func() (string, error)
	// BindOverride replaces the default bind address set (127.0.0.1 +
	// gateway) when non-empty; see BindOverrideEnv.
	BindOverride string
	// Grace overrides the SIGTERM → SIGKILL window; 0 means defaultGrace.
	Grace time.Duration
	// Logf receives daemon diagnostics (wired to daemon.log by the CLI
	// entry); nil discards them.
	Logf func(format string, args ...any)
}

// Daemon is the host-side bridge daemon. Single-instance-per-project is the
// caller's responsibility (the ensure flock on daemon.pid) — the daemon
// itself assumes it owns the bridge dir.
type Daemon struct {
	cfg       Config
	root      string // realpath'd ProjectRoot, containment baseline
	bridgeDir string
	token     string
	port      int

	mu        sync.Mutex
	started   bool
	closed    bool
	listeners []net.Listener

	accepts  sync.WaitGroup
	sessions sync.WaitGroup
}

// New returns an unstarted Daemon for cfg.
func New(cfg Config) *Daemon {
	return &Daemon{cfg: cfg}
}

// DefaultBridgeDir returns the bridge runtime directory for a project root
// (design D8 on-disk layout).
func DefaultBridgeDir(projectRoot string) string {
	return filepath.Join(projectRoot, ".dwe", "bridge")
}

// Start binds all listeners, writes the port file (after bind — design D3),
// creates the token file if absent, and begins accepting connections.
func (d *Daemon) Start() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		return errors.New("bridge: daemon already started")
	}

	if !filepath.IsAbs(d.cfg.ProjectRoot) {
		return fmt.Errorf("bridge: project root must be absolute, got %q", d.cfg.ProjectRoot)
	}
	root, err := filepath.EvalSymlinks(d.cfg.ProjectRoot)
	if err != nil {
		return fmt.Errorf("bridge: resolving project root: %w", err)
	}
	d.root = root

	d.bridgeDir = d.cfg.BridgeDir
	if d.bridgeDir == "" {
		d.bridgeDir = DefaultBridgeDir(d.cfg.ProjectRoot)
	}
	if err := os.MkdirAll(d.bridgeDir, bridgeDirPerm); err != nil {
		return fmt.Errorf("bridge: creating bridge dir: %w", err)
	}

	// The token survives daemon restarts — it is the stable project
	// identity read by already-running containers (design D6).
	token, err := ensureTokenFile(d.TokenPath())
	if err != nil {
		return err
	}
	d.token = token

	unixLn, err := d.openUnixListener()
	if err != nil {
		return err
	}
	tcpLns, port, err := d.openTCPListeners()
	if err != nil {
		_ = unixLn.Close()
		return err
	}
	d.port = port

	// Write the port file atomically (temp + rename): the shim reads it
	// concurrently during the daemon restart window, and a non-atomic
	// truncate-then-write could expose an empty or partial file. A parse
	// error is not os.ErrNotExist, so the shim's readPortToken would NOT
	// retry it — atomicity keeps the reader from ever seeing a partial port.
	if err := writePortFileAtomic(d.PortPath(), port); err != nil {
		_ = unixLn.Close()
		for _, ln := range tcpLns {
			_ = ln.Close()
		}
		return fmt.Errorf("bridge: writing port file: %w", err)
	}

	d.listeners = append([]net.Listener{unixLn}, tcpLns...)
	for _, ln := range d.listeners {
		d.accepts.Go(func() { d.acceptLoop(ln) })
	}
	d.started = true
	return nil
}

// Close stops accepting, waits for in-flight sessions, and removes the
// transport endpoint files. The token file is kept (stable project
// identity, design D6). Safe to call more than once.
func (d *Daemon) Close() error {
	d.mu.Lock()
	if d.closed || !d.started {
		d.closed = true
		d.mu.Unlock()
		return nil
	}
	d.closed = true
	listeners := d.listeners
	d.mu.Unlock()

	for _, ln := range listeners {
		_ = ln.Close()
	}
	d.accepts.Wait()
	d.sessions.Wait()
	_ = os.Remove(d.SocketPath())
	_ = os.Remove(d.PortPath())
	return nil
}

// Port returns the bound TCP port (valid after Start).
func (d *Daemon) Port() int { return d.port }

// BridgeDir returns the resolved bridge runtime directory (valid after Start).
func (d *Daemon) BridgeDir() string { return d.bridgeDir }

// SocketPath returns the unix transport socket path.
func (d *Daemon) SocketPath() string { return SocketPath(d.bridgeDir) }

// PortPath returns the TCP port file path.
func (d *Daemon) PortPath() string { return PortPath(d.bridgeDir) }

// writePortFileAtomic writes the port number to path via a temp file in the
// same directory followed by a rename, so a concurrent shim reader never
// observes a truncated or empty port file (see the call site in Start).
func writePortFileAtomic(path string, port int) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".port-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op once renamed
	if _, err := tmp.WriteString(strconv.Itoa(port) + "\n"); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(portFilePerm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// TokenPath returns the token file path.
func (d *Daemon) TokenPath() string { return filepath.Join(d.bridgeDir, tokenFileName) }

func (d *Daemon) isClosed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

func (d *Daemon) grace() time.Duration {
	if d.cfg.Grace > 0 {
		return d.cfg.Grace
	}
	return defaultGrace
}

func (d *Daemon) logf(format string, args ...any) {
	if d.cfg.Logf != nil {
		d.cfg.Logf(format, args...)
	}
}

// acceptLoop serves one listener until it is closed. Accept errors other
// than closure are logged and end the loop — the auto-stop machinery (or
// the next ensure) recovers the daemon rather than spinning here.
func (d *Daemon) acceptLoop(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				d.logf("bridge: accept on %s: %v", ln.Addr(), err)
			}
			return
		}
		d.sessions.Go(func() { d.handleConn(conn) })
	}
}
