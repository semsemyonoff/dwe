package bridgeclient

import (
	"bytes"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeproto"
)

// shortTempDir returns a temp dir with a short absolute path: unix socket
// paths are capped (104 bytes on darwin) and t.TempDir() embeds the full test
// name, which can push host.sock over the limit.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dwebr")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// serveOnce accepts one connection on ln, decodes the HELLO frame onto the
// returned channel, then hands the connection to script (which plays the
// daemon side and closes by returning).
func serveOnce(t *testing.T, ln net.Listener, script func(t *testing.T, conn net.Conn)) <-chan bridgeproto.Hello {
	t.Helper()
	helloCh := make(chan bridgeproto.Hello, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		frameType, payload, err := bridgeproto.ReadFrame(conn)
		if err != nil {
			t.Errorf("fake daemon: reading first frame: %v", err)
			return
		}
		if frameType != bridgeproto.FrameHello {
			t.Errorf("fake daemon: first frame = %s, want hello", frameType)
			return
		}
		hello, err := bridgeproto.DecodeHello(payload)
		if err != nil {
			t.Errorf("fake daemon: decoding hello: %v", err)
			return
		}
		helloCh <- hello
		if script != nil {
			script(t, conn)
		}
	}()
	return helloCh
}

// startTCPBridge starts a fake daemon on loopback TCP and writes the port and
// token files into bridgeDir, the way the real daemon does after bind.
func startTCPBridge(t *testing.T, bridgeDir string, script func(t *testing.T, conn net.Conn)) (string, <-chan bridgeproto.Hello) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	port := ln.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(bridgeDir, portFileName), []byte(strconv.Itoa(port)+"\n"), 0o600); err != nil {
		t.Fatalf("writing port file: %v", err)
	}
	token, err := bridgeproto.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if err := bridgeproto.WriteTokenFile(filepath.Join(bridgeDir, tokenFileName), token); err != nil {
		t.Fatalf("WriteTokenFile: %v", err)
	}
	return token, serveOnce(t, ln, script)
}

// startUnixBridge starts a fake daemon on bridgeDir/host.sock.
func startUnixBridge(t *testing.T, bridgeDir string, script func(t *testing.T, conn net.Conn)) <-chan bridgeproto.Hello {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(bridgeDir, sockFileName))
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	return serveOnce(t, ln, script)
}

// testOptions returns Options wired for tests: loopback TCP host, fast
// retries, and buffer-backed stdio.
func testOptions(bridgeDir string, stdout, stderr *bytes.Buffer) Options {
	return Options{
		BridgeDir:  bridgeDir,
		Project:    "my-proj",
		Argv:       []string{"commands", "lint"},
		Cwd:        "/anywhere",
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		TCPHost:    "127.0.0.1",
		RetryDelay: time.Millisecond,
	}
}

func exitScript(code int32) func(t *testing.T, conn net.Conn) {
	return func(t *testing.T, conn net.Conn) {
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameExit, bridgeproto.EncodeExitPayload(code)); err != nil {
			t.Errorf("fake daemon: writing exit frame: %v", err)
		}
	}
}

func TestRun_TCPHappyPath(t *testing.T) {
	bridgeDir := shortTempDir(t) // no host.sock → straight to TCP
	var stdout, stderr bytes.Buffer
	token, helloCh := startTCPBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameStdout, []byte("out-data")); err != nil {
			t.Errorf("writing stdout frame: %v", err)
		}
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameStderr, []byte("err-data")); err != nil {
			t.Errorf("writing stderr frame: %v", err)
		}
		exitScript(7)(t, conn)
	})

	opts := testOptions(bridgeDir, &stdout, &stderr)
	opts.HostWorkspace = "/Users/foo/projects/my-proj"
	opts.ContainerWorkspace = "/workspace"
	opts.Cwd = "/workspace/src"
	opts.Env = []string{
		"PATH=/usr/bin",
		"DWE_BRIDGE_DIR=/dwe-bridge",
		"DWE_PROJECT_ROOT=/somewhere",
	}
	opts.Term = "xterm-256color"

	if code := Run(opts); code != 7 {
		t.Errorf("Run = %d, want 7 (stderr: %q)", code, stderr.String())
	}
	if got := stdout.String(); got != "out-data" {
		t.Errorf("stdout = %q, want %q", got, "out-data")
	}
	if got := stderr.String(); got != "err-data" {
		t.Errorf("stderr = %q, want %q", got, "err-data")
	}

	hello := <-helloCh
	if hello.ProtocolVersion != bridgeproto.ProtocolVersion {
		t.Errorf("hello protocol_version = %d, want %d", hello.ProtocolVersion, bridgeproto.ProtocolVersion)
	}
	if hello.Token != token {
		t.Errorf("hello token = %q, want the token file content on TCP", hello.Token)
	}
	if hello.TTY {
		t.Error("hello tty = true, shim must always send tty: false")
	}
	if hello.Cwd != "/Users/foo/projects/my-proj/src" {
		t.Errorf("hello cwd = %q, want translated host path", hello.Cwd)
	}
	if hello.Term != "xterm-256color" {
		t.Errorf("hello term = %q", hello.Term)
	}
	wantEnv := []string{"PATH=/usr/bin"}
	if len(hello.Env) != len(wantEnv) || hello.Env[0] != wantEnv[0] {
		t.Errorf("hello env = %v, want %v (bridge vars stripped)", hello.Env, wantEnv)
	}
	wantArgv := []string{"commands", "lint"}
	if len(hello.Argv) != 2 || hello.Argv[0] != wantArgv[0] || hello.Argv[1] != wantArgv[1] {
		t.Errorf("hello argv = %v, want %v", hello.Argv, wantArgv)
	}
}

