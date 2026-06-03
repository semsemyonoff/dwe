package prompt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMain installs a fail-fast docker stub so unit tests never spawn a real
// docker process. Tests that need specific refresh behavior swap it via
// swapDockerPs (which must NOT call t.Parallel — see swapDockerPs docs).
func TestMain(m *testing.M) {
	dockerPsFunc = func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("test stub: docker disabled")
	}
	os.Exit(m.Run())
}

// swapDockerPs replaces dockerPsFunc for the test's duration. Callers must NOT
// call t.Parallel(). The Go testing framework runs non-parallel top-level
// tests sequentially before any parallel test resumes, so swaps performed in
// non-parallel tests do not race with parallel readers.
func swapDockerPs(t *testing.T, fn func(context.Context, string) ([]byte, error)) {
	t.Helper()
	prev := dockerPsFunc
	dockerPsFunc = fn
	t.Cleanup(func() { dockerPsFunc = prev })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestRunFromDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T) (cwd string)
		args       []string
		wantCode   int
		wantStdout string
	}{
		{
			name: "in_project_name_from_config",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: my-proj\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} my-proj\n",
		},
		{
			name: "in_subdirectory_walk_up",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: walked\n")
				sub := filepath.Join(root, "a", "b", "c")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				return sub
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} walked\n",
		},
		{
			name: "name_fallback_to_dir_basename",
			setup: func(t *testing.T) string {
				root := filepath.Join(t.TempDir(), "fallback-dir")
				if err := os.Mkdir(root, 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, "workspace.yml"), "project: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} fallback-dir\n",
		},
		{
			name: "outside_any_project",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			args:       nil,
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "check_inside_project",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: chk\n")
				return root
			},
			args:       []string{"--check"},
			wantCode:   0,
			wantStdout: "",
		},
		{
			name: "check_outside_any_project",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			args:       []string{"--check"},
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "unknown_arg",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: x\n")
				return root
			},
			args:       []string{"foo"},
			wantCode:   1,
			wantStdout: "",
		},
		{
			name: "status_state_file_absent",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_pending_only",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "pending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⟳\n",
		},
		{
			name: "status_deployed",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: deployed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✓\n",
		},
		{
			name: "status_partial",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: partial\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⚠\n",
		},
		{
			name: "status_failed",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: failed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✗\n",
		},
		{
			name: "status_deployed_plus_pending_pending_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: deployed\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⟳\n",
		},
		{
			name: "status_partial_plus_pending_partial_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: partial\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ⚠\n",
		},
		{
			name: "status_failed_plus_pending_failed_wins",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: failed\npending: {}\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✗\n",
		},
		{
			name: "status_not_deployed_no_icon",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: not_deployed\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_corrupted_state_yml_no_icon",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project: [this is: not valid\n  - bad\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p\n",
		},
		{
			name: "status_state_yml_with_unknown_fields_ignored",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: deployed\n  extra: stuff\nservices:\n  db:\n    status: deployed\nunknown_top_level: 42\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✓\n",
		},
		{
			name: "corrupted_workspace_yml",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "workspace.yml"), "project: [this is: not valid yaml\n  - bad\n")
				return root
			},
			args:       nil,
			wantCode:   1,
			wantStdout: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cwd := tt.setup(t)
			var buf bytes.Buffer
			code := runFromDir(&buf, tt.args, cwd, false)
			if code != tt.wantCode {
				t.Errorf("exit code: got %d, want %d (stdout=%q)", code, tt.wantCode, buf.String())
			}
			if got := buf.String(); got != tt.wantStdout {
				t.Errorf("stdout: got %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

// sgr is a test helper that formats a TrueColor SGR-wrapped glyph the same way
// production code does. Tests build expected strings with it so a renderer
// change that breaks SGR formatting flags them immediately.
func sgr(r, g, b uint8, glyph string) string {
	return "\x1b[38;2;" +
		strconv.Itoa(int(r)) + ";" +
		strconv.Itoa(int(g)) + ";" +
		strconv.Itoa(int(b)) + "m" +
		glyph + "\x1b[39m"
}

func TestRunFromDirColor(t *testing.T) {
	t.Parallel()

	const stylesAllCustom = "colors:\n" +
		"  accent: \"#010203\"\n" +
		"  success: \"#040506\"\n" +
		"  warning: \"#070809\"\n" +
		"  danger: \"#0A0B0C\"\n"

	tests := []struct {
		name       string
		styles     string // contents of workspace/styles.yml; empty string means file absent
		state      string // contents of .dwe/deploy/state.yml; empty means absent
		wantStdout string
	}{
		{
			name:       "defaults_when_styles_absent_no_status",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p\n",
		},
		{
			name:       "custom_accent_applied",
			styles:     stylesAllCustom,
			wantStdout: "{" + sgr(0x01, 0x02, 0x03, "▪") + "} p\n",
		},
		{
			name:       "deployed_uses_success_default",
			state:      "project:\n  status: deployed\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0x22, 0xC5, 0x5E, "✓") + "\n",
		},
		{
			name:       "deployed_uses_custom_success",
			styles:     stylesAllCustom,
			state:      "project:\n  status: deployed\n",
			wantStdout: "{" + sgr(0x01, 0x02, 0x03, "▪") + "} p " + sgr(0x04, 0x05, 0x06, "✓") + "\n",
		},
		{
			name:       "pending_uses_warning_default",
			state:      "pending: {}\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xF5, 0x9E, 0x0B, "⟳") + "\n",
		},
		{
			name:       "partial_uses_warning_default",
			state:      "project:\n  status: partial\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xF5, 0x9E, 0x0B, "⚠") + "\n",
		},
		{
			name:       "failed_uses_danger_default",
			state:      "project:\n  status: failed\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xEF, 0x44, 0x44, "✗") + "\n",
		},
		{
			name:       "empty_individual_token_falls_back_to_default",
			styles:     "colors:\n  accent: \"\"\n  success: \"#112233\"\n",
			state:      "project:\n  status: deployed\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0x11, 0x22, 0x33, "✓") + "\n",
		},
		{
			name:       "malformed_accent_renders_plain_no_panic",
			styles:     "colors:\n  accent: \"not-a-color\"\n",
			wantStdout: "{▪} p\n",
		},
		{
			name:       "malformed_accent_hash_short_renders_plain",
			styles:     "colors:\n  accent: \"#XYZ\"\n",
			wantStdout: "{▪} p\n",
		},
		{
			name:       "malformed_status_color_renders_status_glyph_plain",
			styles:     "colors:\n  success: \"bogus\"\n",
			state:      "project:\n  status: deployed\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p ✓\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
			if tt.styles != "" {
				writeFile(t, filepath.Join(root, "workspace/styles.yml"), tt.styles)
			}
			if tt.state != "" {
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), tt.state)
			}
			var buf bytes.Buffer
			code := runFromDir(&buf, nil, root, true)
			if code != 0 {
				t.Fatalf("exit code: got %d, want 0 (stdout=%q)", code, buf.String())
			}
			if got := buf.String(); got != tt.wantStdout {
				t.Errorf("stdout:\n got %q\nwant %q", got, tt.wantStdout)
			}
		})
	}
}

