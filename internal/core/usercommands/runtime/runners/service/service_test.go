package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"

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
// Includes realistic per-command defaults matching workspace/docker.yml.
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
}

func TestExecRunner_BuildCommand_ExecMode(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "php artisan list", nil)
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Services: map[string]config.ServiceConfig{
				"main":      {Container: "app-main", CLI: config.ServiceCLIConfig{User: "www-data"}},
				"installer": {Container: "app-installer", CLI: config.ServiceCLIConfig{User: "root"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "-p dwe-laravel") {
		t.Errorf("expected '-p dwe-laravel', got: %s", args)
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}, Raw: map[string]any{}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{"main": map[string]any{"dir_internal": ""}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Raw: map[string]any{
				"services": map[string]any{"main": map[string]any{"dir_internal": 42}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	if _, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil)); err == nil {
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", files))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", files))
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
	compose := testComposeWithGlobalArgs("dwe-laravel", nil, []string{"--ansi", "always", "--progress", "tty"})
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	compose := testComposeWithGlobalArgs("dwe-laravel", nil, []string{"--ansi", "always"})
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
		},
		DockerConfig: &config.DockerConfig{
			ProjectName: "dwe-laravel",
			Args: config.DockerArgs{
				Global: []string{"--ansi", "always"},
				Exec:   []string{},
				Run:    []string{"--rm"},
			},
		},
	}
	compose := ctx.Compose()
	if compose.ProjectName != "dwe-laravel" {
		t.Errorf("expected project name 'dwe-laravel', got: %s", compose.ProjectName)
	}
	if len(compose.GlobalArgs) != 2 || compose.GlobalArgs[0] != "--ansi" {
		t.Errorf("expected global args from docker config, got: %v", compose.GlobalArgs)
	}
}

func TestRunContext_Compose_WithoutDockerConfig(t *testing.T) {
	ctx := RunContext{
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
		},
	}
	compose := ctx.Compose()
	if compose.ProjectName != "dwe-laravel" {
		t.Errorf("expected project name 'dwe-laravel', got: %s", compose.ProjectName)
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{"service": "catalog"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{"service": "main"},
		Context: map[string]any{},
	}
	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config:  &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		Params:  map[string]any{"subdir": "src"},
		Context: map[string]any{},
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
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
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
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

// TestBuildServiceArgv_ShellFromConfig verifies that buildServiceArgv resolves
// the shell via config.ShellBin instead of a hardcoded "sh".
func TestBuildServiceArgv_ShellFromConfig(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *config.DweConfig
		wantShell string
	}{
		{
			name:      "nil config defaults to sh",
			cfg:       nil,
			wantShell: "sh",
		},
		{
			name:      "empty config defaults to sh",
			cfg:       &config.DweConfig{},
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
			argv, err := buildServiceArgv(context.Background(), ctx)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(argv) == 0 || argv[0] != tc.wantShell {
				t.Errorf("argv[0] = %q, want %q", argv[0], tc.wantShell)
			}
		})
	}
}

// TestExecRunner_BuildCommand_ArgvAppendFrom: the expression runs on the HOST
// (it computes the argument list) while its items land at the tail of the
// container argv — the whole point of the field is that a container command can
// receive a host-computed file list without the author rebuilding
// `docker compose exec` by hand.
func TestExecRunner_BuildCommand_ArgvAppendFrom(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:           CommandTypeServiceExec,
			ID:             "quality.staged",
			Service:        "app-main",
			Mode:           ExecModeExec,
			Argv:           []string{"ruff", "check", "${args}"},
			ArgvAppendFrom: `printf '%s\n' 'src/a b.py' 'src/c.py'`,
		},
		Render:      &tpl.RenderContext{Args: []string{"--fix"}},
		Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		ProjectRoot: t.TempDir(),
	}
	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	tail := c.Args[len(c.Args)-6:]
	want := []string{"app-main", "ruff", "check", "--fix", "src/a b.py", "src/c.py"}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("argv tail = %q, want %q", tail, want)
		}
	}
}

