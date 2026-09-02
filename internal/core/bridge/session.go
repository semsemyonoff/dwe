package bridge

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
	"github.com/semsemyonoff/dwe/internal/shared/bridgeproto"
)

// helloTimeout bounds how long an accepted connection may sit without
// delivering its HELLO frame, so half-open connections cannot pile up.
const helloTimeout = 10 * time.Second

// outputChunkSize bounds a single STDOUT/STDERR frame payload (well under
// bridgeproto.MaxPayload).
const outputChunkSize = 32 * 1024

// launchFailureExitCode mirrors the shell's "command not found" convention:
// a subprocess that could not be started still ends the session with an
// ordinary STDERR + EXIT pair, not a protocol ERROR (those are reserved for
// the D4 code set).
const launchFailureExitCode = 127

// handleConn runs one session: auth + HELLO validation (design D5), then the
// subprocess proxy. One connection carries exactly one command.
func (d *Daemon) handleConn(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	if d.isClosed() {
		d.sendError(conn, bridgeproto.ErrCodeDaemonShuttingDown, "bridge daemon is shutting down")
		return
	}
	hello, cwd, ok := d.acceptHello(conn)
	if !ok {
		return
	}
	d.runSession(conn, hello, cwd)
}

// acceptHello reads and validates the HELLO frame, authenticating the
// connection first (token on TCP, peercred on unix — design D5). On any
// failure it sends the corresponding ERROR frame and reports !ok.
func (d *Daemon) acceptHello(conn net.Conn) (bridgeproto.Hello, string, bool) {
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	frameType, payload, err := bridgeproto.ReadFrame(conn)
	if err != nil {
		d.logf("bridge: reading hello: %v", err)
		d.sendError(conn, bridgeproto.ErrCodeBadHello, "could not read HELLO frame")
		return bridgeproto.Hello{}, "", false
	}
	_ = conn.SetReadDeadline(time.Time{})
	if frameType != bridgeproto.FrameHello {
		d.logf("bridge: first frame is %s, want hello", frameType)
		d.sendError(conn, bridgeproto.ErrCodeBadHello,
			fmt.Sprintf("first frame is %s, want hello", frameType))
		return bridgeproto.Hello{}, "", false
	}
	hello, err := bridgeproto.DecodeHello(payload)
	if err != nil {
		d.logf("bridge: %v", err)
		d.sendError(conn, bridgeproto.ErrCodeBadHello, "malformed HELLO payload")
		return bridgeproto.Hello{}, "", false
	}

	if unixConn, isUnix := conn.(*net.UnixConn); isUnix {
		same, err := bridgeproto.PeerIsSameUser(unixConn)
		if err != nil || !same {
			d.sendError(conn, bridgeproto.ErrCodeAuthFailed,
				"unix peer credentials do not match the daemon user")
			return bridgeproto.Hello{}, "", false
		}
	} else if !bridgeproto.TokenEqual(d.token, hello.Token) {
		d.sendError(conn, bridgeproto.ErrCodeAuthFailed,
			"invalid or missing bridge token")
		return bridgeproto.Hello{}, "", false
	}

	if hello.ProtocolVersion != bridgeproto.ProtocolVersion {
		d.sendError(conn, bridgeproto.ErrCodeVersionMismatch,
			fmt.Sprintf("daemon speaks protocol %d, shim sent %d",
				bridgeproto.ProtocolVersion, hello.ProtocolVersion))
		return bridgeproto.Hello{}, "", false
	}

	cwd, note := resolveCwd(hello.Cwd, d.root)
	if note != "" {
		d.logf("bridge: %s; running from project root", note)
	}

	if hello.TTY {
		d.sendError(conn, bridgeproto.ErrCodeTTYUnsupported,
			"the bridge never allocates a pty; run interactive commands on the host")
		return bridgeproto.Hello{}, "", false
	}

	return hello, cwd, true
}

// runSession forks the dwe subprocess through the exec seam and pumps frames
// until it exits (design D5 steps 3–5).
func (d *Daemon) runSession(conn net.Conn, hello bridgeproto.Hello, cwd string) {
	launch := d.cfg.Launch
	if launch == nil {
		launch = launchOS
	}
	proc, err := launch(LaunchSpec{
		ExecPath: d.cfg.ExecPath,
		Argv:     hello.Argv,
		Dir:      cwd,
		Env:      subprocessEnv(hello.Env),
	})
	if err != nil {
		msg := fmt.Sprintf("dwe bridge: starting dwe subprocess: %v\n", err)
		_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameStderr, []byte(msg))
		_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameExit,
			bridgeproto.EncodeExitPayload(launchFailureExitCode))
		return
	}

	// exited closes once the subprocess has been reaped, so the connection
	// reader can tell "our EXIT frame closed the conn" from "the container
	// went away mid-command". The reader joins the sessions group (it ends
	// when handleConn closes the conn), keeping Close deterministic.
	exited := make(chan struct{})
	d.sessions.Go(func() { d.pumpConn(conn, proc, exited) })

	var outputs sync.WaitGroup
	outputs.Add(2)
	go pumpOutput(conn, bridgeproto.FrameStdout, proc.Stdout(), &outputs)
	go pumpOutput(conn, bridgeproto.FrameStderr, proc.Stderr(), &outputs)

	// Wait must come after both pipes hit EOF (os/exec closes them on Wait).
	outputs.Wait()
	code := proc.Wait()
	close(exited)
	_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameExit,
		bridgeproto.EncodeExitPayload(int32(code))) //nolint:gosec // exit codes fit int32
}

