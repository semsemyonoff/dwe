package bridge

import (
	"bytes"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
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

// fakeProcess is the in-process Process substitute behind the exec seam.
// A per-test script goroutine plays the subprocess: it reads p.stdinR,
// writes p.stdoutW / p.stderrW, consumes p.signals, and calls p.finish.
type fakeProcess struct {
	stdinR  *io.PipeReader
	stdinW  *io.PipeWriter
	stdoutR *io.PipeReader
	stdoutW *io.PipeWriter
	stderrR *io.PipeReader
	stderrW *io.PipeWriter

	signals chan syscall.Signal
	exit    chan int
}

func newFakeProcess() *fakeProcess {
	p := &fakeProcess{
		signals: make(chan syscall.Signal, 8),
		exit:    make(chan int, 1),
	}
	p.stdinR, p.stdinW = io.Pipe()
	p.stdoutR, p.stdoutW = io.Pipe()
	p.stderrR, p.stderrW = io.Pipe()
	return p
}

func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdinW }
func (p *fakeProcess) Stdout() io.ReadCloser { return p.stdoutR }
func (p *fakeProcess) Stderr() io.ReadCloser { return p.stderrR }

func (p *fakeProcess) Signal(sig syscall.Signal) error {
	select {
	case p.signals <- sig:
	default:
	}
	return nil
}

func (p *fakeProcess) Kill() error {
	p.finish(137)
	return nil
}

func (p *fakeProcess) Wait() int { return <-p.exit }

// finish ends the fake subprocess: output pipes hit EOF and Wait unblocks
// with code. Safe to call twice (Kill racing a script's own finish).
func (p *fakeProcess) finish(code int) {
	_ = p.stdoutW.Close()
	_ = p.stderrW.Close()
	select {
	case p.exit <- code:
	default:
	}
}

// launchRecorder captures every LaunchSpec the daemon produced.
type launchRecorder struct {
	mu    sync.Mutex
	specs []LaunchSpec
}

func (r *launchRecorder) add(spec LaunchSpec) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, spec)
}

func (r *launchRecorder) last(t *testing.T) LaunchSpec {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.specs) == 0 {
		t.Fatal("no subprocess was launched")
	}
	return r.specs[len(r.specs)-1]
}

// fakeLauncher returns an exec seam that records specs and runs script as
// the subprocess body.
func fakeLauncher(rec *launchRecorder, script func(p *fakeProcess)) LaunchFunc {
	return func(spec LaunchSpec) (Process, error) {
		rec.add(spec)
		p := newFakeProcess()
		go script(p)
		return p, nil
	}
}

// exitScript drains stdin and exits with code.
func exitScript(code int) func(p *fakeProcess) {
	return func(p *fakeProcess) {
		_, _ = io.Copy(io.Discard, p.stdinR)
		p.finish(code)
	}
}

