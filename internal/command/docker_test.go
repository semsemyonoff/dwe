package command

import (
	"reflect"
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

	expectedSubs := []string{"up", "down", "stop", "restart", "logs", "ps", "exec", "run", "pull", "build", "project-name"}
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

func TestStripDockerCommandSeparator(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "removes separator before command",
			args: []string{"db", "--", "mariadb", "-e", "SELECT 1"},
			want: []string{"db", "mariadb", "-e", "SELECT 1"},
		},
		{
			name: "leaves args without separator unchanged",
			args: []string{"db", "mariadb", "-e", "SELECT 1"},
			want: []string{"db", "mariadb", "-e", "SELECT 1"},
		},
		{
			name: "removes only first separator",
			args: []string{"app", "--", "sh", "-lc", "printf -- hello"},
			want: []string{"app", "sh", "-lc", "printf -- hello"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDockerCommandSeparator(tt.args)
			assertArgs(t, tt.name, got, tt.want)
		})
	}
}

// TestResolvePullInvocation verifies the pull resolver logic.
func TestResolvePullInvocation(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
		Services: map[string]config.ServiceConfig{
			"api": {
				Enabled: false,
				Compose: []string{"compose.api.yaml"},
			},
			"cache": {
				Enabled: true,
				Compose: []string{"compose.cache.yaml"},
			},
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Pull: []string{"--policy", "always"},
		},
	}

	tests := []struct {
		name    string
		all     bool
		want    func(*docker.Compose) bool
		wantArg []string
	}{
		{
			name: "pull without --all uses ComposeFiles",
			all:  false,
			want: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			wantArg: []string{"svc"},
		},
		{
			name: "pull with --all uses ComposeFilesAll",
			all:  true,
			want: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			wantArg: []string{"svc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compose, extraArgs := resolvePullInvocation(cfg, dockerCfg, tt.all, []string{"svc"})
			if !tt.want(compose) {
				t.Errorf("resolvePullInvocation(%v) file count mismatch", tt.all)
			}
			assertArgs(t, tt.name, extraArgs, tt.wantArg)
		})
	}
}

// TestDockerPullCmd verifies pull command registration and flag parsing.
func TestDockerPullCmd(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	pullCmd := newDockerPullCmd(flags)

	if pullCmd.Name() != "pull" {
		t.Errorf("pull command name = %q, want %q", pullCmd.Name(), "pull")
	}

	// Verify that --force is rejected (it's build-only).
	err := pullCmd.Flags().Parse([]string{"--force"})
	if err == nil {
		t.Error("pull command should reject --force flag, but did not")
	}
}

// TestDockerPullArgs verifies that pull builds correct docker compose arguments.
func TestDockerPullArgs(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Global: []string{"--ansi", "always"},
			Pull:   []string{"--policy", "always"},
		},
	}

	compose := docker.NewCompose(cfg, dockerCfg)
	args := compose.BuildArgs("pull", "svc")
	expected := []string{
		"compose",
		"-p", "test-project",
		"-f", "compose.yaml",
		"--ansi", "always",
		"pull",
		"--policy", "always",
		"svc",
	}
	assertArgs(t, "pull args", args, expected)
}

// TestResolveBuildInvocation verifies the build resolver logic.
func TestResolveBuildInvocation(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
		Services: map[string]config.ServiceConfig{
			"api": {
				Enabled: false,
				Compose: []string{"compose.api.yaml"},
			},
			"cache": {
				Enabled: true,
				Compose: []string{"compose.cache.yaml"},
			},
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Build: []string{"--parallel", "4"},
		},
	}

	tests := []struct {
		name     string
		all      bool
		force    bool
		services []string
		check    func(*docker.Compose) bool
		wantArg  []string
	}{
		{
			name:     "build without flags uses ComposeFiles",
			all:      false,
			force:    false,
			services: nil,
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			wantArg: []string{},
		},
		{
			name:     "build with --all uses ComposeFilesAll",
			all:      true,
			force:    false,
			services: []string{"svc"},
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			wantArg: []string{"svc"},
		},
		{
			name:     "build with --force prepends --no-cache --pull",
			all:      false,
			force:    true,
			services: []string{"svc"},
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			wantArg: []string{"--no-cache", "--pull", "svc"},
		},
		{
			name:     "build with --all and --force uses ComposeFilesAll and force flags",
			all:      true,
			force:    true,
			services: []string{"svc"},
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			wantArg: []string{"--no-cache", "--pull", "svc"},
		},
		{
			name:     "build with multiple services",
			all:      false,
			force:    false,
			services: []string{"svc1", "svc2"},
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			wantArg: []string{"svc1", "svc2"},
		},
		{
			name:     "build force with multiple services",
			all:      true,
			force:    true,
			services: []string{"svc1", "svc2"},
			check: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			wantArg: []string{"--no-cache", "--pull", "svc1", "svc2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			compose, extraArgs := resolveBuildInvocation(cfg, dockerCfg, tt.all, tt.force, tt.services)
			if !tt.check(compose) {
				t.Errorf("resolveBuildInvocation file set mismatch")
			}
			assertArgs(t, tt.name, extraArgs, tt.wantArg)
		})
	}
}

