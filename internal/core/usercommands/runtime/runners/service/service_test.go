package service

import (
	"context"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// Local aliases keep the moved tests readable without rewriting every type
// qualifier.
type (
	CommandDef = model.CommandDef
	RunContext = spec.RunContext
	UserMode   = model.UserMode
	ExecMode   = model.ExecMode
	RunnerDef  = model.RunnerDef
)

const (
	CommandTypeServiceExec = model.CommandTypeServiceExec
	CommandTypeServiceRun  = model.CommandTypeServiceRun
	ExecModeExec           = model.ExecModeExec
	ExecModeRun            = model.ExecModeRun
	UserModeCurrent        = model.UserModeCurrent
	UserModeInternal       = model.UserModeInternal
	UserModeRoot           = model.UserModeRoot
)

// testCompose returns a minimal *docker.Compose for use in tests.
// Includes realistic per-command defaults matching devbox/docker.yml.
func testCompose(projectName string, files []string) *docker.Compose {
	return &docker.Compose{
		ProjectName: projectName,
		Files:       files,
		CommandArgs: map[string][]string{
			"run":  {"--rm"},
			"exec": {},
		},
	}
}

// testComposeWithGlobalArgs returns a *docker.Compose with global args set.
func testComposeWithGlobalArgs(projectName string, files []string, globalArgs []string) *docker.Compose {
	return &docker.Compose{
		ProjectName: projectName,
		Files:       files,
		GlobalArgs:  globalArgs,
		CommandArgs: map[string][]string{
			"run":  {"--rm"},
			"exec": {},
		},
	}
}

func makeServiceExecCtx(svc string, user UserMode, workdir string, mode ExecMode, run string, argv []string) RunContext {
	return RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: svc,
			User:    user,
			Workdir: workdir,
			Mode:    mode,
			Cmd:     run,
			Argv:    argv,
		},
		Render:  &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
}