func TestRun_UnixHappyPath_NoTokenSent(t *testing.T) {
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	helloCh := startUnixBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameStdout, []byte("via-unix")); err != nil {
			t.Errorf("writing stdout frame: %v", err)
		}
		exitScript(0)(t, conn)
	})
	// A token file exists, but the unix path must never send it (peercred
	// authenticates there).
	if err := bridgeproto.WriteTokenFile(filepath.Join(bridgeDir, tokenFileName), "deadbeef"); err != nil {
		t.Fatalf("WriteTokenFile: %v", err)
	}

	opts := testOptions(bridgeDir, &stdout, &stderr)
	if code := Run(opts); code != 0 {
		t.Errorf("Run = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if got := stdout.String(); got != "via-unix" {
		t.Errorf("stdout = %q, want %q", got, "via-unix")
	}
	hello := <-helloCh
	if hello.Token != "" {
		t.Errorf("hello token = %q, want empty on the unix transport", hello.Token)
	}
}

func TestRun_UnixToTCPFallback(t *testing.T) {
	bridgeDir := shortTempDir(t)

	// Leave a dead host.sock behind, the way Docker Desktop containers see
	// the host's socket inode: present but refusing connections (D1.1).
	deadLn, err := net.Listen("unix", filepath.Join(bridgeDir, sockFileName))
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}
	deadLn.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = deadLn.Close()

	var stdout, stderr bytes.Buffer
	token, helloCh := startTCPBridge(t, bridgeDir, exitScript(3))

	opts := testOptions(bridgeDir, &stdout, &stderr)
	if code := Run(opts); code != 3 {
		t.Errorf("Run = %d, want 3 (stderr: %q)", code, stderr.String())
	}
	hello := <-helloCh
	if hello.Token != token {
		t.Errorf("hello token = %q, want token after TCP fallback", hello.Token)
	}
}

func TestRun_StdinAndSignalForwarding(t *testing.T) {
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	stdinData := make(chan []byte, 1)
	signalNum := make(chan byte, 1)

	_, _ = startTCPBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		var collected []byte
		gotClose, gotSignal := false, false
		for !gotClose || !gotSignal {
			frameType, payload, err := bridgeproto.ReadFrame(conn)
			if err != nil {
				t.Errorf("fake daemon: reading frame: %v", err)
				return
			}
			switch frameType {
			case bridgeproto.FrameStdin:
				collected = append(collected, payload...)
			case bridgeproto.FrameStdinClose:
				gotClose = true
			case bridgeproto.FrameSignal:
				num, err := bridgeproto.DecodeSignalPayload(payload)
				if err != nil {
					t.Errorf("decoding signal payload: %v", err)
					return
				}
				signalNum <- num
				gotSignal = true
			default:
				t.Errorf("fake daemon: unexpected frame %s", frameType)
				return
			}
		}
		stdinData <- collected
		exitScript(130)(t, conn)
	})

	sigs := make(chan os.Signal, 1)
	sigs <- syscall.SIGINT

	opts := testOptions(bridgeDir, &stdout, &stderr)
	opts.Stdin = strings.NewReader("ping")
	opts.Signals = sigs

	if code := Run(opts); code != 130 {
		t.Errorf("Run = %d, want 130 (stderr: %q)", code, stderr.String())
	}
	if got := string(<-stdinData); got != "ping" {
		t.Errorf("daemon received stdin %q, want %q", got, "ping")
	}
	if num := <-signalNum; num != byte(syscall.SIGINT) {
		t.Errorf("daemon received signal %d, want %d (SIGINT)", num, byte(syscall.SIGINT))
	}
}