func TestRunFromDirNoColorSuppression(t *testing.T) {
	t.Parallel()
	// useColor=false simulates the NO_COLOR-set branch (which Run flips off
	// before calling runFromDir). Both NO_COLOR="" (set, empty) and NO_COLOR=1
	// (set, non-empty) take this same branch in Run; LookupEnv treats both as
	// "found = true". Verified by TestRunNoColorEnv below which runs Run().
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
	writeFile(t, filepath.Join(root, "workspace/styles.yml"),
		"colors:\n  accent: \"#010203\"\n  success: \"#040506\"\n")
	writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: deployed\n")

	var buf bytes.Buffer
	code := runFromDir(&buf, nil, root, false)
	if code != 0 {
		t.Fatalf("exit: %d", code)
	}
	got := buf.String()
	want := "{▪} p ✓\n"
	if got != want {
		t.Errorf("stdout: got %q, want %q", got, want)
	}
	if containsEsc(got) {
		t.Errorf("expected no ANSI escapes, got %q", got)
	}
}

func containsEsc(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			return true
		}
	}
	return false
}

func TestRunNoColorEnv(t *testing.T) {
	// Cannot t.Parallel(): t.Setenv forbids parallel tests.
	tests := []struct {
		name   string
		set    bool
		value  string
		wantNo bool // true => expect ANSI suppressed
	}{
		{name: "unset", set: false, wantNo: false},
		{name: "set_empty", set: true, value: "", wantNo: true},
		{name: "set_nonempty", set: true, value: "1", wantNo: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("NO_COLOR", tt.value)
			} else {
				orig, existed := os.LookupEnv("NO_COLOR")
				_ = os.Unsetenv("NO_COLOR")
				t.Cleanup(func() {
					if existed {
						_ = os.Setenv("NO_COLOR", orig)
					}
				})
			}
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
			// chdir into root so Run's os.Getwd lands inside the project.
			t.Chdir(root)

			var buf bytes.Buffer
			code := Run(&buf, nil)
			if code != 0 {
				t.Fatalf("exit: %d", code)
			}
			hasEsc := containsEsc(buf.String())
			if tt.wantNo && hasEsc {
				t.Errorf("expected no ANSI escapes, got %q", buf.String())
			}
			if !tt.wantNo && !hasEsc {
				t.Errorf("expected ANSI escapes, got %q", buf.String())
			}
		})
	}
}

func TestRenderUsesDefaultForegroundReset(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
	writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), "project:\n  status: deployed\n")
	var buf bytes.Buffer
	if code := runFromDir(&buf, nil, root, true); code != 0 {
		t.Fatalf("exit: %d", code)
	}
	out := buf.String()
	if !bytes.Contains([]byte(out), []byte("\x1b[39m")) {
		t.Errorf("expected default-foreground reset \\x1b[39m in %q", out)
	}
	if bytes.Contains([]byte(out), []byte("\x1b[0m")) {
		t.Errorf("did not expect full reset \\x1b[0m in %q", out)
	}
	if !bytes.HasSuffix([]byte(out), []byte("\n")) {
		t.Errorf("expected trailing newline, got %q", out)
	}
}