// TestExecRunner_BuildCommand_ArgvAppendFromEmpty: the skip sentinel must reach
// the caller instead of `ruff check` running with no file list (which would
// lint the whole tree).
func TestExecRunner_BuildCommand_ArgvAppendFromEmpty(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:           CommandTypeServiceExec,
			ID:             "quality.staged",
			Service:        "app-main",
			Mode:           ExecModeExec,
			Argv:           []string{"ruff", "check"},
			ArgvAppendFrom: "true",
		},
		Render:      &tpl.RenderContext{},
		Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		ProjectRoot: t.TempDir(),
	}
	r := &ExecRunner{}
	if _, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil)); !errors.Is(err, spec.ErrArgvAppendEmpty) {
		t.Fatalf("err = %v, want spec.ErrArgvAppendEmpty", err)
	}
}

// TestExecRunner_BuildCommand_ProbesBeforeArgvAppendFrom pins the ordering
// inside BuildCommand: the exec-or-fail container probe must run BEFORE the
// argv_append_from expression.
//
// Probing second lets a stopped service be reported as
// "skipped (nothing to process)" with exit 0 whenever the expression happens to
// yield nothing — hiding that the container is down — and executes the
// expression's host side effects for an invocation that could never have run.
func TestExecRunner_BuildCommand_ProbesBeforeArgvAppendFrom(t *testing.T) {
	baseDir := t.TempDir()

	// A stand-in for `docker` that always reports no running container: empty
	// stdout is how isContainerRunning spells "not running".
	fakeDocker := filepath.Join(baseDir, "fake-docker")
	if err := os.WriteFile(fakeDocker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("writing fake docker: %v", err)
	}

	marker := filepath.Join(baseDir, "expression-ran")
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:           CommandTypeServiceExec,
			ID:             "quality.staged",
			Service:        "app-main",
			Mode:           model.ExecModeExecOrFail,
			Argv:           []string{"ruff", "check"},
			ArgvAppendFrom: "touch " + marker,
		},
		Render:      &tpl.RenderContext{},
		Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		ProjectRoot: baseDir,
	}

	compose := testCompose("dwe-laravel", nil)
	compose.Bin = fakeDocker
	compose.BaseDir = baseDir

	r := &ExecRunner{}
	_, err := r.BuildCommand(context.Background(), ctx, compose)
	if err == nil {
		t.Fatal("expected the not-running diagnostic, got nil")
	}
	if errors.Is(err, spec.ErrArgvAppendEmpty) {
		t.Fatalf("a stopped service must not be reported as an empty item list: %v", err)
	}
	if !strings.Contains(err.Error(), "is not running") {
		t.Fatalf("err = %v, want the exec-or-fail not-running diagnostic", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("argv_append_from ran despite the service being down")
	}
}

