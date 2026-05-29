package logs_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	cmdlogs "devbox-cli/internal/cli/logs"

	"github.com/spf13/cobra"
)

// newTestRoot builds a minimal cobra root command with the logs subcommand
// attached. It does NOT run PersistentPreRunE — callers that need project
// resolution should use cli.NewRootCmd() instead.
func newTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{
		Use:           "devbox",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.AddCommand(cmdlogs.NewCmd("", flags))
	return root
}

func execCmd(t *testing.T, root *cobra.Command, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	root.SetOut(&outBuf)
	root.SetErr(&errBuf)
	root.SetArgs(args)
	err = root.Execute()
	return outBuf.String(), errBuf.String(), err
}

// writeLogsTestConfig creates a temp dir with devbox.yml and optional service
// folders. services maps service name → container template string.
func writeLogsTestConfig(t *testing.T, dir string, svcs map[string]string) string {
	t.Helper()
	return writeLogsTestConfigWithDockerBin(t, dir, svcs, "")
}

// writeLogsTestConfigWithDockerBin is like writeLogsTestConfig but also writes
// a .devbox/config file that sets the docker binary override, so tests can use
// a fake docker binary without touching PATH.
func writeLogsTestConfigWithDockerBin(t *testing.T, dir string, svcs map[string]string, dockerBin string) string {
	t.Helper()
	content := "schema_version: \"2\"\nproject:\n  name: test\n  prefix: devbox\n"
	if err := os.WriteFile(filepath.Join(dir, "devbox.yml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}
	for name, container := range svcs {
		svcDir := filepath.Join(dir, "devbox", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", svcDir, err)
		}
		yml := "type: app\ncontainer: " + container + "\n"
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(yml), 0o644); err != nil {
			t.Fatalf("write service.yml: %v", err)
		}
	}
	if dockerBin != "" {
		devboxDir := filepath.Join(dir, ".devbox")
		if err := os.MkdirAll(devboxDir, 0o755); err != nil {
			t.Fatalf("mkdir .devbox: %v", err)
		}
		cfg := fmt.Sprintf("binary_docker = %s\n", dockerBin)
		if err := os.WriteFile(filepath.Join(devboxDir, "config"), []byte(cfg), 0o644); err != nil {
			t.Fatalf("write .devbox/config: %v", err)
		}
	}
	return filepath.Join(dir, "devbox.yml")
}

// makeFakeDocker creates an executable shell script in dir and returns its path.
func makeFakeDocker(t *testing.T, dir, name, script string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker %s: %v", p, err)
	}
	return p
}

// TestLogsCmd_NoArgs verifies that invoking logs without a service name
// returns an error (cobra.ExactArgs(1) enforcement).
func TestLogsCmd_NoArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "text"}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs")
	if err == nil {
		t.Error("expected error when no service argument provided, got nil")
	}
}

// TestLogsCmd_OneArg verifies that invoking logs with exactly one argument
// succeeds when the service exists and docker exits 0.
func TestLogsCmd_OneArg(t *testing.T) {
	dir := t.TempDir()
	fakeBin := makeFakeDocker(t, dir, "docker", "#!/bin/sh\nexit 0\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Errorf("unexpected error with one arg for known service: %v", err)
	}
}

// TestLogsCmd_TooManyArgs verifies that more than one positional arg is rejected.
func TestLogsCmd_TooManyArgs(t *testing.T) {
	flags := &cmdctx.RootFlags{Output: "text"}
	root := newTestRoot(flags)
	_, _, err := execCmd(t, root, "logs", "svcA", "svcB")
	if err == nil {
		t.Error("expected error with two positional args, got nil")
	}
}

// TestLogsCmd_FlagsRegistered verifies the expected flags exist on the command.
func TestLogsCmd_FlagsRegistered(t *testing.T) {
	flags := &cmdctx.RootFlags{}
	root := newTestRoot(flags)

	var logsCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "logs" {
			logsCmd = c
			break
		}
	}
	if logsCmd == nil {
		t.Fatal("logs command not found in root")
	}

	for _, flagName := range []string{"tail", "since", "follow"} {
		if logsCmd.Flags().Lookup(flagName) == nil {
			t.Errorf("expected flag --%s to be registered", flagName)
		}
	}

	// Verify -f shorthand for --follow.
	if logsCmd.Flags().ShorthandLookup("f") == nil {
		t.Error("expected -f shorthand for --follow")
	}
}

// TestResolveLogsTarget_UnknownService verifies that requesting a service not
// in the project returns a service_unknown CodedError with a hint listing
// available services.
func TestResolveLogsTarget_UnknownService(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, map[string]string{
		"myapp": "myapp-container",
	})
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	_, _, err := cmdlogs.ResolveLogsTarget(flags, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown service, got nil")
	}
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("expected *cmdctx.CodedError, got %T: %v", err, err)
	}
	if coded.Code != "service_unknown" {
		t.Errorf("error code = %q, want %q", coded.Code, "service_unknown")
	}
	if !strings.Contains(coded.Hint, "myapp") {
		t.Errorf("hint should list known service names; got: %q", coded.Hint)
	}
	if detail, ok := coded.Details["requested"]; !ok || detail != "nonexistent" {
		t.Errorf("details[requested] = %v, want %q", detail, "nonexistent")
	}
}

