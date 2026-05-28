package env

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/validate"
)

// withIsolatedPath swaps $PATH to dir for the test duration.
func withIsolatedPath(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir)
}

// writeStubBinary creates an executable shell script at dir/name that exits with
// the given exit code. If stdout is non-empty it is printed on stdout. POSIX-only.
func writeStubBinary(t *testing.T, dir, name string, exitCode int, stdout string) {
	t.Helper()
	path := filepath.Join(dir, name)
	body := "#!/bin/sh\n"
	if stdout != "" {
		body += "echo '" + strings.ReplaceAll(stdout, "'", "'\\''") + "'\n"
	}
	if exitCode != 0 {
		body += "exit " + strconv.Itoa(exitCode) + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}

func findSeverity(diags []validate.Diagnostic, target string) validate.Severity {
	for _, d := range diags {
		if d.Target == target {
			return d.Severity
		}
	}
	return validate.SeverityUnknown
}

func TestDockerBinValidator_Missing(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir)
	v := &dockerBinValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_bin") != validate.SeverityError {
		t.Fatalf("want error, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "docker") {
		t.Fatalf("message: %s", diags[0].Message)
	}
	if diags[0].Hint == "" {
		t.Fatal("hint should be non-empty")
	}
}

func TestDockerBinValidator_Present(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	v := &dockerBinValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_bin") != validate.SeverityOK {
		t.Fatalf("want OK, got %+v", diags)
	}
}

func TestDockerDaemon_Skipped_WhenBinMissing(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir)
	v := &dockerDaemonValidator{}
	if diags := v.Run(validate.Context{}); len(diags) != 0 {
		t.Fatalf("want skip (nil), got %+v", diags)
	}
}

func TestDockerDaemon_Unreachable(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 1, "Cannot connect to the Docker daemon")
	withIsolatedPath(t, dir)
	v := &dockerDaemonValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_daemon") != validate.SeverityError {
		t.Fatalf("want error, got %+v", diags)
	}
}

func TestDockerDaemon_OK(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "24.0.0")
	withIsolatedPath(t, dir)
	v := &dockerDaemonValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_daemon") != validate.SeverityOK {
		t.Fatalf("want OK, got %+v", diags)
	}
}

func TestDockerCompose_OK(t *testing.T) {
	dir := t.TempDir()
	// Modern Docker Desktop ships Compose v5+ — the major version is no
	// longer load-bearing; what matters is that the `compose` subcommand
	// answers (proving the plugin is installed, not legacy docker-compose).
	writeStubBinary(t, dir, "docker", 0, "5.1.4")
	withIsolatedPath(t, dir)
	v := &dockerComposeValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_compose") != validate.SeverityOK {
		t.Fatalf("want OK, got %+v", diags)
	}
}

func TestDockerCompose_EmptyVersion(t *testing.T) {
	dir := t.TempDir()
	writeStubBinary(t, dir, "docker", 0, "")
	withIsolatedPath(t, dir)
	v := &dockerComposeValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_compose") != validate.SeverityError {
		t.Fatalf("want error, got %+v", diags)
	}
}

func TestDockerCompose_PluginMissing(t *testing.T) {
	dir := t.TempDir()
	// Stub exits non-zero (compose plugin not installed).
	writeStubBinary(t, dir, "docker", 1, "docker: 'compose' is not a docker command")
	withIsolatedPath(t, dir)
	v := &dockerComposeValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "docker_compose") != validate.SeverityError {
		t.Fatalf("want error, got %+v", diags)
	}
}

func TestGitBin_MissingAndPresent(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir)
	v := &gitBinValidator{}
	if findSeverity(v.Run(validate.Context{}), "git_bin") != validate.SeverityError {
		t.Fatal("want error when git missing")
	}
	writeStubBinary(t, dir, "git", 0, "")
	if findSeverity(v.Run(validate.Context{}), "git_bin") != validate.SeverityOK {
		t.Fatal("want OK when git present")
	}
}

func TestShellBin_MissingAndPresent(t *testing.T) {
	dir := t.TempDir()
	withIsolatedPath(t, dir)
	v := &shellBinValidator{}
	if findSeverity(v.Run(validate.Context{}), "shell_bin") != validate.SeverityError {
		t.Fatal("want error when sh missing")
	}
	writeStubBinary(t, dir, "sh", 0, "")
	if findSeverity(v.Run(validate.Context{}), "shell_bin") != validate.SeverityOK {
		t.Fatal("want OK when sh present")
	}
}

