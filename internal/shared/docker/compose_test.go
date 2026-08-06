package docker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TestExec_RunsInBaseDir guarantees that docker compose subprocesses inherit
// BaseDir as their working directory, so relative `-f compose.yaml` paths
// resolve against the project root even when dwe is invoked from a project
// subdirectory. Regression for the cwd-bug where commands launched from a
// subdir crashed with "open <cwd>/compose.yaml: no such file or directory".
func TestExec_RunsInBaseDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is sh-based")
	}
	tmp := t.TempDir()
	// Build a stub docker binary that writes its own CWD to a file and exits 0.
	stubDir := filepath.Join(tmp, "stub")
	if err := os.Mkdir(stubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwdFile := filepath.Join(tmp, "cwd.txt")
	stubPath := filepath.Join(stubDir, "docker-cwd-stub")
	stub := "#!/bin/sh\npwd > " + cwdFile + "\n"
	if err := os.WriteFile(stubPath, []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pretend baseDir is the project root.
	baseDir := filepath.Join(tmp, "project")
	if err := os.Mkdir(baseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Switch our own cwd to a *different* directory — this is what reproduces
	// "ran from a subdir" semantics for the parent process. t.Chdir restores
	// the cwd at test end and panics if the test (or another in the package)
	// is parallel — both desirable here.
	otherDir := filepath.Join(tmp, "elsewhere")
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(otherDir)

	c := &Compose{Bin: stubPath, BaseDir: baseDir}
	if err := c.Exec("ps"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got, err := os.ReadFile(cwdFile)
	if err != nil {
		t.Fatalf("read cwd file: %v", err)
	}
	gotPath := strings.TrimSpace(string(got))
	if gotPath == "" {
		t.Fatal("stub wrote empty cwd — child likely did not run")
	}
	// macOS resolves /var → /private/var; compare via EvalSymlinks. Surface
	// EvalSymlinks errors so an unreadable path can't masquerade as "" == "".
	wantResolved, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		t.Fatalf("EvalSymlinks baseDir: %v", err)
	}
	gotResolved, err := filepath.EvalSymlinks(gotPath)
	if err != nil {
		t.Fatalf("EvalSymlinks child cwd %q: %v", gotPath, err)
	}
	if gotResolved != wantResolved {
		t.Fatalf("child CWD = %q, want %q (BaseDir not applied)", gotResolved, wantResolved)
	}
}

func TestNewCompose(t *testing.T) {
	cfg := &config.DweConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "dwe-laravel",
		Args: config.DockerArgs{
			Global: []string{"--ansi", "always"},
			Up:     []string{"-d", "--remove-orphans"},
			Run:    []string{"--rm"},
		},
	}

	c := NewCompose(cfg, dockerCfg, "")

	if c.ProjectName != "dwe-laravel" {
		t.Errorf("ProjectName = %q, want %q", c.ProjectName, "dwe-laravel")
	}
	if len(c.Files) != 1 || c.Files[0] != "compose.yaml" {
		t.Errorf("Files = %v, want [compose.yaml]", c.Files)
	}
	if len(c.GlobalArgs) != 2 || c.GlobalArgs[0] != "--ansi" {
		t.Errorf("GlobalArgs = %v, want [--ansi always]", c.GlobalArgs)
	}
	if up := c.CommandArgs["up"]; len(up) != 2 || up[0] != "-d" {
		t.Errorf("CommandArgs[up] = %v, want [-d --remove-orphans]", up)
	}
	if run := c.CommandArgs["run"]; len(run) != 1 || run[0] != "--rm" {
		t.Errorf("CommandArgs[run] = %v, want [--rm]", run)
	}
}

func TestNewCompose_DefaultsProjectNameToFullName(t *testing.T) {
	cfg := &config.DweConfig{Compose: config.ComposeConfig{Base: "compose.yaml"}}
	cfg.Project.Prefix = "dwe"
	cfg.Project.Name = "laravel"

	// docker.yml absent → empty DockerConfig → ProjectName must fall back to
	// FullName so every compose call carries -p "dwe-laravel" instead of
	// silently scoping resources by the project directory basename.
	c := NewCompose(cfg, &config.DockerConfig{}, "")
	if c.ProjectName != "dwe-laravel" {
		t.Errorf("ProjectName = %q, want %q (FullName fallback)", c.ProjectName, "dwe-laravel")
	}
	args := c.BuildArgs("up")
	if len(args) < 3 || args[1] != "-p" || args[2] != "dwe-laravel" {
		t.Errorf("BuildArgs should pass -p dwe-laravel, got %v", args)
	}
}

func TestNewCompose_DockerYmlProjectNameWins(t *testing.T) {
	cfg := &config.DweConfig{Compose: config.ComposeConfig{Base: "compose.yaml"}}
	cfg.Project.Prefix = "dwe"
	cfg.Project.Name = "laravel"

	// An explicit project_name (e.g. underscore separator) overrides FullName.
	c := NewCompose(cfg, &config.DockerConfig{ProjectName: "dwe_laravel"}, "")
	if c.ProjectName != "dwe_laravel" {
		t.Errorf("ProjectName = %q, want %q (docker.yml wins)", c.ProjectName, "dwe_laravel")
	}
}

// TestNewCompose_ProjectNameIsNormalized pins the -p value to the SAME
// normalization config.ComposeProjectName applies. Compose v2 rejects an
// uppercase project name outright, and every compose-bypass path (status,
// per-service stop/restart, reset --service, the bridge overlay, the
// config.container_name validator) derives "<project>-<container>" from the
// normalized name — so a raw -p here would scope compose's own resources under
// a project name no label lookup ever asks for.
func TestNewCompose_ProjectNameIsNormalized(t *testing.T) {
	tests := []struct {
		name      string
		prefix    string
		project   string
		dockerCfg *config.DockerConfig
		want      string
	}{
		{
			name:      "FullName fallback is lowercased",
			prefix:    "dwe",
			project:   "cueBreaker",
			dockerCfg: &config.DockerConfig{},
			want:      "dwe-cuebreaker",
		},
		{
			name:      "docker.yml project_name is lowercased",
			prefix:    "dwe",
			project:   "laravel",
			dockerCfg: &config.DockerConfig{ProjectName: "MyApp"},
			want:      "myapp",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.DweConfig{Compose: config.ComposeConfig{Base: "compose.yaml"}}
			cfg.Project.Prefix = tt.prefix
			cfg.Project.Name = tt.project

			c := NewCompose(cfg, tt.dockerCfg, "")
			if c.ProjectName != tt.want {
				t.Errorf("ProjectName = %q, want %q", c.ProjectName, tt.want)
			}
			if got := config.ComposeProjectName(tt.dockerCfg, cfg); got != c.ProjectName {
				t.Errorf("ProjectName = %q diverges from config.ComposeProjectName = %q", c.ProjectName, got)
			}
			args := c.BuildArgs("up")
			if len(args) < 3 || args[1] != "-p" || args[2] != tt.want {
				t.Errorf("BuildArgs should pass -p %s, got %v", tt.want, args)
			}
		})
	}
}