// startDaemon runs a daemon over a fake launcher on a fresh project root and
// returns it. mutate adjusts the Config before Start.
func startDaemon(t *testing.T, launch LaunchFunc, mutate func(cfg *Config)) *Daemon {
	t.Helper()
	root := shortTempDir(t)
	cfg := Config{
		ProjectRoot: root,
		BridgeDir:   filepath.Join(root, ".dwe", "bridge"),
		Launch:      launch,
		Grace:       100 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d := New(cfg)
	if err := d.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// clientOptions wires a real bridgeclient at the daemon: clientDir plays the
// in-container /dwe-bridge mount, cwd defaults to the project root.
func clientOptions(d *Daemon, clientDir string, stdout, stderr *bytes.Buffer) bridgeclient.Options {
	return bridgeclient.Options{
		BridgeDir:  clientDir,
		Project:    "my-proj",
		Argv:       []string{"commands", "lint"},
		Cwd:        d.cfg.ProjectRoot,
		Stdin:      strings.NewReader(""),
		Stdout:     stdout,
		Stderr:     stderr,
		TCPHost:    "127.0.0.1",
		RetryDelay: time.Millisecond,
	}
}

// tcpClientDir builds a client-side bridge dir for the TCP path: port and
// token copied from the daemon, no host.sock (the way a Desktop container
// effectively sees it after the dead-socket fallback).
func tcpClientDir(t *testing.T, d *Daemon) string {
	t.Helper()
	dir := shortTempDir(t)
	port, err := os.ReadFile(d.PortPath())
	if err != nil {
		t.Fatalf("reading daemon port file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "port"), port, 0o644); err != nil {
		t.Fatalf("writing client port file: %v", err)
	}
	token, err := os.ReadFile(d.TokenPath())
	if err != nil {
		t.Fatalf("reading daemon token file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), token, 0o600); err != nil {
		t.Fatalf("writing client token file: %v", err)
	}
	return dir
}

func TestSession_TCPHappyPath(t *testing.T) {
	t.Setenv("PATH", "/host/usr/bin") // the daemon's own (host) PATH, forced onto the subprocess
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, func(p *fakeProcess) {
		_, _ = p.stdoutW.Write([]byte("out-data"))
		_, _ = p.stderrW.Write([]byte("err-data"))
		_, _ = io.Copy(io.Discard, p.stdinR)
		p.finish(7)
	}), nil)

	var stdout, stderr bytes.Buffer
	opts := clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)
	opts.Env = []string{
		"PATH=/workspace/evil:/usr/bin", // container-controlled — must NOT reach the host subprocess
		"GIT_INDEX_FILE=/workspace/.git/index",
		"DWE_BRIDGE_DIR=/dwe-bridge",
		"DWE_PROJECT_ROOT=/elsewhere",
		"DWE_INVOKED_FROM=spoofed",
		"DWE_NONINTERACTIVE=spoofed",
	}

	if code := bridgeclient.Run(opts); code != 7 {
		t.Fatalf("Run = %d, want 7 (stderr: %q)", code, stderr.String())
	}
	if got := stdout.String(); got != "out-data" {
		t.Errorf("stdout = %q, want %q", got, "out-data")
	}
	if got := stderr.String(); got != "err-data" {
		t.Errorf("stderr = %q, want %q", got, "err-data")
	}

	spec := rec.last(t)
	if got, want := spec.Argv, []string{"commands", "lint"}; !slices.Equal(got, want) {
		t.Errorf("spec argv = %v, want %v (pass-through)", got, want)
	}
	wantEnv := []string{
		"GIT_INDEX_FILE=/workspace/.git/index",
		"PATH=/host/usr/bin", // forced to the daemon's PATH, client's stripped
		"DWE_INVOKED_FROM=container",
		"DWE_NONINTERACTIVE=1",
	}
	if !slices.Equal(spec.Env, wantEnv) {
		t.Errorf("spec env = %v, want %v (re-filtered + forced host-controlled vars)", spec.Env, wantEnv)
	}
	wantDir, err := filepath.EvalSymlinks(d.cfg.ProjectRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if spec.Dir != wantDir {
		t.Errorf("spec dir = %q, want %q", spec.Dir, wantDir)
	}
}

func TestSession_UnixHappyPath(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, func(p *fakeProcess) {
		_, _ = p.stdoutW.Write([]byte("via-unix"))
		_, _ = io.Copy(io.Discard, p.stdinR)
		p.finish(0)
	}), nil)

	var stdout, stderr bytes.Buffer
	// The daemon's own bridge dir contains a live host.sock, so the client
	// takes the unix transport (peercred auth, same uid in-process).
	opts := clientOptions(d, d.BridgeDir(), &stdout, &stderr)

	if code := bridgeclient.Run(opts); code != 0 {
		t.Fatalf("Run = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if got := stdout.String(); got != "via-unix" {
		t.Errorf("stdout = %q, want %q", got, "via-unix")
	}
	rec.last(t) // a subprocess was launched via the unix path
}

func TestSession_CwdTranslationIntoSubdir(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)
	sub := filepath.Join(d.cfg.ProjectRoot, "services", "main")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	var stdout, stderr bytes.Buffer
	opts := clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)
	opts.HostWorkspace = d.cfg.ProjectRoot
	opts.ContainerWorkspace = "/workspace"
	opts.Cwd = "/workspace/services/main"

	if code := bridgeclient.Run(opts); code != 0 {
		t.Fatalf("Run = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	wantDir, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	if got := rec.last(t).Dir; got != wantDir {
		t.Errorf("spec dir = %q, want translated subdir %q", got, wantDir)
	}
}

func TestSession_StdinForwarding(t *testing.T) {
	rec := &launchRecorder{}
	stdinSeen := make(chan string, 1)
	d := startDaemon(t, fakeLauncher(rec, func(p *fakeProcess) {
		data, _ := io.ReadAll(p.stdinR)
		stdinSeen <- string(data)
		_, _ = p.stdoutW.Write(data)
		p.finish(0)
	}), nil)

	var stdout, stderr bytes.Buffer
	opts := clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)
	opts.Stdin = strings.NewReader("ping-payload")

	if code := bridgeclient.Run(opts); code != 0 {
		t.Fatalf("Run = %d, want 0 (stderr: %q)", code, stderr.String())
	}
	if got := <-stdinSeen; got != "ping-payload" {
		t.Errorf("subprocess stdin = %q, want %q", got, "ping-payload")
	}
	if got := stdout.String(); got != "ping-payload" {
		t.Errorf("echoed stdout = %q, want %q", got, "ping-payload")
	}
}

