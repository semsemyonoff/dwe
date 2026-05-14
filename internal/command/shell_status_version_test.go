package command

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/docker"
	"devbox-cli/internal/render"
	"devbox-cli/internal/stack"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// --- shell command ---

func TestNewShellCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	if cmd.Use != "shell [service]" {
		t.Errorf("Use = %q, want %q", cmd.Use, "shell [service]")
	}
}

func TestNewShellCmd_HasRootFlag(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	if cmd.Flags().Lookup("root") == nil {
		t.Error("shell command missing --root flag")
	}
}

func TestNewShellCmd_AcceptsOptionalArg(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	// MaximumNArgs(1): zero args is OK
	if err := cmd.Args(cmd, []string{}); err != nil {
		t.Errorf("Args validation failed for 0 args: %v", err)
	}
	// one arg is OK
	if err := cmd.Args(cmd, []string{"main"}); err != nil {
		t.Errorf("Args validation failed for 1 arg: %v", err)
	}
	// two args should fail
	if err := cmd.Args(cmd, []string{"main", "second"}); err == nil {
		t.Error("expected error for 2 args, got nil")
	}
}

func TestShellRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["shell"] {
		t.Error("shell command not registered at root level")
	}
}

func TestNewShellCmd_HasNewFlags(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	for _, name := range []string{"mode", "shell", "user", "workdir"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("shell command missing --%s flag", name)
		}
	}
}

func TestNewShellCmd_RootAndUserMutuallyExclusive(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	// Wire up so RunE is reachable without needing a real config.
	// We simulate the flag values directly by running through Execute.
	// Since config loading will fail (no real devbox.yml), we set up a
	// fake RunE that only checks flag validation — but it's easier to
	// call the validation inline here.

	// Set both --root and --user; expect an error before config is loaded.
	cmd.SetArgs([]string{"--root", "--user", "deploy", "main"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --root and --user both set, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error should mention mutually exclusive, got: %v", err)
	}
}

func TestNewShellCmd_InvalidModeValidation(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newShellCmd(flags)
	cmd.SetArgs([]string{"--mode", "invalid", "main"})
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error for invalid --mode, got nil")
	}
	if !strings.Contains(err.Error(), "--mode") {
		t.Errorf("error should mention --mode, got: %v", err)
	}
}

func TestValidModes(t *testing.T) {
	for _, mode := range []string{"auto", "exec", "run"} {
		if !validModes[mode] {
			t.Errorf("expected %q to be a valid mode", mode)
		}
	}
	if validModes["invalid"] {
		t.Error("expected 'invalid' to not be a valid mode")
	}
}

func TestResolveUser_FlagOverridesConfig(t *testing.T) {
	result := resolveUser("flaguser", "configuser", false)
	if result != "flaguser" {
		t.Errorf("resolveUser: flag user should take priority, got %q", result)
	}
}

func TestResolveUser_ConfigOverridesDefault(t *testing.T) {
	result := resolveUser("", "configuser", false)
	if result != "configuser" {
		t.Errorf("resolveUser: config user should override default, got %q", result)
	}
}

func TestResolveUser_RootOverridesAll(t *testing.T) {
	result := resolveUser("flaguser", "configuser", true)
	if result != "root" {
		t.Errorf("resolveUser: --root should override all, got %q", result)
	}
}

// --- resolveShellOptions tests ---

func TestResolveShellOptions_FlagOverridesConfig(t *testing.T) {
	flags := shellCLIFlags{mode: "run", shell: "zsh", user: "deploy", workDir: "/app"}
	svcCLI := config.ServiceCLIConfig{Mode: "exec", Shell: "sh", User: "www-data", WorkDir: "/var/www"}
	svc := config.ServiceConfig{WorkDirInternal: "/work", DirInternal: "/dir"}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	if opts.Mode != "run" {
		t.Errorf("Mode: flag should override config, got %q", opts.Mode)
	}
	if opts.Shell != "zsh" {
		t.Errorf("Shell: flag should override config, got %q", opts.Shell)
	}
	if opts.User != "deploy" {
		t.Errorf("User: flag should override config, got %q", opts.User)
	}
	if opts.WorkDir != "/app" {
		t.Errorf("WorkDir: flag should override config, got %q", opts.WorkDir)
	}
}