// BenchmarkPromptRun measures runFromDir for the deployed-status case (the
// most common steady-state). It exercises the full hot path: walk-up, three
// file reads, three yaml unmarshals, and TrueColor render. The end-to-end
// 50 ms cold-start budget is verified manually with `time dwe prompt`.
//
// Baseline (Apple M1 Max, go test -bench=. -benchtime=2s):
//
//	~66 µs/op, ~27 KB/op, ~190 allocs/op
//
// Most allocations come from yaml.Unmarshal reflection on three files.
// Sharp jumps from this baseline (e.g. doubled allocs/op) signal a hot-path
// regression worth investigating before merge.
func BenchmarkPromptRun(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "workspace.yml"),
		[]byte("project:\n  name: bench\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "styles.yml"),
		[]byte("colors:\n  accent: \"#2EC3EB\"\n  success: \"#22C55E\"\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".dwe", "deploy"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".dwe", "deploy", "state.yml"),
		[]byte("project:\n  status: deployed\n"), 0o644); err != nil {
		b.Fatal(err)
	}

	var buf bytes.Buffer
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf.Reset()
		if code := runFromDir(&buf, nil, root, true); code != 0 {
			b.Fatalf("exit: %d", code)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		{in: "my-project", want: "my-project"},
		{in: "hello world", want: "hello world"},
		{in: "tab\there", want: "tabhere"},
		{in: "newline\nhere", want: "newlinehere"},
		{in: "escape\x1b[31mred\x1b[0m", want: "escape[31mred[0m"},
		{in: "del\x7fchar", want: "delchar"},
		{in: "\x01\x02\x03", want: ""},
		{in: "normal 日本語", want: "normal 日本語"},
		{in: "", want: ""},
		// C1 controls (U+0080–U+009F): functional escape introducers in 8-bit terminals.
		// A crafted project.name could use e.g. U+009B (CSI) to inject terminal sequences.
		{in: "a\xc2\x80b", want: "ab"}, // U+0080
		{in: "a\xc2\x9bb", want: "ab"}, // U+009B CSI
		{in: "a\xc2\x9db", want: "ab"}, // U+009D OSC
		{in: "a\xc2\x9fb", want: "ab"}, // U+009F
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeName(tt.in); got != tt.want {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestRenderStripControlCharsFromName(t *testing.T) {
	t.Parallel()
	// Verify render strips control characters from names so a crafted project.name
	// (e.g. from a cloned repo) cannot inject ANSI sequences or newlines into the
	// shell prompt. Call render directly to bypass YAML (which rejects bare control
	// bytes anyway — this exercises the sanitization layer that matters at runtime).
	pal := palette{accent: color{enabled: false}}
	got := render("evil\x1b[31mred\nline", "", statusNone, stackNone, pal, false)
	want := "{▪} evil[31mredline\n"
	if got != want {
		t.Errorf("render with control chars: got %q, want %q", got, want)
	}
	if containsEsc(got) {
		t.Errorf("output still contains ESC sequence: %q", got)
	}
}

// seedServices writes workspace/services/<name>/service.yml fixtures with the
// given `dir:` values, plus any extra subdirs to mkdir under root.
func seedServices(t *testing.T, root string, dirsByName map[string]string) {
	t.Helper()
	for name, dir := range dirsByName {
		yml := "type: app\ncontainer: " + name + "\n"
		if dir != "" {
			yml += "dir: " + dir + "\n"
		}
		writeFile(t, filepath.Join(root, "workspace", "services", name, "service.yml"), yml)
	}
}

// seedServiceYAML writes a single service.yml with arbitrary content (used for
// extends and edge-case fixtures that seedServices's simple map can't express).
func seedServiceYAML(t *testing.T, root, name, body string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "workspace", "services", name, "service.yml"), body)
}

func TestDetectService(t *testing.T) {
	t.Parallel()

	t.Run("services_dir_missing_returns_empty", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if got := detectService(root, root); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("cwd_in_resolved_service_dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "services", "backend")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "backend" {
			t.Errorf("got %q, want backend", got)
		}
	})

	t.Run("cwd_in_deep_subdir_of_service_dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "services", "backend", "src", "api")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "backend" {
			t.Errorf("got %q, want backend", got)
		}
	})

	t.Run("cwd_at_root_no_match", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		if got := detectService(root, root); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("config_dir_no_longer_matches", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		// Standing inside workspace/services/backend (the config dir) must NOT
		// produce a service tag — only the source dir (./services/backend) does.
		cwd := filepath.Join(root, "workspace", "services", "backend")
		if got := detectService(cwd, root); got != "" {
			t.Errorf("got %q, want empty (config dir is not a source mount)", got)
		}
	})

	t.Run("longest_match_wins_on_nested_dirs", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{
			"outer": "./code",
			"inner": "./code/inner",
		})
		cwd := filepath.Join(root, "code", "inner", "sub")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "inner" {
			t.Errorf("got %q, want inner (longest match)", got)
		}
	})

	t.Run("service_without_dir_field_skipped", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"db": ""}) // no dir
		if got := detectService(filepath.Join(root, "anywhere"), root); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("malformed_service_yml_skipped", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "workspace", "services", "broken", "service.yml"),
			"::: not yaml :::")
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "services", "backend", "src")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "backend" {
			t.Errorf("got %q, want backend (malformed sibling ignored)", got)
		}
	})

	t.Run("cwd_outside_any_service_dir", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "docs")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("sibling_path_does_not_match_via_prefix", func(t *testing.T) {
		t.Parallel()
		// Defends against the classic prefix-bug: `services/backend` must not
		// match `services/backend-other` via raw HasPrefix.
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "services", "backend-other", "src")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "" {
			t.Errorf("got %q, want empty (sibling not under backend)", got)
		}
	})

	t.Run("dir_dot_resolves_to_root_is_skipped", func(t *testing.T) {
		t.Parallel()
		// `dir: .` would make resolved == root, which would catch every cwd in
		// the project. Must be silently skipped — otherwise standing in
		// workspace/ or docs/ would render [whole-repo].
		root := t.TempDir()
		seedServices(t, root, map[string]string{"whole-repo": "."})
		if got := detectService(root, root); got != "" {
			t.Errorf("at root: got %q, want empty (dir: . must not catch root)", got)
		}
		sub := filepath.Join(root, "docs")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(sub, root); got != "" {
			t.Errorf("at subdir: got %q, want empty (dir: . must not catch subdirs)", got)
		}
	})

	t.Run("dir_parent_traversal_is_skipped", func(t *testing.T) {
		t.Parallel()
		// `dir: ..` resolves above root and could pollute prompts in unrelated
		// sibling projects sharing the parent. Must be silently skipped.
		root := t.TempDir()
		seedServices(t, root, map[string]string{"escaped": ".."})
		// cwd at root itself — even there, dir:.. cannot win.
		if got := detectService(root, root); got != "" {
			t.Errorf("got %q, want empty (dir: .. must not match anything)", got)
		}
	})

	t.Run("dir_absolute_inside_root_matches", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// Construct an absolute path inside root and seed it as `dir:`.
		// filepath.Join(root, "/abs") would sandbox it under root — the fix
		// must use the absolute path verbatim.
		absDir := filepath.Join(root, "abs", "code")
		seedServices(t, root, map[string]string{"app": absDir})
		cwd := filepath.Join(absDir, "src")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "app" {
			t.Errorf("got %q, want app (absolute dir inside root must match)", got)
		}
	})

	t.Run("dir_absolute_outside_root_skipped", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		// Absolute path pointing somewhere outside root — must be skipped so
		// we don't tag /etc or another project's tree.
		seedServices(t, root, map[string]string{"foreign": "/etc"})
		if got := detectService("/etc", root); got != "" {
			t.Errorf("got %q, want empty (absolute dir outside root must not match)", got)
		}
	})

	t.Run("unclean_cwd_with_trailing_separator_matches", func(t *testing.T) {
		t.Parallel()
		// $PWD-honoring shells can pass cwd with a trailing slash; the new
		// detector must clean cwd before comparing to resolved.
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		cwd := filepath.Join(root, "services", "backend") + string(filepath.Separator)
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "backend" {
			t.Errorf("got %q, want backend (trailing separator must not break match)", got)
		}
	})

	t.Run("unclean_cwd_with_dot_segment_matches", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		seedServices(t, root, map[string]string{"backend": "./services/backend"})
		// cwd with embedded `.` segment, e.g. PWD=/proj/./services/backend
		cwd := filepath.Join(root, "services", "backend")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		sep := string(filepath.Separator)
		dirty := root + sep + "." + sep + filepath.Join("services", "backend")
		if got := detectService(dirty, root); got != "backend" {
			t.Errorf("got %q, want backend (embedded . segment must not break match)", got)
		}
	})

	t.Run("extends_inherits_dir_from_parent", func(t *testing.T) {
		t.Parallel()
		// Child service with no own dir but `extends: parent` must inherit
		// parent's dir so the child is still detectable.
		root := t.TempDir()
		seedServices(t, root, map[string]string{"main": "./code/main"})
		seedServiceYAML(t, root, "main-debug", "type: app\ncontainer: main-debug\nextends: main\n")
		cwd := filepath.Join(root, "code", "main", "src")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		// Both services resolve to the same dir; first-seen-by-ReadDir order
		// (alphabetical) wins. "main" sorts before "main-debug" → "main".
		if got := detectService(cwd, root); got != "main" {
			t.Errorf("got %q, want main (alphabetically first when two services share a resolved dir)", got)
		}
	})

	t.Run("extends_child_unique_dir_match_when_parent_disabled", func(t *testing.T) {
		t.Parallel()
		// Only the extends child exists with inherited dir from a parent
		// that itself is absent → child still detectable via the chain.
		root := t.TempDir()
		seedServices(t, root, map[string]string{"parent": "./code/shared"})
		seedServiceYAML(t, root, "child", "type: app\ncontainer: child\nextends: parent\n")
		// Remove the parent's source dir from the equation by cd-ing into a
		// location only reachable through the inherited dir.
		cwd := filepath.Join(root, "code", "shared", "deep")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		got := detectService(cwd, root)
		if got != "parent" && got != "child" {
			t.Errorf("got %q, want parent or child (both share inherited dir)", got)
		}
	})

	t.Run("extends_cycle_does_not_hang", func(t *testing.T) {
		t.Parallel()
		// a → b → a cycle: resolveDir must short-circuit. Both end up with
		// empty resolved dir and are skipped.
		root := t.TempDir()
		seedServiceYAML(t, root, "a", "type: app\ncontainer: a\nextends: b\n")
		seedServiceYAML(t, root, "b", "type: app\ncontainer: b\nextends: a\n")
		if got := detectService(filepath.Join(root, "anywhere"), root); got != "" {
			t.Errorf("got %q, want empty (cycle must not produce a match)", got)
		}
	})

	t.Run("extends_dead_end_no_match", func(t *testing.T) {
		t.Parallel()
		// Child extends a non-existent parent → resolveDir returns "" → skip.
		root := t.TempDir()
		seedServiceYAML(t, root, "orphan", "type: app\ncontainer: orphan\nextends: ghost\n")
		if got := detectService(filepath.Join(root, "anywhere"), root); got != "" {
			t.Errorf("got %q, want empty (missing extends parent must not match)", got)
		}
	})

	t.Run("extends_long_chain_resolves", func(t *testing.T) {
		t.Parallel()
		// A 12-edge chain (13 nodes) must still resolve — the cap was removed
		// and the per-walk seen-set is the only stopper. Tests both the
		// off-by-one fix and that no fresh ceiling crept back in.
		root := t.TempDir()
		const chainLen = 13
		for i := range chainLen - 1 {
			seedServiceYAML(t, root, fmt.Sprintf("s%02d", i),
				fmt.Sprintf("type: app\ncontainer: s%02d\nextends: s%02d\n", i, i+1))
		}
		// Final node carries the dir.
		seedServices(t, root, map[string]string{
			fmt.Sprintf("s%02d", chainLen-1): "./code/leaf",
		})
		cwd := filepath.Join(root, "code", "leaf")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		// Among the chain, the leaf service AND every inheriting child resolve
		// to the same dir → alphabetically first wins ("s00" — head of chain).
		if got := detectService(cwd, root); got != "s00" {
			t.Errorf("got %q, want s00 (long chain must resolve through every link)", got)
		}
	})

	t.Run("oversized_service_yml_is_skipped", func(t *testing.T) {
		t.Parallel()
		// A service.yml larger than the hot-path cap (64 KB) must be skipped
		// rather than read/parsed — guards the shell prompt against pathological
		// generated YAML or anchor-expansion bombs.
		root := t.TempDir()
		// Build a >64 KB YAML that *would* parse to a valid dir if read.
		var buf strings.Builder
		buf.WriteString("type: app\ncontainer: big\ndir: ./services/big\n")
		// Pad with a long comment to push the file over the cap.
		buf.WriteString("# ")
		buf.WriteString(strings.Repeat("x", 70*1024))
		buf.WriteByte('\n')
		seedServiceYAML(t, root, "big", buf.String())
		// Also seed a normal sibling so detect still functions for valid entries.
		seedServices(t, root, map[string]string{"small": "./code/small"})
		cwd := filepath.Join(root, "services", "big")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(cwd, root); got != "" {
			t.Errorf("got %q, want empty (oversized service.yml must be skipped on hot path)", got)
		}
		// Sanity: small sibling still detects normally.
		smallCwd := filepath.Join(root, "code", "small")
		if err := os.MkdirAll(smallCwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := detectService(smallCwd, root); got != "small" {
			t.Errorf("normal sibling: got %q, want small", got)
		}
	})
}

