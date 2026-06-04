package service

import (
	"context"
	"os/exec"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/internal/runio"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime/spec"
	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// RunRunner executes type=service_run commands via `docker compose run --rm`.
type RunRunner struct{}

// BuildCommand constructs the exec.Cmd for the given context.
// The supplied ctx is attached to the returned *exec.Cmd.
func (r *RunRunner) BuildCommand(ctx context.Context, rc spec.RunContext, compose *docker.Compose) (*exec.Cmd, error) {
	svc, user, workdir, _, err := resolveServiceFields(rc)
	if err != nil {
		return nil, err
	}

	argv, err := buildServiceArgv(rc)
	if err != nil {
		return nil, err
	}

	envVars, err := runio.BuildRenderedEnv(rc.Cmd, rc)
	if err != nil {
		return nil, err
	}

	composeArgs, err := buildRenderedComposeArgs(rc)
	if err != nil {
		return nil, err
	}

	return buildDockerComposeCmd(ctx, rc, compose, svc, user, workdir, argv, envVars, composeArgs, false), nil
}

// Run executes the command in a one-off container.
func (r *RunRunner) Run(ctx context.Context, rc spec.RunContext) error {
	compose := rc.Compose()
	c, err := r.BuildCommand(ctx, rc, compose)
	if err != nil {
		return err
	}
	defer runio.WireChildIO(rc, c)()
	return c.Run()
}
