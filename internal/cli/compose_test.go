package command

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/docker"
	"devbox-cli/internal/shared/render"

	"github.com/spf13/cobra"
)

func makeComposeCfg(base string, services map[string]config.ServiceConfig) *config.DevboxConfig {
	return &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: base,
		},
		Services: services,
	}
}

func mkToolSvc(enabled bool, compose string) config.ServiceConfig {
	svc := config.ServiceConfig{Type: config.ServiceTypeTool, Enabled: enabled}
	if compose != "" {
		svc.Compose = []string{compose}
	}
	return svc
}

func TestBuildComposeFileList_baseOnly(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", nil)
	got := cfg.ComposeFiles()
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want [compose.yaml]", got)
	}
}

func TestBuildComposeFileList_noBase(t *testing.T) {
	cfg := makeComposeCfg("", map[string]config.ServiceConfig{
		"adminer": mkToolSvc(true, "compose/tools/adminer.yml"),
	})
	got := cfg.ComposeFiles()
	// No base — only adminer overlay
	if len(got) != 1 || got[0] != "compose/tools/adminer.yml" {
		t.Errorf("got %v, want [compose/tools/adminer.yml]", got)
	}
}

func TestBuildComposeFileList_oneToolEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"adminer":       mkToolSvc(false, "compose/tools/adminer.yml"),
		"redis_insight": mkToolSvc(true, "compose/tools/redis_insight.yml"),
		"mailpit":       mkToolSvc(false, "compose/tools/mailpit.yml"),
	})
	got := cfg.ComposeFiles()
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	if got[0] != "compose.yaml" {
		t.Errorf("got[0] = %q, want compose.yaml", got[0])
	}
	if got[1] != "compose/tools/redis_insight.yml" {
		t.Errorf("got[1] = %q, want compose/tools/redis_insight.yml", got[1])
	}
}

func TestBuildComposeFileList_multipleToolsEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"adminer":       mkToolSvc(true, "compose/tools/adminer.yml"),
		"redis_insight": mkToolSvc(true, "compose/tools/redis_insight.yml"),
		"mailpit":       mkToolSvc(false, "compose/tools/mailpit.yml"),
	})
	got := cfg.ComposeFiles()
	// base + adminer + redis_insight (sorted: adminer < redis_insight < mailpit)
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 entries", got)
	}
	if got[0] != "compose.yaml" {
		t.Errorf("got[0] = %q, want compose.yaml", got[0])
	}
	if got[1] != "compose/tools/adminer.yml" {
		t.Errorf("got[1] = %q, want adminer.yml", got[1])
	}
	if got[2] != "compose/tools/redis_insight.yml" {
		t.Errorf("got[2] = %q, want redis_insight.yml", got[2])
	}
}

func TestBuildComposeFileList_disabledExcluded(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"adminer": mkToolSvc(false, "compose/tools/adminer.yml"),
		"mailpit": mkToolSvc(false, "compose/tools/mailpit.yml"),
	})
	got := cfg.ComposeFiles()
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want only base", got)
	}
}

func TestBuildComposeFileList_serviceOverlayEnabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"main-debug": {
			Type:      config.ServiceTypeApp,
			Container: "app-main-debug",
			Enabled:   true,
			Compose:   []string{"compose/services/main/debug.yml"},
		},
	})
	got := cfg.ComposeFiles()
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 entries", got)
	}
	if got[1] != "compose/services/main/debug.yml" {
		t.Errorf("got[1] = %q, want debug.yml", got[1])
	}
}

func TestBuildComposeFileList_serviceOverlayDisabled(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"main-debug": {
			Type:      config.ServiceTypeApp,
			Container: "app-main-debug",
			Enabled:   false,
			Compose:   []string{"compose/services/main/debug.yml"},
		},
	})
	got := cfg.ComposeFiles()
	if len(got) != 1 {
		t.Errorf("got %v, want only base (service disabled)", got)
	}
}

