package bridge

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	corebridge "github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/bridge/shimassets"
)

// init stubs the daemon-touching seams for the whole test binary (mirrors
// cli/status): the production ensure spawns a detached daemon via
// os.Executable() — re-executing the test binary (the documented recursion
// hazard) — and a real StopDaemon would SIGTERM a flock held by the test
// process itself. Tests install recorders on top via the stub helpers.
func init() {
	ensureDaemonFn = func(corebridge.EnsureConfig) (bool, error) { return false, nil }
	stopDaemonFn = func(string) (bool, error) { return false, nil }
}

// fixtureFlags builds a minimal project (one required app service) and
// returns RootFlags as the root resolver would: ConfigPath + Root set.
func fixtureFlags(t *testing.T, bridgeEnabled bool) *cmdctx.RootFlags {
	t.Helper()
	dir := t.TempDir()
	ws := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: dwe\n"
	mustWrite(t, filepath.Join(dir, "workspace.yml"), ws)
	svc := "type: app\ncontainer: app-main\nrequired: true\n"
	if bridgeEnabled {
		// The bridge is strictly opt-in — no service type enables it by default.
		svc += "bridge:\n  enabled: true\n"
	}
	mustWrite(t, filepath.Join(dir, "workspace", "services", "main", "service.yml"), svc)
	return &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml"), Root: dir}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// execBridgeWith runs the bridge subtree with the given flags and returns
// stdout and stderr separately.
func execBridgeWith(t *testing.T, flags *cmdctx.RootFlags, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewCmd("advanced", flags)
	var out, errBuf bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errBuf.String(), err
}

// --- seam stubs ---------------------------------------------------------------

func stubEnsure(t *testing.T, started bool, err error) *[]corebridge.EnsureConfig {
	t.Helper()
	var calls []corebridge.EnsureConfig
	prev := ensureDaemonFn
	t.Cleanup(func() { ensureDaemonFn = prev })
	ensureDaemonFn = func(cfg corebridge.EnsureConfig) (bool, error) {
		calls = append(calls, cfg)
		return started, err
	}
	return &calls
}

func stubStop(t *testing.T, signaled bool, err error) *[]string {
	t.Helper()
	var dirs []string
	prev := stopDaemonFn
	t.Cleanup(func() { stopDaemonFn = prev })
	stopDaemonFn = func(bridgeDir string) (bool, error) {
		dirs = append(dirs, bridgeDir)
		return signaled, err
	}
	return &dirs
}

func stubProbe(t *testing.T, probe corebridge.DaemonProbe, err error) {
	t.Helper()
	prev := probeDaemonFn
	t.Cleanup(func() { probeDaemonFn = prev })
	probeDaemonFn = func(string) (corebridge.DaemonProbe, error) { return probe, err }
}

func stubShims(t *testing.T, states []shimassets.ShimState) {
	t.Helper()
	prev := shimStatusFn
	t.Cleanup(func() { shimStatusFn = prev })
	shimStatusFn = func(string) ([]shimassets.ShimState, error) { return states, nil }
}

func stubNow(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowFn
	t.Cleanup(func() { nowFn = prev })
	nowFn = func() time.Time { return now }
}

func codedErr(t *testing.T, err error, wantCode string) *cmdctx.CodedError {
	t.Helper()
	ce, ok := errors.AsType[*cmdctx.CodedError](err)
	if !ok {
		t.Fatalf("err = %v (%T), want *cmdctx.CodedError", err, err)
	}
	if ce.Code != wantCode {
		t.Fatalf("error code = %q, want %q", ce.Code, wantCode)
	}
	return ce
}

// --- subtree shape ------------------------------------------------------------

func TestBridgeSubtreeHasUserCommands(t *testing.T) {
	cmd := NewCmd("advanced", &cmdctx.RootFlags{})
	want := map[string]bool{"start": false, "stop": false, "status": false, "logs": false, "daemon": true}
	got := map[string]bool{}
	for _, sub := range cmd.Commands() {
		got[sub.Name()] = sub.Hidden
	}
	for name, hidden := range want {
		gotHidden, ok := got[name]
		if !ok {
			t.Errorf("subcommand %q missing", name)
			continue
		}
		if gotHidden != hidden {
			t.Errorf("subcommand %q hidden = %v, want %v", name, gotHidden, hidden)
		}
	}
}

func TestBridgeSubcommandsRejectArgs(t *testing.T) {
	for _, name := range []string{"start", "stop", "status", "logs"} {
		t.Run(name, func(t *testing.T) {
			_, _, err := execBridgeWith(t, fixtureFlags(t, true), name, "extra-arg")
			if err == nil {
				t.Fatalf("bridge %s extra-arg: err = nil, want args error", name)
			}
		})
	}
}

// --- start ----------------------------------------------------------------------

func TestStartRequiresBridgeEnabled(t *testing.T) {
	calls := stubEnsure(t, true, nil)
	_, _, err := execBridgeWith(t, fixtureFlags(t, false), "start")
	_ = codedErr(t, err, "bridge_not_enabled")
	if len(*calls) != 0 {
		t.Errorf("ensure called %d times with bridge disabled, want 0", len(*calls))
	}
}

func TestStartEnsuresDaemon(t *testing.T) {
	calls := stubEnsure(t, true, nil)
	flags := fixtureFlags(t, true)
	out, _, err := execBridgeWith(t, flags, "start")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "bridge daemon started\n" {
		t.Errorf("stdout = %q, want started message", out)
	}
	if len(*calls) != 1 {
		t.Fatalf("ensure called %d times, want 1", len(*calls))
	}
	if got := (*calls)[0].ProjectRoot; got != flags.Root {
		t.Errorf("ensure ProjectRoot = %q, want %q", got, flags.Root)
	}
}

func TestStartAlreadyRunning(t *testing.T) {
	stubEnsure(t, false, nil)
	out, _, err := execBridgeWith(t, fixtureFlags(t, true), "start")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "bridge daemon already running\n" {
		t.Errorf("stdout = %q, want already-running message", out)
	}
}

func TestStartEnsureErrorWrapped(t *testing.T) {
	stubEnsure(t, false, errors.New("spawn refused"))
	_, _, err := execBridgeWith(t, fixtureFlags(t, true), "start")
	ce := codedErr(t, err, "bridge_start_failed")
	if !strings.Contains(ce.Message, "spawn refused") {
		t.Errorf("message = %q, want the ensure error", ce.Message)
	}
}

func TestStartJSON(t *testing.T) {
	stubEnsure(t, true, nil)
	flags := fixtureFlags(t, true)
	flags.Output = "json"
	out, _, err := execBridgeWith(t, flags, "start")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"started":true,"already_running":false}` + "\n"; out != want {
		t.Errorf("json = %q, want %q", out, want)
	}
}

// --- stop -----------------------------------------------------------------------

func TestStopSignalsDaemon(t *testing.T) {
	dirs := stubStop(t, true, nil)
	flags := fixtureFlags(t, true)
	out, _, err := execBridgeWith(t, flags, "stop")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "bridge daemon signaled to stop\n" {
		t.Errorf("stdout = %q, want signaled message", out)
	}
	if want := corebridge.DefaultBridgeDir(flags.Root); len(*dirs) != 1 || (*dirs)[0] != want {
		t.Errorf("stop dirs = %v, want [%s]", *dirs, want)
	}
}

func TestStopNotRunning(t *testing.T) {
	stubStop(t, false, nil)
	out, _, err := execBridgeWith(t, fixtureFlags(t, true), "stop")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "bridge daemon is not running\n" {
		t.Errorf("stdout = %q, want not-running message", out)
	}
}

func TestStopNotRunningBridgeDisabled(t *testing.T) {
	stubStop(t, false, nil)
	out, _, err := execBridgeWith(t, fixtureFlags(t, false), "stop")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "bridge daemon is not running (no enabled service has the host bridge enabled)\n"
	if out != want {
		t.Errorf("stdout = %q, want %q", out, want)
	}
}

func TestStopErrorWrapped(t *testing.T) {
	stubStop(t, false, errors.New("kill refused"))
	_, _, err := execBridgeWith(t, fixtureFlags(t, true), "stop")
	_ = codedErr(t, err, "bridge_stop_failed")
}

func TestStopJSON(t *testing.T) {
	stubStop(t, true, nil)
	flags := fixtureFlags(t, true)
	flags.Output = "json"
	out, _, err := execBridgeWith(t, flags, "stop")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"signaled":true,"bridge_enabled":true}` + "\n"; out != want {
		t.Errorf("json = %q, want %q", out, want)
	}
}