func TestRun_VersionMismatchMessage(t *testing.T) {
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	_, _ = startTCPBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		payload, err := bridgeproto.EncodeError(bridgeproto.ErrorInfo{
			Code:    bridgeproto.ErrCodeVersionMismatch,
			Message: "daemon speaks protocol 2, shim sent 1",
		})
		if err != nil {
			t.Errorf("encoding error frame: %v", err)
			return
		}
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameError, payload); err != nil {
			t.Errorf("writing error frame: %v", err)
		}
	})

	opts := testOptions(bridgeDir, &stdout, &stderr)
	if code := Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	want := "dwe bridge: shim outdated, re-run `dwe deploy` to refresh shim binaries\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want verbatim %q", got, want)
	}
}

func TestRun_GenericErrorFrame(t *testing.T) {
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	_, _ = startTCPBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		payload, err := bridgeproto.EncodeError(bridgeproto.ErrorInfo{
			Code:    bridgeproto.ErrCodeCwdOutsideProject,
			Message: "cwd /etc is outside the project root",
		})
		if err != nil {
			t.Errorf("encoding error frame: %v", err)
			return
		}
		if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameError, payload); err != nil {
			t.Errorf("writing error frame: %v", err)
		}
	})

	opts := testOptions(bridgeDir, &stdout, &stderr)
	if code := Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	want := "dwe bridge: cwd /etc is outside the project root\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestRun_UnreachableFail(t *testing.T) {
	bridgeDir := shortTempDir(t) // no sock, no port, no token
	var stdout, stderr bytes.Buffer
	opts := testOptions(bridgeDir, &stdout, &stderr)

	if code := Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	want := "dwe bridge: host daemon is not running for project \"my-proj\"\n" +
		"            (start the stack on the host: `dwe run`, or `dwe bridge start`)\n"
	if got := stderr.String(); got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestRun_UnreachableWarn(t *testing.T) {
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	opts := testOptions(bridgeDir, &stdout, &stderr)
	opts.Unreachable = OnUnreachableWarn

	if code := Run(opts); code != 0 {
		t.Errorf("Run = %d, want 0 under on_unreachable: warn", code)
	}
	got := stderr.String()
	if !strings.Contains(got, "warning") || !strings.Contains(got, `for project "my-proj"`) {
		t.Errorf("stderr = %q, want a warning naming the project", got)
	}
}

func TestRun_ConnClosedBeforeAnyFrame_IsUnreachable(t *testing.T) {
	// Docker Desktop's host proxy can accept the TCP connect and then close
	// when nothing listens host-side — must surface as the unreachable
	// policy, not a cryptic connection error.
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer
	_, _ = startTCPBridge(t, bridgeDir, func(t *testing.T, conn net.Conn) {
		// Return immediately: serveOnce closes the connection.
	})

	opts := testOptions(bridgeDir, &stdout, &stderr)
	if code := Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "host daemon is not running") {
		t.Errorf("stderr = %q, want the unreachable diagnostic", got)
	}
}

func TestRun_NoBridgeDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts := testOptions("", &stdout, &stderr)
	opts.BridgeDir = ""

	if code := Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "DWE_BRIDGE_DIR is not set") {
		t.Errorf("stderr = %q, want the misconfiguration diagnostic", got)
	}
}

func TestRun_PortTokenRetry(t *testing.T) {
	// Port/token files appearing between retries (daemon restart window)
	// must be picked up by the 2×RetryDelay re-read.
	bridgeDir := shortTempDir(t)
	var stdout, stderr bytes.Buffer

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	_ = serveOnce(t, ln, exitScript(0))
	port := ln.Addr().(*net.TCPAddr).Port

	opts := testOptions(bridgeDir, &stdout, &stderr)
	opts.RetryDelay = 50 * time.Millisecond

	go func() {
		time.Sleep(20 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(bridgeDir, portFileName), []byte(strconv.Itoa(port)+"\n"), 0o600)
		_ = bridgeproto.WriteTokenFile(filepath.Join(bridgeDir, tokenFileName), "cafe01")
	}()

	if code := Run(opts); code != 0 {
		t.Errorf("Run = %d, want 0 after retry pickup (stderr: %q)", code, stderr.String())
	}
}