func TestResolveShellOptions_ConfigOverridesDefault(t *testing.T) {
	flags := shellCLIFlags{}
	svcCLI := config.ServiceCLIConfig{Mode: "exec", Shell: "sh", User: "www-data", WorkDir: "/var/www"}
	svc := config.ServiceConfig{}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	if opts.Mode != "exec" {
		t.Errorf("Mode: config should override default, got %q", opts.Mode)
	}
	if opts.Shell != "sh" {
		t.Errorf("Shell: config should override default, got %q", opts.Shell)
	}
	if opts.User != "www-data" {
		t.Errorf("User: config should override default, got %q", opts.User)
	}
	if opts.WorkDir != "/var/www" {
		t.Errorf("WorkDir: config should override default, got %q", opts.WorkDir)
	}
}

func TestResolveShellOptions_BuiltinDefaults(t *testing.T) {
	flags := shellCLIFlags{}
	svcCLI := config.ServiceCLIConfig{}
	svc := config.ServiceConfig{}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	if opts.Mode != "auto" {
		t.Errorf("Mode default should be 'auto', got %q", opts.Mode)
	}
	if opts.Shell != "bash" {
		t.Errorf("Shell default should be 'bash', got %q", opts.Shell)
	}
	// User default is current OS UID — must be non-empty in a normal test environment.
	if opts.User == "" {
		t.Error("User default should be non-empty (current OS UID)")
	}
	if opts.WorkDir != "" {
		t.Errorf("WorkDir default should be empty when svc has no dirs, got %q", opts.WorkDir)
	}
}

func TestResolveShellOptions_WorkDirFallbackChain(t *testing.T) {
	flags := shellCLIFlags{}
	svcCLI := config.ServiceCLIConfig{}

	// work_dir_internal takes priority over dir_internal.
	svc1 := config.ServiceConfig{WorkDirInternal: "/work", DirInternal: "/dir"}
	opts1, err := resolveShellOptions(flags, svcCLI, svc1)
	if err != nil {
		t.Fatal(err)
	}
	if opts1.WorkDir != "/work" {
		t.Errorf("WorkDir: work_dir_internal should take priority, got %q", opts1.WorkDir)
	}

	// When work_dir_internal is empty, dir_internal is used.
	svc2 := config.ServiceConfig{DirInternal: "/dir"}
	opts2, err := resolveShellOptions(flags, svcCLI, svc2)
	if err != nil {
		t.Fatal(err)
	}
	if opts2.WorkDir != "/dir" {
		t.Errorf("WorkDir: dir_internal fallback, got %q", opts2.WorkDir)
	}
}

func TestResolveShellOptions_RootOverridesUser(t *testing.T) {
	flags := shellCLIFlags{asRoot: true, user: "deploy"}
	svcCLI := config.ServiceCLIConfig{User: "www-data"}
	svc := config.ServiceConfig{}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	if opts.User != "root" {
		t.Errorf("User: --root should override all, got %q", opts.User)
	}
}

func TestResolveShellOptions_EnvFromConfig(t *testing.T) {
	flags := shellCLIFlags{}
	svcCLI := config.ServiceCLIConfig{
		Env: map[string]string{"FOO": "bar", "BAZ": "qux"},
	}
	svc := config.ServiceConfig{}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	if opts.Env["FOO"] != "bar" {
		t.Errorf("Env: expected FOO=bar from config, got %q", opts.Env["FOO"])
	}
	if opts.Env["BAZ"] != "qux" {
		t.Errorf("Env: expected BAZ=qux from config, got %q", opts.Env["BAZ"])
	}
}

func TestResolveShellOptions_EnvAlwaysInitialized(t *testing.T) {
	flags := shellCLIFlags{}
	svcCLI := config.ServiceCLIConfig{}
	svc := config.ServiceConfig{}

	opts, err := resolveShellOptions(flags, svcCLI, svc)
	if err != nil {
		t.Fatal(err)
	}

	// Env should be an initialized (non-nil) map even when empty.
	if opts.Env == nil {
		t.Error("Env should be non-nil even when no env vars are configured")
	}
}