func TestRunFromDirServiceTag(t *testing.T) {
	t.Parallel()

	// Each test seeds workspace/services/api/service.yml with `dir: ./code/api`
	// and runs the prompt with cwd inside that resolved source path.
	tests := []struct {
		name       string
		sourceSub  string // path under <root>/code/api where the prompt is run; empty = root
		state      string // .dwe/deploy/state.yml contents; empty = absent
		useColor   bool
		wantStdout string
	}{
		{
			name:       "no_service_no_status_no_color",
			wantStdout: "{▪} p\n",
		},
		{
			name:       "service_root_no_status_no_color",
			sourceSub:  ".",
			wantStdout: "{▪} p [api]\n",
		},
		{
			name:       "service_deep_subdir_no_status_no_color",
			sourceSub:  filepath.Join("src", "handlers"),
			wantStdout: "{▪} p [api]\n",
		},
		{
			name:       "service_with_status_no_color",
			sourceSub:  ".",
			state:      "project:\n  status: deployed\n",
			wantStdout: "{▪} p [api] ✓\n",
		},
		{
			name:       "service_with_status_color",
			sourceSub:  ".",
			state:      "project:\n  status: deployed\n",
			useColor:   true,
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p [api] " + sgr(0x22, 0xC5, 0x5E, "✓") + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
			seedServices(t, root, map[string]string{"api": "./code/api"})
			if tt.state != "" {
				writeFile(t, filepath.Join(root, ".dwe/deploy/state.yml"), tt.state)
			}
			cwd := root
			if tt.sourceSub != "" {
				cwd = filepath.Join(root, "code", "api", tt.sourceSub)
				if err := os.MkdirAll(cwd, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var buf bytes.Buffer
			code := runFromDir(&buf, nil, cwd, tt.useColor)
			if code != 0 {
				t.Fatalf("exit: %d (stdout=%q)", code, buf.String())
			}
			if got := buf.String(); got != tt.wantStdout {
				t.Errorf("stdout: got %q, want %q", got, tt.wantStdout)
			}
		})
	}
}

func TestRenderServiceTagSanitized(t *testing.T) {
	t.Parallel()
	// Render directly: service name with control chars + Bidi must be stripped
	// before reaching the output. Ensures detectService -> render handoff is safe.
	pal := palette{accent: color{enabled: false}}
	got := render("p", "ev\x1b[31mil\nsvc", statusNone, stackNone, pal, false)
	want := "{▪} p [ev[31milsvc]\n"
	if got != want {
		t.Errorf("render with control chars in service: got %q, want %q", got, want)
	}
	if containsEsc(got) {
		t.Errorf("output still contains ESC sequence: %q", got)
	}
}

func TestReadPaletteMuted(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                string
		styles              string
		wantR, wantG, wantB uint8
		wantEnabled         bool
	}{
		{name: "default_when_absent", wantR: 0x6B, wantG: 0x72, wantB: 0x80, wantEnabled: true},
		{name: "default_when_empty", styles: "colors:\n  muted: \"\"\n", wantR: 0x6B, wantG: 0x72, wantB: 0x80, wantEnabled: true},
		{name: "custom_token_applied", styles: "colors:\n  muted: \"#112233\"\n", wantR: 0x11, wantG: 0x22, wantB: 0x33, wantEnabled: true},
		{name: "malformed_disables", styles: "colors:\n  muted: \"bogus\"\n", wantEnabled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tt.styles != "" {
				writeFile(t, filepath.Join(root, "workspace/styles.yml"), tt.styles)
			}
			pal := readPalette(root)
			if pal.muted.enabled != tt.wantEnabled {
				t.Fatalf("muted.enabled: got %v, want %v", pal.muted.enabled, tt.wantEnabled)
			}
			if !tt.wantEnabled {
				return
			}
			if pal.muted.r != tt.wantR || pal.muted.g != tt.wantG || pal.muted.b != tt.wantB {
				t.Errorf("muted RGB: got (%d,%d,%d), want (%d,%d,%d)",
					pal.muted.r, pal.muted.g, pal.muted.b, tt.wantR, tt.wantG, tt.wantB)
			}
		})
	}
}

func TestParseHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in                  string
		wantR, wantG, wantB uint8
		wantOK              bool
	}{
		{in: "#2EC3EB", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
		{in: "2EC3EB", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
		{in: "#2ec3eb", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
		{in: "2ec3eb", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
		{in: "#000000", wantR: 0x00, wantG: 0x00, wantB: 0x00, wantOK: true},
		{in: "#ffffff", wantR: 0xFF, wantG: 0xFF, wantB: 0xFF, wantOK: true},
		{in: "", wantOK: false},
		{in: "#XYZ", wantOK: false},
		{in: "#12345", wantOK: false},
		{in: "#1234567", wantOK: false},
		{in: "not-a-color", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			r, g, b, ok := parseHex(tt.in)
			if ok != tt.wantOK || r != tt.wantR || g != tt.wantG || b != tt.wantB {
				t.Errorf("parseHex(%q) = (%d,%d,%d,%v), want (%d,%d,%d,%v)",
					tt.in, r, g, b, ok, tt.wantR, tt.wantG, tt.wantB, tt.wantOK)
			}
		})
	}
}

func TestReadCache(t *testing.T) {
	t.Parallel()

	when := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	whenStr := when.Format(time.RFC3339)

	tests := []struct {
		name        string
		write       bool
		content     string
		wantState   stackKind
		wantUpdated time.Time
		wantOK      bool
	}{
		{
			name:        "valid_running",
			write:       true,
			content:     "updated_at: " + whenStr + "\nstate: running\n",
			wantState:   stackRunning,
			wantUpdated: when,
			wantOK:      true,
		},
		{
			name:        "valid_partial",
			write:       true,
			content:     "updated_at: " + whenStr + "\nstate: partial\n",
			wantState:   stackPartial,
			wantUpdated: when,
			wantOK:      true,
		},
		{
			name:        "valid_stopped",
			write:       true,
			content:     "updated_at: " + whenStr + "\nstate: stopped\n",
			wantState:   stackStopped,
			wantUpdated: when,
			wantOK:      true,
		},
		{
			name:   "missing_file",
			write:  false,
			wantOK: false,
		},
		{
			name:    "bad_state",
			write:   true,
			content: "updated_at: " + whenStr + "\nstate: weird\n",
			wantOK:  false,
		},
		{
			name:    "empty_state",
			write:   true,
			content: "updated_at: " + whenStr + "\n",
			wantOK:  false,
		},
		{
			name:    "bad_yaml",
			write:   true,
			content: "state: [running\n  - oops\n",
			wantOK:  false,
		},
		{
			name:    "bad_timestamp_unparseable",
			write:   true,
			content: "updated_at: not-a-time\nstate: running\n",
			wantOK:  false, // yaml.Unmarshal into time.Time fails on a non-RFC3339 string
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "prompt-cache.yml")
			if tt.write {
				writeFile(t, path, tt.content)
			}
			state, updatedAt, ok := readCache(path)
			if ok != tt.wantOK {
				t.Fatalf("ok: got %v, want %v (state=%v)", ok, tt.wantOK, state)
			}
			if !tt.wantOK {
				return
			}
			if state != tt.wantState {
				t.Errorf("state: got %v, want %v", state, tt.wantState)
			}
			if !updatedAt.Equal(tt.wantUpdated) {
				t.Errorf("updatedAt: got %v, want %v", updatedAt, tt.wantUpdated)
			}
		})
	}
}