func TestBuildComposeFileList_unknownToolWithoutCompose(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"unknown_tool": {Type: config.ServiceTypeTool, Enabled: true},
	})
	got := cfg.ComposeFiles()
	// unknown tool without a compose file → only base
	if len(got) != 1 || got[0] != "compose.yaml" {
		t.Errorf("got %v, want only base for tool without compose", got)
	}
}

func TestBuildComposeFileList_orderToolsInfraApps(t *testing.T) {
	cfg := makeComposeCfg("compose.yaml", map[string]config.ServiceConfig{
		"app1":  {Type: config.ServiceTypeApp, Enabled: true, Compose: []string{"compose/services/app1.yml"}},
		"db":    {Type: config.ServiceTypeInfra, Enabled: true, Compose: []string{"compose/infra/db.yml"}},
		"adm":   mkToolSvc(true, "compose/tools/adm.yml"),
		"web":   {Type: config.ServiceTypeApp, Enabled: true, Compose: []string{"compose/services/web.yml"}},
		"cache": {Type: config.ServiceTypeInfra, Enabled: true, Compose: []string{"compose/infra/cache.yml"}},
	})
	got := cfg.ComposeFiles()
	want := []string{
		"compose.yaml",
		"compose/tools/adm.yml",
		"compose/infra/cache.yml",
		"compose/infra/db.yml",
		"compose/services/app1.yml",
		"compose/services/web.yml",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// discardWriter returns a render.Writer that discards all output (useful in tests).
func discardWriter() *render.Writer {
	return render.NewWriter(io.Discard)
}

func TestWaitContainersHealthy_allHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) { return "healthy", nil }
	err := docker.WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestWaitContainersHealthy_unhealthyReturnsError(t *testing.T) {
	getHealth := func(id string) (string, error) { return "unhealthy", nil }
	err := docker.WaitContainersHealthy([]string{"c1"}, getHealth, 3, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected error for unhealthy container, got nil")
	}
}

func TestWaitContainersHealthy_noHealthcheckSkipped(t *testing.T) {
	getHealth := func(id string) (string, error) { return "none", nil }
	err := docker.WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil for no-healthcheck containers, got %v", err)
	}
}