// TestExecRunner_BuildCommand_WorkdirChain walks every rung of the workdir
// chain documented on resolveServiceFields, including the two rungs no local
// workspace exercises (work_dir_internal without cli.workdir, and the
// `internal` opt-out sentinel).
func TestExecRunner_BuildCommand_WorkdirChain(t *testing.T) {
	rawDirInternal := map[string]any{
		"services": map[string]any{
			"main": map[string]any{"dir_internal": "/from/config"},
		},
	}

	tests := []struct {
		name     string
		cmd      *CommandDef
		services map[string]config.ServiceConfig
		raw      map[string]any
		want     string // "" means: no --workdir flag at all
	}{
		{
			name: "sentinel skips the fallback",
			cmd:  &CommandDef{Workdir: "internal"},
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", CLI: config.ServiceCLIConfig{WorkDir: "/cli"}},
			},
		},
		{
			name: "sentinel inside runner block",
			cmd:  &CommandDef{Runner: &RunnerDef{Workdir: "internal"}},
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", CLI: config.ServiceCLIConfig{WorkDir: "/cli"}},
			},
		},
		{
			name: "sentinel outranks workdir_from",
			cmd:  &CommandDef{Workdir: "internal", WorkdirFrom: "services.main.dir_internal"},
			raw:  rawDirInternal,
		},
		{
			name: "workdir_from beats the literal",
			cmd:  &CommandDef{Workdir: "/literal", WorkdirFrom: "services.main.dir_internal"},
			raw:  rawDirInternal,
			want: "/from/config",
		},
		{
			name: "literal beats cli.workdir",
			cmd:  &CommandDef{Workdir: "/literal"},
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", CLI: config.ServiceCLIConfig{WorkDir: "/cli"}},
			},
			want: "/literal",
		},
		{
			name: "cli.workdir beats work_dir_internal",
			cmd:  &CommandDef{},
			services: map[string]config.ServiceConfig{
				"main": {
					Container:       "app-main",
					CLI:             config.ServiceCLIConfig{WorkDir: "/cli"},
					WorkDirInternal: "/work",
					DirInternal:     "/dir",
				},
			},
			want: "/cli",
		},
		{
			name: "work_dir_internal without cli.workdir",
			cmd:  &CommandDef{},
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", WorkDirInternal: "/work", DirInternal: "/dir"},
			},
			want: "/work",
		},
		{
			name: "dir_internal is the last rung",
			cmd:  &CommandDef{},
			services: map[string]config.ServiceConfig{
				"main": {Container: "app-main", DirInternal: "/dir"},
			},
			want: "/dir",
		},
		{
			name: "container differs from the services map key",
			cmd:  &CommandDef{},
			services: map[string]config.ServiceConfig{
				"other": {Container: "app-other", DirInternal: "/other"},
				"main":  {Container: "app-main", DirInternal: "/dir"},
			},
			want: "/dir",
		},
		{
			name: "no service entry leaves the image WORKDIR alone",
			cmd:  &CommandDef{},
			services: map[string]config.ServiceConfig{
				"other": {Container: "app-other", DirInternal: "/other"},
			},
		},
		{
			name: "nothing declared anywhere",
			cmd:  &CommandDef{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			def := *tt.cmd
			def.Type = CommandTypeServiceExec
			def.Service = "app-main"
			def.Mode = ExecModeExec
			def.Cmd = "ls"

			ctx := RunContext{
				Cmd:    &def,
				Render: &tpl.RenderContext{},
				Config: &config.DweConfig{
					Project:  config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
					Services: tt.services,
					Raw:      tt.raw,
				},
				Params:  map[string]any{},
				Context: map[string]any{},
			}

			r := &ExecRunner{}
			c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			args := strings.Join(c.Args, " ")
			if tt.want == "" {
				if strings.Contains(args, "--workdir") {
					t.Fatalf("expected no --workdir flag, got: %s", args)
				}
				return
			}
			if !strings.Contains(args, "--workdir "+tt.want) {
				t.Fatalf("expected '--workdir %s', got: %s", tt.want, args)
			}
		})
	}
}

// TestRunRunner_BuildCommand_WorkdirChain pins that service_run inherits the
// same chain through the shared resolveServiceFields helper.
func TestRunRunner_BuildCommand_WorkdirChain(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"main": {Container: "app-main", WorkDirInternal: "/work"},
	}

	newCtx := func(workdir string) RunContext {
		return RunContext{
			Cmd: &CommandDef{
				Type:    CommandTypeServiceRun,
				Service: "app-main",
				Workdir: workdir,
				Cmd:     "composer install",
			},
			Render: &tpl.RenderContext{},
			Config: &config.DweConfig{
				Project:  config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
				Services: services,
			},
			Params:  map[string]any{},
			Context: map[string]any{},
		}
	}

	r := &RunRunner{}
	c, err := r.BuildCommand(context.Background(), newCtx(""), testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args := strings.Join(c.Args, " "); !strings.Contains(args, "--workdir /work") {
		t.Fatalf("expected the service fallback to apply to service_run, got: %s", args)
	}

	c, err = r.BuildCommand(context.Background(), newCtx("internal"), testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args := strings.Join(c.Args, " "); strings.Contains(args, "--workdir") {
		t.Fatalf("expected the internal sentinel to suppress --workdir, got: %s", args)
	}
}

// clearColorEnv guarantees the colour-control vars are absent for one test, so
// the only thing that can force colours is the suppressed-TTY disjunct.
func clearColorEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NO_COLOR", "CLICOLOR_FORCE", "DWE_BRIDGE_STDIN_TTY"} {
		t.Setenv(name, "x")
		_ = os.Unsetenv(name)
	}
}