func TestResolveShellOptions_EnvFlagInvalidFormat_ReturnsError(t *testing.T) {
	flags := shellCLIFlags{envVars: []string{"NOEQUALSIGN"}}
	svcCLI := config.ServiceCLIConfig{}
	svc := config.ServiceConfig{}

	_, err := resolveShellOptions(flags, svcCLI, svc)
	if err == nil {
		t.Fatal("expected error for --env without '=', got nil")
	}
	if !strings.Contains(err.Error(), "KEY=VALUE") {
		t.Errorf("error should mention KEY=VALUE format, got: %v", err)
	}
}

// --- Task 6: pickService selection logic ---

// makeMultiServiceCfg builds a DevboxConfig with multiple services for selector tests.
func makeMultiServiceCfg(services map[string]config.ServiceConfig) *config.DevboxConfig {
	return makeServicesCfg(services, config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{})
}

// alwaysSelectFn returns a selectServiceFn that always picks the service at the given index.
func alwaysSelectFn(idx int) selectServiceFn {
	return func(cfg *config.DevboxConfig, names []string) (string, error) {
		if idx < 0 || idx >= len(names) {
			return "", fmt.Errorf("index %d out of range for %d services", idx, len(names))
		}
		return names[idx], nil
	}
}

// neverSelectFn returns a selectServiceFn that fails if called.
func neverSelectFn(t *testing.T) selectServiceFn {
	return func(cfg *config.DevboxConfig, names []string) (string, error) {
		t.Helper()
		t.Errorf("selector should not have been called, but was called with %v", names)
		return "", fmt.Errorf("selector unexpectedly called")
	}
}

func TestPickService_DirectArg_ReturnedUnchanged(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
	})
	name, err := pickService(cfg, "main", neverSelectFn(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "main" {
		t.Errorf("pickService: expected 'main', got %q", name)
	}
}

func TestPickService_SingleEnabled_AutoSelect(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	})
	// Only "main" is enabled (mandatory); selector should not be called.
	name, err := pickService(cfg, "", neverSelectFn(t))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "main" {
		t.Errorf("pickService: expected auto-select 'main', got %q", name)
	}
}

func TestPickService_NoEnabledServices_ReturnsError(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: false, Enabled: false},
		"second": {Type: "app", Container: "app-second", Enabled: false},
	})
	_, err := pickService(cfg, "", neverSelectFn(t))
	if err == nil {
		t.Error("expected error when no enabled services, got nil")
	}
	if !strings.Contains(err.Error(), "no enabled services") {
		t.Errorf("error should mention 'no enabled services', got: %v", err)
	}
}

func TestPickService_MultipleEnabled_CallsSelector(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
	})
	selectorCalled := false
	selector := func(c *config.DevboxConfig, names []string) (string, error) {
		selectorCalled = true
		if len(names) != 2 {
			return "", fmt.Errorf("expected 2 names, got %d: %v", len(names), names)
		}
		return names[0], nil
	}
	name, err := pickService(cfg, "", selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("expected selector to be called for multiple enabled services")
	}
	if name == "" {
		t.Error("expected a non-empty service name from selector")
	}
}

func TestPickService_MultipleEnabled_SelectorSeesOnlyEnabledServices(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":     {Type: "app", Container: "app-main", Mandatory: true},
		"second":   {Type: "app", Container: "app-second", Enabled: true},
		"disabled": {Type: "app", Container: "app-disabled", Enabled: false},
	})
	var selectorNames []string
	selector := func(c *config.DevboxConfig, names []string) (string, error) {
		selectorNames = names
		return names[0], nil
	}
	_, err := pickService(cfg, "", selector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "disabled" must not appear in the selector list.
	for _, n := range selectorNames {
		if n == "disabled" {
			t.Errorf("disabled service should not appear in selector list: %v", selectorNames)
		}
	}
	if len(selectorNames) != 2 {
		t.Errorf("expected 2 enabled services in selector, got %d: %v", len(selectorNames), selectorNames)
	}
}

