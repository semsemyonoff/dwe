package command

import (
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
)

// TestDockerPipelineBuildsCompose verifies that the docker pipeline correctly
// assembles a Compose struct from config and docker policy.
func TestDockerPipelineBuildsCompose(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "devbox-laravel",
		Args: config.DockerArgs{
			Global:  []string{"--ansi", "always", "--progress", "tty"},
			Up:      []string{"-d", "--remove-orphans"},
			Down:    []string{},
			Stop:    []string{},
			Restart: []string{},
			Logs:    []string{"-f"},
			Ps:      []string{},
			Exec:    []string{},
			Run:     []string{"--rm"},
		},
		Env: config.DockerEnvConfig{
			AutoGenerate: true,
			Commands:     []string{"up", "run", "exec"},
		},
	}

	compose := docker.NewCompose(cfg, dockerCfg)

	// Verify up args include policy defaults + service args.
	args := compose.BuildArgs("up", "redis")
	expected := []string{
		"compose",
		"-p", "devbox-laravel",
		"-f", "compose.yaml",
		"--ansi", "always", "--progress", "tty",
		"up",
		"-d", "--remove-orphans",
		"redis",
	}
	assertArgs(t, "up", args, expected)

	// Verify down has no per-command defaults.
	args = compose.BuildArgs("down")
	expected = []string{
		"compose",
		"-p", "devbox-laravel",
		"-f", "compose.yaml",
		"--ansi", "always", "--progress", "tty",
		"down",
	}
	assertArgs(t, "down", args, expected)

	// Verify logs includes -f.
	args = compose.BuildArgs("logs", "nginx")
	expected = []string{
		"compose",
		"-p", "devbox-laravel",
		"-f", "compose.yaml",
		"--ansi", "always", "--progress", "tty",
		"logs",
		"-f",
		"nginx",
	}
	assertArgs(t, "logs", args, expected)

	// Verify exec passes through args.
	args = compose.BuildArgs("exec", "app-main", "--", "php", "artisan", "--version")
	expected = []string{
		"compose",
		"-p", "devbox-laravel",
		"-f", "compose.yaml",
		"--ansi", "always", "--progress", "tty",
		"exec",
		"app-main", "--", "php", "artisan", "--version",
	}
	assertArgs(t, "exec", args, expected)

	// Verify run includes --rm.
	args = compose.BuildArgs("run", "app-main", "--", "composer", "install")
	expected = []string{
		"compose",
		"-p", "devbox-laravel",
		"-f", "compose.yaml",
		"--ansi", "always", "--progress", "tty",
		"run",
		"--rm",
		"app-main", "--", "composer", "install",
	}
	assertArgs(t, "run", args, expected)
}

// TestDockerEnvAutoGeneration verifies ShouldGenerateEnv logic.
func TestDockerEnvAutoGeneration(t *testing.T) {
	envCfg := config.DockerEnvConfig{
		AutoGenerate: true,
		Commands:     []string{"up", "run", "exec"},
	}

	tests := []struct {
		command string
		want    bool
	}{
		{"up", true},
		{"run", true},
		{"exec", true},
		{"down", false},
		{"stop", false},
		{"restart", false},
		{"logs", false},
		{"ps", false},
		{"wait", false},
	}

	for _, tt := range tests {
		if got := envCfg.ShouldGenerateEnv(tt.command); got != tt.want {
			t.Errorf("ShouldGenerateEnv(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

// TestDockerEnvAutoGenerationDisabled verifies no commands trigger env gen when disabled.
func TestDockerEnvAutoGenerationDisabled(t *testing.T) {
	envCfg := config.DockerEnvConfig{
		AutoGenerate: false,
		Commands:     []string{"up", "run", "exec"},
	}

	for _, cmd := range []string{"up", "run", "exec", "down"} {
		if envCfg.ShouldGenerateEnv(cmd) {
			t.Errorf("ShouldGenerateEnv(%q) = true when auto_generate is false", cmd)
		}
	}
}

// TestDockerCommandSubcommands verifies the docker command group has all expected subcommands.
func TestDockerCommandSubcommands(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	dockerCmd := newDockerCmd(flags)

	expectedSubs := []string{"up", "down", "stop", "restart", "logs", "ps", "exec", "run", "wait", "project-name"}
	commands := dockerCmd.Commands()

	if len(commands) != len(expectedSubs) {
		t.Fatalf("docker has %d subcommands, want %d", len(commands), len(expectedSubs))
	}

	nameSet := make(map[string]bool)
	for _, c := range commands {
		nameSet[c.Name()] = true
	}

	for _, name := range expectedSubs {
		if !nameSet[name] {
			t.Errorf("missing subcommand %q", name)
		}
	}
}

func assertArgs(t *testing.T, label string, got, expected []string) {
	t.Helper()
	if len(got) != len(expected) {
		t.Fatalf("%s: arg count = %d, want %d\nGot:  %v\nWant: %v", label, len(got), len(expected), got, expected)
	}
	for i := range got {
		if got[i] != expected[i] {
			t.Errorf("%s: arg[%d] = %q, want %q", label, i, got[i], expected[i])
		}
	}
}
