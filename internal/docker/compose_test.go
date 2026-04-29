package docker

import (
	"slices"
	"testing"

	"devbox-cli/internal/config"
)

func TestNewCompose(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "devbox-laravel",
		Args: config.DockerArgs{
			Global: []string{"--ansi", "always"},
			Up:     []string{"-d", "--remove-orphans"},
			Run:    []string{"--rm"},
		},
	}

	c := NewCompose(cfg, dockerCfg)

	if c.ProjectName != "devbox-laravel" {
		t.Errorf("ProjectName = %q, want %q", c.ProjectName, "devbox-laravel")
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
