package commands

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/tpl"
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "run --rm --no-deps --entrypoint ") {
		t.Errorf("expected 'run --rm --no-deps --entrypoint', got: %s", args)
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	if !strings.Contains(args, "--workdir /var/www/html") {
		t.Errorf("expected '--workdir /var/www/html', got: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_ComposeFiles(t *testing.T) {
	files := []string{"compose.yaml", "compose/services/second/app.yml"}
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", files))
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
	files := []string{"compose.yaml", "compose/services/second/app.yml"}
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
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", files))
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

func TestServiceExecRunner_BuildCommand_GlobalArgs(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "ls", nil)
	r := &ServiceExecRunner{}
	compose := testComposeWithGlobalArgs("devbox-laravel", nil, []string{"--ansi", "always", "--progress", "tty"})
	c, err := r.BuildCommand(ctx, compose)
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

func TestServiceRunRunner_BuildCommand_GlobalArgs(t *testing.T) {
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
	compose := testComposeWithGlobalArgs("devbox-laravel", nil, []string{"--ansi", "always"})
	c, err := r.BuildCommand(ctx, compose)
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

func TestServiceExecRunner_BuildCommand_ComposeArgsEmpty(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{}
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	args := strings.Join(c.Args, " ")
	// Should not have any extra flags
	if strings.Count(args, " ") < strings.Count("docker compose exec app-main", " ") {
		t.Logf("args: %s", args)
	}
}

func TestServiceExecRunner_BuildCommand_ComposeArgsLiteral(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"-T", "--name", "test-container"}
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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

func TestServiceRunRunner_BuildCommand_ComposeArgsLiteral(t *testing.T) {
	ctx := RunContext{
		Cmd: &CommandDef{
			Type:        CommandTypeServiceRun,
			Service:     "app-main",
			Run:         "php -v",
			ComposeArgs: []string{"-d", "--rm"},
		},
		Render:  &tpl.RenderContext{Host: tpl.CurrentHostInfo()},
		Config:  &config.DevboxConfig{Project: config.ProjectConfig{Prefix: "devbox", Name: "laravel"}},
		Params:  map[string]any{},
		Context: map[string]any{},
	}
	r := &ServiceRunRunner{}
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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

func TestServiceExecRunner_BuildCommand_ComposeArgsTemplate(t *testing.T) {
	ctx := makeServiceExecCtx("app-main", "", "", ExecModeExec, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"--name", "${param.name}"}
	ctx.Render.Params = map[string]any{"name": "custom-name"}
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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

func TestServiceExecRunner_BuildCommand_ComposeArgsPositioning(t *testing.T) {
	// Verify compose_args are inserted between run defaults and --user flag
	ctx := makeServiceExecCtx("app-main", UserModeRoot, "", ExecModeRun, "id", nil)
	ctx.Cmd.ComposeArgs = []string{"-d", "--name", "test"}
	r := &ServiceExecRunner{}
	c, err := r.BuildCommand(ctx, testCompose("devbox-laravel", nil))
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
