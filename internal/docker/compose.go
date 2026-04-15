// Package docker provides a unified interface for executing docker compose commands.
// It centralizes project name, file list, and argument construction so that all
// callers (CLI commands, service runners, deploy steps) use the same pipeline.
package docker

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"devbox-cli/internal/config"
)

// Compose encapsulates the state needed to build and execute docker compose commands.
type Compose struct {
	ProjectName string
	Files       []string
	GlobalArgs  []string
	CommandArgs map[string][]string // per-command default args
}

// NewCompose creates a Compose from the resolved devbox config and docker policy.
func NewCompose(cfg *config.DevboxConfig, dockerCfg *config.DockerConfig) *Compose {
	cmdArgs := map[string][]string{
		"up":      dockerCfg.Args.Up,
		"down":    dockerCfg.Args.Down,
		"stop":    dockerCfg.Args.Stop,
		"restart": dockerCfg.Args.Restart,
		"logs":    dockerCfg.Args.Logs,
		"ps":      dockerCfg.Args.Ps,
		"exec":    dockerCfg.Args.Exec,
		"run":     dockerCfg.Args.Run,
	}

	return &Compose{
		ProjectName: dockerCfg.ProjectName,
		Files:       cfg.ComposeFiles(),
		GlobalArgs:  dockerCfg.Args.Global,
		CommandArgs: cmdArgs,
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
func (c *Compose) Exec(command string, extraArgs ...string) error {
	args := c.BuildArgs(command, extraArgs...)
	cmd := exec.Command("docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

// ContainerIDs returns the IDs of running containers for this compose project.
// It uses BuildInternalArgs to bypass per-command policy defaults.
func (c *Compose) ContainerIDs() ([]string, error) {
	args := c.BuildInternalArgs("ps", "-q")

	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps -q: %w", err)
	}

	var ids []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ids = append(ids, line)
		}
	}
	return ids, nil
}