func TestReadStack(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-30 * time.Second).Format(time.RFC3339)
	stale := now.Add(-5 * time.Minute).Format(time.RFC3339)

	tests := []struct {
		name    string
		content string // cache content; empty means absent
		want    stackKind
	}{
		{name: "no_cache_returns_none", content: "", want: stackNone},
		{name: "fresh_running", content: "updated_at: " + fresh + "\nstate: running\n", want: stackRunning},
		{name: "fresh_partial", content: "updated_at: " + fresh + "\nstate: partial\n", want: stackPartial},
		{name: "fresh_stopped", content: "updated_at: " + fresh + "\nstate: stopped\n", want: stackStopped},
		// Stale + default fail-fast docker stub → refresh fails → fall back to stale cached value.
		{name: "stale_refresh_fail_falls_back_to_stale", content: "updated_at: " + stale + "\nstate: running\n", want: stackRunning},
		{name: "bad_state_returns_none", content: "updated_at: " + fresh + "\nstate: weird\n", want: stackNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tt.content != "" {
				writeFile(t, filepath.Join(root, ".dwe", "prompt-cache.yml"), tt.content)
			}
			if got := readStack(root, "p", now); got != tt.want {
				t.Errorf("readStack: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStackKindIconAndColor(t *testing.T) {
	t.Parallel()
	pal := palette{
		success: color{r: 0x22, g: 0xC5, b: 0x5E, enabled: true},
		warning: color{r: 0xF5, g: 0x9E, b: 0x0B, enabled: true},
		muted:   color{r: 0x6B, g: 0x72, b: 0x80, enabled: true},
	}
	tests := []struct {
		s        stackKind
		wantIcon string
		wantC    color
	}{
		{s: stackNone, wantIcon: "", wantC: color{}},
		{s: stackRunning, wantIcon: "●", wantC: pal.success},
		{s: stackPartial, wantIcon: "◐", wantC: pal.warning},
		{s: stackStopped, wantIcon: "○", wantC: pal.muted},
	}
	for _, tt := range tests {
		if got := tt.s.icon(); got != tt.wantIcon {
			t.Errorf("stackKind(%d).icon() = %q, want %q", tt.s, got, tt.wantIcon)
		}
		if got := tt.s.color(pal); got != tt.wantC {
			t.Errorf("stackKind(%d).color() = %+v, want %+v", tt.s, got, tt.wantC)
		}
	}
}

func TestRenderStackIcon(t *testing.T) {
	t.Parallel()
	pal := palette{
		accent:  color{r: 0x2E, g: 0xC3, b: 0xEB, enabled: true},
		success: color{r: 0x22, g: 0xC5, b: 0x5E, enabled: true},
		warning: color{r: 0xF5, g: 0x9E, b: 0x0B, enabled: true},
		muted:   color{r: 0x6B, g: 0x72, b: 0x80, enabled: true},
	}

	tests := []struct {
		name     string
		service  string
		status   statusKind
		stack    stackKind
		useColor bool
		want     string
	}{
		{name: "stack_none_omitted", want: "{▪} p\n"},
		{name: "running_no_color", stack: stackRunning, want: "{▪} p ●\n"},
		{name: "partial_no_color", stack: stackPartial, want: "{▪} p ◐\n"},
		{name: "stopped_no_color", stack: stackStopped, want: "{▪} p ○\n"},
		{
			name: "running_with_status_no_color", status: statusDeployed, stack: stackRunning,
			want: "{▪} p ✓ ●\n",
		},
		{
			name: "service_and_running_no_color", service: "api", stack: stackRunning,
			want: "{▪} p [api] ●\n",
		},
		{
			name: "service_status_running_no_color", service: "api", status: statusDeployed, stack: stackRunning,
			want: "{▪} p [api] ✓ ●\n",
		},
		{
			name: "running_with_color", stack: stackRunning, useColor: true,
			want: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0x22, 0xC5, 0x5E, "●") + "\n",
		},
		{
			name: "partial_with_color", stack: stackPartial, useColor: true,
			want: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xF5, 0x9E, 0x0B, "◐") + "\n",
		},
		{
			name: "stopped_with_color", stack: stackStopped, useColor: true,
			want: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0x6B, 0x72, 0x80, "○") + "\n",
		},
		{
			name: "service_status_stack_with_color", service: "api", status: statusDeployed, stack: stackRunning, useColor: true,
			want: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p [api] " + sgr(0x22, 0xC5, 0x5E, "✓") + " " + sgr(0x22, 0xC5, 0x5E, "●") + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := render("p", tt.service, tt.status, tt.stack, pal, tt.useColor)
			if got != tt.want {
				t.Errorf("render: got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunFromDirStackIconFromFreshCache(t *testing.T) {
	t.Parallel()
	// Integration check that runFromDir reads a fresh cache entry and emits the
	// stack icon without hitting docker. Cache timestamp is 30 s in the past,
	// well within the 2-minute TTL, so readStack returns the cached state
	// directly and never calls dockerPsFunc.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
	recent := time.Now().Add(-30 * time.Second).UTC().Format(time.RFC3339)
	writeFile(t, filepath.Join(root, ".dwe", "prompt-cache.yml"),
		"updated_at: "+recent+"\nstate: running\n")
	var buf bytes.Buffer
	if code := runFromDir(&buf, nil, root, false); code != 0 {
		t.Fatalf("exit: %d (stdout=%q)", code, buf.String())
	}
	got := buf.String()
	want := "{▪} p ●\n"
	if got != want {
		t.Errorf("stdout: got %q, want %q", got, want)
	}
}

func TestRunFromDirStackIconStaleCacheRefreshFailFallsBackToStale(t *testing.T) {
	t.Parallel()
	// Stale cache + failing docker stub (set by TestMain default) → readStack
	// falls back to the stale cached value rather than omitting the icon.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "workspace.yml"), "project:\n  name: p\n")
	past := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	writeFile(t, filepath.Join(root, ".dwe", "prompt-cache.yml"),
		"updated_at: "+past+"\nstate: running\n")
	var buf bytes.Buffer
	if code := runFromDir(&buf, nil, root, false); code != 0 {
		t.Fatalf("exit: %d (stdout=%q)", code, buf.String())
	}
	got := buf.String()
	want := "{▪} p ●\n"
	if got != want {
		t.Errorf("stdout: got %q, want %q", got, want)
	}
}

// Task 4 tests: refresh path. These tests MUST NOT call t.Parallel — they
// swap the package-level dockerPsFunc and rely on Go's test framework running
// non-parallel top-level tests sequentially before parallel tests resume.

func TestRefreshStack_TimeoutReturnsNone(t *testing.T) {
	swapDockerPs(t, func(ctx context.Context, _ string) ([]byte, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state, ok := refreshStack(ctx, "p")
	if ok || state != stackNone {
		t.Errorf("got (%v, %v), want (stackNone, false)", state, ok)
	}
}

func TestRefreshStack_OneRunningContainer_ReturnsRunning(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("abc123\n"), nil
	})
	state, ok := refreshStack(context.Background(), "p")
	if !ok || state != stackRunning {
		t.Errorf("got (%v, %v), want (stackRunning, true)", state, ok)
	}
}

func TestRefreshStack_NoContainers_ReturnsStopped(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte(""), nil
	})
	state, ok := refreshStack(context.Background(), "p")
	if !ok || state != stackStopped {
		t.Errorf("got (%v, %v), want (stackStopped, true)", state, ok)
	}
}

func TestRefreshStack_WhitespaceOnly_ReturnsStopped(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("   \n  \n"), nil
	})
	state, ok := refreshStack(context.Background(), "p")
	if !ok || state != stackStopped {
		t.Errorf("got (%v, %v), want (stackStopped, true)", state, ok)
	}
}