func TestExecRunner_BuildCommand_ExecMode(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "php artisan list", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "exec") {
		t.Errorf("expected 'exec' in args, got: %s", args)
	}
	if !strings.Contains(args, "app-main") {
		t.Errorf("expected service name in args, got: %s", args)
	}
	if !strings.Contains(args, "sh") {
		t.Errorf("expected 'sh -c' in args, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_RunMode(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeRun, "php artisan migrate", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "run") {
		t.Errorf("expected 'run' in args, got: %s", args)
	}
	if !strings.Contains(args, "--rm") {
		t.Errorf("expected '--rm' in args, got: %s", args)
	}
	if strings.Contains(args, " exec ") {
		t.Errorf("should not have 'exec' in run mode, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_UserRoot(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "", ExecModeExec, "id", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user root") {
		t.Errorf("expected '--user root', got: %s", args)
	}
}

func TestExecRunner_BuildCommand_UserInternalSkipsFlag(t *testing.T) {
	// user: internal should never emit --user, even if cli.user is set.
	ctx := makeServiceExecCtx("app-main", UserModeInternal, "", ExecModeExec, "id", nil)
	ctx.Config.Services = map[string]config.ServiceConfig{
		"main": {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if strings.Contains(args, "--user") {
		t.Errorf("expected no --user flag for internal mode, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_EmptyUserFallsBackToCLIUser(t *testing.T) {
	// When user is omitted, services.<svc>.cli.user is used as the default.
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Config.Services = map[string]config.ServiceConfig{
		"main": {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user www-data") {
		t.Errorf("expected '--user www-data' from cli.user fallback, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_EmptyUserNoCLIUserSkipsFlag(t *testing.T) {
	// When user is omitted and cli.user is also empty, no --user flag is added.
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Config.Services = map[string]config.ServiceConfig{
		"main": {Container: "app-main"},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if strings.Contains(args, "--user") {
		t.Errorf("expected no --user flag without cli.user fallback, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_ExplicitUserOverridesCLIUser(t *testing.T) {
	// An explicit user: at the top level wins over cli.user fallback.
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "", ExecModeExec, "id", nil)
	ctx.Config.Services = map[string]config.ServiceConfig{
		"main": {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user root") {
		t.Errorf("expected explicit '--user root' to win over cli.user, got: %s", args)
	}
	if strings.Contains(args, "www-data") {
		t.Errorf("cli.user should not be applied when user: is explicit, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_RunnerServiceUsesItsOwnCLIUser(t *testing.T) {
	// runner.service redirects to a different service; cli.user fallback
	// should resolve against that redirected service, not the original.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-main",
			Mode:    ExecModeExec,
			Cmd:     "id",
			Runner: &RunnerDef{
				Service: "app-installer",
			},
		},
		Render: &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Services: map[string]config.ServiceConfig{
				"main":      {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
				"installer": {Container: "app-installer", CLI: config.ServiceCLIConfig{User: "root"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user root") {
		t.Errorf("expected cli.user of redirected service (root), got: %s", args)
	}
	if strings.Contains(args, "www-data") {
		t.Errorf("original service cli.user (www-data) leaked into args: %s", args)
	}
}

func TestExecRunner_BuildCommand_UserCurrent(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", UserModeCurrent, "", ExecModeExec, "id", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user") {
		t.Errorf("expected '--user' flag for current mode, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_Workdir(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "/var/www", ExecModeExec, "ls", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www") {
		t.Errorf("expected '--workdir /var/www', got: %s", args)
	}
}

func TestExecRunner_BuildCommand_Argv(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "", []string{"php", "artisan", "list"})
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "php artisan list") {
		t.Errorf("expected 'php artisan list' in args, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_ProjectFlag(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "ls", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "-p devbox-laravel") {
		t.Errorf("expected '-p devbox-laravel', got: %s", args)
	}
}

func TestRunRunner_BuildCommand_AlwaysRun(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-main",
			Cmd:     "composer install",
		},
		Render:  &tpl.RenderContext{},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "run --rm --no-deps --entrypoint ") {
		t.Errorf("expected 'run --rm --no-deps --entrypoint', got: %s", args)
	}
	if strings.Contains(args, " exec ") {
		t.Errorf("RunRunner must not use exec, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_RunnerOverride(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-main",
			User:    UserModeRoot,
			Mode:    ExecModeExec,
			Cmd:     "composer install",
			Runner: &RunnerDef{
				Service: "app-installer",
				User:    UserModeCurrent,
				Mode:    ExecModeRun,
			},
		},
		Render:  &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	// runner overrides service to app-installer and mode to run
	if !strings.Contains(args, "app-installer") {
		t.Errorf("expected runner service 'app-installer', got: %s", args)
	}
	if !strings.Contains(args, "run --rm --no-deps") {
		t.Errorf("expected 'run --rm' due to runner mode override, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFrom(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			WorkdirFrom: "services.main.dir_internal",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{
					"main": map[string]any{
						"dir_internal": "/var/www/html",
					},
				},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www/html") {
		t.Errorf("expected '--workdir /var/www/html', got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFromWinsOverLiteral(t *testing.T) {
	// When both workdir and workdir_from are set, the config-driven path wins.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			Workdir:     "/literal/fallback",
			WorkdirFrom: "services.main.dir_internal",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{
					"main": map[string]any{"dir_internal": "/var/www/html"},
				},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www/html") {
		t.Errorf("expected workdir_from to win, got: %s", args)
	}
	if strings.Contains(args, "/literal/fallback") {
		t.Errorf("literal workdir should not be used when workdir_from resolves, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFromMissingFallsBackToLiteral(t *testing.T) {
	// When workdir_from path is missing in config, fall back to the literal.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			Workdir:     "/literal/fallback",
			WorkdirFrom: "does.not.exist",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render:  &tpl.RenderContext{},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}, Raw: map[string]any{}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /literal/fallback") {
		t.Errorf("expected literal workdir fallback, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFromEmptyFallsBackToLiteral(t *testing.T) {
	// When workdir_from resolves to an empty string, fall back to the literal.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			Workdir:     "/literal/fallback",
			WorkdirFrom: "services.main.dir_internal",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{"main": map[string]any{"dir_internal": ""}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /literal/fallback") {
		t.Errorf("expected literal workdir fallback for empty config value, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFromNonStringErrors(t *testing.T) {
	// A non-string value at workdir_from is a hard error (configuration bug).
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			WorkdirFrom: "services.main.dir_internal",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{"main": map[string]any{"dir_internal": 42}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	if _, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil)); err == nil {
		t.Fatal("expected error for non-string workdir_from value, got nil")
	}
}

func TestExecRunner_BuildCommand_ComposeFiles(t *testing.T) {
	files := []string{"compose.yaml", "compose/services/second/app.yml"}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-second",
			Mode:    ExecModeExec,
			Cmd:     "composer install",
		},
		Render: &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{
				Base: "compose.yaml",
			},
			Services: map[string]config.ServiceConfig{
				"second": {Enabled: true, Compose: []string{"compose/services/second/app.yml"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", files))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "-f compose.yaml") {
		t.Errorf("expected '-f compose.yaml' in args, got: %s", args)
	}
	if !strings.Contains(args, "-f compose/services/second/app.yml") {
		t.Errorf("expected '-f compose/services/second/app.yml' in args, got: %s", args)
	}
}

func TestRunRunner_BuildCommand_ComposeFiles(t *testing.T) {
	files := []string{"compose.yaml", "compose/services/second/app.yml"}
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-second",
			Cmd:     "composer install",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{
				Base: "compose.yaml",
			},
			Services: map[string]config.ServiceConfig{
				"second": {Enabled: true, Compose: []string{"compose/services/second/app.yml"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", files))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "-f compose.yaml") {
		t.Errorf("expected '-f compose.yaml' in args, got: %s", args)
	}
	if !strings.Contains(args, "-f compose/services/second/app.yml") {
		t.Errorf("expected '-f compose/services/second/app.yml' in args, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_GlobalArgs(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "ls", nil)
	r := &ExecRunner{}
	compose := testComposeWithGlobalArgs("devbox-laravel", nil, []string{"--ansi", "always", "--progress", "tty"})
	c, err := r.BuildCommand(context.Background(), ctx, compose)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--ansi always --progress tty") {
		t.Errorf("expected global args in command, got: %s", args)
	}
	// Global args should appear before the subcommand (exec)
	ansiIdx := strings.Index(args, "--ansi")
	execIdx := strings.Index(args, "exec")
	if ansiIdx > execIdx {
		t.Errorf("global args should appear before exec subcommand, got: %s", args)
	}
}

func TestRunRunner_BuildCommand_GlobalArgs(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-main",
			Cmd:     "composer install",
		},
		Render:  &tpl.RenderContext{},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	compose := testComposeWithGlobalArgs("devbox-laravel", nil, []string{"--ansi", "always"})
	c, err := r.BuildCommand(context.Background(), ctx, compose)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--ansi always") {
		t.Errorf("expected global args in command, got: %s", args)
	}
}

func TestRunContext_Compose_WithDockerConfig(t *testing.T) {
	ctx := RunContext{
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
		},
		DockerConfig: &config.DockerConfig{
			ProjectName: "devbox-laravel",
			Args: config.DockerArgs{
				Global: []string{"--ansi", "always"},
				Exec:   []string{},
				Run:    []string{"--rm"},
			},
		},
	}
	compose := ctx.Compose()
	if compose.ProjectName != "devbox-laravel" {
		t.Errorf("expected project name 'devbox-laravel', got: %s", compose.ProjectName)
	}
	if len(compose.GlobalArgs) != 2 || compose.GlobalArgs[0] != "--ansi" {
		t.Errorf("expected global args from docker config, got: %v", compose.GlobalArgs)
	}
}

func TestRunContext_Compose_WithoutDockerConfig(t *testing.T) {
	ctx := RunContext{
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
		},
	}
	compose := ctx.Compose()
	if compose.ProjectName != "devbox-laravel" {
		t.Errorf("expected project name 'devbox-laravel', got: %s", compose.ProjectName)
	}
	if len(compose.GlobalArgs) != 0 {
		t.Errorf("expected no global args without docker config, got: %v", compose.GlobalArgs)
	}
}

func TestRunContext_Compose_NilConfig(t *testing.T) {
	ctx := RunContext{}
	compose := ctx.Compose()
	if compose.ProjectName != "" {
		t.Errorf("expected empty project name, got: %s", compose.ProjectName)
	}
	if compose.Files != nil {
		t.Errorf("expected nil files, got: %v", compose.Files)
	}
}

func TestExecRunner_BuildCommand_ComposeArgsEmpty(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "exec") || !strings.Contains(args, "app-main") {
		t.Errorf("expected exec command targeting app-main, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_ComposeArgsLiteral(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"-T", "--name", "test-container"}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := c.Args
	// Find where compose_args should be: after "exec" and before "--user"
	var foundT, foundName, foundTestContainer bool
	var tIndex, userIndex int
	for i, arg := range args {
		if arg == "-T" {
			foundT = true
			tIndex = i
		}
		if arg == "--name" {
			foundName = true
		}
		if arg == "test-container" {
			foundTestContainer = true
		}
		if arg == "--user" {
			userIndex = i
		}
	}
	if !foundT || !foundName || !foundTestContainer {
		t.Errorf("expected -T --name test-container in args, got: %v", args)
	}
	if userIndex > 0 && tIndex > 0 && tIndex >= userIndex {
		t.Errorf("expected -T before --user, but -T was at index %d and --user at %d", tIndex, userIndex)
	}
}

func TestRunRunner_BuildCommand_ComposeArgsLiteral(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceRun,
			Service:     "app-main",
			Cmd:         "php -v",
			ComposeArgs: []string{"-d", "--rm"},
		},
		Render:  &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := c.Args
	// Verify -d and --rm are present
	found := false
	for i, arg := range args {
		if arg == "-d" {
			found = true
			// Check that -d comes after "run" but before "--workdir" (if present)
			for j := i + 1; j < len(args); j++ {
				if args[j] == "--workdir" {
					t.Errorf("expected -d before --workdir, but --workdir at index %d", j)
					break
				}
			}
			break
		}
	}
	if !found {
		t.Errorf("expected -d in args, got: %v", args)
	}
}

func TestExecRunner_BuildCommand_ComposeArgsTemplate(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"--name", "${param.name}"}
	ctx.Render.Params = map[string]any{"name": "custom-name"}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "custom-name") {
		t.Errorf("expected 'custom-name' after template substitution, got: %s", args)
	}
	if strings.Contains(args, "${param.name}") {
		t.Errorf("expected template to be rendered, but found literal ${param.name} in: %s", args)
	}
}

func TestExecRunner_BuildCommand_ComposeArgsPositioning(t *testing.T) {
	// Verify compose_args are inserted between run defaults and --user flag
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "", ExecModeRun, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"-d", "--name", "test"}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := c.Args
	// Find indices of key markers
	runIdx, dIdx, userIdx := -1, -1, -1
	for i, arg := range args {
		if arg == "run" {
			runIdx = i
		}
		if arg == "-d" {
			dIdx = i
		}
		if arg == "--user" {
			userIdx = i
		}
	}
	if runIdx < 0 || dIdx < 0 || userIdx < 0 {
		t.Fatalf("couldn't find expected args in %v", args)
	}
	if dIdx <= runIdx || dIdx >= userIdx {
		t.Errorf("compose_args (-d) should be between run and --user, got indices: run=%d, -d=%d, --user=%d", runIdx, dIdx, userIdx)
	}
}

func TestExecRunner_BuildCommand_ServiceTemplated(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-${param.service}",
			Mode:    ExecModeExec,
			Cmd:     "id",
		},
		Render: &tpl.RenderContext{
			Host:   tpl.CurrentHostInfo(),
			Params: map[string]any{"service": "catalog"},
		},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{"service": "catalog"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, " app-catalog") {
		t.Errorf("expected rendered 'app-catalog' as service in args, got: %s", args)
	}
	if strings.Contains(args, "${param.service}") {
		t.Errorf("expected service template to be rendered, found literal in: %s", args)
	}
}

func TestRunRunner_BuildCommand_ServiceTemplated(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-${param.service}",
			Cmd:     "id",
		},
		Render: &tpl.RenderContext{
			Host:   tpl.CurrentHostInfo(),
			Params: map[string]any{"service": "main"},
		},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{"service": "main"},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, " app-main") {
		t.Errorf("expected rendered 'app-main' as service in args, got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirFromTemplated(t *testing.T) {
	// workdir_from itself can be a template so the same generic command works
	// across multiple services keyed by ${param.service}.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-${param.service}",
			WorkdirFrom: "services.${param.service}.work_dir_internal",
			Mode:        ExecModeExec,
			Cmd:         "ls",
		},
		Render: &tpl.RenderContext{
			Params: map[string]any{"service": "catalog"},
		},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{
					"catalog": map[string]any{"work_dir_internal": "/workspace/src"},
				},
			},
		},
		Params:  map[string]any{"service": "catalog"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /workspace/src") {
		t.Errorf("expected rendered workdir_from to resolve to '/workspace/src', got: %s", args)
	}
}

func TestExecRunner_BuildCommand_WorkdirLiteralTemplated(t *testing.T) {
	// The literal workdir field is also rendered as a template, matching the
	// same contract as argv/cmd/compose_args.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-main",
			Workdir: "/workspace/${param.subdir}",
			Mode:    ExecModeExec,
			Cmd:     "ls",
		},
		Render: &tpl.RenderContext{
			Params: map[string]any{"subdir": "src"},
		},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{"subdir": "src"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /workspace/src") {
		t.Errorf("expected rendered workdir '/workspace/src', got: %s", args)
	}
}

func TestExecRunner_BuildCommand_RunnerServiceTemplated(t *testing.T) {
	// runner.service / runner.workdir_from are also rendered so the override
	// block behaves identically to the top-level fields.
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-main",
			Mode:    ExecModeExec,
			Cmd:     "id",
			Runner: &RunnerDef{
				Service:     "app-${param.service}",
				WorkdirFrom: "services.${param.service}.work_dir_internal",
			},
		},
		Render: &tpl.RenderContext{
			Host:   tpl.CurrentHostInfo(),
			Params: map[string]any{"service": "catalog"},
		},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{
					"catalog": map[string]any{"work_dir_internal": "/workspace/catalog"},
				},
			},
		},
		Params:  map[string]any{"service": "catalog"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, " app-catalog") {
		t.Errorf("expected runner.service template rendered to 'app-catalog', got: %s", args)
	}
	if !strings.Contains(args, "--workdir /workspace/catalog") {
		t.Errorf("expected runner.workdir_from template resolved to '/workspace/catalog', got: %s", args)
	}
}

// TestBuildServiceArgv_ShellFromConfig verifies that buildServiceArgv uses
// cfg.Binaries.Shell instead of a hardcoded "sh".
func TestBuildServiceArgv_ShellFromConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.DevboxConfig
		wantShell string
	}{
		{
			name:      "nil config defaults to sh",
			cfg:       nil,
			wantShell: "sh",
		},
		{
			name:      "empty config defaults to sh",
			cfg:       &config.DevboxConfig{},
			wantShell: "sh",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := RunContext{
				Cmd: &CommandDef{
					Type: CommandTypeServiceExec,
					Cmd:  "echo hello",
				},
				Config: tc.cfg,
				Render: &tpl.RenderContext{},
			}
			argv, err := buildServiceArgv(ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(argv) == 0 || argv[0] != tc.wantShell {
				t.Errorf("argv[0] = %q, want %q", argv[0], tc.wantShell)
			}
		})
	}
}
