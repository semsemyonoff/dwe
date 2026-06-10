// Package bridgeclient is the in-container side of the dwe host bridge: it
// selects a transport to the host daemon (unix socket, then TCP fallback —
// design D2), sends the HELLO frame, and pumps stdio/signals until the
// daemon reports the subprocess exit code.
//
// This package is a leaf: it MUST NOT import anything from internal/core or
// internal/cli. The standalone shim binary (cmd/dwe-shim) is a thin main over
// it and stays free of cobra and lipgloss, mirroring internal/shared/prompt.
package bridgeclient

import (
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeproto"
)

// Default tuning. Unix dial is fast-fail (a dead socket inode on Docker
// Desktop refuses instantly); TCP gets more headroom for the Desktop proxy.
const (
	defaultUnixDialTimeout = 300 * time.Millisecond
	defaultTCPDialTimeout  = 2 * time.Second
	defaultRetryDelay      = 100 * time.Millisecond
	defaultTCPHost         = "host.docker.internal"

	// stdinChunkSize bounds a single STDIN frame payload (well under
	// bridgeproto.MaxPayload).
	stdinChunkSize = 32 * 1024
)

// OnUnreachableWarn is the DWE_BRIDGE_UNREACHABLE value that turns an
// unreachable daemon into a warning + exit 0 instead of an error + exit 1
// (design D10). The overlay sets it from `bridge.on_unreachable: warn`.
const OnUnreachableWarn = "warn"

// Options configures one bridged command invocation. The zero value is not
// usable — populate from OptionsFromEnv (production) or directly (tests).
type Options struct {
	// BridgeDir is $DWE_BRIDGE_DIR — the read-only in-container mount of the
	// host's .dwe/bridge directory (host.sock / port / token files).
	BridgeDir string
	// HostWorkspace / ContainerWorkspace drive cwd translation (D7).
	HostWorkspace      string
	ContainerWorkspace string
	// Project is $DWE_BRIDGE_PROJECT, used in diagnostics only.
	Project string
	// Unreachable is $DWE_BRIDGE_UNREACHABLE ("" or OnUnreachableWarn).
	Unreachable string

	// Argv is the dwe argument vector, pass-through and never translated.
	Argv []string
	// Env is the raw environment; Run strips bridge-internal variables (D7).
	Env []string
	// Cwd is the container working directory; Run translates it (D7).
	Cwd string
	// Term carries $TERM for diagnostics.
	Term string

	Stdin  io.Reader // nil → subprocess stdin closed immediately
	Stdout io.Writer
	Stderr io.Writer

	// Signals delivers SIGINT/SIGTERM to forward as SIGNAL frames; nil means
	// no signal forwarding (tests).
	Signals <-chan os.Signal

	// TCPHost overrides the TCP transport host (tests use 127.0.0.1).
	TCPHost string
	// Dial/retry tuning; zero values take the package defaults.
	UnixDialTimeout time.Duration
	TCPDialTimeout  time.Duration
	RetryDelay      time.Duration
}

func (o Options) withDefaults() Options {
	if o.TCPHost == "" {
		o.TCPHost = defaultTCPHost
	}
	if o.UnixDialTimeout == 0 {
		o.UnixDialTimeout = defaultUnixDialTimeout
	}
	if o.TCPDialTimeout == 0 {
		o.TCPDialTimeout = defaultTCPDialTimeout
	}
	if o.RetryDelay == 0 {
		o.RetryDelay = defaultRetryDelay
	}
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	return o
}

// OptionsFromEnv builds Options for the shim binary from the process
// environment and the given argument vector (os.Args[1:]).
func OptionsFromEnv(argv []string, signals <-chan os.Signal) Options {
	cwd, _ := os.Getwd()
	return Options{
		BridgeDir:          os.Getenv("DWE_BRIDGE_DIR"),
		HostWorkspace:      os.Getenv("DWE_HOST_WORKSPACE"),
		ContainerWorkspace: os.Getenv("DWE_CONTAINER_WORKSPACE"),
		Project:            os.Getenv("DWE_BRIDGE_PROJECT"),
		Unreachable:        os.Getenv("DWE_BRIDGE_UNREACHABLE"),
		Argv:               argv,
		Env:                os.Environ(),
		Cwd:                cwd,
		Term:               os.Getenv("TERM"),
		Stdin:              os.Stdin,
		Stdout:             os.Stdout,
		Stderr:             os.Stderr,
		Signals:            signals,
	}
}