func TestReadStack_StaleCache_RefreshOk(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("abc\n"), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	stale := now.Add(-5 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+stale+"\nstate: stopped\n")

	got := readStack(root, "p", now)
	if got != stackRunning {
		t.Fatalf("readStack: got %v, want stackRunning", got)
	}
	state, updatedAt, ok := readCache(cachePath)
	if !ok || state != stackRunning {
		t.Fatalf("expected cache state=running ok=true, got ok=%v state=%v", ok, state)
	}
	if !updatedAt.Equal(now) {
		t.Errorf("expected updatedAt=%v, got %v", now, updatedAt)
	}
}

func TestReadStack_StaleCache_RefreshFail_FallbackToStale(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("docker fail")
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	staleStr := now.Add(-5 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+staleStr+"\nstate: partial\n")

	got := readStack(root, "p", now)
	if got != stackPartial {
		t.Errorf("readStack: got %v, want stackPartial (stale fallback)", got)
	}
	_, updatedAt, ok := readCache(cachePath)
	if !ok || updatedAt.Format(time.RFC3339) != staleStr {
		t.Errorf("cache modified; expected stale timestamp %q unchanged, got %q ok=%v",
			staleStr, updatedAt.Format(time.RFC3339), ok)
	}
}

func TestReadStack_NoCache_RefreshFail_ReturnsNone(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("docker fail")
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	if got := readStack(root, "p", now); got != stackNone {
		t.Errorf("readStack: got %v, want stackNone", got)
	}
}

func TestReadStack_NoCache_RefreshFail_DoesNotPoisonCache(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return nil, errors.New("docker fail")
	})
	root := t.TempDir()
	_ = readStack(root, "p", time.Now())
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no cache file, got stat err=%v", err)
	}
}

func TestReadStack_StaleRunningCache_RefreshReturnsZero_ReturnsStaleNoWrite(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte(""), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	staleStr := now.Add(-5 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+staleStr+"\nstate: running\n")

	got := readStack(root, "p", now)
	if got != stackRunning {
		t.Errorf("readStack: got %v, want stackRunning (stale)", got)
	}
	_, updatedAt, ok := readCache(cachePath)
	if !ok || updatedAt.Format(time.RFC3339) != staleStr {
		t.Errorf("expected cache unchanged; got updatedAt=%v ok=%v",
			updatedAt.Format(time.RFC3339), ok)
	}
}

func TestReadStack_StalePartialCache_RefreshReturnsZero_ReturnsStaleNoWrite(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte(""), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	staleStr := now.Add(-5 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+staleStr+"\nstate: partial\n")

	got := readStack(root, "p", now)
	if got != stackPartial {
		t.Errorf("readStack: got %v, want stackPartial", got)
	}
	_, updatedAt, ok := readCache(cachePath)
	if !ok || updatedAt.Format(time.RFC3339) != staleStr {
		t.Errorf("expected cache unchanged; got updatedAt=%v ok=%v",
			updatedAt.Format(time.RFC3339), ok)
	}
}

func TestReadStack_NoCache_RefreshReturnsZero_ReturnsNoneNoWrite(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte(""), nil
	})
	root := t.TempDir()
	if got := readStack(root, "p", time.Now()); got != stackNone {
		t.Errorf("readStack: got %v, want stackNone", got)
	}
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no cache file, got stat err=%v", err)
	}
}

func TestReadStack_NoCache_RefreshReturnsRunning_Writes(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("abc\n"), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	if got := readStack(root, "p", now); got != stackRunning {
		t.Errorf("readStack: got %v, want stackRunning", got)
	}
	state, updatedAt, ok := readCache(filepath.Join(root, ".dwe", "prompt-cache.yml"))
	if !ok || state != stackRunning {
		t.Errorf("expected cache state=running, ok=%v state=%v", ok, state)
	}
	if !updatedAt.Equal(now) {
		t.Errorf("expected updatedAt=%v, got %v", now, updatedAt)
	}
}

