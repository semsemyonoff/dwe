package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// resetTraceState restores the package-global trace + slog state after a test
// that exercises PersistentPreRunE (which calls trace.Configure and may install
// the slog handler at Debug). Also pins DWE_DEBUG out of the host environment so
// the flag→level mapping under test is authoritative.
func resetTraceState(t *testing.T) {
	t.Helper()
	oldDweDebug, hadDweDebug := os.LookupEnv("DWE_DEBUG")
	_ = os.Unsetenv("DWE_DEBUG")
	prevSlog := slog.Default()
	t.Cleanup(func() {
		if hadDweDebug {
			_ = os.Setenv("DWE_DEBUG", oldDweDebug)
		} else {
			_ = os.Unsetenv("DWE_DEBUG")
		}
		trace.Configure(nil, trace.LevelOff)
		slog.SetDefault(prevSlog)
	})
}

// captureRealStderr swaps os.Stderr for an os.Pipe for the duration of fn and
// returns whatever was written to it. trace.Configure(os.Stderr, …) captures the
// real os.Stderr inside PersistentPreRunE, so the swap must wrap Execute. The
// diagnostic payloads here are tiny single lines, well within the pipe buffer, so
// a synchronous read after closing the write end will not deadlock.
func captureRealStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	old := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = old }()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading captured stderr: %v", err)
	}
	_ = r.Close()
	return string(out)
}

// assertSingleJSONDocument fails unless b decodes as exactly one JSON value with
// no trailing tokens — the core stdout contract that diagnostic flags must not
// break.
func assertSingleJSONDocument(t *testing.T, b []byte) {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout: %q", err, string(b))
	}
	if dec.More() {
		t.Fatalf("stdout has more than one JSON document: %q", string(b))
	}
	// Confirm the stream is fully consumed (only EOF remains).
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("stdout has trailing content after the JSON document (err=%v): %q", err, string(b))
	}
}

// TestRootJSON_DiagnosticFlagsKeepStdoutClean verifies that adding -v or --debug
// to a read-only JSON command leaves stdout a single valid JSON document, and
// that any diagnostics land on the real stderr (never on stdout).
func TestRootJSON_DiagnosticFlagsKeepStdoutClean(t *testing.T) {
	resetTraceState(t)

	cases := []struct {
		name string
		flag string
		// wantStderrSubstr, if non-empty, must appear on the captured os.Stderr,
		// proving the diagnostic channel is exercised and routed off stdout.
		wantStderrSubstr string
	}{
		{"verbose", "--verbose", ""},
		{"debug", "--debug", "config loaded"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := makeV2Project(t, dir)

			root, _ := NewRootCmdWithFlags()
			var out, errBuf bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errBuf)
			root.SetArgs([]string{"--output", "json", tc.flag})
			if err := root.PersistentFlags().Set("config", cfgPath); err != nil {
				t.Fatalf("setting config: %v", err)
			}

			stderr := captureRealStderr(t, func() {
				if err := root.Execute(); err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			})

			// stdout must be exactly one JSON document.
			assertSingleJSONDocument(t, out.Bytes())

			// Diagnostics must never bleed into stdout.
			if strings.Contains(out.String(), "config loaded") {
				t.Errorf("diagnostic text leaked into stdout: %q", out.String())
			}

			// At Debug, the config-load summary must appear on the real stderr.
			if tc.wantStderrSubstr != "" && !strings.Contains(stderr, tc.wantStderrSubstr) {
				t.Errorf("expected %q on stderr at %s, got: %q", tc.wantStderrSubstr, tc.flag, stderr)
			}
		})
	}
}

// TestRootJSON_ErrorEnvelopeWithVerbose verifies that when a command fails in
// JSON mode with -v, the error envelope is still the clean, single-document JSON
// structure on the cobra error writer (mirroring main.go's errHandler path), and
// that no partial data was emitted to stdout.
func TestRootJSON_ErrorEnvelopeWithVerbose(t *testing.T) {
	resetTraceState(t)

	// A non-allowlisted command with no discoverable project fails deterministically
	// with a project_not_found coded error from PersistentPreRunE — no docker needed.
	dir := t.TempDir()
	t.Chdir(dir)

	root, flags := NewRootCmdWithFlags()
	var out, errBuf bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs([]string{"--output", "json", "--verbose", "info"})

	var execErr error
	_ = captureRealStderr(t, func() {
		execErr = root.Execute()
	})
	if execErr == nil {
		t.Fatal("expected an error for info with no project, got nil")
	}

	// No data document should have reached stdout.
	if strings.TrimSpace(out.String()) != "" {
		t.Errorf("expected empty stdout on error, got: %q", out.String())
	}

	// main.go's errHandler writes the envelope via cmdctx.WriteError in JSON mode;
	// reproduce that here and assert the envelope is a single clean JSON document.
	cmdctx.WriteError(flags, root, execErr)

	assertSingleJSONDocument(t, errBuf.Bytes())

	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(errBuf.Bytes(), &env); err != nil {
		t.Fatalf("error envelope is not valid JSON: %v\nstderr: %q", err, errBuf.String())
	}
	if env.Error.Code != "project_not_found" {
		t.Errorf("error code: got %q, want project_not_found", env.Error.Code)
	}
}