// Run executes one bridged command and returns the process exit code: the
// subprocess code from the EXIT frame on success, 1 on any bridge-level
// error, or 0 when the daemon is unreachable and the warn policy is active.
func Run(opts Options) int {
	opts = opts.withDefaults()
	if opts.BridgeDir == "" {
		_, _ = fmt.Fprintln(opts.Stderr,
			"dwe bridge: DWE_BRIDGE_DIR is not set — this shim must run inside a dwe-bridged container")
		return 1
	}

	conn, token, err := dialTransport(opts)
	if err != nil {
		return reportUnreachable(opts)
	}
	defer func() { _ = conn.Close() }()

	hello := bridgeproto.Hello{
		ProtocolVersion: bridgeproto.ProtocolVersion,
		Token:           token, // non-empty only on TCP (unix uses peercred)
		Argv:            opts.Argv,
		Env:             StripEnv(opts.Env),
		Cwd:             TranslateCwd(opts.Cwd, opts.ContainerWorkspace, opts.HostWorkspace),
		TTY:             false, // always — the bridge never allocates a pty (D11)
		Term:            opts.Term,
	}
	payload, err := bridgeproto.EncodeHello(hello)
	if err != nil {
		_, _ = fmt.Fprintf(opts.Stderr, "dwe bridge: %v\n", err)
		return 1
	}
	if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameHello, payload); err != nil {
		return reportUnreachable(opts)
	}

	go pumpStdin(conn, opts.Stdin)
	go pumpSignals(conn, opts.Signals)

	return readLoop(conn, opts)
}

// readLoop consumes daemon frames until EXIT or ERROR. A connection that
// dies before delivering any frame is treated as unreachable: Docker
// Desktop's host proxy can accept the TCP connect and only then discover
// nothing listens on the host side, surfacing as an immediate EOF or reset.
func readLoop(conn net.Conn, opts Options) int {
	gotFrame := false
	for {
		frameType, payload, err := bridgeproto.ReadFrame(conn)
		if err != nil {
			if !gotFrame {
				return reportUnreachable(opts)
			}
			_, _ = fmt.Fprintf(opts.Stderr, "dwe bridge: connection to host daemon lost: %v\n", err)
			return 1
		}
		gotFrame = true
		switch frameType {
		case bridgeproto.FrameStdout:
			_, _ = opts.Stdout.Write(payload)
		case bridgeproto.FrameStderr:
			_, _ = opts.Stderr.Write(payload)
		case bridgeproto.FrameExit:
			code, err := bridgeproto.DecodeExitPayload(payload)
			if err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dwe bridge: %v\n", err)
				return 1
			}
			return int(code)
		case bridgeproto.FrameError:
			info, err := bridgeproto.DecodeError(payload)
			if err != nil {
				_, _ = fmt.Fprintf(opts.Stderr, "dwe bridge: %v\n", err)
				return 1
			}
			_, _ = fmt.Fprintln(opts.Stderr, errorMessage(info))
			return 1
		default:
			// Unknown frame types are skipped for forward compatibility
			// within a protocol version.
		}
	}
}

// pumpStdin forwards local stdin as STDIN frames and signals EOF with a
// STDIN_CLOSE frame. A nil reader closes the subprocess stdin immediately.
func pumpStdin(conn net.Conn, stdin io.Reader) {
	if stdin == nil {
		_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameStdinClose, nil)
		return
	}
	buf := make([]byte, stdinChunkSize)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			if werr := bridgeproto.WriteFrame(conn, bridgeproto.FrameStdin, buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameStdinClose, nil)
			return
		}
	}
}

// pumpSignals forwards SIGINT/SIGTERM as SIGNAL frames so the daemon can
// signal the subprocess process group.
func pumpSignals(conn net.Conn, signals <-chan os.Signal) {
	if signals == nil {
		return
	}
	for sig := range signals {
		num, ok := signalNumber(sig)
		if !ok {
			continue
		}
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameSignal, bridgeproto.EncodeSignalPayload(num)); err != nil {
			return
		}
	}
}

func signalNumber(sig os.Signal) (byte, bool) {
	s, ok := sig.(syscall.Signal)
	if !ok || s <= 0 || s > 255 {
		return 0, false
	}
	return byte(s), true
}

// errorMessage renders a daemon ERROR frame for humans. version_mismatch has
// a fixed remedy (D6): the materialized shims are refreshed by `dwe deploy`.
func errorMessage(info bridgeproto.ErrorInfo) string {
	if info.Code == bridgeproto.ErrCodeVersionMismatch {
		return "dwe bridge: shim outdated, re-run `dwe deploy` to refresh shim binaries"
	}
	msg := info.Message
	if msg == "" {
		msg = info.Code
	}
	return "dwe bridge: " + msg
}

// reportUnreachable applies the D10 unreachable-daemon policy: by default an
// error and exit 1 (a hook must block the commit); with
// DWE_BRIDGE_UNREACHABLE=warn a warning and exit 0.
func reportUnreachable(opts Options) int {
	forProject := ""
	if opts.Project != "" {
		forProject = fmt.Sprintf(" for project %q", opts.Project)
	}
	if strings.TrimSpace(opts.Unreachable) == OnUnreachableWarn {
		_, _ = fmt.Fprintf(opts.Stderr,
			"dwe bridge: warning: host daemon is not running%s; continuing (on_unreachable: warn)\n",
			forProject)
		return 0
	}
	_, _ = fmt.Fprintf(opts.Stderr,
		"dwe bridge: host daemon is not running%s\n            (start the stack on the host: `dwe run`, or `dwe bridge start`)\n",
		forProject)
	return 1
}
