package pipeline

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestExecShellAction_EchoesCommand asserts execShellAction echoes the resolved
// `sh -c <cmd>` invocation at Verbose (and Debug) and stays silent at LevelOff.
func TestExecShellAction_EchoesCommand(t *testing.T) {
	cases := []struct {
		name     string
		level    trace.Level
		wantEcho bool
	}{
		{"verbose", trace.LevelVerbose, true},
		{"debug", trace.LevelDebug, true},
		{"off", trace.LevelOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			trace.Configure(&buf, tc.level)
			defer trace.Configure(nil, trace.LevelOff)

			actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
			if err := execShellAction(context.Background(), config.Action{Type: "shell", Cmd: "true"}, actx); err != nil {
				t.Fatalf("execShellAction: %v", err)
			}

			gotEcho := strings.Contains(buf.String(), "$ sh -c true")
			if gotEcho != tc.wantEcho {
				t.Fatalf("level=%v: echo present=%v, want %v (buf=%q)", tc.level, gotEcho, tc.wantEcho, buf.String())
			}
		})
	}
}

// TestExecDweAction_EchoesCommand asserts execDweAction echoes the nested dwe
// invocation as the `sh -c '<dwe-bin> <cmd>'` form it actually runs, at Verbose,
// using the resolved binary (not a hard-coded "dwe") so the echo matches what
// buildDweCmd executes. A pre-cancelled context makes runChildCmd's cmd.Run()
// return immediately (exec checks ctx before launching) so the test never
// re-executes the test binary as a child dwe process.
func TestExecDweAction_EchoesCommand(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // child process never launches

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
	// Error is expected (context canceled); we only assert the echo fired first.
	_ = execDweAction(ctx, config.Action{Type: "dwe", Cmd: "deploy run"}, actx)

	bin := resolveDweBin(config.DweBin(actx.Cfg))
	want := "$ " + trace.FormatCommand([]string{config.ShellBin(actx.Cfg), "-c", dweShellPayload(bin, "deploy run")})
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("dwe echo missing: want %q in %q", want, buf.String())
	}
}

// TestExecDweAction_EchoesQuotedArgsFaithfully is the regression guard for the
// bug where strings.Fields split a.Cmd and re-quoted each fragment, mangling
// quoted arguments. The command runs through `sh -c`, so a quoted value must be
// echoed verbatim (one logical argument), not as separate quote-containing words.
func TestExecDweAction_EchoesQuotedArgsFaithfully(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
	_ = execDweAction(ctx, config.Action{Type: "dwe", Cmd: `render env --path "foo bar"`}, actx)

	bin := resolveDweBin(config.DweBin(actx.Cfg))
	want := "$ " + trace.FormatCommand([]string{config.ShellBin(actx.Cfg), "-c", dweShellPayload(bin, `render env --path "foo bar"`)})
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("quoted-arg dwe echo mangled: want %q in %q", want, buf.String())
	}
	// The old strings.Fields behaviour would have emitted the value split into
	// two quote-containing words — make sure that never reappears.
	if strings.Contains(buf.String(), `'"foo'`) {
		t.Fatalf("quoted arg was split into fragments: %q", buf.String())
	}
}

// TestExecDweAction_SilentAtOff asserts no echo at LevelOff.
func TestExecDweAction_SilentAtOff(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelOff)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}}
	_ = execDweAction(ctx, config.Action{Type: "dwe", Cmd: "deploy run"}, actx)

	if buf.Len() != 0 {
		t.Fatalf("dwe echo leaked at LevelOff: %q", buf.String())
	}
}

// TestExecCommandAction_EchoesCommand asserts execCommandAction echoes the
// resolved user command (`dwe <id>`) at Verbose and is silent at LevelOff.
func TestExecCommandAction_EchoesCommand(t *testing.T) {
	cases := []struct {
		name     string
		level    trace.Level
		wantEcho bool
	}{
		{"verbose", trace.LevelVerbose, true},
		{"off", trace.LevelOff, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			trace.Configure(&buf, tc.level)
			defer trace.Configure(nil, trace.LevelOff)

			reg := usercommands.NewEmptyRegistry()
			reg.AddCommandForTest(&usercommands.CommandDef{
				ID:   "noop.cmd",
				Type: usercommands.CommandTypeShell,
				Cmd:  "true",
			})
			actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}, Reg: reg}
			if err := execCommandAction(context.Background(), config.Action{Type: "command", Cmd: "noop.cmd"}, actx); err != nil {
				t.Fatalf("execCommandAction: %v", err)
			}

			gotEcho := strings.Contains(buf.String(), "$ dwe noop.cmd")
			if gotEcho != tc.wantEcho {
				t.Fatalf("level=%v: echo present=%v, want %v (buf=%q)", tc.level, gotEcho, tc.wantEcho, buf.String())
			}
		})
	}
}