// --- status ---------------------------------------------------------------------

func TestStatusJSONRunning(t *testing.T) {
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	stubProbe(t, corebridge.DaemonProbe{Running: true, PID: 4242, StartedAt: startedAt}, nil)
	stubNow(t, startedAt.Add(90*time.Second))
	stubShims(t, []shimassets.ShimState{
		{Name: "shim-linux-amd64", State: shimassets.StateCurrent},
		{Name: "shim-linux-arm64", State: shimassets.StateStale},
	})
	flags := fixtureFlags(t, true)
	flags.Output = "json"
	bridgeDir := corebridge.DefaultBridgeDir(flags.Root)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// fileExists only stats the socket path; a plain file stands in for it.
	mustWrite(t, corebridge.SocketPath(bridgeDir), "")
	mustWrite(t, corebridge.PortPath(bridgeDir), "54321\n")

	out, _, err := execBridgeWith(t, flags, "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := fmt.Sprintf(
		`{"enabled":true,"running":true,"pid":4242,"started_at":"2026-06-10T12:00:00Z",`+
			`"uptime_seconds":90,"socket":%q,"port":54321,`+
			`"shims":[{"name":"shim-linux-amd64","state":"current"},{"name":"shim-linux-arm64","state":"stale"}]}`,
		corebridge.SocketPath(bridgeDir)) + "\n"
	if out != want {
		t.Errorf("json =\n%s\nwant\n%s", out, want)
	}
}