// pumpConn consumes client frames: STDIN/STDIN_CLOSE feed the subprocess
// stdin, SIGNAL forwards to the process group. A read error means the shim
// connection is gone — either we closed it after EXIT (exited is closed) or
// the container died mid-command, in which case the subprocess gets SIGTERM,
// the grace window, then SIGKILL (design D5 step 4).
func (d *Daemon) pumpConn(conn net.Conn, proc Process, exited <-chan struct{}) {
	stdin := proc.Stdin()
	stdinOpen := true
	for {
		frameType, payload, err := bridgeproto.ReadFrame(conn)
		if err != nil {
			select {
			case <-exited:
			default:
				d.terminate(proc, exited)
			}
			return
		}
		switch frameType {
		case bridgeproto.FrameStdin:
			if stdinOpen {
				if _, err := stdin.Write(payload); err != nil {
					_ = stdin.Close()
					stdinOpen = false
				}
			}
		case bridgeproto.FrameStdinClose:
			if stdinOpen {
				_ = stdin.Close()
				stdinOpen = false
			}
		case bridgeproto.FrameSignal:
			num, err := bridgeproto.DecodeSignalPayload(payload)
			if err != nil {
				continue
			}
			sig := syscall.Signal(num)
			// V1 forwards only SIGINT/SIGTERM (design D4); anything else
			// from a non-standard client is dropped.
			if sig != syscall.SIGINT && sig != syscall.SIGTERM {
				continue
			}
			_ = proc.Signal(sig)
		default:
			// Unknown frame types are skipped for forward compatibility
			// within a protocol version.
		}
	}
}

// terminate gives the subprocess SIGTERM, then SIGKILL when it outlives the
// grace window. exited unblocks the wait as soon as the main session
// goroutine reaps the process.
func (d *Daemon) terminate(proc Process, exited <-chan struct{}) {
	_ = proc.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(d.grace()):
		_ = proc.Kill()
	}
}

// pumpOutput chunks one subprocess pipe into frames. After a connection
// write fails the pipe is still drained to EOF, so the subprocess never
// blocks on a full pipe and Wait can reap it.
func pumpOutput(conn net.Conn, frameType bridgeproto.FrameType, r io.Reader, wg *sync.WaitGroup) {
	defer wg.Done()
	buf := make([]byte, outputChunkSize)
	connOK := true
	for {
		n, err := r.Read(buf)
		if n > 0 && connOK {
			if bridgeproto.WriteFrame(conn, frameType, buf[:n]) != nil {
				connOK = false
			}
		}
		if err != nil {
			return
		}
	}
}

// sendError emits a protocol ERROR frame; the shim renders it and exits 1.
func (d *Daemon) sendError(conn net.Conn, code, message string) {
	payload, err := bridgeproto.EncodeError(bridgeproto.ErrorInfo{Code: code, Message: message})
	if err != nil {
		d.logf("bridge: %v", err)
		return
	}
	_ = bridgeproto.WriteFrame(conn, bridgeproto.FrameError, payload)
	d.logf("bridge: rejected connection: %s: %s", code, message)
}

// resolveCwd maps the client-reported cwd onto a host working directory for
// the forked dwe. A cwd that resolves inside the project is used as-is; any
// other shape FALLS BACK to the daemon's own project root with a log note
// instead of rejecting the session. The untranslatable shapes are routine —
// a git hook or script that cd'd outside the service's dir/dir_internal
// mapping (e.g. a host-layout `cd ../..` walking out of the container
// mount) sends `/` or a container-only path here — and rejection would
// break every such hook. The fallback cannot be abused to point the host
// dwe at foreign state: the substituted root is the daemon's pinned project
// root, never a client-chosen path, and an in-project cwd is what a
// host-side run from the root would use anyway.
func resolveCwd(cwd, root string) (resolved, note string) {
	if !filepath.IsAbs(cwd) {
		return root, fmt.Sprintf("cwd %q is not an absolute host path", cwd)
	}
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		return root, fmt.Sprintf("cwd %q does not resolve on the host: %v", cwd, err)
	}
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return root, fmt.Sprintf("cwd %q is outside the project root", cwd)
	}
	return resolved, ""
}

// hostControlledEnv are the variables force-set below; client-sent values
// are stripped first so a container can never spoof them (design D7).
var hostControlledEnv = []string{EnvInvokedFrom, EnvNonInteractive}