func TestSession_SignalForwarding(t *testing.T) {
	rec := &launchRecorder{}
	gotSignal := make(chan syscall.Signal, 1)
	d := startDaemon(t, fakeLauncher(rec, func(p *fakeProcess) {
		sig := <-p.signals
		gotSignal <- sig
		p.finish(128 + int(sig))
	}), nil)

	sigs := make(chan os.Signal, 1)
	sigs <- syscall.SIGINT
	// bridgeclient's signal pump ranges over the channel; close it so the
	// goroutine drains (goleak).
	t.Cleanup(func() { close(sigs) })

	var stdout, stderr bytes.Buffer
	opts := clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)
	opts.Signals = sigs

	if code := bridgeclient.Run(opts); code != 130 {
		t.Fatalf("Run = %d, want 130 (stderr: %q)", code, stderr.String())
	}
	if sig := <-gotSignal; sig != syscall.SIGINT {
		t.Errorf("subprocess saw signal %v, want SIGINT", sig)
	}
}

func TestSession_ExitCodePassthrough(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(42)), nil)

	var stdout, stderr bytes.Buffer
	if code := bridgeclient.Run(clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)); code != 42 {
		t.Errorf("Run = %d, want 42 (stderr: %q)", code, stderr.String())
	}
}

func TestSession_AuthFailedOnWrongToken(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)

	// A client holding another project's token (cross-project connect, D6).
	clientDir := tcpClientDir(t, d)
	wrongToken, err := bridgeproto.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if err := bridgeproto.WriteTokenFile(filepath.Join(clientDir, "token"), wrongToken); err != nil {
		t.Fatalf("WriteTokenFile: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := bridgeclient.Run(clientOptions(d, clientDir, &stdout, &stderr)); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "invalid or missing bridge token") {
		t.Errorf("stderr = %q, want the auth_failed message", got)
	}
	if len(rec.specs) != 0 {
		t.Error("subprocess launched despite failed auth")
	}
}

func TestSession_CwdOutsideProject(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)
	outside := shortTempDir(t)

	var stdout, stderr bytes.Buffer
	opts := clientOptions(d, tcpClientDir(t, d), &stdout, &stderr)
	opts.Cwd = outside // exists on the host but is not inside the project

	if code := bridgeclient.Run(opts); code != 1 {
		t.Errorf("Run = %d, want 1", code)
	}
	if got := stderr.String(); !strings.Contains(got, "outside the project root") {
		t.Errorf("stderr = %q, want the containment message", got)
	}
	if len(rec.specs) != 0 {
		t.Error("subprocess launched despite cwd outside the project")
	}
}

// rawHello dials the daemon's TCP port directly and sends a hand-crafted
// HELLO (bridgeclient cannot produce these invalid ones), returning the
// daemon's ERROR frame.
func rawHello(t *testing.T, d *Daemon, hello bridgeproto.Hello) bridgeproto.ErrorInfo {
	t.Helper()
	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.Port())))
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	defer func() { _ = conn.Close() }()
	payload, err := bridgeproto.EncodeHello(hello)
	if err != nil {
		t.Fatalf("EncodeHello: %v", err)
	}
	if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameHello, payload); err != nil {
		t.Fatalf("writing hello: %v", err)
	}
	frameType, payload, err := bridgeproto.ReadFrame(conn)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	if frameType != bridgeproto.FrameError {
		t.Fatalf("response frame = %s, want error", frameType)
	}
	info, err := bridgeproto.DecodeError(payload)
	if err != nil {
		t.Fatalf("DecodeError: %v", err)
	}
	return info
}

func TestSession_TTYUnsupported(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)
	token, err := bridgeproto.ReadTokenFile(d.TokenPath())
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}

	info := rawHello(t, d, bridgeproto.Hello{
		ProtocolVersion: bridgeproto.ProtocolVersion,
		Token:           token,
		Argv:            []string{"status"},
		Cwd:             d.cfg.ProjectRoot,
		TTY:             true,
	})
	if info.Code != bridgeproto.ErrCodeTTYUnsupported {
		t.Errorf("error code = %q, want %q", info.Code, bridgeproto.ErrCodeTTYUnsupported)
	}
	if len(rec.specs) != 0 {
		t.Error("subprocess launched despite tty request")
	}
}