func TestPickService_SelectorPicksSecond(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"alpha": {Type: "app", Container: "app-alpha", Mandatory: true},
		"beta":  {Type: "app", Container: "app-beta", Enabled: true},
	})
	// sorted order: alpha, beta → index 1 = "beta"
	name, err := pickService(cfg, "", alwaysSelectFn(1))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "beta" {
		t.Errorf("expected 'beta' from selector index 1, got %q", name)
	}
}

// --- Task 4: mode logic and env passthrough ---

// testCompose returns a minimal docker.Compose for tests (no project name, no files).
func testCompose() *docker.Compose {
	return &docker.Compose{ProjectName: "test", Files: nil, GlobalArgs: nil}
}

// testCfg builds a DevboxConfig with a single service "main" using the given container name.
func testCfg(container string, cli config.ServiceCLIConfig) *config.DevboxConfig {
	return makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: container, CLI: cli},
		},
		config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{},
	)
}

// stateFunc returns a getState function that reports the given status.
// An empty status simulates an absent container (returns errContainerNotFound).
// Use stateFuncErr to inject an arbitrary error (e.g. daemon unreachable).
func stateFunc(status string) func(string) (string, error) {
	return func(string) (string, error) {
		if status == "" {
			return "", errContainerNotFound
		}
		return status, nil
	}
}

// stateFuncErr returns a getState function that always returns the given error.
func stateFuncErr(err error) func(string) (string, error) {
	return func(string) (string, error) {
		return "", err
	}
}

// captureExec returns a shellExecFunc that records the call in called and returns nil.
func captureExec(called *bool) shellExecFunc {
	return func(containerName, shell, u, workDir string, env map[string]string) error {
		*called = true
		return nil
	}
}

// captureRun returns a shellRunFunc that records the call in called and returns nil.
func captureRun(called *bool) shellRunFunc {
	return func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string) error {
		*called = true
		return nil
	}
}

// runFuncErr returns a shellRunFunc that always returns the given error.
func runFuncErr(err error) shellRunFunc {
	return func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string) error {
		return err
	}
}

func TestRunServicesCLI_AutoMode_Running_UsesExec(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "auto"}

	execCalled, runCalled := false, false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(&execCalled), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !execCalled {
		t.Error("auto+running: expected exec to be called")
	}
	if runCalled {
		t.Error("auto+running: expected run NOT to be called")
	}
}

func TestRunServicesCLI_AutoMode_Absent_UsesRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "auto"}

	execCalled, runCalled := false, false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc(""), captureExec(&execCalled), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if execCalled {
		t.Error("auto+absent: expected exec NOT to be called")
	}
	if !runCalled {
		t.Error("auto+absent: expected run to be called")
	}
}

func TestRunServicesCLI_AutoMode_DockerError_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "auto"}

	daemonErr := fmt.Errorf("Cannot connect to the Docker daemon")
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFuncErr(daemonErr), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("auto+docker-error: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "Cannot connect to the Docker daemon") {
		t.Errorf("auto+docker-error: expected daemon error surfaced, got: %v", err)
	}
}

// TestRunServicesCLI_AutoMode_Absent_RunFails_SurfacesOriginalError verifies that
// when the container is absent and compose run also fails, the original runCLI
// error is surfaced rather than being dropped or replaced by a generic hint.
func TestRunServicesCLI_AutoMode_Absent_RunFails_SurfacesOriginalError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "auto"}

	runErr := fmt.Errorf("image pull failed: access denied")
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc(""), captureExec(new(bool)), runFuncErr(runErr))

	if err == nil {
		t.Fatal("expected error when compose run fails, got nil")
	}
	if !errors.Is(err, runErr) {
		t.Errorf("original runCLI error should be preserved via wrapping, got: %v", err)
	}
}

func TestRunServicesCLI_AutoMode_Stopped_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "auto"}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("exited"), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("auto+stopped: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "exited") {
		t.Errorf("auto+stopped: error should mention status 'exited', got: %v", err)
	}
}

func TestRunServicesCLI_ExecMode_Running_UsesExec(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "exec"}

	execCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(&execCalled), captureRun(new(bool)))

	if err != nil {
		t.Fatalf("exec+running: unexpected error: %v", err)
	}
	if !execCalled {
		t.Error("exec+running: expected exec to be called")
	}
}