// dangerousEnvNames are exact-match variables a container could set to hijack
// execution of the host-side dwe or its grandchild processes (docker / git /
// sh, all resolved by bare name). The bridge crosses a container→host trust
// boundary, so these are always dropped daemon-side regardless of what the
// client sent. The dynamic-loader families (LD_*, DYLD_*) are stripped by
// prefix in subprocessEnv. PATH is dropped here and force-replaced with the
// daemon's own PATH so bare-name binary lookups resolve against host
// directories, never container-controlled ones.
var dangerousEnvNames = map[string]struct{}{
	"PATH":      {}, // force-replaced with the daemon's PATH below
	"BASH_ENV":  {}, // sourced by non-interactive bash (sh -c) at startup
	"ENV":       {}, // sourced by POSIX sh at startup
	"SHELLOPTS": {},
	"BASHOPTS":  {},
	"IFS":       {}, // alters shell word splitting of the command payload
}

// dangerousEnvPrefixes match the dynamic-linker variable families
// (LD_PRELOAD, LD_LIBRARY_PATH, LD_AUDIT, DYLD_INSERT_LIBRARIES, …) that let a
// container inject code into any host process the bridged dwe spawns.
var dangerousEnvPrefixes = []string{"LD_", "DYLD_"}

// hostIdentityEnvNames are process-identity and config-resolution variables
// that must reflect the HOST user, not the container. The forked dwe (and the
// docker / git / ssh it spawns) resolves docker contexts (~/.docker/config.json),
// git config, and agent sockets through them — a container HOME silently sends
// the docker CLI to the default unix:///var/run/docker.sock, which does not
// exist on a Docker Desktop / OrbStack mac. Client values are dropped; the
// daemon's own values are appended instead (absent ones stay absent).
//
// DWE_AGE_KEY / DWE_AGE_KEY_FILE belong here for the same reason plus a
// sharper one: DWE_AGE_KEY_FILE names a HOST path the forked dwe opens and
// parses as an age identity, so honoring a client value would let a container
// pick which host file is read. Membership here means the daemon drops the
// client's value AND appends its own, which is what keeps a host that runs
// with an env-only identity (CI) working for bridged `render config` /
// `vars get`. The shim strips both names too (bridgeclient.StripEnv).
var hostIdentityEnvNames = []string{
	"HOME", "USER", "LOGNAME", "TMPDIR", "SSH_AUTH_SOCK",
	"DWE_AGE_KEY", "DWE_AGE_KEY_FILE",
}

// hostIdentityEnvPrefixes are the variable families steering how the host
// talks to docker (DOCKER_HOST, DOCKER_CONFIG, DOCKER_CONTEXT, COMPOSE_* …)
// plus XDG config-dir resolution — host-controlled for the same reason as
// PATH: a container must not choose which docker endpoint or config files the
// host-side dwe uses.
var hostIdentityEnvPrefixes = []string{"DOCKER_", "COMPOSE_", "XDG_"}

// isHostIdentityEnv reports whether name belongs to the host-identity set.
func isHostIdentityEnv(name string) bool {
	return slices.Contains(hostIdentityEnvNames, name) || hasAnyPrefix(name, hostIdentityEnvPrefixes)
}

// daemonEnviron is the daemon's own environment; injectable for tests.
var daemonEnviron = os.Environ

// hostIdentityEnv returns the daemon's own values for the host-identity set,
// in environ order; variables the daemon itself lacks are simply absent.
func hostIdentityEnv() []string {
	var out []string
	for _, kv := range daemonEnviron() {
		if name, _, ok := strings.Cut(kv, "="); ok && isHostIdentityEnv(name) {
			out = append(out, kv)
		}
	}
	return out
}

// hostPath returns the daemon's own PATH so a bridged subprocess resolves
// docker / git / sh against host binaries, never container-controlled
// directories. Falls back to a conservative default if the daemon somehow has
// no PATH in its own environment.
func hostPath() string {
	if p := os.Getenv("PATH"); p != "" {
		return p
	}
	return "/usr/local/bin:/usr/bin:/bin"
}

// subprocessEnv builds the subprocess environment from the HELLO env: the
// shim's strip set is re-applied (defense-in-depth — the daemon does not
// trust the client to have filtered), execution-hijacking variables (loader
// families, shell-startup files, PATH) are dropped, the host-controlled and
// host-identity variables are dropped, and the daemon-owned values (the
// host-identity set, host PATH, and the two host-controlled DWE_* vars) are
// appended.
func subprocessEnv(env []string) []string {
	clean := make([]string, 0, len(env)+len(hostControlledEnv)+1)
	for _, kv := range bridgeclient.StripEnv(env) {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(hostControlledEnv, name) {
			continue
		}
		if _, dangerous := dangerousEnvNames[name]; dangerous {
			continue
		}
		if hasAnyPrefix(name, dangerousEnvPrefixes) {
			continue
		}
		if isHostIdentityEnv(name) {
			continue
		}
		clean = append(clean, kv)
	}
	clean = append(clean, hostIdentityEnv()...)
	return append(clean,
		"PATH="+hostPath(),
		EnvInvokedFrom+"="+InvokedFromContainer,
		EnvNonInteractive+"=1")
}

// hasAnyPrefix reports whether s starts with any of the given prefixes.
func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
