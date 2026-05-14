package command

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/config"
	pipeline "devbox-cli/internal/pipeline"
)

// --- ExecStep / builtin validation tests ---

// TestExecBuiltinStep_validatesBeforeRun verifies that ExecStep enforces
// builtin validation before running, so devbox deploy step / reset step
// cannot bypass it.
func TestExecBuiltinStep_validatesBeforeRun(t *testing.T) {
	cases := []struct {
		name    string
		step    config.DeployStep
		wantErr string
	}{
		{
			name: "unknown builtin name", step: config.DeployStep{Type: "builtin", Cmd: "typo_name"},
			wantErr: `invalid builtin "typo_name"`,
		},
		{
			name: "remove_paths missing paths param", step: config.DeployStep{Type: "builtin", Cmd: "remove_paths", With: map[string]any{}},
			wantErr: `invalid builtin "remove_paths"`,
		},
		{
			name: "remove_paths with root-equivalent path", step: config.DeployStep{Type: "builtin", Cmd: "remove_paths", With: map[string]any{"paths": []any{"."}}},
			wantErr: `invalid builtin "remove_paths"`,
		},
		{
			name: "service_configs_copy with invalid mode", step: config.DeployStep{
				Type: "builtin",
				Cmd:  "service_configs_copy",
				With: map[string]any{"service": "main", "mode": "bogus"},
			},
			wantErr: `invalid builtin "service_configs_copy"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := pipeline.ExecStep(tc.step, t.TempDir(), &config.DevboxConfig{}, nil, nil, false)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// --- copyConfigFile / updateEnvFile tests ---

func TestCopyConfigFile_defaultSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	if err := os.WriteFile(src, []byte("KEY=newvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("KEY=oldvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=oldvalue\n" {
		t.Errorf("default mode: dest was overwritten, got %q", string(got))
	}
}

func TestCopyConfigFile_defaultCreatesWhenMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "subdir", "dest.env")

	if err := os.WriteFile(src, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "default"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=value\n" {
		t.Errorf("default mode: expected src content, got %q", string(got))
	}
}

func TestCopyConfigFile_replaceOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	if err := os.WriteFile(src, []byte("KEY=newvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("KEY=oldvalue\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "replace"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != "KEY=newvalue\n" {
		t.Errorf("replace mode: expected new value, got %q", string(got))
	}
}

func TestCopyConfigFile_updateMergesNewKeys(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	srcContent := "EXISTING=srcval\nNEW_KEY=newval\n"
	destContent := "EXISTING=destval\n"

	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(destContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	result := string(got)

	if strings.Contains(result, "EXISTING=srcval") {
		t.Errorf("update mode: EXISTING was overwritten with src value: %q", result)
	}
	if !strings.Contains(result, "EXISTING=destval") {
		t.Errorf("update mode: EXISTING dest value not preserved: %q", result)
	}
	if !strings.Contains(result, "NEW_KEY=newval") {
		t.Errorf("update mode: NEW_KEY not appended: %q", result)
	}
}

func TestCopyConfigFile_updatePreservesExistingValues(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	srcContent := "DB_HOST=db\nAPP_KEY=\n"
	destContent := "DB_HOST=mydb\nAPP_KEY=base64:secret\nEXTRA=custom\n"

	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte(destContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	result := string(got)

	if !strings.Contains(result, "DB_HOST=mydb") {
		t.Errorf("update mode: DB_HOST was changed from user value: %q", result)
	}
	if !strings.Contains(result, "APP_KEY=base64:secret") {
		t.Errorf("update mode: APP_KEY was changed from user value: %q", result)
	}
	if !strings.Contains(result, "EXTRA=custom") {
		t.Errorf("update mode: EXTRA was removed: %q", result)
	}
}

func TestCopyConfigFile_updateCreatesWhenDestMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.env")
	dest := filepath.Join(dir, "dest.env")

	srcContent := "KEY=value\n"
	if err := os.WriteFile(src, []byte(srcContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := builtin.CopyConfigFile(src, dest, "update"); err != nil {
		t.Fatalf("copyConfigFile: %v", err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != srcContent {
		t.Errorf("update mode (dest missing): expected src content, got %q", string(got))
	}
}

// --- envLineKey / parseEnvKeys tests ---

func TestEnvLineKey(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{"KEY=value", "KEY"},
		{"KEY=", "KEY"},
		{"KEY=val=ue", "KEY"},
		{"# comment", ""},
		{"", ""},
		{"   ", ""},
		{"  KEY = value", "KEY"},
	}
	for _, tc := range cases {
		got := builtin.EnvLineKey(tc.line)
		if got != tc.want {
			t.Errorf("envLineKey(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestParseEnvKeys(t *testing.T) {
	data := []byte("# comment\nFOO=1\nBAR=2\n\nBAZ=3\n")
	keys := builtin.ParseEnvKeys(data)
	for _, k := range []string{"FOO", "BAR", "BAZ"} {
		if !keys[k] {
			t.Errorf("parseEnvKeys: expected key %q", k)
		}
	}
	if keys["# comment"] {
		t.Error("parseEnvKeys: comment line should not be a key")
	}
}

// --- command flag tests ---

func TestDeployRunNoUIFlag(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	deployCmd := newDeployRunCmd(flags)
	if f := deployCmd.Flags().Lookup("ui"); f != nil {
		t.Error("deploy run should not have --ui flag after TUI removal")
	}
}

func TestResetRunNoUIFlag(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	resetCmd := newResetRunCmd(flags)
	if f := resetCmd.Flags().Lookup("ui"); f != nil {
		t.Error("reset run should not have --ui flag after TUI removal")
	}
}

// --- deploy state tracking tests ---

func TestDeployRunCmd_StateFlags(t *testing.T) {
	// Verify that the deploy run command has the required state flags
	flags := &rootFlags{configPath: "devbox.yml"}
	deployCmd := newDeployRunCmd(flags)

	tests := []struct {
		flagName string
		wantFlag bool
	}{
		{"force", true},
		{"resume", true},
		{"non-interactive", true},
		{"service", true},
	}

	for _, tt := range tests {
		f := deployCmd.Flags().Lookup(tt.flagName)
		if tt.wantFlag && f == nil {
			t.Errorf("deploy run should have --%s flag", tt.flagName)
		}
		if !tt.wantFlag && f != nil {
			t.Errorf("deploy run should not have --%s flag", tt.flagName)
		}
	}
}

func TestDeployRunCmd_ForceFlagBypass(t *testing.T) {
	// Test that --force flag bypasses state checks
	t.Run("force flag exists", func(t *testing.T) {
		flags := &rootFlags{configPath: "devbox.yml"}
		deployCmd := newDeployRunCmd(flags)
		forceFlag := deployCmd.Flags().Lookup("force")
		if forceFlag == nil {
			t.Error("--force flag is required for idempotent deploys")
			return
		}
		if forceFlag.Value.Type() != "bool" {
			t.Errorf("--force should be a boolean flag, got %s", forceFlag.Value.Type())
		}
	})
}

func TestDeployRunCmd_ResumeFlagPresent(t *testing.T) {
	// Test that --resume flag is available
	flags := &rootFlags{configPath: "devbox.yml"}
	deployCmd := newDeployRunCmd(flags)
	resumeFlag := deployCmd.Flags().Lookup("resume")
	if resumeFlag == nil {
		t.Error("--resume flag is required for continuing from failed deploys")
	}
}

func TestDeployRunCmd_NonInteractiveFlagPresent(t *testing.T) {
	// Test that -y/--non-interactive flag is available
	flags := &rootFlags{configPath: "devbox.yml"}
	deployCmd := newDeployRunCmd(flags)
	niFlag := deployCmd.Flags().Lookup("non-interactive")
	if niFlag == nil {
		t.Error("--non-interactive flag is required for CI environments")
	}
	if niFlag != nil && niFlag.Shorthand == "" {
		t.Error("--non-interactive flag should have a short form (-y)")
	}
}