func TestBuildArgs_FullPipeline(t *testing.T) {
	c := &Compose{
		ProjectName: "myproject",
		Files:       []string{"compose.yaml", "compose/tools/adminer.yml"},
		GlobalArgs:  []string{"--ansi", "always", "--progress", "tty"},
		CommandArgs: map[string][]string{
			"up":   {"-d", "--remove-orphans"},
			"logs": {"-f"},
		},
	}

	args := c.BuildArgs("up", "redis")

	expected := []string{
		"compose",
		"-p", "myproject",
		"-f", "compose.yaml",
		"-f", "compose/tools/adminer.yml",
		"--ansi", "always", "--progress", "tty",
		"up",
		"-d", "--remove-orphans",
		"redis",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestBuildArgs_NoExtraArgs(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{
			"down": {},
		},
	}

	args := c.BuildArgs("down")

	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"--ansi", "always",
		"down",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestBuildArgs_UnknownCommand(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		CommandArgs: map[string][]string{},
	}

	args := c.BuildArgs("build", "--no-cache")

	// Unknown commands should still work, just without per-command defaults.
	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"build",
		"--no-cache",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestBuildArgs_EmptyProjectName(t *testing.T) {
	c := &Compose{
		Files:       []string{"compose.yaml"},
		CommandArgs: map[string][]string{},
	}

	args := c.BuildArgs("ps")

	// No -p flag when ProjectName is empty.
	expected := []string{
		"compose",
		"-f", "compose.yaml",
		"ps",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestBuildInternalArgs_SkipsPolicyDefaults(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{
			"ps": {"--services", "--format", "table"},
		},
	}

	// BuildArgs should include ps policy defaults.
	withPolicy := c.BuildArgs("ps", "-q")
	if !containsArg(withPolicy, "--services") {
		t.Error("BuildArgs should include policy defaults for ps")
	}

	// BuildInternalArgs should NOT include ps policy defaults or global args.
	internal := c.BuildInternalArgs("ps", "-q")
	if containsArg(internal, "--services") {
		t.Error("BuildInternalArgs should not include policy defaults")
	}
	if containsArg(internal, "--ansi") {
		t.Error("BuildInternalArgs should not include global args")
	}

	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"ps",
		"-q",
	}
	if len(internal) != len(expected) {
		t.Fatalf("BuildInternalArgs = %v, want %v", internal, expected)
	}
	for i, got := range internal {
		if got != expected[i] {
			t.Errorf("BuildInternalArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func containsArg(args []string, target string) bool {
	return slices.Contains(args, target)
}

func TestBuildArgs_MultipleExtraArgs(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{
			"exec": {},
		},
	}

	args := c.BuildArgs("exec", "app-main", "--", "php", "artisan", "--version")

	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"--ansi", "always",
		"exec",
		"app-main", "--", "php", "artisan", "--version",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

// TestBuildArgs_DoubleDashSeparatorExec verifies that -- separator in docker exec
// is preserved in the correct position: between service name and command.
func TestBuildArgs_DoubleDashSeparatorExec(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{},
		CommandArgs: map[string][]string{
			"exec": {"-T"},
		},
	}

	// Test: docker exec <svc> -- <cmd with flags>
	args := c.BuildArgs("exec", "db", "--", "mariadb", "-u", "root", "-h", "localhost")

	// Verify -- is at position 4 (after "exec" and "db")
	dashDashIndex := -1
	for i, arg := range args {
		if arg == "--" {
			dashDashIndex = i
			break
		}
	}

	if dashDashIndex < 0 {
		t.Fatal("-- separator not found in args")
	}

	// The order should be: compose, -p proj, -f compose.yaml, exec, -T, db, --, mariadb, ...
	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"exec",
		"-T",
		"db", "--", "mariadb", "-u", "root", "-h", "localhost",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

// TestBuildArgs_DoubleDashSeparatorRun verifies that -- separator in docker run
// is preserved in the correct position: between service name and command.
func TestBuildArgs_DoubleDashSeparatorRun(t *testing.T) {
	c := &Compose{
		ProjectName: "proj",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{},
		CommandArgs: map[string][]string{
			"run": {"--rm"},
		},
	}

	// Test: docker run <svc> -- <cmd with flags>
	args := c.BuildArgs("run", "app-main", "--", "composer", "install", "--prefer-dist")

	// Verify -- is present and flags after it are preserved
	dashDashIndex := -1
	for i, arg := range args {
		if arg == "--" {
			dashDashIndex = i
			break
		}
	}

	if dashDashIndex < 0 {
		t.Fatal("-- separator not found in args")
	}

	expected := []string{
		"compose",
		"-p", "proj",
		"-f", "compose.yaml",
		"run",
		"--rm",
		"app-main", "--", "composer", "install", "--prefer-dist",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

// --- BinName ---

func TestBinName_ZeroValue(t *testing.T) {
	c := &Compose{}
	if got := c.BinName(); got != "docker" {
		t.Errorf("BinName() on zero-value Compose = %q, want %q", got, "docker")
	}
}

func TestBinName_NilReceiver(t *testing.T) {
	var c *Compose
	if got := c.BinName(); got != "docker" {
		t.Errorf("BinName() on nil Compose = %q, want %q", got, "docker")
	}
}

func TestBinName_EmptyString(t *testing.T) {
	c := &Compose{Bin: ""}
	if got := c.BinName(); got != "docker" {
		t.Errorf("BinName() on empty Bin = %q, want %q", got, "docker")
	}
}

func TestBinName_CustomBin(t *testing.T) {
	c := &Compose{Bin: "podman"}
	if got := c.BinName(); got != "podman" {
		t.Errorf("BinName() = %q, want %q", got, "podman")
	}
}

func TestNewCompose_PopulatesBinFromConfig(t *testing.T) {
	cfg := &config.DweConfig{
		Compose: config.ComposeConfig{Base: "compose.yaml"},
	}
	dockerCfg := &config.DockerConfig{ProjectName: "test"}
	c := NewCompose(cfg, dockerCfg, "")
	// DockerBin returns "docker" when userconfig is nil
	if c.Bin != "docker" {
		t.Errorf("NewCompose Bin = %q, want %q", c.Bin, "docker")
	}
	if c.BinName() != "docker" {
		t.Errorf("NewCompose BinName() = %q, want %q", c.BinName(), "docker")
	}
}

func TestNewCompose_DefaultBinWhenNotSet(t *testing.T) {
	cfg := &config.DweConfig{
		Compose: config.ComposeConfig{Base: "compose.yaml"},
	}
	dockerCfg := &config.DockerConfig{ProjectName: "test"}
	c := NewCompose(cfg, dockerCfg, "")
	if c.BinName() != "docker" {
		t.Errorf("NewCompose BinName() = %q, want %q", c.BinName(), "docker")
	}
}

func TestFormatCommandQuotesUnsafeArgs(t *testing.T) {
	got := trace.FormatCommand([]string{
		"docker",
		"compose",
		"exec",
		"-e",
		"MYSQL_PWD",
		"db",
		"--",
		"sh",
		"-c",
		"echo 'hello world'",
	})
	want := "docker compose exec -e MYSQL_PWD db -- sh -c 'echo '\\''hello world'\\'''"
	if got != want {
		t.Fatalf("trace.FormatCommand = %q, want %q", got, want)
	}
}

func TestNewComposeAll(t *testing.T) {
	cfg := &config.DweConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
		Services: map[string]config.ServiceConfig{
			"adminer": {
				Type:    config.ServiceTypeTool,
				Compose: []string{"compose/tools/adminer.yml"},
			},
			"api": {
				Type:    config.ServiceTypeApp,
				Compose: []string{"compose/services/api.yml"},
			},
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "dwe-test",
		Args: config.DockerArgs{
			Global: []string{"--ansi", "always"},
			Pull:   []string{"--policy", "always"},
			Build:  []string{"--progress", "plain"},
		},
	}

	c := NewComposeAll(cfg, dockerCfg, "")

	// Verify Files come from ComposeFilesAll() instead of ComposeFiles()
	wantFiles := cfg.ComposeFilesAll()
	if len(c.Files) != len(wantFiles) {
		t.Fatalf("NewComposeAll Files len = %d, want %d\nGot:  %v\nWant: %v",
			len(c.Files), len(wantFiles), c.Files, wantFiles)
	}
	for i, f := range c.Files {
		if f != wantFiles[i] {
			t.Errorf("Files[%d] = %q, want %q", i, f, wantFiles[i])
		}
	}

	// Verify pull and build are in CommandArgs
	if pull := c.CommandArgs["pull"]; len(pull) != 2 || pull[0] != "--policy" || pull[1] != "always" {
		t.Errorf("CommandArgs[pull] = %v, want [--policy always]", pull)
	}
	if build := c.CommandArgs["build"]; len(build) != 2 || build[0] != "--progress" || build[1] != "plain" {
		t.Errorf("CommandArgs[build] = %v, want [--progress plain]", build)
	}
}

func TestBuildArgs_Pull(t *testing.T) {
	c := &Compose{
		ProjectName: "myproject",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{
			"pull": {"--policy", "always"},
		},
	}

	args := c.BuildArgs("pull", "redis")

	expected := []string{
		"compose",
		"-p", "myproject",
		"-f", "compose.yaml",
		"--ansi", "always",
		"pull",
		"--policy", "always",
		"redis",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

func TestBuildArgs_BuildWithForceFlags(t *testing.T) {
	c := &Compose{
		ProjectName: "myproject",
		Files:       []string{"compose.yaml"},
		GlobalArgs:  []string{"--ansi", "always"},
		CommandArgs: map[string][]string{
			"build": {"--progress", "plain"},
		},
	}

	// Simulating --force flags appended by caller
	args := c.BuildArgs("build", "--no-cache", "--pull", "api")

	expected := []string{
		"compose",
		"-p", "myproject",
		"-f", "compose.yaml",
		"--ansi", "always",
		"build",
		"--progress", "plain",
		"--no-cache", "--pull",
		"api",
	}

	if len(args) != len(expected) {
		t.Fatalf("BuildArgs length = %d, want %d\nGot:  %v\nWant: %v", len(args), len(expected), args, expected)
	}
	for i, got := range args {
		if got != expected[i] {
			t.Errorf("BuildArgs[%d] = %q, want %q", i, got, expected[i])
		}
	}
}

// writeStub builds an executable shell stub at tmp/<name> that runs body and
// returns its absolute path. Skips on Windows (sh-based).
func writeStub(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell stub is sh-based")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "docker-stub")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// captureTrace points the trace sink at a buffer at the given level for the
// duration of the test, restoring LevelOff afterwards.
func captureTrace(t *testing.T, lvl trace.Level) *strings.Builder {
	t.Helper()
	buf := &strings.Builder{}
	trace.Configure(buf, lvl)
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })
	return buf
}

func TestExec_EchoesAtVerbose(t *testing.T) {
	stub := writeStub(t, "exit 0")
	buf := captureTrace(t, trace.LevelVerbose)

	c := &Compose{Bin: stub}
	if err := c.Exec("ps"); err != nil {
		t.Fatalf("Exec: %v", err)
	}

	want := "$ " + trace.FormatCommand(append([]string{stub}, c.BuildArgs("ps")...))
	got := strings.TrimSpace(buf.String())
	if got != want {
		t.Fatalf("echo = %q, want %q", got, want)
	}
}

func TestExec_EchoesEvenOnFailure(t *testing.T) {
	stub := writeStub(t, "exit 7")
	buf := captureTrace(t, trace.LevelVerbose)

	c := &Compose{Bin: stub}
	if err := c.Exec("ps"); err == nil {
		t.Fatal("Exec: expected error from failing stub")
	}
	if !strings.Contains(buf.String(), "$ ") {
		t.Fatalf("expected command echo even on failure, got %q", buf.String())
	}
}

func TestExec_SilentAtLevelOff(t *testing.T) {
	stub := writeStub(t, "exit 0")
	buf := captureTrace(t, trace.LevelOff)

	c := &Compose{Bin: stub}
	if err := c.Exec("ps"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no output at LevelOff, got %q", buf.String())
	}
}

func TestProbe_EchoesOnlyAtDebug(t *testing.T) {
	stub := writeStub(t, "exit 0") // prints nothing → empty probe result

	t.Run("verbose is silent", func(t *testing.T) {
		buf := captureTrace(t, trace.LevelVerbose)
		c := &Compose{Bin: stub}
		if _, err := c.ContainerIDs(); err != nil {
			t.Fatalf("ContainerIDs: %v", err)
		}
		if _, err := c.RunningServices(context.Background(), []string{"app"}); err != nil {
			t.Fatalf("RunningServices: %v", err)
		}
		if buf.Len() != 0 {
			t.Fatalf("read-only probes must not echo at Verbose, got %q", buf.String())
		}
	})

	t.Run("debug echoes", func(t *testing.T) {
		buf := captureTrace(t, trace.LevelDebug)
		c := &Compose{Bin: stub}
		if _, err := c.ContainerIDs(); err != nil {
			t.Fatalf("ContainerIDs: %v", err)
		}
		if !strings.Contains(buf.String(), "ps") || !strings.Contains(buf.String(), "$ ") {
			t.Fatalf("expected probe echo at Debug, got %q", buf.String())
		}

		buf2 := captureTrace(t, trace.LevelDebug)
		if _, err := c.RunningServices(context.Background(), []string{"app"}); err != nil {
			t.Fatalf("RunningServices: %v", err)
		}
		if !strings.Contains(buf2.String(), "--services") {
			t.Fatalf("expected RunningServices probe echo at Debug, got %q", buf2.String())
		}
	})
}

func TestRunningServices_SurfacesStderrOnFailure(t *testing.T) {
	// A compose ps probe that exits non-zero with a stderr message: the wrapped
	// error must include that message, not just "exit status 1", so a failure is
	// diagnosable without a live repro.
	stub := writeStub(t, `echo "no configuration file provided: not found" >&2; exit 1`)
	c := &Compose{Bin: stub}

	_, err := c.RunningServices(context.Background(), []string{"app"})
	if err == nil {
		t.Fatal("expected an error from a failing probe")
	}
	if !strings.Contains(err.Error(), "no configuration file provided: not found") {
		t.Fatalf("error must surface subprocess stderr, got %q", err.Error())
	}
}

func TestExec_DebugEmitsTimingEnvAndCwd(t *testing.T) {
	stub := writeStub(t, "exit 0")
	baseDir := t.TempDir()
	buf := captureTrace(t, trace.LevelDebug)

	c := &Compose{Bin: stub, BaseDir: baseDir, ProcessEnv: map[string]string{"DOCKER_CLI_HINTS": "false"}}
	if err := c.Exec("ps"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"$ ", "cwd=" + baseDir, "env=DOCKER_CLI_HINTS=false", "↳ exit 0 in"} {
		if !strings.Contains(out, want) {
			t.Errorf("debug output %q missing %q", out, want)
		}
	}
}

func TestExec_DebugReportsNonZeroExit(t *testing.T) {
	stub := writeStub(t, "exit 7")
	buf := captureTrace(t, trace.LevelDebug)

	c := &Compose{Bin: stub}
	if err := c.Exec("ps"); err == nil {
		t.Fatal("Exec: expected error from failing stub")
	}
	if !strings.Contains(buf.String(), "↳ exit 7 in") {
		t.Errorf("debug output missing non-zero exit code: %q", buf.String())
	}
}

func TestExec_VerboseHasNoTimingOrEnv(t *testing.T) {
	stub := writeStub(t, "exit 0")
	buf := captureTrace(t, trace.LevelVerbose)

	c := &Compose{Bin: stub, BaseDir: t.TempDir(), ProcessEnv: map[string]string{"K": "v"}}
	if err := c.Exec("ps"); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	out := buf.String()
	for _, unwanted := range []string{"cwd=", "env=", "↳ exit"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("verbose output should not contain %q, got %q", unwanted, out)
		}
	}
}

func TestExitCodeString(t *testing.T) {
	if got := exitCodeString(nil); got != "0" {
		t.Errorf("exitCodeString(nil) = %q, want %q", got, "0")
	}
	stub := writeStub(t, "exit 5")
	err := exec.Command(stub).Run()
	if got := exitCodeString(err); got != "5" {
		t.Errorf("exitCodeString(exit 5) = %q, want %q", got, "5")
	}
	if got := exitCodeString(errors.New("did not start")); got != "?" {
		t.Errorf("exitCodeString(plain err) = %q, want %q", got, "?")
	}
}

func TestSplitNonEmptyLines(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "  \n\t\n  ", nil},
		{"single", "abc", []string{"abc"}},
		{"multiple", "a\nb\nc", []string{"a", "b", "c"}},
		{"trims and drops blanks", "  a  \n\n  b\n", []string{"a", "b"}},
		{"trailing newline", "id1\nid2\n", []string{"id1", "id2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitNonEmptyLines([]byte(tt.in))
			if !slices.Equal(got, tt.want) {
				t.Errorf("splitNonEmptyLines(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