func TestSession_VersionMismatch(t *testing.T) {
	rec := &launchRecorder{}
	d := startDaemon(t, fakeLauncher(rec, exitScript(0)), nil)
	token, err := bridgeproto.ReadTokenFile(d.TokenPath())
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}

	info := rawHello(t, d, bridgeproto.Hello{
		ProtocolVersion: bridgeproto.ProtocolVersion + 1,
		Token:           token,
		Argv:            []string{"status"},
		Cwd:             d.cfg.ProjectRoot,
	})
	if info.Code != bridgeproto.ErrCodeVersionMismatch {
		t.Errorf("error code = %q, want %q", info.Code, bridgeproto.ErrCodeVersionMismatch)
	}
	if len(rec.specs) != 0 {
		t.Error("subprocess launched despite version mismatch")
	}
}

func TestSession_ConnLossTerminatesSubprocess(t *testing.T) {
	rec := &launchRecorder{}
	started := make(chan *fakeProcess, 1)
	// No script goroutine: the test itself plays the long-running
	// subprocess, so it is the only consumer of p.signals.
	launch := func(spec LaunchSpec) (Process, error) {
		rec.add(spec)
		p := newFakeProcess()
		started <- p
		return p, nil
	}
	d := startDaemon(t, launch, nil)
	token, err := bridgeproto.ReadTokenFile(d.TokenPath())
	if err != nil {
		t.Fatalf("ReadTokenFile: %v", err)
	}

	conn, err := net.Dial("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.Port())))
	if err != nil {
		t.Fatalf("dial daemon: %v", err)
	}
	payload, err := bridgeproto.EncodeHello(bridgeproto.Hello{
		ProtocolVersion: bridgeproto.ProtocolVersion,
		Token:           token,
		Argv:            []string{"commands", "watch"},
		Cwd:             d.cfg.ProjectRoot,
	})
	if err != nil {
		t.Fatalf("EncodeHello: %v", err)
	}
	if err := bridgeproto.WriteFrame(conn, bridgeproto.FrameHello, payload); err != nil {
		t.Fatalf("writing hello: %v", err)
	}
	p := <-started

	// Container dies mid-command: the connection drops without STDIN_CLOSE.
	_ = conn.Close()

	select {
	case sig := <-p.signals:
		// The daemon must SIGTERM the subprocess (grace then SIGKILL).
		if sig != syscall.SIGTERM {
			t.Errorf("subprocess saw signal %v, want SIGTERM", sig)
		}
		p.finish(128 + int(sig))
	case <-time.After(5 * time.Second):
		p.finish(1) // unblock the session so Close cannot hang
		t.Fatal("subprocess was not signaled after connection loss")
	}
}

func TestSubprocessEnv(t *testing.T) {
	t.Setenv("PATH", "/host/usr/bin") // the daemon's own (host) PATH
	in := []string{
		"PATH=/workspace/evil:/usr/bin", // container-controlled — must NOT pass through
		"LD_PRELOAD=/workspace/evil.so", // loader hijack — dropped by prefix
		"DYLD_INSERT_LIBRARIES=/evil",   // macOS loader hijack — dropped by prefix
		"BASH_ENV=/workspace/rc.sh",     // shell-startup hijack — dropped
		"IFS=:",                         // word-split hijack — dropped
		"DWE_BRIDGE_DIR=/dwe-bridge",    // shim strip set, re-filtered
		"DWE_PROJECT_ROOT=/elsewhere",   // discovery override, re-filtered
		"DWE_INVOKED_FROM=host",         // host-controlled, never client-set
		"DWE_NONINTERACTIVE=0",          // host-controlled, never client-set
		"TERM=xterm",
	}
	want := []string{
		"TERM=xterm",
		"PATH=/host/usr/bin", // forced to the daemon's PATH, not the client's
		"DWE_INVOKED_FROM=container",
		"DWE_NONINTERACTIVE=1",
	}
	if got := subprocessEnv(in); !slices.Equal(got, want) {
		t.Errorf("subprocessEnv = %v, want %v", got, want)
	}
}

func TestValidateCwd(t *testing.T) {
	root := shortTempDir(t)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	sub := filepath.Join(root, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	outside := shortTempDir(t)
	// A real directory whose path is a string-prefix extension of the root:
	// the boundary-aware containment check must still reject it.
	sibling := root + "-sibling"
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sibling) })

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantErr string
	}{
		{"project root", root, resolvedRoot, ""},
		{"subdir", sub, filepath.Join(resolvedRoot, "sub"), ""},
		{"outside", outside, "", "outside the project root"},
		{"sibling prefix is not containment", sibling, "", "outside the project root"},
		{"nonexistent", filepath.Join(root, "missing"), "", "does not resolve"},
		{"relative", "sub", "", "not an absolute host path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateCwd(tt.cwd, resolvedRoot)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("validateCwd(%q) err = %v, want containing %q", tt.cwd, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateCwd(%q): %v", tt.cwd, err)
			}
			if got != tt.want {
				t.Errorf("validateCwd(%q) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}
