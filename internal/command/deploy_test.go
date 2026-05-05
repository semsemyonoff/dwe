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

// --- config-check command tests ---

func writeConfigCheckFixture(t *testing.T, configFiles []string) (dir string, flags *rootFlags) {
	t.Helper()
	dir = t.TempDir()

	devboxYML := `schema_version: "1"
project:
  name: laravel
  prefix: devbox
`
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devboxYML), 0o644); err != nil {
		t.Fatalf("write devbox.yml: %v", err)
	}

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatalf("mkdir devbox/: %v", err)
	}

	var sb strings.Builder
	for _, f := range configFiles {
		sb.WriteString("\n      - " + f)
	}
	cfgList := sb.String()
	servicesYML := `services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    configs:` + cfgList + "\n"
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatalf("write services.yml: %v", err)
	}

	return dir, &rootFlags{configPath: devboxPath}
}

func writeConfigFile(t *testing.T, dir, name string) {
	t.Helper()
	dest := filepath.Join(dir, "services", "main", "configs", name)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(dest, []byte("KEY=value\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestDeployConfigCheckCmd_allPresentReturnsNil(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env"})
	writeConfigFile(t, dir, ".env")

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil when all configs present, got: %v", err)
	}
}

func TestDeployConfigCheckCmd_missingFileReturnsError(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{".env"})

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err == nil {
		t.Error("expected error when config file missing, got nil")
	}
}

func TestDeployConfigCheckCmd_partialMissingReturnsError(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env", "other.conf"})
	writeConfigFile(t, dir, ".env")

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err == nil {
		t.Error("expected error when some configs missing, got nil")
	}
}

func TestDeployConfigCheckCmd_allMultiplePresentReturnsNil(t *testing.T) {
	dir, flags := writeConfigCheckFixture(t, []string{".env", "other.conf"})
	writeConfigFile(t, dir, ".env")
	writeConfigFile(t, dir, "other.conf")

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil when all configs present, got: %v", err)
	}
}

func TestDeployConfigCheckCmd_unknownServiceReturnsError(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{".env"})

	cmd := newDeployConfigCheckCmd(flags)
	err := cmd.RunE(cmd, []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown service, got nil")
	}
}

func TestDeployConfigCheckCmd_emptyConfigsListReturnsNil(t *testing.T) {
	_, flags := writeConfigCheckFixture(t, []string{})

	cmd := newDeployConfigCheckCmd(flags)
	if err := cmd.RunE(cmd, []string{"main"}); err != nil {
		t.Errorf("expected nil for empty configs list, got: %v", err)
	}
}

func TestDeployConfigCmd_pathTraversalRejected(t *testing.T) {
	dir := t.TempDir()

	devboxYML := `
schema_version: "1"
project:
  name: laravel
  prefix: devbox
`
	devboxPath := filepath.Join(dir, "devbox.yml")
	if err := os.WriteFile(devboxPath, []byte(devboxYML), 0o644); err != nil {
		t.Fatal(err)
	}

	devboxDir := filepath.Join(dir, "devbox")
	if err := os.MkdirAll(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	servicesYML := `
services:
  main:
    type: app
    container: app-main
    mandatory: true
    dir: ./services/main
    dir_internal: /var/www/app
    configs:
      - ../../etc/passwd
`
	if err := os.WriteFile(filepath.Join(devboxDir, "services.yml"), []byte(servicesYML), 0o644); err != nil {
		t.Fatal(err)
	}

	srcDir := filepath.Join(dir, "configs", "services", "main")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "../../etc/passwd"), []byte("KEY=value\n"), 0o644); err != nil {
		_ = err
	}

	flags := &rootFlags{configPath: devboxPath}
	cmd := newDeployConfigCmd(flags)
	err := cmd.RunE(cmd, []string{"main"})
	if err == nil {
		t.Error("expected path traversal error, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "escapes") {
		t.Errorf("expected 'escapes' in error message, got: %v", err)
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
