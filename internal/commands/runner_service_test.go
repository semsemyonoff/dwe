package commands

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
)

func makeServiceExecCtx(svc string, user UserMode, workdir string, mode ExecMode, run string, argv []string) RunContext {
	return RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: svc,
			User:    user,
			Workdir: workdir,
			Mode:    mode,
			Run:     run,
			Argv:    argv,
		},
		Render:  &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
}

func TestServiceExecRunner_BuildCommand_ExecMode(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "php artisan list", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
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

func TestServiceExecRunner_BuildCommand_RunMode(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeRun, "php artisan migrate", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
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

func TestServiceExecRunner_BuildCommand_UserRoot(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "", ExecModeExec, "id", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user root") {
		t.Errorf("expected '--user root', got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_UserCurrent(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", UserModeCurrent, "", ExecModeExec, "id", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--user") {
		t.Errorf("expected '--user' flag for current mode, got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_Workdir(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "/var/www", ExecModeExec, "ls", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www") {
		t.Errorf("expected '--workdir /var/www', got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_Argv(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "", []string{"php", "artisan", "list"})
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "php artisan list") {
		t.Errorf("expected 'php artisan list' in args, got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_ProjectFlag(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "ls", nil)
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "-p devbox-laravel") {
		t.Errorf("expected '-p devbox-laravel', got: %s", args)
	}
}

func TestServiceRunRunner_BuildCommand_AlwaysRun(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-main",
			Run:     "composer install",
		},
		Render:  &tpl.RenderContext{},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ServiceRunRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "run --rm --no-deps") {
		t.Errorf("expected 'run --rm', got: %s", args)
	}
	if strings.Contains(args, " exec ") {
		t.Errorf("ServiceRunRunner must not use exec, got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_RunnerOverride(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-main",
			User:    UserModeRoot,
			Mode:    ExecModeExec,
			Run:     "composer install",
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
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
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

func TestServiceExecRunner_BuildCommand_WorkdirFrom(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceExec,
			Service:     "app-main",
			WorkdirFrom: "services.main.dir_internal",
			Mode:        ExecModeExec,
			Run:         "ls",
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
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www/html") {
		t.Errorf("expected '--workdir /var/www/html', got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_ComposeFiles(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceExec,
			Service: "app-second",
			Mode:    ExecModeExec,
			Run:     "composer install",
		},
		Render: &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{
				Base:     "compose.yaml",
				Overlays: map[string]string{},
			},
			Services: map[string]config.ServiceConfig{
				"second": {Enabled: true, Compose: []string{"compose/services/second/app.yml"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
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

func TestServiceRunRunner_BuildCommand_ComposeFiles(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:    CommandTypeServiceRun,
			Service: "app-second",
			Run:     "composer install",
		},
		Render: &tpl.RenderContext{},
		Config: &config.DevboxConfig{
			Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"},
			Compose: config.ComposeConfig{
				Base:     "compose.yaml",
				Overlays: map[string]string{},
			},
			Services: map[string]config.ServiceConfig{
				"second": {Enabled: true, Compose: []string{"compose/services/second/app.yml"}},
			},
		},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ServiceRunRunner{}
	c, err := r.BuildCommand(ctx, "devbox-laravel")
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

func TestResolveProjectFull(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *config.DevboxConfig
		expected string
	}{
		{"nil config", nil, ""},
		{"prefix and name", &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}}, "devbox-laravel"},
		{"name only", &config.DevboxConfig{Project: config.ProjectConfig{Name: "laravel"}}, "laravel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveProjectFull(tt.cfg)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}