// TestDockerBuildCmd verifies build command registration and flag parsing.
func TestDockerBuildCmd(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	buildCmd := newDockerBuildCmd(flags)

	if buildCmd.Name() != "build" {
		t.Errorf("build command name = %q, want %q", buildCmd.Name(), "build")
	}

	// Verify that --all and --force are recognized.
	err := buildCmd.Flags().Parse([]string{"--all", "--force"})
	if err != nil {
		t.Errorf("build command should accept --all and --force, but got error: %v", err)
	}
}

// TestDockerBuildArgs verifies that build builds correct docker compose arguments.
func TestDockerBuildArgs(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Global: []string{"--ansi", "always"},
			Build:  []string{"--parallel", "4"},
		},
	}

	tests := []struct {
		name      string
		extraArgs []string
		expected  []string
	}{
		{
			name:      "build without force",
			extraArgs: []string{"svc"},
			expected: []string{
				"compose",
				"-p", "test-project",
				"-f", "compose.yaml",
				"--ansi", "always",
				"build",
				"--parallel", "4",
				"svc",
			},
		},
		{
			name:      "build with force flags",
			extraArgs: []string{"--no-cache", "--pull", "svc"},
			expected: []string{
				"compose",
				"-p", "test-project",
				"-f", "compose.yaml",
				"--ansi", "always",
				"build",
				"--parallel", "4",
				"--no-cache", "--pull",
				"svc",
			},
		},
		{
			name:      "build multiple services",
			extraArgs: []string{"svc1", "svc2"},
			expected: []string{
				"compose",
				"-p", "test-project",
				"-f", "compose.yaml",
				"--ansi", "always",
				"build",
				"--parallel", "4",
				"svc1", "svc2",
			},
		},
	}

	compose := docker.NewCompose(cfg, dockerCfg)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := compose.BuildArgs("build", tt.extraArgs...)
			assertArgs(t, tt.name, args, tt.expected)
		})
	}
}