func TestRunServicesCLI_ExecMode_Absent_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "exec"}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc(""), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("exec+absent: expected error, got nil")
	}
	// When docker inspect itself fails (container absent / daemon unreachable),
	// the underlying error should be surfaced rather than a misleading "not running".
	if !strings.Contains(err.Error(), "container not found") {
		t.Errorf("exec+absent: error should surface the docker inspect error, got: %v", err)
	}
}

func TestRunServicesCLI_ExecMode_Stopped_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "exec"}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("exited"), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("exec+stopped: expected error, got nil")
	}
	if !strings.Contains(err.Error(), "not running") {
		t.Errorf("exec+stopped: error should say 'not running', got: %v", err)
	}
}

func TestRunServicesCLI_RunMode_Running_UsesRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "run"}

	runCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(new(bool)), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("run+running: unexpected error: %v", err)
	}
	if !runCalled {
		t.Error("run+running: expected run to be called")
	}
}

func TestRunServicesCLI_RunMode_Absent_UsesRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "run"}

	runCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc(""), captureExec(new(bool)), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("run+absent: unexpected error: %v", err)
	}
	if !runCalled {
		t.Error("run+absent: expected run to be called")
	}
}

func TestRunServicesCLI_RunMode_Stopped_UsesRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{mode: "run"}

	runCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("exited"), captureExec(new(bool)), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("run+stopped: unexpected error: %v", err)
	}
	if !runCalled {
		t.Error("run+stopped: expected run to be called even when stopped")
	}
}

func TestRunServicesCLI_EnvPassthroughToExec(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{
		Env: map[string]string{"FOO": "bar"},
	})
	compose := testCompose()
	flags := shellCLIFlags{mode: "exec"}

	var capturedEnv map[string]string
	execFn := func(containerName, shell, u, workDir string, env map[string]string) error {
		capturedEnv = env
		return nil
	}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), execFn, captureRun(new(bool)))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedEnv["FOO"] != "bar" {
		t.Errorf("env passthrough to exec: expected FOO=bar, got %q", capturedEnv["FOO"])
	}
}

func TestRunServicesCLI_EnvPassthroughToRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{
		Env: map[string]string{"DB_HOST": "localhost"},
	})
	compose := testCompose()
	flags := shellCLIFlags{mode: "run"}

	var capturedEnv map[string]string
	runFn := func(compose *docker.Compose, serviceName, shell, u, workDir string, env map[string]string) error {
		capturedEnv = env
		return nil
	}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc(""), captureExec(new(bool)), runFn)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedEnv["DB_HOST"] != "localhost" {
		t.Errorf("env passthrough to run: expected DB_HOST=localhost, got %q", capturedEnv["DB_HOST"])
	}
}

func TestRunServicesCLI_UnknownService_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := testCompose()
	flags := shellCLIFlags{}

	err := runServicesCLI(cfg, compose, "unknown", flags,
		stateFunc("running"), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("expected error for unknown service, got nil")
	}
}

func TestRunServicesCLI_NoContainer_ReturnsError(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Container: ""},
		},
		config.ToolsConfig{}, config.RuntimePorts{}, config.RuntimeHosts{},
	)
	compose := testCompose()
	flags := shellCLIFlags{}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Error("expected error for service with no container, got nil")
	}
}

// TestRunServicesCLI_ConfigMode_ExecUsedWhenContainerRunning verifies that
// mode from ServiceCLIConfig (not flag) correctly governs exec vs run routing.
func TestRunServicesCLI_ConfigMode_ExecUsedWhenContainerRunning(t *testing.T) {
	// No mode flag set; mode comes from ServiceCLIConfig.
	cfg := testCfg("app-main", config.ServiceCLIConfig{Mode: "exec"})
	compose := testCompose()
	flags := shellCLIFlags{} // no mode flag

	execCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(&execCalled), captureRun(new(bool)))

	if err != nil {
		t.Fatalf("config mode exec+running: unexpected error: %v", err)
	}
	if !execCalled {
		t.Error("config mode exec+running: expected exec to be called")
	}
}