func TestTranslateCwd(t *testing.T) {
	tests := []struct {
		name        string
		cwd         string
		containerWS string
		hostWS      string
		want        string
	}{
		{"inside workspace", "/workspace/src/app", "/workspace", "/Users/foo/proj", "/Users/foo/proj/src/app"},
		{"workspace root", "/workspace", "/workspace", "/Users/foo/proj", "/Users/foo/proj"},
		{"outside workspace passes through", "/etc", "/workspace", "/Users/foo/proj", "/etc"},
		{"path-boundary aware", "/workspaces/other", "/workspace", "/Users/foo/proj", "/workspaces/other"},
		{"trailing slash on container ws", "/workspace/src", "/workspace/", "/Users/foo/proj", "/Users/foo/proj/src"},
		{"trailing slash on host ws", "/workspace/src", "/workspace", "/Users/foo/proj/", "/Users/foo/proj/src"},
		{"empty container ws", "/workspace/src", "", "/Users/foo/proj", "/workspace/src"},
		{"empty host ws", "/workspace/src", "/workspace", "", "/workspace/src"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TranslateCwd(tt.cwd, tt.containerWS, tt.hostWS); got != tt.want {
				t.Errorf("TranslateCwd(%q, %q, %q) = %q, want %q",
					tt.cwd, tt.containerWS, tt.hostWS, got, tt.want)
			}
		})
	}
}

func TestStripEnv(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"DWE_BRIDGE_DIR=/dwe-bridge",
		"DWE_HOST_WORKSPACE=/Users/foo/proj",
		"DWE_CONTAINER_WORKSPACE=/workspace",
		"DWE_BRIDGE_PROJECT=my-proj",
		"DWE_BRIDGE_UNREACHABLE=warn",
		// A container that inherited (or forged) the nested-runtime marker
		// must not make the host-side dwe treat a bridged user invocation as
		// nested and drop its container TTY.
		"DWE_NESTED_RUNTIME=1",
		"DWE_PROJECT_ROOT=/elsewhere",
		"DWE_PROJECT_ROOT_OVERRIDE=/elsewhere2",
		"DWE_DEBUG=1",
		"TERM=xterm",
		"malformed-no-equals",
	}
	want := []string{"PATH=/usr/bin", "DWE_DEBUG=1", "TERM=xterm"}
	got := StripEnv(in)
	if len(got) != len(want) {
		t.Fatalf("StripEnv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StripEnv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOptionsFromEnv(t *testing.T) {
	t.Setenv("DWE_BRIDGE_DIR", "/dwe-bridge")
	t.Setenv("DWE_HOST_WORKSPACE", "/Users/foo/proj")
	t.Setenv("DWE_CONTAINER_WORKSPACE", "/workspace")
	t.Setenv("DWE_BRIDGE_PROJECT", "my-proj")
	t.Setenv("DWE_BRIDGE_UNREACHABLE", "warn")
	t.Setenv("TERM", "xterm-256color")

	sigs := make(chan os.Signal)
	opts := OptionsFromEnv([]string{"status"}, sigs)

	if opts.BridgeDir != "/dwe-bridge" {
		t.Errorf("BridgeDir = %q", opts.BridgeDir)
	}
	if opts.HostWorkspace != "/Users/foo/proj" {
		t.Errorf("HostWorkspace = %q", opts.HostWorkspace)
	}
	if opts.ContainerWorkspace != "/workspace" {
		t.Errorf("ContainerWorkspace = %q", opts.ContainerWorkspace)
	}
	if opts.Project != "my-proj" {
		t.Errorf("Project = %q", opts.Project)
	}
	if opts.Unreachable != "warn" {
		t.Errorf("Unreachable = %q", opts.Unreachable)
	}
	if opts.Term != "xterm-256color" {
		t.Errorf("Term = %q", opts.Term)
	}
	if len(opts.Argv) != 1 || opts.Argv[0] != "status" {
		t.Errorf("Argv = %v", opts.Argv)
	}
	cwd, _ := os.Getwd()
	if opts.Cwd != cwd {
		t.Errorf("Cwd = %q, want %q", opts.Cwd, cwd)
	}
	if opts.Signals == nil {
		t.Error("Signals not wired through")
	}
	// The raw environment is forwarded; Run strips it at HELLO build time.
	found := false
	for _, kv := range opts.Env {
		if kv == "DWE_BRIDGE_DIR=/dwe-bridge" {
			found = true
		}
	}
	if !found {
		t.Error("Env should be the raw environment (stripping happens in Run)")
	}
}