// countArg returns how many times an exact argv token appears.
func countArg(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}

// openPTY returns a pty slave usable as a terminal-backed stdio stream.
func openPTY(t *testing.T) *os.File {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Skipf("pty.Open: %v", err)
	}
	t.Cleanup(func() { _ = tty.Close(); _ = ptmx.Close() })
	return tty
}

// TestExecRunner_BuildCommand_ContainerTTY walks the auto-detect: a run the
// user did not launch themselves gets `-T`, an interactive one does not, and
// any TTY flag already present in compose_args hands the decision back to the
// author whatever its value.
func TestExecRunner_BuildCommand_ContainerTTY(t *testing.T) {
	tty := openPTY(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	tests := []struct {
		name        string
		composeArgs []string
		userInvoked bool
		stdout      io.Writer
		stdin       io.Reader
		wantT       int
	}{
		{"pipeline step gets -T", nil, false, nil, nil, 1},
		{"user invocation on a terminal keeps the tty", nil, true, tty, tty, 0},
		{"user invocation on a pipe gets -T", nil, true, pw, pr, 1},
		{"explicit -T is not duplicated", []string{"-T"}, false, nil, nil, 1},
		{"-T=false suppresses the injection", []string{"-T=false"}, false, nil, nil, 0},
		{"--no-tty suppresses the injection", []string{"--no-tty"}, false, nil, nil, 0},
		{"--no-tty=false is the force-a-tty escape hatch", []string{"--no-tty=false"}, false, nil, nil, 0},
		{"--no-TTY matches case-insensitively", []string{"--no-TTY"}, false, nil, nil, 0},
		{"an unrelated flag does not suppress the injection", []string{"--name", "box"}, false, nil, nil, 1},
		{"-d is orthogonal to the tty decision", []string{"-d"}, false, nil, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorEnv(t)
			ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
			ctx.Cmd.ComposeArgs = tt.composeArgs
			ctx.UserInvoked = tt.userInvoked
			ctx.Stdout = tt.stdout
			ctx.Stdin = tt.stdin

			r := &ExecRunner{}
			c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := countArg(c.Args, "-T"); got != tt.wantT {
				t.Errorf("-T count = %d, want %d; args: %v", got, tt.wantT, c.Args)
			}
		})
	}
}

// TestExecRunner_BuildCommand_ContainerTTY_Bridged pins the bridge arm: this
// dwe's own streams are pipes, yet the far side sits at a real terminal and
// WireChildIO will fabricate a PTY after the argv is fixed — so no `-T`.
func TestExecRunner_BuildCommand_ContainerTTY_Bridged(t *testing.T) {
	if term.IsTerminal(os.Stdout.Fd()) {
		t.Skip("the bridged shape requires this process's own stdout to be a pipe")
	}
	clearColorEnv(t)
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("DWE_BRIDGE_STDIN_TTY", "1")

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.UserInvoked = true
	ctx.Stdout = pw
	ctx.Stdin = pr

	r := &ExecRunner{}
	c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := countArg(c.Args, "-T"); got != 0 {
		t.Errorf("bridged-interactive run must keep the container tty; args: %v", c.Args)
	}
}