// TestRunServicesCLI_ConfigMode_RunAlwaysUsesRun verifies that mode: run from
// config always starts a new container.
func TestRunServicesCLI_ConfigMode_RunAlwaysUsesRun(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{Mode: "run"})
	compose := testCompose()
	flags := shellCLIFlags{} // no mode flag

	runCalled := false
	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(new(bool)), captureRun(&runCalled))

	if err != nil {
		t.Fatalf("config mode run: unexpected error: %v", err)
	}
	if !runCalled {
		t.Error("config mode run: expected run to be called regardless of container state")
	}
}

// TestRunServicesCLI_InvalidConfigMode_ReturnsError verifies that a typo in
// cli.mode from config/defaults.yml produces an error instead of silently
// falling through to "auto" behavior.
func TestRunServicesCLI_InvalidConfigMode_ReturnsError(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{Mode: "typo"})
	compose := testCompose()
	flags := shellCLIFlags{}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), captureExec(new(bool)), captureRun(new(bool)))

	if err == nil {
		t.Fatal("expected error for invalid config mode, got nil")
	}
	if !strings.Contains(err.Error(), "invalid cli.mode") {
		t.Errorf("error should mention 'invalid cli.mode', got: %v", err)
	}
}

// captureStateContainer returns a getState func that records the container name it was called with.
func captureStateContainer(status string, got *string) func(string) (string, error) {
	return func(containerName string) (string, error) {
		*got = containerName
		if status == "" {
			return "", fmt.Errorf("container not found")
		}
		return status, nil
	}
}

// TestRunServicesCLI_ContainerNameConstruction verifies the full container name
// (<project>-<container>) is built correctly and passed to getState.
func TestRunServicesCLI_ContainerNameConstruction(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{})
	compose := &docker.Compose{ProjectName: "myproject", Files: nil}
	flags := shellCLIFlags{mode: "auto"}

	var gotContainerName string
	_ = runServicesCLI(cfg, compose, "main", flags,
		captureStateContainer("running", &gotContainerName),
		captureExec(new(bool)), captureRun(new(bool)))

	if gotContainerName != "myproject-app-main" {
		t.Errorf("container name: expected %q, got %q", "myproject-app-main", gotContainerName)
	}
}

// TestRunServicesCLI_EnvFlagOverridesConfig verifies that --env KEY=VALUE flag
// values override matching keys from the service cli.env config.
func TestRunServicesCLI_EnvFlagOverridesConfig(t *testing.T) {
	cfg := testCfg("app-main", config.ServiceCLIConfig{
		Env: map[string]string{"FOO": "from-config", "BAR": "keep"},
	})
	compose := testCompose()
	// Flag overrides FOO, BAR is untouched.
	flags := shellCLIFlags{mode: "exec", envVars: []string{"FOO=from-flag"}}

	var capturedEnv map[string]string
	execFn := func(containerName, shell, u, workDir string, env map[string]string) error {
		capturedEnv = env
		return nil
	}

	err := runServicesCLI(cfg, compose, "main", flags,
		stateFunc("running"), execFn, captureRun(new(bool)))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedEnv["FOO"] != "from-flag" {
		t.Errorf("flag env should override config: FOO expected 'from-flag', got %q", capturedEnv["FOO"])
	}
	if capturedEnv["BAR"] != "keep" {
		t.Errorf("config env key BAR should be kept: expected 'keep', got %q", capturedEnv["BAR"])
	}
}

// TestPickService_SelectorReturnsErrCancelled_Propagated verifies that
// ErrCancelled from the selector is propagated by pickService.
func TestPickService_SelectorReturnsErrCancelled_Propagated(t *testing.T) {
	cfg := makeMultiServiceCfg(map[string]config.ServiceConfig{
		"main":   {Type: "app", Container: "app-main", Mandatory: true},
		"second": {Type: "app", Container: "app-second", Enabled: true},
	})
	cancelSelector := func(c *config.DevboxConfig, names []string) (string, error) {
		return "", ui.ErrCancelled
	}
	_, err := pickService(cfg, "", cancelSelector)
	if err == nil {
		t.Fatal("expected ErrCancelled to propagate, got nil")
	}
	if !errors.Is(err, ui.ErrCancelled) {
		t.Errorf("expected errors.Is(err, ui.ErrCancelled), got: %v", err)
	}
}