func TestStatusJSONStopped(t *testing.T) {
	stubShims(t, nil)
	flags := fixtureFlags(t, false)
	flags.Output = "json"

	out, _, err := execBridgeWith(t, flags, "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"enabled":false,"running":false,"shims":[]}` + "\n"; out != want {
		t.Errorf("json = %q, want %q", out, want)
	}
}

func TestStatusTextRunning(t *testing.T) {
	startedAt := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	stubProbe(t, corebridge.DaemonProbe{Running: true, PID: 4242, StartedAt: startedAt}, nil)
	stubNow(t, startedAt.Add(90*time.Second))
	stubShims(t, []shimassets.ShimState{
		{Name: "shim-linux-amd64", State: shimassets.StateCurrent},
		{Name: "shim-linux-arm64", State: shimassets.StateStale},
	})
	flags := fixtureFlags(t, true)
	bridgeDir := corebridge.DefaultBridgeDir(flags.Root)
	if err := os.MkdirAll(bridgeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, corebridge.SocketPath(bridgeDir), "")
	mustWrite(t, corebridge.PortPath(bridgeDir), "54321\n")

	out, _, err := execBridgeWith(t, flags, "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "daemon:  running (pid 4242, up 1m30s)\n" +
		"socket:  " + corebridge.SocketPath(bridgeDir) + "\n" +
		"port:    54321\n" +
		"shims:   shim-linux-amd64 current, shim-linux-arm64 stale\n"
	if out != want {
		t.Errorf("text =\n%q\nwant\n%q", out, want)
	}
}

func TestStatusTextStoppedDisabled(t *testing.T) {
	stubShims(t, nil)
	out, _, err := execBridgeWith(t, fixtureFlags(t, false), "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := "daemon:  not running\n" +
		"bridge:  no enabled service has the host bridge enabled\n" +
		"shims:   none\n"
	if out != want {
		t.Errorf("text =\n%q\nwant\n%q", out, want)
	}
}

func TestStatusProbeErrorWrapped(t *testing.T) {
	stubProbe(t, corebridge.DaemonProbe{}, errors.New("flock io error"))
	_, _, err := execBridgeWith(t, fixtureFlags(t, true), "status")
	_ = codedErr(t, err, "bridge_status_failed")
}

// TestStatusRealProbeOnFreshProject runs the real pidfile probe (no stubs):
// a fresh project has no daemon — running must be false.
func TestStatusRealProbeOnFreshProject(t *testing.T) {
	stubShims(t, nil)
	out, _, err := execBridgeWith(t, fixtureFlags(t, true), "status")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "daemon:  not running") {
		t.Errorf("output %q missing not-running line", out)
	}
}

// --- logs -----------------------------------------------------------------------

func writeDaemonLog(t *testing.T, flags *cmdctx.RootFlags, lines ...string) string {
	t.Helper()
	bridgeDir := corebridge.DefaultBridgeDir(flags.Root)
	logPath := corebridge.LogPath(bridgeDir)
	mustWrite(t, logPath, strings.Join(lines, "\n")+"\n")
	return logPath
}

func TestLogsTail(t *testing.T) {
	flags := fixtureFlags(t, true)
	writeDaemonLog(t, flags, "l1", "l2", "l3")

	out, _, err := execBridgeWith(t, flags, "logs", "--tail", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "l2\nl3\n" {
		t.Errorf("stdout = %q, want last two lines", out)
	}
}

func TestLogsTailZeroMeansAll(t *testing.T) {
	flags := fixtureFlags(t, true)
	writeDaemonLog(t, flags, "l1", "l2", "l3")

	out, _, err := execBridgeWith(t, flags, "logs", "--tail", "0")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "l1\nl2\nl3\n" {
		t.Errorf("stdout = %q, want all lines", out)
	}
}

func TestLogsNegativeTailRejected(t *testing.T) {
	flags := fixtureFlags(t, true)
	_, _, err := execBridgeWith(t, flags, "logs", "--tail", "-1")
	ce := codedErr(t, err, "invalid_tail")
	if got := cmdctx.ExitCodeFor(ce); got != 2 {
		t.Errorf("exit code = %d, want 2 (usage error)", got)
	}
}

func TestLogsMissingFileNotice(t *testing.T) {
	flags := fixtureFlags(t, true)
	out, errOut, err := execBridgeWith(t, flags, "logs")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty", out)
	}
	if !strings.Contains(errOut, "no bridge daemon log at ") {
		t.Errorf("stderr = %q, want missing-log notice", errOut)
	}
}

func TestLogsMissingFileJSON(t *testing.T) {
	flags := fixtureFlags(t, true)
	flags.Output = "json"
	out, _, err := execBridgeWith(t, flags, "logs")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"lines":[]}` + "\n"; out != want {
		t.Errorf("json = %q, want %q", out, want)
	}
}

func TestLogsJSON(t *testing.T) {
	flags := fixtureFlags(t, true)
	flags.Output = "json"
	writeDaemonLog(t, flags, "l1", "l2", "l3")

	out, _, err := execBridgeWith(t, flags, "logs", "--tail", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if want := `{"lines":["l2","l3"]}` + "\n"; out != want {
		t.Errorf("json = %q, want %q", out, want)
	}
}