// TestLegacyImageCommandMapping verifies legacy make targets map to new commands.
func TestLegacyImageCommandMapping(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
		Services: map[string]config.ServiceConfig{
			"api": {
				Type:    config.ServiceTypeApp,
				Enabled: false,
				Compose: []string{"compose.api.yaml"},
			},
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Pull:  []string{},
			Build: []string{},
		},
	}

	tests := []struct {
		name            string
		legacyTarget    string
		allFlag         bool
		forceFlag       bool
		services        []string
		expectedCompose func(*docker.Compose) bool
		expectedArgs    []string
	}{
		{
			name:         "image_pull -> devbox docker pull",
			legacyTarget: "image_pull",
			allFlag:      false,
			forceFlag:    false,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			expectedArgs: []string{},
		},
		{
			name:         "image_pull_all -> devbox docker pull --all",
			legacyTarget: "image_pull_all",
			allFlag:      true,
			forceFlag:    false,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			expectedArgs: []string{},
		},
		{
			name:         "image_rebuild -> devbox docker build",
			legacyTarget: "image_rebuild",
			allFlag:      false,
			forceFlag:    false,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			expectedArgs: []string{},
		},
		{
			name:         "image_rebuild_% -> devbox docker build <service>",
			legacyTarget: "image_rebuild_%",
			allFlag:      false,
			forceFlag:    false,
			services:     []string{"svc-x"},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			expectedArgs: []string{"svc-x"},
		},
		{
			name:         "image_rebuild_force -> devbox docker build --force",
			legacyTarget: "image_rebuild_force",
			allFlag:      false,
			forceFlag:    true,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFiles())
			},
			expectedArgs: []string{"--no-cache", "--pull"},
		},
		{
			name:         "image_rebuild_all -> devbox docker build --all",
			legacyTarget: "image_rebuild_all",
			allFlag:      true,
			forceFlag:    false,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			expectedArgs: []string{},
		},
		{
			name:         "image_rebuild_all_force -> devbox docker build --all --force",
			legacyTarget: "image_rebuild_all_force",
			allFlag:      true,
			forceFlag:    true,
			services:     []string{},
			expectedCompose: func(c *docker.Compose) bool {
				return reflect.DeepEqual(c.Files, cfg.ComposeFilesAll())
			},
			expectedArgs: []string{"--no-cache", "--pull"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.legacyTarget, func(t *testing.T) {
			var compose *docker.Compose
			var extraArgs []string

			if tt.legacyTarget == "image_pull" || tt.legacyTarget == "image_pull_all" {
				compose, extraArgs = resolvePullInvocation(cfg, dockerCfg, tt.allFlag, tt.services)
			} else {
				compose, extraArgs = resolveBuildInvocation(cfg, dockerCfg, tt.allFlag, tt.forceFlag, tt.services)
			}

			if !tt.expectedCompose(compose) {
				t.Errorf("%s: compose files mismatch", tt.legacyTarget)
			}
			assertArgs(t, tt.legacyTarget, extraArgs, tt.expectedArgs)
		})
	}
}

// TestAllFlagDoesNotMutateLocalConfig verifies that --all returns the full compose file set
// (including disabled services) without modifying any external state. The resolver functions
// are pure value functions; this test confirms --all expands the file set beyond the active set.
func TestAllFlagDoesNotMutateLocalConfig(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
		Services: map[string]config.ServiceConfig{
			"api": {
				Type:    config.ServiceTypeApp,
				Enabled: false,
				Compose: []string{"compose.api.yaml"},
			},
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "test-project",
		Args: config.DockerArgs{
			Pull:  []string{},
			Build: []string{},
		},
	}

	// --all should return ComposeFilesAll() (includes disabled "api" service).
	withAll, _ := resolvePullInvocation(cfg, dockerCfg, true, nil)
	withoutAll, _ := resolvePullInvocation(cfg, dockerCfg, false, nil)

	if !reflect.DeepEqual(withAll.Files, cfg.ComposeFilesAll()) {
		t.Errorf("resolvePullInvocation(--all) files = %v, want %v", withAll.Files, cfg.ComposeFilesAll())
	}
	if !reflect.DeepEqual(withoutAll.Files, cfg.ComposeFiles()) {
		t.Errorf("resolvePullInvocation(no --all) files = %v, want %v", withoutAll.Files, cfg.ComposeFiles())
	}
	if len(withAll.Files) <= len(withoutAll.Files) {
		t.Errorf("--all should include disabled services: got %d files (--all) vs %d (active only)", len(withAll.Files), len(withoutAll.Files))
	}

	// Same assertions for build.
	buildWithAll, _ := resolveBuildInvocation(cfg, dockerCfg, true, false, nil)
	buildWithoutAll, _ := resolveBuildInvocation(cfg, dockerCfg, false, false, nil)

	if !reflect.DeepEqual(buildWithAll.Files, cfg.ComposeFilesAll()) {
		t.Errorf("resolveBuildInvocation(--all) files = %v, want %v", buildWithAll.Files, cfg.ComposeFilesAll())
	}
	if !reflect.DeepEqual(buildWithoutAll.Files, cfg.ComposeFiles()) {
		t.Errorf("resolveBuildInvocation(no --all) files = %v, want %v", buildWithoutAll.Files, cfg.ComposeFiles())
	}
	if len(buildWithAll.Files) <= len(buildWithoutAll.Files) {
		t.Errorf("build --all should include disabled services: got %d files (--all) vs %d (active only)", len(buildWithAll.Files), len(buildWithoutAll.Files))
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