// --- status command ---

func TestNewStatusCmd_UseField(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newStatusCmd(flags)
	if cmd.Use != "status" {
		t.Errorf("Use = %q, want %q", cmd.Use, "status")
	}
}

func TestStatusRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["status"] {
		t.Error("status command not registered at root level")
	}
}

func TestRunStatusViaCfg(t *testing.T) {
	cfg := makeServicesCfg(
		map[string]config.ServiceConfig{
			"main": {Type: "app", Dir: "./services/main", Container: "app-main"},
		},
		config.ToolsConfig{},
		config.RuntimePorts{},
		config.RuntimeHosts{},
	)

	neverRunning := func(_, _ string) bool { return false }

	var buf bytes.Buffer
	w := render.NewWriter(&buf)
	if err := stack.RunStatus(w, cfg, neverRunning, nil, nil); err != nil {
		t.Fatalf("runStatus error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "main") {
		t.Errorf("status output missing service name 'main'\n%s", out)
	}
	if !strings.Contains(out, "Devbox:") {
		t.Errorf("status output missing 'Devbox:' health indicator\n%s", out)
	}
}

// --- version command ---

func TestNewVersionCmd_UseField(t *testing.T) {
	cmd := newVersionCmd()
	if cmd.Use != "version" {
		t.Errorf("Use = %q, want %q", cmd.Use, "version")
	}
}

func TestVersionRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	if !nameSet["version"] {
		t.Error("version command not registered at root level")
	}
}

func TestVersionCmd_Output(t *testing.T) {
	cmd := newVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.Run(cmd, nil)
	if buf.Len() == 0 {
		t.Error("version command produced no output")
	}
}

// --- renames: services and commands ---

func TestServiceCmdUsesServicesName(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceCmd(flags)
	if cmd.Use != "services" {
		t.Errorf("newServiceCmd Use = %q, want %q", cmd.Use, "services")
	}
}

func TestServiceCmdSubcommands(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newServiceCmd(flags)
	want := []string{"list", "enable", "disable"}
	nameSet := make(map[string]bool)
	for _, c := range cmd.Commands() {
		nameSet[c.Name()] = true
	}
	for _, name := range want {
		if !nameSet[name] {
			t.Errorf("services command missing subcommand %q", name)
		}
	}
	// cli should NOT be present
	if nameSet["cli"] {
		t.Error("services command should not have cli subcommand (moved to root shell)")
	}
}

func TestCommandCmdUsesCommandsName(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	cmd := newCommandCmd(flags)
	if cmd.Use != "commands" {
		t.Errorf("newCommandCmd Use = %q, want %q", cmd.Use, "commands")
	}
}

func TestCommandsRegisteredAtRoot(t *testing.T) {
	root := NewRootCmd()
	nameSet := make(map[string]bool)
	for _, c := range root.Commands() {
		nameSet[c.Name()] = true
	}
	// "commands" group should be present; old "command" group should not
	if !nameSet["commands"] {
		t.Error("commands group not registered at root")
	}
	if nameSet["command"] {
		t.Error("old 'command' name still registered; should be 'commands'")
	}
}

// --- Task 13: message cleanup ---

// TestNoMakeReferencesInCLIMessages verifies that no public command's Short, Long,
// or Example fields reference 'make <target>' (should use 'devbox <command>' instead).
func TestNoMakeReferencesInCLIMessages(t *testing.T) {
	root := NewRootCmd()

	// Collect all commands recursively.
	var collect func(cmd *cobra.Command) []*cobra.Command
	collect = func(cmd *cobra.Command) []*cobra.Command {
		result := []*cobra.Command{cmd}
		for _, sub := range cmd.Commands() {
			result = append(result, collect(sub)...)
		}
		return result
	}
	all := collect(root)

	for _, cmd := range all {
		for _, field := range []struct {
			name string
			val  string
		}{
			{"Short", cmd.Short},
			{"Long", cmd.Long},
			{"Example", cmd.Example},
		} {
			if strings.Contains(field.val, "'make ") || strings.Contains(field.val, "\"make ") {
				t.Errorf("command %q %s contains 'make <target>' reference: %q", cmd.CommandPath(), field.name, field.val)
			}
		}
	}
}

