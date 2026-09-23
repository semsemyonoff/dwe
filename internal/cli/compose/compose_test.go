package compose

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

func makeComposeCfg(base string, services map[string]config.ServiceConfig) *config.DweConfig {
	return &config.DweConfig{
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

// TestComposeSubcommands verifies the compose command group exposes exactly the
// files, raw and argv subcommands.
func TestComposeSubcommands(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
	composeCmd := NewCmd("", flags)

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
	cfg := &config.DweConfig{
		Compose: config.ComposeConfig{
			Base: "compose.yaml",
		},
	}
	dockerCfg := &config.DockerConfig{
		ProjectName: "dwe-laravel",
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

	compose := docker.NewCompose(cfg, dockerCfg, "")

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
			contains: []string{"docker", "compose", "-p", "dwe-laravel", "-f", "compose.yaml", "--ansi", "always", "up", "-d", "--remove-orphans", "redis"},
		},
		{
			name:     "down no extras",
			command:  "down",
			extra:    nil,
			contains: []string{"docker", "compose", "-p", "dwe-laravel", "-f", "compose.yaml", "--ansi", "always", "down"},
		},
		{
			name:     "logs with service",
			command:  "logs",
			extra:    []string{"nginx"},
			contains: []string{"docker", "compose", "-p", "dwe-laravel", "logs", "-f", "nginx"},
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
	flags := &cmdctx.RootFlags{ConfigPath: "workspace.yml"}
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

// TestParseRawFlags verifies --bare / --all parsing from raw args.
func TestParseRawFlags(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantBare bool
		wantAll  bool
		wantRest []string
		wantErr  error
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
		{
			name:     "all before separator",
			args:     []string{"--all", "--", "config"},
			wantAll:  true,
			wantRest: []string{"config"},
		},
		{
			name:     "all without separator",
			args:     []string{"--all", "config", "--services"},
			wantAll:  true,
			wantRest: []string{"config", "--services"},
		},
		{
			name:     "all only",
			args:     []string{"--all"},
			wantAll:  true,
			wantRest: nil,
		},
		{
			name:     "all after separator passes through to compose",
			args:     []string{"--", "ps", "--all"},
			wantRest: []string{"ps", "--all"},
		},
		{
			name:     "all directly after leading separator is still dwe's flag",
			args:     []string{"--", "--all", "ps"},
			wantAll:  true,
			wantRest: []string{"ps"},
		},
		{
			name:     "all after positional passes through to compose",
			args:     []string{"ps", "--all"},
			wantRest: []string{"ps", "--all"},
		},
		{
			name:     "bare directly after leading separator is still dwe's flag",
			args:     []string{"--", "--bare"},
			wantBare: true,
			wantRest: nil,
		},
		{
			name:     "bare on both sides of leading separator",
			args:     []string{"--bare", "--", "--bare", "up"},
			wantBare: true,
			wantRest: []string{"up"},
		},
		{
			name:     "second separator before positional passes through",
			args:     []string{"--", "--", "ps"},
			wantRest: []string{"--", "ps"},
		},
		{
			name:     "bare after positional passes through to compose",
			args:     []string{"--", "up", "--bare"},
			wantRest: []string{"up", "--bare"},
		},
		{
			name:     "all before separator and after it",
			args:     []string{"--all", "--", "ps", "--all"},
			wantAll:  true,
			wantRest: []string{"ps", "--all"},
		},
		{
			name:    "bare and all are mutually exclusive",
			args:    []string{"--bare", "--all", "--", "ps"},
			wantErr: errBareWithAll,
		},
		{
			name:    "all and bare are mutually exclusive in either order",
			args:    []string{"--all", "--bare"},
			wantErr: errBareWithAll,
		},
		{
			name:     "bare with all passed through to compose is fine",
			args:     []string{"--bare", "--", "ps", "--all"},
			wantBare: true,
			wantRest: []string{"ps", "--all"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, rest, err := parseRawFlags(tt.args)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if opts.bare != tt.wantBare {
				t.Errorf("bare = %v, want %v", opts.bare, tt.wantBare)
			}
			if opts.all != tt.wantAll {
				t.Errorf("all = %v, want %v", opts.all, tt.wantAll)
			}
			if !slices.Equal(rest, tt.wantRest) {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

// makeOverlayProject extends makeMinimalProject with a compose base and two
// tool overlays: otel (enabled) and adminer (disabled). The active chain is
// base + otel; ComposeFilesAll adds adminer, sorted ahead of otel.
func makeOverlayProject(t *testing.T) string {
	t.Helper()
	dir := makeMinimalProject(t)
	cfgYAML := "project:\n  name: test\n  prefix: dwe\ncompose:\n  base: compose.yaml\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"adminer", "otel"} {
		svcDir := filepath.Join(dir, "workspace", "services", name)
		if err := os.MkdirAll(svcDir, 0o755); err != nil {
			t.Fatal(err)
		}
		svcYML := "type: tool\ncontainer: " + name + "\ncompose:\n  - compose/" + name + ".yml\n"
		if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte(svcYML), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	defaultsYML := "schema_version: \"1\"\nservices:\n  otel:\n    enabled: true\n  adminer:\n    enabled: false\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace", "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

var (
	enabledChain = []string{"compose.yaml", "compose/otel.yml"}
	allChain     = []string{"compose.yaml", "compose/adminer.yml", "compose/otel.yml"}
)

// fFiles returns the values of every -f flag in argv, in order.
func fFiles(argv []string) []string {
	var files []string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] == "-f" {
			files = append(files, argv[i+1])
		}
	}
	return files
}

func TestComposeFilesCmd_all(t *testing.T) {
	dir := makeOverlayProject(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "enabled overlays only", args: nil, want: enabledChain},
		{name: "all overlays", args: []string{"--all"}, want: allChain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newComposeFilesCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
			if !slices.Equal(got, tt.want) {
				t.Errorf("printed lines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComposeArgvCmd_all(t *testing.T) {
	dir := makeOverlayProject(t)
	tests := []struct {
		name      string
		args      []string
		wantFiles []string
		wantTail  []string
	}{
		{
			name:      "enabled overlays only",
			args:      []string{"config"},
			wantFiles: enabledChain,
			wantTail:  []string{"config"},
		},
		{
			name:      "all before command",
			args:      []string{"--all", "config"},
			wantFiles: allChain,
			wantTail:  []string{"config"},
		},
		{
			name:      "all after command passes through to compose",
			args:      []string{"ps", "--all"},
			wantFiles: enabledChain,
			wantTail:  []string{"ps", "--all"},
		},
		{
			name:      "all trailing a command's own args passes through",
			args:      []string{"exec", "app", "ls", "--all"},
			wantFiles: enabledChain,
			wantTail:  []string{"exec", "app", "ls", "--all"},
		},
		{
			name:      "all after separator passes through to compose",
			args:      []string{"ps", "--", "--all"},
			wantFiles: enabledChain,
			wantTail:  []string{"ps", "--all"},
		},
		{
			name:      "separator after command is dropped as before",
			args:      []string{"ps", "--", "-q"},
			wantFiles: enabledChain,
			wantTail:  []string{"ps", "-q"},
		},
		{
			name:      "leading separator is dropped, a later one kept",
			args:      []string{"--", "run", "app", "--", "ls"},
			wantFiles: enabledChain,
			wantTail:  []string{"run", "--rm", "app", "--", "ls"},
		},
		{
			name:      "only the first separator after command is dropped",
			args:      []string{"run", "--", "app", "--", "ls"},
			wantFiles: enabledChain,
			wantTail:  []string{"run", "--rm", "app", "--", "ls"},
		},
		{
			name:      "all before separator and command",
			args:      []string{"--all", "--", "ps"},
			wantFiles: allChain,
			wantTail:  []string{"ps"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newComposeArgvCmd(&cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			argv := strings.Fields(buf.String())
			if got := fFiles(argv); !slices.Equal(got, tt.wantFiles) {
				t.Errorf("-f files = %q, want %q (argv %q)", got, tt.wantFiles, argv)
			}
			if !slices.Equal(argv[len(argv)-len(tt.wantTail):], tt.wantTail) {
				t.Errorf("argv %q does not end with %q", argv, tt.wantTail)
			}
		})
	}
}

// TestComposeArgvCmd_allThroughTree runs argv via the `compose` parent, so the
// non-interspersed parsing is checked after cobra merges the parent's flags.
func TestComposeArgvCmd_allThroughTree(t *testing.T) {
	dir := makeOverlayProject(t)
	tests := []struct {
		name      string
		args      []string
		wantFiles []string
		wantTail  []string
	}{
		{name: "trailing all reaches compose", args: []string{"argv", "exec", "app", "ls", "--all"}, wantFiles: enabledChain, wantTail: []string{"exec", "app", "ls", "--all"}},
		{name: "leading all widens the chain", args: []string{"argv", "--all", "ps"}, wantFiles: allChain, wantTail: []string{"ps"}},
		{name: "separator after command dropped", args: []string{"argv", "ps", "--", "-q"}, wantFiles: enabledChain, wantTail: []string{"ps", "-q"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCmd("", &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")})
			var buf bytes.Buffer
			cmd.SetOut(&buf)
			cmd.SetErr(&buf)
			cmd.SetArgs(tt.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			argv := strings.Fields(buf.String())
			if got := fFiles(argv); !slices.Equal(got, tt.wantFiles) {
				t.Errorf("-f files = %q, want %q (argv %q)", got, tt.wantFiles, argv)
			}
			if !slices.Equal(argv[len(argv)-len(tt.wantTail):], tt.wantTail) {
				t.Errorf("argv %q does not end with %q", argv, tt.wantTail)
			}
		})
	}
}

// TestBuildRawComposeArgs covers `dwe compose raw` argv construction end to
// end from the raw args, short of executing docker.
func TestBuildRawComposeArgs(t *testing.T) {
	dir := makeOverlayProject(t)
	cfg, err := config.LoadConfig(filepath.Join(dir, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "enabled overlays only",
			args: []string{"--", "config"},
			want: []string{"-p", "dwe-test", "-f", "compose.yaml", "-f", "compose/otel.yml", "config"},
		},
		{
			name: "all overlays",
			args: []string{"--all", "--", "config"},
			want: []string{"-p", "dwe-test", "-f", "compose.yaml", "-f", "compose/adminer.yml", "-f", "compose/otel.yml", "config"},
		},
		{
			name: "all after separator reaches compose, chain stays enabled-only",
			args: []string{"--", "ps", "--all"},
			want: []string{"-p", "dwe-test", "-f", "compose.yaml", "-f", "compose/otel.yml", "ps", "--all"},
		},
		{
			name: "bare drops every -f",
			args: []string{"--bare", "--", "-f", "installer.yml", "up"},
			want: []string{"-p", "dwe-test", "-f", "installer.yml", "up"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts, rest, err := parseRawFlags(tt.args)
			if err != nil {
				t.Fatalf("parseRawFlags: %v", err)
			}
			got := buildRawComposeArgs(cfg, "dwe-test", opts, rest)
			if !slices.Equal(got, tt.want) {
				t.Errorf("argv = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestComposeRawCmd_bareWithAll pins that the conflict is reported before any
// config load or docker exec.
func TestComposeRawCmd_bareWithAll(t *testing.T) {
	cmd := newComposeRawCmd(&cmdctx.RootFlags{ConfigPath: "/nonexistent/workspace.yml"})
	if err := cmd.RunE(cmd, []string{"--bare", "--all", "--", "ps"}); !errors.Is(err, errBareWithAll) {
		t.Fatalf("err = %v, want %v", err, errBareWithAll)
	}
}

func commandNames(cmds []*cobra.Command) []string {
	names := make([]string, len(cmds))
	for i, c := range cmds {
		names[i] = c.Name()
	}
	return names
}

func makeMinimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cfgYAML := `project:
  name: test
  prefix: dwe
`
	if err := os.WriteFile(filepath.Join(dir, "workspace.yml"), []byte(cfgYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	svcDir := filepath.Join(workspaceDir, "services", "main")
	if err := os.MkdirAll(svcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "service.yml"), []byte("type: app\ndir: ./services/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerYML := "project_name: dwe-test\n"
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(dockerYML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestComposeFilesCmd_RunE(t *testing.T) {
	dir := makeMinimalProject(t)
	composeYAML := "services:\n  web:\n    image: nginx\n"
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(composeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
	cmd := newComposeFilesCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestComposeFilesCmd_RunE_composeAfterTier(t *testing.T) {
	dir := makeMinimalProject(t)
	toolDir := filepath.Join(dir, "workspace", "services", "otel")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	toolYML := "type: tool\ncontainer: otel\ncompose_after:\n  - compose/otel-apps.yml\n"
	if err := os.WriteFile(filepath.Join(toolDir, "service.yml"), []byte(toolYML), 0o644); err != nil {
		t.Fatal(err)
	}
	defaultsYML := "schema_version: \"1\"\nservices:\n  otel:\n    enabled: true\n"
	if err := os.WriteFile(filepath.Join(dir, "workspace", "defaults.yml"), []byte(defaultsYML), 0o644); err != nil {
		t.Fatal(err)
	}

	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}

	cmd := newComposeFilesCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// The minimal project declares no compose.base, and the tool has no
	// compose: of its own, so the compose_after file is the whole chain.
	want := []string{"compose/otel-apps.yml"}
	if !slices.Equal(got, want) {
		t.Errorf("printed lines = %v, want %v", got, want)
	}
}

func TestComposeFilesCmd_RunE_InvalidConfig(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "/nonexistent/workspace.yml"}
	cmd := newComposeFilesCmd(flags)
	if err := cmd.RunE(cmd, nil); err == nil {
		t.Fatal("expected error for invalid config path")
	}
}

func TestComposeArgvCmd_RunE(t *testing.T) {
	dir := makeMinimalProject(t)
	flags := &cmdctx.RootFlags{ConfigPath: filepath.Join(dir, "workspace.yml")}
	cmd := newComposeArgvCmd(flags)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	if err := cmd.RunE(cmd, []string{"up"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "docker compose") {
		t.Errorf("expected 'docker compose' in output, got: %q", buf.String())
	}
}

func TestComposeArgvCmd_RunE_InvalidConfig(t *testing.T) {
	flags := &cmdctx.RootFlags{ConfigPath: "/nonexistent/workspace.yml"}
	cmd := newComposeArgvCmd(flags)
	if err := cmd.RunE(cmd, []string{"up"}); err == nil {
		t.Fatal("expected error for invalid config path")
	}
}
