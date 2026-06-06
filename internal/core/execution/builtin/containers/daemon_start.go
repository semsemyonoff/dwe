package containers

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"regexp"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/daemon"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// startArgsInput holds all pre-rendered fields needed to build the
// docker compose run -d argv for a daemon. Extracted into a pure function so
// tests can assert argv shape without invoking docker.
type startArgsInput struct {
	FullName    string
	Service     string
	User        string
	Workdir     string
	AutoRemove  bool
	Argv        []string
	ComposeArgs []string
	EnvKeys     []string // sorted; values supplied via cmd.Env
	ProjectFull string
	DaemonID    string
	LabelParams map[string]any
}

// buildStartExtraArgs returns the argv elements appended after `compose run`'s
// own header (project / file flags / global args / command defaults). Order
// matches runner_service.go's compose-run path so daemons follow the same
// command-fields-win-on-conflict semantics as service_run.
func buildStartExtraArgs(in startArgsInput) []string {
	extraArgs := []string{"-d", "--no-deps", "--entrypoint", ""}
	if in.AutoRemove {
		extraArgs = append(extraArgs, "--rm")
	}
	extraArgs = append(extraArgs, "--name", in.FullName)
	extraArgs = append(extraArgs, in.ComposeArgs...)

	switch in.User {
	case "current":
		h := tpl.CurrentHostInfo()
		extraArgs = append(extraArgs, "--user", h.UID+":"+h.GID)
	case "root":
		extraArgs = append(extraArgs, "--user", "root")
	case "", "internal":
		// no flag
	default:
		extraArgs = append(extraArgs, "--user", in.User)
	}

	if in.Workdir != "" {
		extraArgs = append(extraArgs, "--workdir", in.Workdir)
	}

	for _, k := range in.EnvKeys {
		extraArgs = append(extraArgs, "-e", k)
	}

	extraArgs = append(extraArgs, daemon.StandardLabels(in.ProjectFull, in.DaemonID, in.LabelParams)...)
	extraArgs = append(extraArgs, in.Service)
	extraArgs = append(extraArgs, in.Argv...)
	return extraArgs
}

// DaemonStart implements docker_daemon_start.
//
// It reads pre-rendered fields from with: (templating already applied by
// runtime's renderBuiltinWith) and invokes `docker compose run -d ...` through
// docker.Compose so docker.yml policy (project_name, file flags, command
// defaults, configured binary, process_env) applies.
type DaemonStart struct{}

// Validate checks that the with parameters are valid before the pipeline runs.
func (DaemonStart) Validate(with map[string]any) error {
	if spec.GetStringParam(with, "service", "") == "" {
		return fmt.Errorf("docker_daemon_start: service required")
	}
	if spec.GetStringParam(with, "container_template", "") == "" {
		return fmt.Errorf("docker_daemon_start: container_template required")
	}
	return nil
}

// Describe returns a short human-readable description used in plan output.
func (DaemonStart) Describe(with map[string]any) string {
	return "start daemon: " + spec.GetStringParam(with, "container_template", "?")
}