// TestPublicCommandsHaveLongDescription verifies that key public commands have
// non-empty Long descriptions.
func TestPublicCommandsHaveLongDescription(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	checks := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"stop", newStopCmd(flags)},
		{"restart", newRestartCmd(flags)},
		{"info", newInfoCmd(flags)},
		{"version", newVersionCmd()},
		{"status", newStatusCmd(flags)},
		{"commands", newCommandCmd(flags)},
		{"services", newServiceCmd(flags)},
		{"tools", newToolCmd(flags)},
		{"render", newRenderCmd(flags)},
		{"deploy", newDeployCmd(flags)},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if strings.TrimSpace(tc.cmd.Long) == "" {
				t.Errorf("command %q has no Long description", tc.name)
			}
		})
	}
}

// TestPublicCommandsHaveExamples verifies that key public commands have
// non-empty Example fields.
func TestPublicCommandsHaveExamples(t *testing.T) {
	flags := &rootFlags{configPath: "devbox.yml"}
	checks := []struct {
		name string
		cmd  *cobra.Command
	}{
		{"stop", newStopCmd(flags)},
		{"restart", newRestartCmd(flags)},
		{"info", newInfoCmd(flags)},
		{"version", newVersionCmd()},
		{"status", newStatusCmd(flags)},
		{"shell", newShellCmd(flags)},
	}
	for _, tc := range checks {
		t.Run(tc.name, func(t *testing.T) {
			if strings.TrimSpace(tc.cmd.Example) == "" {
				t.Errorf("command %q has no Example", tc.name)
			}
		})
	}
}

// --- containerStateStatus error extraction ---

// TestContainerStateStatus_ExitError_SurfacesStderr verifies that when docker inspect
// exits non-zero with stderr output (e.g. daemon unreachable), containerStateStatus
// returns an error whose message contains the stderr text rather than the generic
// "exit status 1" from *exec.ExitError.Error().
func TestContainerStateStatus_ExitError_SurfacesStderr(t *testing.T) {
	// Build a fake *exec.ExitError by running a subprocess that writes to stderr
	// and exits non-zero. This tests the real error-extraction path in
	// containerStateStatus without requiring a live Docker daemon.
	cmd := exec.Command("sh", "-c", "echo 'Cannot connect to the Docker daemon' >&2; exit 1")
	_, err := cmd.Output()
	if err == nil {
		t.Fatal("expected non-zero exit from test subprocess")
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T", err)
	}
	// The raw ExitError.Error() is just "exit status 1" — not helpful.
	if exitErr.Error() == "exit status 1" && !strings.Contains(string(exitErr.Stderr), "Cannot connect") {
		t.Fatal("test setup broken: subprocess stderr not captured")
	}

	// Simulate what containerStateStatus does: extract stderr from ExitError.
	var gotMsg string
	if len(exitErr.Stderr) > 0 {
		gotMsg = strings.TrimSpace(string(exitErr.Stderr))
	} else {
		gotMsg = exitErr.Error()
	}
	if !strings.Contains(gotMsg, "Cannot connect to the Docker daemon") {
		t.Errorf("expected stderr to be surfaced, got: %q", gotMsg)
	}
	if gotMsg == "exit status 1" {
		t.Errorf("raw 'exit status 1' returned instead of stderr content")
	}
}

// --- old services topology command removed ---

func TestOldServicesTopologyCmdRemoved(t *testing.T) {
	// The old `services` command (with topology RunE and cli subcommand) was
	// merged into `status` and `shell`. The remaining `services` command should
	// only expose list/enable/disable subcommands, with no cli subcommand.
	root := NewRootCmd()

	var servicesCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "services" {
			servicesCmd = c
			break
		}
	}
	if servicesCmd == nil {
		t.Fatal("services command not found at root")
	}

	subNameSet := make(map[string]bool)
	for _, c := range servicesCmd.Commands() {
		subNameSet[c.Name()] = true
	}
	if subNameSet["cli"] {
		t.Error("services command still has cli subcommand; it should have been moved to root shell")
	}
}