// TestRunRunner_BuildCommand_ContainerTTY proves service_run inherits the same
// behaviour — it shares buildDockerComposeCmd with the exec runner.
func TestRunRunner_BuildCommand_ContainerTTY(t *testing.T) {
	tty := openPTY(t)

	tests := []struct {
		name        string
		composeArgs []string
		userInvoked bool
		stdout      io.Writer
		stdin       io.Reader
		wantT       int
	}{
		{"pipeline step gets -T", nil, false, nil, nil, 1},
		{"user invocation on a terminal keeps the tty", nil, true, tty, tty, 0},
		{"--no-tty=false suppresses the injection", []string{"--no-tty=false"}, false, nil, nil, 0},
		{"-d is orthogonal", []string{"-d"}, false, nil, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorEnv(t)
			ctx := RunContext{
				Cmd: &CommandDef{
					Type:        CommandTypeServiceRun,
					Service:     "app-main",
					Cmd:         "php -v",
					ComposeArgs: tt.composeArgs,
				},
				Render:      &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
				Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
				Params:      map[string]any{},
				Context:     map[string]any{},
				UserInvoked: tt.userInvoked,
				Stdout:      tt.stdout,
				Stdin:       tt.stdin,
			}
			r := &RunRunner{}
			c, err := r.BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := countArg(c.Args, "-T"); got != tt.wantT {
				t.Errorf("-T count = %d, want %d; args: %v", got, tt.wantT, c.Args)
			}
		})
	}
}