func TestReadStack_StaleRunningCache_RefreshReturnsRunning_RefreshesTimestamp(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("xyz\n"), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	staleStr := now.Add(-10 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+staleStr+"\nstate: running\n")

	got := readStack(root, "p", now)
	if got != stackRunning {
		t.Errorf("readStack: got %v, want stackRunning", got)
	}
	state, updatedAt, ok := readCache(cachePath)
	if !ok || state != stackRunning {
		t.Fatalf("cache state ok=%v state=%v", ok, state)
	}
	if !updatedAt.Equal(now) {
		t.Errorf("expected updatedAt=%v, got %v", now, updatedAt)
	}
}

func TestReadStack_StaleStoppedCache_RefreshReturnsRunning_Promotes(t *testing.T) {
	swapDockerPs(t, func(_ context.Context, _ string) ([]byte, error) {
		return []byte("xyz\n"), nil
	})
	root := t.TempDir()
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	staleStr := now.Add(-10 * time.Minute).Format(time.RFC3339)
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	writeFile(t, cachePath, "updated_at: "+staleStr+"\nstate: stopped\n")

	got := readStack(root, "p", now)
	if got != stackRunning {
		t.Errorf("readStack: got %v, want stackRunning (promoted)", got)
	}
	state, _, _ := readCache(cachePath)
	if state != stackRunning {
		t.Errorf("expected cache state=running, got %v", state)
	}
}

// TestWriteCache_FailureMode_OriginalFileUntouched verifies the atomic
// guarantee: if writeCache fails (CreateTemp denied in a read-only dir), the
// pre-existing cache content is preserved. This is the "panic during write"
// invariant — even when the write path is interrupted, the original survives.
func TestWriteCache_FailureMode_OriginalFileUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires non-root to enforce 0o555 directory permissions")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".dwe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(dir, "prompt-cache.yml")
	original := "updated_at: 2026-01-01T00:00:00Z\nstate: running\n"
	writeFile(t, cachePath, original)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := writeCache(cachePath, stackStopped, time.Now()); err == nil {
		t.Fatalf("expected writeCache to fail with read-only dir, got nil")
	}
	got, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("read original: %v", err)
	}
	if string(got) != original {
		t.Errorf("original modified after failed writeCache; got %q, want %q", got, original)
	}
}

func TestWriteCache_LeftoverTmp_DoesNotBreakNextWrite(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".dwe")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-seed a stale .tmp file as if a prior write was interrupted.
	stale := filepath.Join(dir, "prompt-cache-leftover.tmp")
	if err := os.WriteFile(stale, []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}

	cachePath := filepath.Join(dir, "prompt-cache.yml")
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	if err := writeCache(cachePath, stackRunning, now); err != nil {
		t.Fatalf("writeCache: %v", err)
	}
	state, updatedAt, ok := readCache(cachePath)
	if !ok || state != stackRunning {
		t.Errorf("expected cache state=running, got state=%v ok=%v", state, ok)
	}
	if !updatedAt.Equal(now) {
		t.Errorf("expected updatedAt=%v, got %v", now, updatedAt)
	}
}

func TestWriteCache_SkipsWhenStateNone(t *testing.T) {
	root := t.TempDir()
	cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
	if err := writeCache(cachePath, stackNone, time.Now()); err != nil {
		t.Fatalf("writeCache(stackNone) should return nil, got %v", err)
	}
	if _, err := os.Stat(cachePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no file for stackNone write, got stat err=%v", err)
	}
}

func TestWriteCache_AllStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state stackKind
		want  string
	}{
		{stackRunning, "running"},
		{stackPartial, "partial"},
		{stackStopped, "stopped"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			cachePath := filepath.Join(root, ".dwe", "prompt-cache.yml")
			now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
			if err := writeCache(cachePath, tc.state, now); err != nil {
				t.Fatalf("writeCache(%v): %v", tc.state, err)
			}
			data, err := os.ReadFile(cachePath)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if !bytes.Contains(data, []byte("state: "+tc.want)) {
				t.Errorf("expected state: %s in %q", tc.want, data)
			}
		})
	}
}

func TestReadComposeProjectName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		workspaceYML string
		dockerYML    string
		displayName  string
		want         string
	}{
		{
			name:         "no_prefix_uses_display_name",
			workspaceYML: "project:\n  name: myapp\n",
			displayName:  "myapp",
			want:         "myapp",
		},
		{
			name:         "prefix_prepended",
			workspaceYML: "project:\n  name: myapp\n  prefix: myorg\n",
			displayName:  "myapp",
			want:         "myorg-myapp",
		},
		{
			name:         "docker_yml_literal_overrides_prefix",
			workspaceYML: "project:\n  name: myapp\n  prefix: myorg\n",
			dockerYML:    "project_name: custom-name\n",
			displayName:  "myapp",
			want:         "custom-name",
		},
		{
			name:         "docker_yml_template_syntax_falls_back_to_prefix",
			workspaceYML: "project:\n  name: myapp\n  prefix: myorg\n",
			dockerYML:    "project_name: ${project.prefix}-${project.name}\n",
			displayName:  "myapp",
			want:         "myorg-myapp",
		},
		{
			name:         "docker_yml_absent_falls_back_to_prefix",
			workspaceYML: "project:\n  name: myapp\n  prefix: myorg\n",
			displayName:  "myapp",
			want:         "myorg-myapp",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "workspace.yml"), tc.workspaceYML)
			if tc.dockerYML != "" {
				writeFile(t, filepath.Join(root, "workspace", "docker.yml"), tc.dockerYML)
			}
			got := readComposeProjectName(root, tc.displayName)
			if got != tc.want {
				t.Errorf("readComposeProjectName: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadDockerProjectNameLiteral(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		dockerYML string
		want      string
	}{
		{name: "absent_returns_empty", dockerYML: "", want: ""},
		{name: "literal_name", dockerYML: "project_name: custom\n", want: "custom"},
		{name: "template_returns_empty", dockerYML: "project_name: ${project.name}\n", want: ""},
		{name: "empty_field_returns_empty", dockerYML: "project_name: \"\"\n", want: ""},
		{name: "no_field_returns_empty", dockerYML: "services:\n  web: {}\n", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if tc.dockerYML != "" {
				writeFile(t, filepath.Join(root, "workspace", "docker.yml"), tc.dockerYML)
			}
			got := readDockerProjectNameLiteral(root)
			if got != tc.want {
				t.Errorf("readDockerProjectNameLiteral: got %q, want %q", got, tc.want)
			}
		})
	}
}
