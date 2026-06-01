// Package docker provides a unified interface for executing docker compose commands.
// It centralizes project name, file list, and argument construction so that all
// callers (CLI commands, service runners, deploy steps) use the same pipeline.
package docker

import (
	"context"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// Compose encapsulates the state needed to build and execute docker compose commands.
type Compose struct {
	// Bin is the Docker-compatible binary name (e.g. "docker", "podman").
	// Always read via BinName() — never access the field directly.
	Bin         string
	ProjectName string
	Files       []string
	GlobalArgs  []string
	CommandArgs map[string][]string // per-command default args
	// ProcessEnv holds additional environment variables injected into every
	// docker compose process (e.g. DOCKER_CLI_HINTS=false).
	// Keys are sorted for deterministic ordering when building os.Environ slices.
	ProcessEnv map[string]string
}

// BinName returns the Docker-compatible binary name. Safe on nil receivers and
// empty Bin fields — both return "docker" so exec.Command never gets an empty name.
func (c *Compose) BinName() string {
	if c == nil || c.Bin == "" {
		return "docker"
	}
	return c.Bin
}

// NewCompose creates a Compose from the resolved dwe config and docker policy.
func NewCompose(cfg *config.DweConfig, dockerCfg *config.DockerConfig) *Compose {
	return buildCompose(cfg, dockerCfg, cfg.ComposeFiles())
}

// NewComposeAll creates a Compose like NewCompose but sources files from ComposeFilesAll(),
// which includes all overlays regardless of enabled state.
func NewComposeAll(cfg *config.DweConfig, dockerCfg *config.DockerConfig) *Compose {
	return buildCompose(cfg, dockerCfg, cfg.ComposeFilesAll())
}

// buildCompose is a private helper that constructs a Compose with the provided file list.
func buildCompose(cfg *config.DweConfig, dockerCfg *config.DockerConfig, files []string) *Compose {
	cmdArgs := map[string][]string{
		"up":      dockerCfg.Args.Up,
		"down":    dockerCfg.Args.Down,
		"stop":    dockerCfg.Args.Stop,
		"restart": dockerCfg.Args.Restart,
		"logs":    dockerCfg.Args.Logs,
		"ps":      dockerCfg.Args.Ps,
		"exec":    dockerCfg.Args.Exec,
		"run":     dockerCfg.Args.Run,
		"pull":    dockerCfg.Args.Pull,
		"build":   dockerCfg.Args.Build,
	}

	return &Compose{
		Bin:         config.DockerBin(cfg),
		ProjectName: dockerCfg.ProjectName,
		Files:       files,
		GlobalArgs:  dockerCfg.Args.Global,
		CommandArgs: cmdArgs,
		ProcessEnv:  dockerCfg.ProcessEnv,
	}
}

// BuildArgs returns the full argument list for `docker compose <command> [extraArgs...]`
// without executing. The returned slice does NOT include the leading "docker" binary name.
//
// Argument order:
//
//	compose -p <project> -f <file>... <globalArgs> <command> <commandDefaultArgs> <extraArgs>
func (c *Compose) BuildArgs(command string, extraArgs ...string) []string {
	args := []string{"compose"}

	// Project name.
	if c.ProjectName != "" {
		args = append(args, "-p", c.ProjectName)
	}

	// File flags.
	for _, f := range c.Files {
		args = append(args, "-f", f)
	}

	// Global args (e.g. --ansi always --progress tty).
	args = append(args, c.GlobalArgs...)

	// Subcommand.
	args = append(args, command)

	// Per-command default args.
	if defaults, ok := c.CommandArgs[command]; ok {
		args = append(args, defaults...)
	}

	// Caller-supplied extra args.
	args = append(args, extraArgs...)

	return args
}

// Exec runs `docker compose <command>` with the full argument pipeline.
// Stdin, stdout, and stderr are connected to the current process.
// ProcessEnv variables are merged on top of the current process environment.
func (c *Compose) Exec(command string, extraArgs ...string) error {
	args := c.BuildArgs(command, extraArgs...)
	bin := c.BinName()
	cmd := exec.Command(bin, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = c.BuildEnv()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", formatCommand(append([]string{bin}, args...)), err)
	}
	return nil
}

func formatCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = quoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.ContainsAny(arg, " \t\n\"'\\$`|&;()<>*?[#~=%") {
		return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return arg
}

// MergeEnv returns the current process environment with overrides applied.
// Equivalent to BuildEnv on a Compose that has only ProcessEnv set.
// Returns nil when overrides is empty.
func MergeEnv(overrides map[string]string) []string {
	return (&Compose{ProcessEnv: overrides}).BuildEnv()
}

// BuildEnv returns the environment for docker processes: current process env
// with ProcessEnv overlaid. Returns nil (inherit unchanged) when ProcessEnv is empty.
func (c *Compose) BuildEnv() []string {
	if len(c.ProcessEnv) == 0 {
		return nil
	}
	// Start from the current process environment.
	base := os.Environ()
	// Build an override set for O(1) lookup.
	override := maps.Clone(c.ProcessEnv)
	// Replace any existing entries that are overridden.
	result := make([]string, 0, len(base)+len(override))
	replaced := make(map[string]bool, len(override))
	for _, entry := range base {
		k, _, _ := strings.Cut(entry, "=")
		if v, ok := override[k]; ok {
			result = append(result, k+"="+v)
			replaced[k] = true
		} else {
			result = append(result, entry)
		}
	}
	// Append keys that were not already present in the base env.
	for k, v := range c.ProcessEnv {
		if !replaced[k] {
			result = append(result, k+"="+v)
		}
	}
	return result
}

// BuildInternalArgs returns the argument list for internal probes (e.g. health
// checks, container-running detection). Unlike BuildArgs it injects neither
// global args nor per-command policy defaults, so that user-facing overrides
// (e.g. args.global: ["--dry-run"], args.ps: ["--services"]) cannot break
// the expected output format of machine-readable queries.
func (c *Compose) BuildInternalArgs(command string, extraArgs ...string) []string {
	args := []string{"compose"}

	if c.ProjectName != "" {
		args = append(args, "-p", c.ProjectName)
	}
	for _, f := range c.Files {
		args = append(args, "-f", f)
	}
	args = append(args, command)
	args = append(args, extraArgs...)

	return args
}

// output runs an internal probe command and returns its stdout.
// ProcessEnv is applied so that daemon/context overrides (e.g. DOCKER_HOST)
// are consistent with Exec-based lifecycle commands.
func (c *Compose) output(args []string) ([]byte, error) {
	cmd := exec.Command(c.BinName(), args...)
	cmd.Env = c.BuildEnv()
	return cmd.Output()
}

// ContainerIDs returns the IDs of running containers for this compose project.
// It uses BuildInternalArgs to bypass per-command policy defaults.
func (c *Compose) ContainerIDs() ([]string, error) {
	args := c.BuildInternalArgs("ps", "-q")

	out, err := c.output(args)
	if err != nil {
		return nil, fmt.Errorf("%s compose ps -q: %w", c.BinName(), err)
	}

	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}

// HealthStatus returns the health status of a container by ID, using this
// Compose's docker binary. Wraps the package-level HealthStatus function.
func (c *Compose) HealthStatus(id string) (string, error) {
	return HealthStatus(c.BinName(), id)
}

// RunningServices returns the subset of the given service names whose container
// is currently in the running state. Unlike ContainerIDsFor (which returns
// container IDs) this queries by service name and uses
// `compose ps --status running --services [services...]` so the output is one
// service name per line — fast, no polling, correct for services without a
// healthcheck. Empty services slice returns nil.
func (c *Compose) RunningServices(ctx context.Context, services []string) ([]string, error) {
	args := c.BuildInternalArgs("ps", "--status", "running", "--services")
	args = append(args, services...)

	cmd := exec.CommandContext(ctx, c.BinName(), args...) //nolint:gosec
	cmd.Env = c.BuildEnv()
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s compose ps --services: %w", c.BinName(), err)
	}

	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// ContainerIDsFor returns the IDs of running containers for the given services.
// services is a list of compose service names; empty list returns no errors and an empty ID slice.
func (c *Compose) ContainerIDsFor(services []string) ([]string, error) {
	if len(services) == 0 {
		return nil, nil
	}

	args := c.BuildInternalArgs("ps", "-q")
	args = append(args, services...)

	out, err := c.output(args)
	if err != nil {
		return nil, fmt.Errorf("%s compose ps -q: %w", c.BinName(), err)
	}

	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}