// TestBuildCommand_ContainerTTY_FromDockerConfigArgs pins that the classifier
// reads the EFFECTIVE flag vector: docker.yml's args.exec / args.run defaults
// are appended before compose_args and must be just as visible.
func TestBuildCommand_ContainerTTY_FromDockerConfigArgs(t *testing.T) {
	composeWith := func(key string, defaults []string) *docker.Compose {
		return &docker.Compose{
			ProjectName: "dwe-laravel",
			CommandArgs: map[string][]string{key: defaults},
		}
	}

	t.Run("exec: --no-tty=false in args.exec suppresses the injection", func(t *testing.T) {
		clearColorEnv(t)
		ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
		c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, composeWith("exec", []string{"--no-tty=false"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := countArg(c.Args, "-T"); got != 0 {
			t.Errorf("args.exec TTY flag must hand the decision to the author; args: %v", c.Args)
		}
	})

	t.Run("run: --no-tty=false in args.run suppresses the injection", func(t *testing.T) {
		clearColorEnv(t)
		ctx := makeServiceExecCtx("app-main", "", "", ExecModeRun, "id", nil)
		c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, composeWith("run", []string{"--no-tty=false"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := countArg(c.Args, "-T"); got != 0 {
			t.Errorf("args.run TTY flag must hand the decision to the author; args: %v", c.Args)
		}
	})

	t.Run("exec: -d in args.exec suppresses the forced colour but not -T", func(t *testing.T) {
		clearColorEnv(t)
		ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
		ctx.Stdout = openPTY(t)
		c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, composeWith("exec", []string{"-d"}))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got := countArg(c.Args, "-T"); got != 1 {
			t.Errorf("detach is orthogonal to the tty decision; args: %v", c.Args)
		}
		assertNoForcedColor(t, c)
	})
}

// assertForcedColor / assertNoForcedColor inspect both surfaces the colour
// trio reaches: the `-e KEY` forwarding flags and the child's own env.
func assertForcedColor(t *testing.T, c *exec.Cmd) {
	t.Helper()
	joined := strings.Join(c.Args, " ")
	for _, key := range []string{"CLICOLOR_FORCE", "FORCE_COLOR", "COLORTERM"} {
		if !strings.Contains(joined, "-e "+key) {
			t.Errorf("expected -e %s to be forwarded, got: %s", key, joined)
		}
	}
	if !slices.Contains(c.Env, "CLICOLOR_FORCE=1") {
		t.Errorf("expected CLICOLOR_FORCE=1 in child env, got: %v", c.Env)
	}
}

func assertNoForcedColor(t *testing.T, c *exec.Cmd) {
	t.Helper()
	joined := strings.Join(c.Args, " ")
	for _, key := range []string{"CLICOLOR_FORCE", "FORCE_COLOR", "COLORTERM"} {
		if strings.Contains(joined, key) {
			t.Errorf("did not expect %s to be forwarded, got: %s", key, joined)
		}
	}
	if slices.Contains(c.Env, "CLICOLOR_FORCE=1") {
		t.Errorf("did not expect CLICOLOR_FORCE=1 in child env, got: %v", c.Env)
	}
}

// TestBuildCommand_SuppressedTTYForcesColor covers the paired half of the `-T`
// injection: taking the container's terminal away also takes its colours away
// unless dwe forces them back — except when the child is detached, where its
// output goes to the Docker logs and ANSI escapes would be permanent.
func TestBuildCommand_SuppressedTTYForcesColor(t *testing.T) {
	tests := []struct {
		name        string
		mode        ExecMode
		runner      string
		composeArgs []string
		stdout      bool // true → pty-backed rc.Stdout
		wantColor   bool
	}{
		{"exec, tty suppressed", ExecModeExec, "exec", nil, true, true},
		{"exec, detached", ExecModeExec, "exec", []string{"-d"}, true, false},
		{"exec, --detach", ExecModeExec, "exec", []string{"--detach"}, true, false},
		{"exec, piped stdout", ExecModeExec, "exec", nil, false, false},
		{"run, tty suppressed", ExecModeRun, "exec", nil, true, true},
		{"run, detached", ExecModeRun, "exec", []string{"-d"}, true, false},
		{"service_run, tty suppressed", "", "run", nil, true, true},
		{"service_run, detached", "", "run", []string{"-d"}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearColorEnv(t)
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatalf("os.Pipe: %v", err)
			}
			t.Cleanup(func() { _ = pr.Close(); _ = pw.Close() })

			var stdout io.Writer = pw
			if tt.stdout {
				stdout = openPTY(t)
			}

			ctx := makeServiceExecCtx("app-main", "", "", tt.mode, "id", nil)
			ctx.Cmd.ComposeArgs = tt.composeArgs
			ctx.Stdout = stdout

			var c *exec.Cmd
			if tt.runner == "run" {
				ctx.Cmd.Type = CommandTypeServiceRun
				c, err = (&RunRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			} else {
				c, err = (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantColor {
				assertForcedColor(t, c)
			} else {
				assertNoForcedColor(t, c)
			}
		})
	}
}

// TestExecRunner_BuildCommand_ContainerTTYPositioning pins that the injected
// -T lands after compose_args and before the flags the runner derives, so an
// author's own compose_args keep their documented position.
func TestExecRunner_BuildCommand_ContainerTTYPositioning(t *testing.T) {
	clearColorEnv(t)
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "/srv", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"--name", "box"}

	c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	idx := func(want string) int { return slices.Index(c.Args, want) }
	nameIdx, tIdx, userIdx, svcIdx := idx("--name"), idx("-T"), idx("--user"), idx("app-main")
	if nameIdx < 0 || tIdx < 0 || userIdx < 0 || svcIdx < 0 {
		t.Fatalf("missing expected args in %v", c.Args)
	}
	if nameIdx >= tIdx || tIdx >= userIdx || userIdx >= svcIdx {
		t.Errorf("expected --name < -T < --user < service, got %d/%d/%d/%d in %v", nameIdx, tIdx, userIdx, svcIdx, c.Args)
	}
}

// composeSubcommand returns the compose subcommand ("exec" or "run") the
// runner picked, by scanning past the `docker compose` prefix and the project
// flag. Tests use it instead of comparing a full argv so they stay agnostic to
// the flags the runner derives (notably the container-TTY `-T`).
func composeSubcommand(t *testing.T, args []string) string {
	t.Helper()
	for _, a := range args[1:] {
		if a == "exec" || a == "run" {
			return a
		}
	}
	t.Fatalf("no compose subcommand in %v", args)
	return ""
}

// stubContainerRunning replaces the container probe seam for the duration of
// the test. The real probe shells out to `docker compose ps`, which is why the
// mode-dependent branches are otherwise untestable in-process.
func stubContainerRunning(t *testing.T, running bool, err error) *int {
	t.Helper()
	calls := 0
	prev := containerRunningFn
	containerRunningFn = func(*docker.Compose, string) (bool, error) {
		calls++
		return running, err
	}
	t.Cleanup(func() { containerRunningFn = prev })
	return &calls
}

// TestExecRunner_BuildCommand_DefaultModeFallsBackToRun pins the flipped
// default: a command that declares no `mode:` takes the exec-or-run branch, so
// a stopped service produces an ephemeral `docker compose run --rm` plus the
// warning that says so, rather than the exec-or-fail refusal.
func TestExecRunner_BuildCommand_DefaultModeFallsBackToRun(t *testing.T) {
	stubContainerRunning(t, false, nil)

	var stderr bytes.Buffer
	ctx := makeServiceExecCtx("app-main", "", "", "", "php artisan migrate", nil)
	ctx.Stderr = &stderr

	c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := composeSubcommand(t, c.Args); got != "run" {
		t.Errorf("compose subcommand = %q, want run (the exec-or-run fallback)", got)
	}
	if !slices.Contains(c.Args, "--rm") {
		t.Errorf("expected the ephemeral --rm in %v", c.Args)
	}
	if warn := stderr.String(); !strings.Contains(warn, "is not running") ||
		!strings.Contains(warn, "ephemeral") {
		t.Errorf("missing the exec-or-run fallback warning, stderr = %q", warn)
	}
}

// TestExecRunner_BuildCommand_DefaultModeUsesExecWhenRunning is the other half
// of the default: a running container still gets a plain exec, and no warning.
func TestExecRunner_BuildCommand_DefaultModeUsesExecWhenRunning(t *testing.T) {
	stubContainerRunning(t, true, nil)

	var stderr bytes.Buffer
	ctx := makeServiceExecCtx("app-main", "", "", "", "php artisan migrate", nil)
	ctx.Stderr = &stderr

	c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := composeSubcommand(t, c.Args); got != "exec" {
		t.Errorf("compose subcommand = %q, want exec", got)
	}
	if stderr.Len() != 0 {
		t.Errorf("no fallback happened, but stderr = %q", stderr.String())
	}
}

// TestExecRunner_BuildCommand_ExplicitExecOrFailStillRefuses proves opting back
// in works: with the default flipped to exec-or-run, `mode: exec-or-fail` is
// how a command declares that it must never create a container, and it still
// refuses with the dwe-level error rather than a raw compose trace.
func TestExecRunner_BuildCommand_ExplicitExecOrFailStillRefuses(t *testing.T) {
	stubContainerRunning(t, false, nil)

	ctx := makeServiceExecCtx("app-main", "", "", model.ExecModeExecOrFail, "php artisan migrate", nil)

	_, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
	if err == nil {
		t.Fatal("expected the exec-or-fail refusal, got nil")
	}
	if !strings.Contains(err.Error(), "is not running") ||
		!strings.Contains(err.Error(), "exec-or-fail") {
		t.Errorf("err = %v, want the exec-or-fail not-running diagnostic", err)
	}
}

// TestExecRunner_BuildCommand_ProbeErrorSelectsExec pins that a probe *error*
// (as opposed to a "not running" answer) selects exec on BOTH probing modes and
// emits no warning — compose is left to report the real problem itself. This is
// pre-existing behaviour on both branches; flipping the default must not
// change it.
func TestExecRunner_BuildCommand_ProbeErrorSelectsExec(t *testing.T) {
	for _, mode := range []ExecMode{"", model.ExecModeExecOrRun, model.ExecModeExecOrFail} {
		t.Run(string(mode), func(t *testing.T) {
			calls := stubContainerRunning(t, false, errors.New("docker daemon unreachable"))

			var stderr bytes.Buffer
			ctx := makeServiceExecCtx("app-main", "", "", mode, "php artisan migrate", nil)
			ctx.Stderr = &stderr

			c, err := (&ExecRunner{}).BuildCommand(context.Background(), ctx, testCompose("dwe-laravel", nil))
			if err != nil {
				t.Fatalf("a probe error must not fail the build: %v", err)
			}
			if *calls != 1 {
				t.Errorf("probe called %d times, want 1", *calls)
			}
			if got := composeSubcommand(t, c.Args); got != "exec" {
				t.Errorf("compose subcommand = %q, want exec", got)
			}
			if stderr.Len() != 0 {
				t.Errorf("a probe error must not warn about an ephemeral fallback, stderr = %q", stderr.String())
			}
		})
	}
}
