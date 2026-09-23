package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/secrets"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// stubDockerOnPath puts a no-op `docker` first on PATH so Run can spawn the
// compose child without a daemon.
func stubDockerOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing stub docker: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestRunners_TraceEchoesSpawnedArgv pins that `-v` echoes the exact compose
// argv a service command spawns — and that the default level emits nothing.
func TestRunners_TraceEchoesSpawnedArgv(t *testing.T) {
	stubDockerOnPath(t)

	tests := []struct {
		name   string
		runner spec.Runner
		mode   ExecMode
		want   string
	}{
		{
			name:   "service_run",
			runner: &RunRunner{},
			want:   "$ docker compose -p dwe-laravel run --no-deps --entrypoint '' -T app-main php -v",
		},
		{
			name:   "service_exec",
			runner: &ExecRunner{},
			mode:   ExecModeExec,
			want:   "$ docker compose -p dwe-laravel exec -T app-main php -v",
		},
	}
	for _, tt := range tests {
		for lvlName, lvl := range map[string]trace.Level{"off": trace.LevelOff, "verbose": trace.LevelVerbose} {
			t.Run(tt.name+"/"+lvlName, func(t *testing.T) {
				var buf bytes.Buffer
				trace.Configure(&buf, lvl)
				t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

				rc := makeServiceExecCtx("app-main", "", "", tt.mode, "", []string{"php", "-v"})
				rc.ProjectRoot = t.TempDir()
				rc.Stdout = &bytes.Buffer{}
				rc.Stderr = &bytes.Buffer{}
				if err := tt.runner.Run(context.Background(), rc); err != nil {
					t.Fatalf("Run: %v", err)
				}

				got := strings.TrimRight(buf.String(), "\n")
				if lvl == trace.LevelOff {
					if got != "" {
						t.Fatalf("default level emitted %q, want nothing", got)
					}
					return
				}
				if got != tt.want {
					t.Fatalf("trace line:\n got %q\nwant %q", got, tt.want)
				}
			})
		}
	}
}

// TestRunners_TraceRedactsSecrets pins that the runner echo goes through the
// central redactor, so a decrypted secret in an argv never reaches stderr.
func TestRunners_TraceRedactsSecrets(t *testing.T) {
	stubDockerOnPath(t)
	var buf bytes.Buffer
	trace.Configure(&buf, trace.LevelVerbose)
	trace.RegisterRedaction([]string{"hunter22secret"})
	t.Cleanup(func() {
		trace.Configure(nil, trace.LevelOff)
		trace.ResetRedaction()
	})

	rc := makeServiceExecCtx("app-main", "", "", ExecModeRun, "", []string{"login", "hunter22secret"})
	rc.ProjectRoot = t.TempDir()
	rc.Stdout = &bytes.Buffer{}
	rc.Stderr = &bytes.Buffer{}
	if err := (&RunRunner{}).Run(context.Background(), rc); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := buf.String()
	if strings.Contains(got, "hunter22secret") {
		t.Fatalf("secret leaked into the trace echo: %q", got)
	}
	if !strings.Contains(got, secrets.RedactPlaceholder) {
		t.Fatalf("trace echo %q lacks the redaction placeholder", got)
	}
}

// TestBuildCommand_EnvFlagsSortedAndValueless pins that the `-e KEY` flags come
// out in sorted order (map iteration would reshuffle argv, and so the -v echo,
// between runs) and that no value lands in argv — values travel via cmd.Env.
func TestBuildCommand_EnvFlagsSortedAndValueless(t *testing.T) {
	rc := makeServiceExecCtx("app-main", "", "", ExecModeExec, "", []string{"env"})
	rc.Cmd.Env = map[string]string{
		"ZETA": "zeta-value", "ALPHA": "alpha-value", "MID": "mid-value",
		"BETA": "beta-value", "OMEGA": "omega-value",
	}
	want := []string{"ALPHA", "BETA", "MID", "OMEGA", "ZETA"}

	for range 20 {
		c, err := (&ExecRunner{}).BuildCommand(context.Background(), rc, testCompose("dwe-laravel", nil))
		if err != nil {
			t.Fatalf("BuildCommand: %v", err)
		}
		var keys []string
		for i, a := range c.Args {
			if a == "-e" && i+1 < len(c.Args) {
				keys = append(keys, c.Args[i+1])
			}
			if strings.Contains(a, "-value") {
				t.Fatalf("env value %q leaked into argv %q", a, c.Args)
			}
		}
		if !slices.Equal(keys, want) {
			t.Fatalf("-e keys = %v, want %v", keys, want)
		}
		if !slices.Contains(c.Env, "ALPHA=alpha-value") {
			t.Fatalf("cmd.Env lacks ALPHA=alpha-value")
		}
	}
}

// TestExecRunner_ProbeEchoesAtDebugOnly pins the exec-or-run container probe
// (`docker compose ps`) to Debug: -v shows the spawned command, not the
// read-only probe that chose between exec and run.
func TestExecRunner_ProbeEchoesAtDebugOnly(t *testing.T) {
	stubDockerOnPath(t)
	for _, lvl := range []trace.Level{trace.LevelVerbose, trace.LevelDebug} {
		var buf bytes.Buffer
		trace.Configure(&buf, lvl)
		t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

		rc := makeServiceExecCtx("app-main", "", "", model.ExecModeExecOrRun, "", []string{"php", "-v"})
		rc.ProjectRoot = t.TempDir()
		rc.Stdout = &bytes.Buffer{}
		rc.Stderr = &bytes.Buffer{}
		if err := (&ExecRunner{}).Run(context.Background(), rc); err != nil {
			t.Fatalf("Run: %v", err)
		}
		got := buf.String()
		probed := strings.Contains(got, " ps --status running")
		if probed != (lvl == trace.LevelDebug) {
			t.Errorf("level %d: probe echoed = %v\ntrace:\n%s", lvl, probed, got)
		}
		if !strings.Contains(got, " run --no-deps ") {
			t.Errorf("level %d: spawned run not echoed\ntrace:\n%s", lvl, got)
		}
	}
}