// TestResolveLogsTarget_KnownService verifies that a known service resolves to
// the expected Docker container name (project prefix + container template).
func TestResolveLogsTarget_KnownService(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, map[string]string{
		"myapp": "myapp-container",
	})
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	containerName, cfg, err := cmdlogs.ResolveLogsTarget(flags, "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Container name = project.FullName() + "-" + container template.
	// From the fixture: prefix=devbox, name=test → fullName=devbox-test.
	want := cfg.Project.FullName() + "-myapp-container"
	if containerName != want {
		t.Errorf("container name = %q, want %q", containerName, want)
	}
}

// TestResolveLogsTarget_NoServicesHint verifies that when a project has no
// services, the hint in the error still renders without crashing.
func TestResolveLogsTarget_NoServicesHint(t *testing.T) {
	dir := t.TempDir()
	cfgPath := writeLogsTestConfig(t, dir, nil)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath}

	_, _, err := cmdlogs.ResolveLogsTarget(flags, "anything")
	if err == nil {
		t.Fatal("expected error for project with no services")
	}
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) || coded.Code != "service_unknown" {
		t.Errorf("expected service_unknown CodedError, got %v", err)
	}
}

// TestLogsCmd_TextMode_Stdout verifies that in text mode the command passes
// docker stdout through to the caller's stdout.
func TestLogsCmd_TextMode_Stdout(t *testing.T) {
	dir := t.TempDir()
	// Fake docker writes two lines to stdout and exits 0.
	fakeBin := makeFakeDocker(t, dir, "docker",
		"#!/bin/sh\necho 'line one from stdout'\necho 'line two from stdout'\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	stdout, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"line one from stdout", "line two from stdout"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q; got: %q", want, stdout)
		}
	}
}

// TestLogsCmd_TextMode_Stderr verifies that docker stderr is streamed to
// the caller's stderr in text mode.
func TestLogsCmd_TextMode_Stderr(t *testing.T) {
	dir := t.TempDir()
	fakeBin := makeFakeDocker(t, dir, "docker",
		"#!/bin/sh\necho 'stderr line' >&2\nexit 0\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, stderr, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "stderr line") {
		t.Errorf("stderr missing 'stderr line'; got: %q", stderr)
	}
}

// TestLogsCmd_TextMode_DockerError verifies that a non-zero docker exit code
// surfaces as a wrapped error.
func TestLogsCmd_TextMode_DockerError(t *testing.T) {
	dir := t.TempDir()
	fakeBin := makeFakeDocker(t, dir, "docker",
		"#!/bin/sh\necho 'some docker error' >&2\nexit 1\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, _, err := execCmd(t, root, "logs", "myapp")
	if err == nil {
		t.Fatal("expected error from docker exit code 1, got nil")
	}
	if !strings.Contains(err.Error(), "docker logs") {
		t.Errorf("error %q should mention 'docker logs'", err.Error())
	}
}

// TestLogsCmd_TextMode_NoSuchContainer verifies that "No such container" on
// docker stderr is transformed to a container_not_found CodedError.
func TestLogsCmd_TextMode_NoSuchContainer(t *testing.T) {
	dir := t.TempDir()
	fakeBin := makeFakeDocker(t, dir, "docker",
		"#!/bin/sh\necho 'Error response from daemon: No such container: devbox-test-myapp' >&2\nexit 1\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, _, err := execCmd(t, root, "logs", "myapp")
	if err == nil {
		t.Fatal("expected container_not_found error, got nil")
	}
	var coded *cmdctx.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("expected *cmdctx.CodedError, got %T: %v", err, err)
	}
	if coded.Code != "container_not_found" {
		t.Errorf("error code = %q, want %q", coded.Code, "container_not_found")
	}
}

// TestLogsCmd_TextMode_ArgsToDocker verifies that --tail and --since flags are
// forwarded to docker by inspecting the args log written by the fake binary.
func TestLogsCmd_TextMode_ArgsToDocker(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 0\n", argsLog)
	fakeBin := makeFakeDocker(t, dir, "docker", script)
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, _, err := execCmd(t, root, "logs", "myapp", "--tail", "20", "--since", "5m")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := os.ReadFile(argsLog)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	argsStr := string(got)
	for _, want := range []string{"logs", "--tail", "20", "--since", "5m"} {
		if !strings.Contains(argsStr, want) {
			t.Errorf("docker args missing %q; got: %q", want, argsStr)
		}
	}
}

// logRec mirrors the NDJSON envelope emitted by `devbox logs --output json`.
type logRec struct {
	Ts     string `json:"ts"`
	Stream string `json:"stream"`
	Msg    string `json:"msg"`
}

// decodeNDJSON decodes all NDJSON records from s into a slice of logRec.
func decodeNDJSON(t *testing.T, s string) []logRec {
	t.Helper()
	var records []logRec
	dec := json.NewDecoder(strings.NewReader(s))
	for {
		var r logRec
		if err := dec.Decode(&r); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("NDJSON decode: %v", err)
		}
		records = append(records, r)
	}
	return records
}