func TestWaitContainersHealthy_startingThenHealthy(t *testing.T) {
	calls := 0
	getHealth := func(id string) (string, error) {
		calls++
		if calls < 3 {
			return "starting", nil
		}
		return "healthy", nil
	}
	err := docker.WaitContainersHealthy([]string{"c1"}, getHealth, 5, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if calls < 3 {
		t.Errorf("expected at least 3 calls, got %d", calls)
	}
}

func TestWaitContainersHealthy_timeout(t *testing.T) {
	getHealth := func(id string) (string, error) { return "starting", nil }
	err := docker.WaitContainersHealthy([]string{"c1"}, getHealth, 2, time.Millisecond, discardWriter())
	if err == nil {
		t.Error("expected timeout error, got nil")
	}
}

func TestWaitContainersHealthy_mixedNoHealthcheckAndHealthy(t *testing.T) {
	getHealth := func(id string) (string, error) {
		if id == "c1" {
			return "none", nil
		}
		return "healthy", nil
	}
	err := docker.WaitContainersHealthy([]string{"c1", "c2"}, getHealth, 3, time.Millisecond, discardWriter())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

// TestComposeSubcommands verifies the compose command group has the expected subcommands
// after the refactor: files, raw, argv (wait removed, run renamed to raw).
func TestComposeSubcommands(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	composeCmd := newComposeCmd(flags)

	expectedSubs := []string{"files", "raw", "argv"}
	commands := composeCmd.Commands()

	if len(commands) != len(expectedSubs) {
		t.Fatalf("compose has %d subcommands, want %d: %v", len(commands), len(expectedSubs), commandNames(commands))
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

// TestComposeArgvOutput verifies that `compose argv` produces the correct output
// using the docker package's BuildArgs.
func TestComposeArgvOutput(t *testing.T) {
	cfg := &config.DevboxConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "devbox-laravel",
		Args: config.DockerArgs{
			Global:  []string{"--ansi", "always"},
			Up:      []string{"-d", "--remove-orphans"},
			Down:    []string{},
			Stop:    []string{},
			Restart: []string{},
			Logs:    []string{"-f"},
			Ps:      []string{},
			Exec:    []string{},
			Run:     []string{"--rm"},
		},
	}

	compose := docker.NewCompose(cfg, dockerCfg)

	tests := []struct {
		name     string
		command  string
		extra    []string
		contains []string
	}{
		{
			name:     "up with service",
			command:  "up",
			extra:    []string{"redis"},
			contains: []string{"docker", "compose", "-p", "devbox-laravel", "-f", "compose.yaml", "--ansi", "always", "up", "-d", "--remove-orphans", "redis"},
		},
		{
			name:     "down no extras",
			command:  "down",
			extra:    nil,
			contains: []string{"docker", "compose", "-p", "devbox-laravel", "-f", "compose.yaml", "--ansi", "always", "down"},
		},
		{
			name:     "logs with service",
			command:  "logs",
			extra:    []string{"nginx"},
			contains: []string{"docker", "compose", "-p", "devbox-laravel", "logs", "-f", "nginx"},
		},
		{
			name:     "run with command",
			command:  "run",
			extra:    []string{"app-main", "--", "composer", "install"},
			contains: []string{"run", "--rm", "app-main", "--", "composer", "install"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := compose.BuildArgs(tt.command, tt.extra...)
			// BuildArgs returns without "docker" prefix; the output line prepends "docker "
			output := "docker " + strings.Join(args, " ")

			for _, want := range tt.contains {
				if !strings.Contains(output, want) {
					t.Errorf("output %q missing %q", output, want)
				}
			}
		})
	}
}

// TestComposeArgvCmd verifies the cobra command for `compose argv` requires at least one arg.
func TestComposeArgvCmd(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "devbox.yml"}
	cmd := newComposeArgvCmd(flags)

	// No args should fail validation.
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when no args provided to argv, got nil")
	}
}

// TestExtractBareFlag verifies --bare parsing from raw args.
func TestExtractBareFlag(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantBare bool
		wantRest []string
	}{
		{
			name:     "no flags",
			args:     []string{"--", "ps"},
			wantBare: false,
			wantRest: []string{"ps"},
		},
		{
			name:     "bare before separator",
			args:     []string{"--bare", "--", "-f", "installer.yml", "run", "--rm", "app"},
			wantBare: true,
			wantRest: []string{"-f", "installer.yml", "run", "--rm", "app"},
		},
		{
			name:     "bare only",
			args:     []string{"--bare"},
			wantBare: true,
			wantRest: nil,
		},
		{
			name:     "empty args",
			args:     nil,
			wantBare: false,
			wantRest: nil,
		},
		{
			name:     "separator only",
			args:     []string{"--"},
			wantBare: false,
			wantRest: nil,
		},
		{
			name:     "bare without separator",
			args:     []string{"--bare", "up"},
			wantBare: true,
			wantRest: []string{"up"},
		},
		{
			name:     "separator in docker args preserved when no leading separator",
			args:     []string{"run", "app-main", "php", "artisan", "test", "--", "--filter", "Foo"},
			wantBare: false,
			wantRest: []string{"run", "app-main", "php", "artisan", "test", "--", "--filter", "Foo"},
		},
		{
			name:     "leading separator stripped but inner separator preserved",
			args:     []string{"--", "run", "app-main", "php", "artisan", "test", "--", "--filter", "Foo"},
			wantBare: false,
			wantRest: []string{"run", "app-main", "php", "artisan", "test", "--", "--filter", "Foo"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bare, rest := extractBareFlag(tt.args)
			if bare != tt.wantBare {
				t.Errorf("bare = %v, want %v", bare, tt.wantBare)
			}
			if len(rest) != len(tt.wantRest) {
				t.Fatalf("rest = %v, want %v", rest, tt.wantRest)
			}
			for i := range rest {
				if rest[i] != tt.wantRest[i] {
					t.Errorf("rest[%d] = %q, want %q", i, rest[i], tt.wantRest[i])
				}
			}
		})
	}
}

func commandNames(cmds []*cobra.Command) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name()
	}
	return names
}