// TestExecCommandAction_NoEchoWhenUnresolved asserts that an unknown command
// errors before any echo (the echo is placed after successful resolution).
func TestExecCommandAction_NoEchoWhenUnresolved(t *testing.T) {
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	reg := usercommands.NewEmptyRegistry()
	actx := ActionContext{WorkDir: t.TempDir(), Cfg: &config.DweConfig{Raw: map[string]any{}}, Reg: reg}
	if err := execCommandAction(context.Background(), config.Action{Type: "command", Cmd: "missing.cmd"}, actx); err == nil {
		t.Fatal("expected error for unresolved command")
	}
	if buf.Len() != 0 {
		t.Fatalf("echoed before resolution succeeded: %q", buf.String())
	}
}

// TestParallelGroup_TraceAttributesEchoToSubStepLog exercises per-sub-step trace
// routing end-to-end: in a parallel group at Verbose, each sub-step's command echo
// lands in its own sub-step log (via the ctx-attached writerLinePrinter) and never
// in a sibling's.
func TestParallelGroup_TraceAttributesEchoToSubStepLog(t *testing.T) {
	trace.Configure(nil, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	tmp := t.TempDir()
	rep := &mockReporter{}
	phase := config.DeployPhase{Name: "p"}
	group := buildParallelGroupStep(phase, "g", true, 0, []config.DeployStep{
		{Name: "alpha", Type: "shell", Cmd: "echo alpha-out"},
		{Name: "beta", Type: "shell", Cmd: "echo beta-out"},
	})

	opts := RunOptions{
		Steps:       []ResolvedStep{group},
		Reporter:    rep,
		Name:        "deploy",
		Config:      &config.DweConfig{Raw: map[string]any{}},
		WorkDir:     tmp,
		LogWriter:   &syncBuf{},
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alpha := readFile(t, filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "alpha.log"))
	beta := readFile(t, filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", "beta.log"))

	if !strings.Contains(alpha, "$ sh -c 'echo alpha-out'") {
		t.Errorf("alpha.log missing its own command echo: %q", alpha)
	}
	if strings.Contains(alpha, "echo beta-out") {
		t.Errorf("alpha.log leaked sibling command echo: %q", alpha)
	}
	if !strings.Contains(beta, "$ sh -c 'echo beta-out'") {
		t.Errorf("beta.log missing its own command echo: %q", beta)
	}
	if strings.Contains(beta, "echo alpha-out") {
		t.Errorf("beta.log leaked sibling command echo: %q", beta)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// writeSecretProject writes a minimal project whose vars carry age markers for
// the given plaintexts (var name → plaintext) and installs the matching
// identity in DWE_AGE_KEY, so config.LoadConfig decrypts them and registers the
// plaintexts with the trace redactor. It returns the workspace.yml path.
func writeSecretProject(t *testing.T, vars map[string]string) string {
	t.Helper()
	id, err := secrets.Keygen()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}
	t.Setenv(secrets.EnvKey, id.Export())

	var b strings.Builder
	b.WriteString("schema_version: \"1\"\nproject:\n  name: tracetest\n  prefix: dwe\nsecrets:\n  recipient: ")
	b.WriteString(id.Recipient())
	b.WriteString("\nvars:\n")
	for _, name := range slices.Sorted(maps.Keys(vars)) {
		marker, err := secrets.Encrypt(vars[name], id.Recipient())
		if err != nil {
			t.Fatalf("encrypt %s: %v", name, err)
		}
		b.WriteString("  " + name + ": \"" + marker + "\"\n")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	return path
}

// loadSecretProject loads a project written by writeSecretProject, asserting
// the markers actually decrypted (a silently unresolved marker would make every
// redaction assertion below vacuous).
func loadSecretProject(t *testing.T, path string) *config.DweConfig {
	t.Helper()
	trace.ResetRedaction()
	t.Cleanup(trace.ResetRedaction)
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.SecretsState.Unresolved) != 0 {
		t.Fatalf("markers unresolved: %+v", cfg.SecretsState.Unresolved)
	}
	if len(cfg.SecretsState.Decrypted) == 0 {
		t.Fatal("no marker decrypted — the fixture would not exercise redaction")
	}
	return cfg
}

// TestVerboseEcho_RedactsDecryptedSecret pins decision 10 end to end at the
// executor: a resolved step command carrying a value LoadConfig decrypted is
// echoed as *** at -v. The control half (after ResetRedaction the same echo
// prints the plaintext) proves the assertion is not vacuous.
func TestVerboseEcho_RedactsDecryptedSecret(t *testing.T) {
	const plain = "s3cret-plaintext-value"
	cfg := loadSecretProject(t, writeSecretProject(t, map[string]string{"token": plain}))

	step, err := RenderStep(cfg, config.DeployStep{Name: "s", Type: "shell", Cmd: "true ${vars.token}"})
	if err != nil {
		t.Fatalf("RenderStep: %v", err)
	}
	if !strings.Contains(step.Cmd, plain) {
		t.Fatalf("resolved cmd = %q, want the decrypted value substituted", step.Cmd)
	}

	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: cfg}
	if err := execShellAction(context.Background(), config.Action{Type: "shell", Cmd: step.Cmd}, actx); err != nil {
		t.Fatalf("execShellAction: %v", err)
	}
	if got := buf.String(); strings.Contains(got, plain) {
		t.Fatalf("echo leaked the decrypted value: %q", got)
	} else if !strings.Contains(got, "$ sh -c 'true ***'") {
		t.Fatalf("echo = %q, want the value replaced by ***", got)
	}

	// Control: without the registration the very same echo is plaintext.
	trace.ResetRedaction()
	buf.Reset()
	if err := execShellAction(context.Background(), config.Action{Type: "shell", Cmd: step.Cmd}, actx); err != nil {
		t.Fatalf("execShellAction (control): %v", err)
	}
	if got := buf.String(); !strings.Contains(got, plain) {
		t.Fatalf("control echo = %q, want the plaintext after ResetRedaction", got)
	}
}

// TestVerboseEcho_RedactsSecretWithApostrophe covers the per-argument pass in
// trace.Command: quoteArg escapes an embedded apostrophe by breaking out of the
// surrounding single quotes, so an emit-only pass would no longer find the
// value as a substring. The context is pre-cancelled so the (deliberately
// unbalanced) shell command never launches a child.
func TestVerboseEcho_RedactsSecretWithApostrophe(t *testing.T) {
	const plain = "pa'ss word-secret"
	cfg := loadSecretProject(t, writeSecretProject(t, map[string]string{"quoted": plain}))

	step, err := RenderStep(cfg, config.DeployStep{Name: "s", Type: "shell", Cmd: "true ${vars.quoted}"})
	if err != nil {
		t.Fatalf("RenderStep: %v", err)
	}

	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the child never launches; the echo already fired

	actx := ActionContext{WorkDir: t.TempDir(), Cfg: cfg}
	_ = execShellAction(ctx, config.Action{Type: "shell", Cmd: step.Cmd}, actx)

	got := buf.String()
	if strings.Contains(got, "pa") && strings.Contains(got, "ss word-secret") {
		t.Fatalf("echo leaked the apostrophe-bearing value: %q", got)
	}
	if !strings.Contains(got, "$ sh -c 'true ***'") {
		t.Fatalf("echo = %q, want the value replaced by ***", got)
	}
}

// TestParallelGroup_SubStepLogsAreRedacted pins that the .dwe/logs mirrors of
// parallel sub-step traces go through the same emit-level redaction as the
// terminal echo — they are written by the ctx-attached printer, the one path
// that bypasses both the global stack and the fallback writer.
func TestParallelGroup_SubStepLogsAreRedacted(t *testing.T) {
	const plain = "parallel-log-secret"
	cfg := loadSecretProject(t, writeSecretProject(t, map[string]string{"token": plain}))

	trace.Configure(nil, trace.LevelVerbose)
	defer trace.Configure(nil, trace.LevelOff)

	sub := make([]config.DeployStep, 0, 2)
	for _, name := range []string{"alpha", "beta"} {
		step, err := RenderStep(cfg, config.DeployStep{Name: name, Type: "shell", Cmd: "true ${vars.token}"})
		if err != nil {
			t.Fatalf("RenderStep %s: %v", name, err)
		}
		sub = append(sub, step)
	}

	tmp := t.TempDir()
	phase := config.DeployPhase{Name: "p"}
	opts := RunOptions{
		Steps:       []ResolvedStep{buildParallelGroupStep(phase, "g", true, 0, sub)},
		Reporter:    &mockReporter{},
		Name:        "deploy",
		Config:      cfg,
		WorkDir:     tmp,
		LogWriter:   &syncBuf{},
		Recorder:    &mockRecorder{},
		SkipDecider: func(addr string, rs ResolvedStep, h string) journal.Decision { return journal.Run },
	}
	if err := RunWithOptions(opts); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, name := range []string{"alpha", "beta"} {
		log := readFile(t, filepath.Join(tmp, ".dwe", "logs", "parallel", "deploy", "g", name+".log"))
		if strings.Contains(log, plain) {
			t.Errorf("%s.log leaked the decrypted value: %q", name, log)
		}
		if !strings.Contains(log, "$ sh -c 'true ***'") {
			t.Errorf("%s.log = %q, want the value replaced by ***", name, log)
		}
	}
}