// TestLogsCmd_JSONMode_NDJSONShape verifies that --output json emits one NDJSON
// object per log line with ts, stream, and msg fields correctly populated.
// Stream attribution is non-deterministic across stdout/stderr goroutines;
// the test asserts per-stream ordering and set equality, not cross-stream order.
func TestLogsCmd_JSONMode_NDJSONShape(t *testing.T) {
	dir := t.TempDir()
	// The fake binary emits timestamped lines matching docker --timestamps format.
	fakeBin := makeFakeDocker(t, dir, "docker",
		"#!/bin/sh\n"+
			"echo '2026-05-29T07:30:00.000000000Z line-from-stdout'\n"+
			"echo '2026-05-29T07:30:01.000000000Z line-from-stderr' >&2\n"+
			"echo '2026-05-29T07:30:02.000000000Z another-stdout'\n")
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "json", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	stdout, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	records := decodeNDJSON(t, stdout)

	// Partition by stream.
	var stdoutRecs, stderrRecs []logRec
	for _, r := range records {
		switch r.Stream {
		case "stdout":
			stdoutRecs = append(stdoutRecs, r)
		case "stderr":
			stderrRecs = append(stderrRecs, r)
		default:
			t.Errorf("unexpected stream %q in record %+v", r.Stream, r)
		}
	}

	// Per-stream ordering must be preserved.
	if len(stdoutRecs) != 2 {
		t.Fatalf("expected 2 stdout records, got %d", len(stdoutRecs))
	}
	if stdoutRecs[0].Msg != "line-from-stdout" {
		t.Errorf("stdout[0].msg = %q, want %q", stdoutRecs[0].Msg, "line-from-stdout")
	}
	if stdoutRecs[1].Msg != "another-stdout" {
		t.Errorf("stdout[1].msg = %q, want %q", stdoutRecs[1].Msg, "another-stdout")
	}

	if len(stderrRecs) != 1 {
		t.Fatalf("expected 1 stderr record, got %d", len(stderrRecs))
	}
	if stderrRecs[0].Msg != "line-from-stderr" {
		t.Errorf("stderr[0].msg = %q, want %q", stderrRecs[0].Msg, "line-from-stderr")
	}

	// Every record must have a non-empty ts and valid stream.
	for i, r := range records {
		if r.Ts == "" {
			t.Errorf("records[%d].ts is empty", i)
		}
	}

	// Timestamps from the fake output must be preserved verbatim.
	if stdoutRecs[0].Ts != "2026-05-29T07:30:00.000000000Z" {
		t.Errorf("stdout[0].ts = %q, want %q", stdoutRecs[0].Ts, "2026-05-29T07:30:00.000000000Z")
	}
	if stdoutRecs[1].Ts != "2026-05-29T07:30:02.000000000Z" {
		t.Errorf("stdout[1].ts = %q, want %q", stdoutRecs[1].Ts, "2026-05-29T07:30:02.000000000Z")
	}
	if stderrRecs[0].Ts != "2026-05-29T07:30:01.000000000Z" {
		t.Errorf("stderr[0].ts = %q, want %q", stderrRecs[0].Ts, "2026-05-29T07:30:01.000000000Z")
	}
}

// TestLogsCmd_JSONMode_TimestampsFlag verifies that --timestamps is forwarded
// to docker when running in JSON mode.
func TestLogsCmd_JSONMode_TimestampsFlag(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 0\n", argsLog)
	fakeBin := makeFakeDocker(t, dir, "docker", script)
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "json", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, readErr := os.ReadFile(argsLog)
	if readErr != nil {
		t.Fatalf("read args log: %v", readErr)
	}
	argsStr := string(got)
	if !strings.Contains(argsStr, "--timestamps") {
		t.Errorf("expected --timestamps in docker args for JSON mode; got: %q", argsStr)
	}
}

// TestLogsCmd_JSONMode_TextModeNoTimestamps verifies that --timestamps is NOT
// automatically injected in text mode.
func TestLogsCmd_JSONMode_TextModeNoTimestamps(t *testing.T) {
	dir := t.TempDir()
	argsLog := filepath.Join(dir, "args.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" >> %s\nexit 0\n", argsLog)
	fakeBin := makeFakeDocker(t, dir, "docker", script)
	cfgPath := writeLogsTestConfigWithDockerBin(t, dir, map[string]string{"myapp": "myapp"}, fakeBin)
	flags := &cmdctx.RootFlags{Output: "text", ConfigPath: cfgPath}
	root := newTestRoot(flags)

	_, _, err := execCmd(t, root, "logs", "myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, readErr := os.ReadFile(argsLog)
	if readErr != nil {
		t.Fatalf("read args log: %v", readErr)
	}
	argsStr := string(got)
	if strings.Contains(argsStr, "--timestamps") {
		t.Errorf("--timestamps should not appear in text mode docker args; got: %q", argsStr)
	}
}