func TestProjectPerms_OK(t *testing.T) {
	root := t.TempDir()
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityOK {
		t.Fatalf("want OK, got %+v", diags)
	}
	// .devbox/ and .devbox/deploy/ should have been created.
	if _, err := os.Stat(filepath.Join(root, ".devbox")); err != nil {
		t.Fatalf(".devbox not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".devbox", "deploy")); err != nil {
		t.Fatalf(".devbox/deploy not created: %v", err)
	}
}

func TestProjectPerms_DeployDirBlockedByFile(t *testing.T) {
	root := t.TempDir()
	devboxDir := filepath.Join(root, ".devbox")
	if err := os.Mkdir(devboxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a regular file where the deploy directory should be.
	if err := os.WriteFile(filepath.Join(devboxDir, "deploy"), []byte("block"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error when .devbox/deploy is a file, got %+v", diags)
	}
}

func TestProjectPerms_NoRoot(t *testing.T) {
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error when ProjectRoot empty, got %+v", diags)
	}
}

func TestProjectPerms_LockFileIsDirectory(t *testing.T) {
	root := t.TempDir()
	deployDir := filepath.Join(root, ".devbox", "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	// Place a directory where deploy.lock should be.
	lockDir := filepath.Join(deployDir, "deploy.lock")
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		t.Fatalf("mkdir deploy.lock: %v", err)
	}
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error when deploy.lock is a directory, got %+v", diags)
	}
	if len(diags) == 0 || !strings.Contains(diags[0].Message, "deploy.lock") {
		t.Errorf("expected deploy.lock in message, got %+v", diags)
	}
}

func TestProjectPerms_ExistingLockFileNotWritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	root := t.TempDir()
	devboxDir := filepath.Join(root, ".devbox")
	deployDir := filepath.Join(devboxDir, "deploy")
	if err := os.MkdirAll(deployDir, 0o755); err != nil {
		t.Fatalf("mkdir -p deploy: %v", err)
	}
	// Create deploy.lock with no permissions — mirrors an existing lock file
	// that lock.Acquire would fail to open with O_RDWR.
	lockFile := filepath.Join(deployDir, "deploy.lock")
	if err := os.WriteFile(lockFile, []byte("42\n"), 0o000); err != nil {
		t.Fatalf("write lock file: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(lockFile, 0o644) })
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error when deploy.lock is not writable, got %+v", diags)
	}
	if len(diags) == 0 || !strings.Contains(diags[0].Message, "deploy.lock") {
		t.Errorf("expected deploy.lock in message, got %+v", diags)
	}
}

func TestProjectPerms_UnwritableDeployDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	root := t.TempDir()
	devboxDir := filepath.Join(root, ".devbox")
	deployDir := filepath.Join(devboxDir, "deploy")
	if err := os.Mkdir(devboxDir, 0o755); err != nil {
		t.Fatalf("mkdir .devbox: %v", err)
	}
	if err := os.Mkdir(deployDir, 0o555); err != nil {
		t.Fatalf("mkdir deploy: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(deployDir, 0o755) })
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error on unwritable .devbox/deploy, got %+v", diags)
	}
}

func TestProjectPerms_Unwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permission bits")
	}
	root := t.TempDir()
	// Pre-create .devbox/ with no write permission.
	devboxDir := filepath.Join(root, ".devbox")
	if err := os.Mkdir(devboxDir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(devboxDir, 0o755) })
	v := &projectPermsValidator{}
	diags := v.Run(validate.Context{ProjectRoot: root})
	if findSeverity(diags, "project_perms") != validate.SeverityError {
		t.Fatalf("want error on unwritable .devbox, got %+v", diags)
	}
}

func TestAll_Composition(t *testing.T) {
	cfg := &config.DevboxConfig{}
	got := All(cfg)
	if len(got) != 7 {
		t.Fatalf("want 7 validators, got %d", len(got))
	}
	ids := map[string]bool{}
	for _, v := range got {
		if v.Domain() != "env" {
			t.Errorf("validator %s domain=%s, want env", v.ID(), v.Domain())
		}
		ids[v.ID()] = true
	}
	for _, want := range []string{
		"docker_bin", "docker_daemon", "docker_compose",
		"git_bin", "shell_bin", "project_perms", "ports_free",
	} {
		if !ids[want] {
			t.Errorf("missing validator: %s", want)
		}
	}
}

func TestAll_NilCfg(t *testing.T) {
	// All must accept a nil cfg without panicking; the binary accessors handle it.
	got := All(nil)
	if len(got) != 7 {
		t.Fatalf("want 7 validators, got %d", len(got))
	}
}