// Run executes the docker_daemon_start builtin.
func (DaemonStart) Run(ctx context.Context, with map[string]any, ectx spec.ExecContext) error {
	if ectx.Config == nil {
		return fmt.Errorf("docker_daemon_start: config not available")
	}
	dockerCfg := ectx.DockerConfig
	if dockerCfg == nil {
		dockerCfg = &config.DockerConfig{}
	}

	projectFull := config.ComposeProjectName(dockerCfg, ectx.Config)
	containerTemplate := spec.GetStringParam(with, "container_template", "")
	fullName, err := daemon.ResolveContainerName(projectFull, containerTemplate)
	if err != nil {
		return err
	}

	service := spec.GetStringParam(with, "service", "")
	user := spec.GetStringParam(with, "user", "")
	workdir := spec.GetStringParam(with, "workdir", "")
	workdirFrom := spec.GetStringParam(with, "workdir_from", "")
	autoRemove := spec.GetBoolParam(with, "auto_remove", true)
	onAlreadyRunning := spec.GetStringParam(with, "on_already_running", "error")
	daemonID := spec.GetStringParam(with, "daemon_id", "")

	if workdir == "" && workdirFrom != "" {
		v, err := config.LookupDotPath(ectx.Config, workdirFrom)
		if err != nil {
			return fmt.Errorf("docker_daemon_start: workdir_from %q: %w", workdirFrom, err)
		}
		if v == nil {
			return fmt.Errorf("docker_daemon_start: workdir_from %q: path not found in config", workdirFrom)
		}
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("docker_daemon_start: workdir_from %q: resolved value is not a string", workdirFrom)
		}
		workdir = s
	}

	argv, err := spec.GetStringSlice(with, "argv")
	if err != nil {
		return fmt.Errorf("docker_daemon_start: %w", err)
	}
	composeArgs, err := spec.GetStringSlice(with, "compose_args")
	if err != nil {
		return fmt.Errorf("docker_daemon_start: %w", err)
	}

	envVars, err := spec.GetStringMap(with, "env")
	if err != nil {
		return fmt.Errorf("docker_daemon_start: %w", err)
	}

	labelParams, err := spec.GetMapAny(with, "label_params")
	if err != nil {
		return fmt.Errorf("docker_daemon_start: %w", err)
	}

	compose := docker.NewCompose(ectx.Config, dockerCfg, ectx.ProjectRoot)

	// Best-effort pre-check via docker ps for the resolved container name.
	if running, _ := isDaemonRunning(ctx, compose, fullName); running {
		if onAlreadyRunning == "noop" {
			_, _ = fmt.Fprintf(ectx.Output.Writer(), "daemon already running: %s\n", fullName)
			return nil
		}
		return fmt.Errorf("%w: %s", daemon.ErrDaemonAlreadyRunning, fullName)
	}

	extraArgs := buildStartExtraArgs(startArgsInput{
		FullName:    fullName,
		Service:     service,
		User:        user,
		Workdir:     workdir,
		AutoRemove:  autoRemove,
		Argv:        argv,
		ComposeArgs: composeArgs,
		EnvKeys:     spec.SortedKeys(envVars),
		ProjectFull: projectFull,
		DaemonID:    daemonID,
		LabelParams: labelParams,
	})

	args := compose.BuildArgs("run", extraArgs...)
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Dir = compose.BaseDir
	combined := make(map[string]string, len(compose.ProcessEnv)+len(envVars))
	maps.Copy(combined, compose.ProcessEnv)
	maps.Copy(combined, envVars)
	cmd.Env = docker.MergeEnv(combined)

	var stderr strings.Builder
	cmd.Stdout = ectx.Output.Writer()
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errOut := stderr.String()
		// TOCTOU translation: docker emits "is already in use by container"
		// when the name collides under race. Translate to typed sentinel.
		if strings.Contains(errOut, "is already in use") {
			if onAlreadyRunning == "noop" {
				_, _ = fmt.Fprintf(ectx.Output.Writer(), "daemon already running: %s\n", fullName)
				return nil
			}
			return fmt.Errorf("%w: %s", daemon.ErrDaemonAlreadyRunning, fullName)
		}
		if errOut != "" {
			return fmt.Errorf("docker compose run: %w: %s", err, strings.TrimSpace(errOut))
		}
		return fmt.Errorf("docker compose run: %w", err)
	}
	_, _ = fmt.Fprintf(ectx.Output.Writer(), "✓ daemon started: %s\n", fullName)
	return nil
}

// isDaemonRunning probes for a running container by exact name. Best-effort:
// any error returns (false, err) and callers treat it as "not running" so the
// authoritative race winner is docker's own name-uniqueness enforcement.
func isDaemonRunning(ctx context.Context, compose *docker.Compose, fullName string) (bool, error) {
	args := []string{"ps", "-q", "--filter", "name=^" + regexp.QuoteMeta(fullName) + "$", "--filter", "status=running"}
	cmd := exec.CommandContext(ctx, compose.BinName(), args...) //nolint:gosec
	cmd.Env = compose.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}
