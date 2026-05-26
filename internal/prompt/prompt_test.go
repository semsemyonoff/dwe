package prompt

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: my-proj\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: walked\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project: {}\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: chk\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: x\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "pending: {}\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: partial\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: failed\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\npending: {}\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: partial\npending: {}\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: failed\npending: {}\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: not_deployed\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project: [this is: not valid\n  - bad\n")
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
				writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n  extra: stuff\nservices:\n  db:\n    status: deployed\nunknown_top_level: 42\n")
				return root
			},
			args:       nil,
			wantCode:   0,
			wantStdout: "{▪} p ✓\n",
		},
		{
			name: "corrupted_devbox_yml",
			setup: func(t *testing.T) string {
				root := t.TempDir()
				writeFile(t, filepath.Join(root, "devbox.yml"), "project: [this is: not valid yaml\n  - bad\n")
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
		styles     string // contents of devbox/styles.yml; empty string means file absent
		state      string // contents of .devbox/deploy/state.yml; empty means absent
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
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xEA, 0xB3, 0x08, "⟳") + "\n",
		},
		{
			name:       "partial_uses_warning_default",
			state:      "project:\n  status: partial\n",
			wantStdout: "{" + sgr(0x2E, 0xC3, 0xEB, "▪") + "} p " + sgr(0xEA, 0xB3, 0x08, "⚠") + "\n",
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
			writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
			if tt.styles != "" {
				writeFile(t, filepath.Join(root, "devbox/styles.yml"), tt.styles)
			}
			if tt.state != "" {
				writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), tt.state)
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
	writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
	writeFile(t, filepath.Join(root, "devbox/styles.yml"),
		"colors:\n  accent: \"#010203\"\n  success: \"#040506\"\n")
	writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n")

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
	if got != "" && (containsEsc(got)) {
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
				_ = os.Unsetenv("NO_COLOR")
			}
			root := t.TempDir()
			writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
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
	writeFile(t, filepath.Join(root, "devbox.yml"), "project:\n  name: p\n")
	writeFile(t, filepath.Join(root, ".devbox/deploy/state.yml"), "project:\n  status: deployed\n")
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
// 50 ms cold-start budget is verified manually with `time devbox prompt`.
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
	if err := os.WriteFile(filepath.Join(root, "devbox.yml"),
		[]byte("project:\n  name: bench\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "devbox"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "devbox", "styles.yml"),
		[]byte("colors:\n  accent: \"#2EC3EB\"\n  success: \"#22C55E\"\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".devbox", "deploy"), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".devbox", "deploy", "state.yml"),
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

func TestParseHex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in                  string
		wantR, wantG, wantB uint8
		wantOK              bool
	}{
		{in: "#2EC3EB", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
		{in: "2EC3EB", wantR: 0x2E, wantG: 0xC3, wantB: 0xEB, wantOK: true},
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
