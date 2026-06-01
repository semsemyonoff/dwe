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
	got := render("evil\x1b[31mred\nline", statusNone, pal, false)
	want := "{▪} evil[31mredline\n"
	if got != want {
		t.Errorf("render with control chars: got %q, want %q", got, want)
	}
	if containsEsc(got) {
		t.Errorf("output still contains ESC sequence: %q", got)
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
