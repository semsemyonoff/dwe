package env

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// Legacy `docker-compose` (v1) was a standalone Python binary invoked as
// `docker-compose ...`. The plugin (v2 and later) is invoked as the
// `docker compose ...` subcommand. So a non-error response from
// `docker compose version` is itself proof the plugin is installed — the
// version-major number does not matter and changes with Docker Desktop
// releases (v2 → v5 etc.).

// dockerProbeTimeout caps the `docker version` / `docker compose version`
// invocations so a hung daemon does not block validate forever.
const dockerProbeTimeout = 5 * time.Second

type dockerBinValidator struct {
	cfg *config.DevboxConfig
}

func (v *dockerBinValidator) ID() string     { return "docker_bin" }
func (v *dockerBinValidator) Domain() string { return "env" }

func (v *dockerBinValidator) Run(_ validate.Context) []validate.Diagnostic {
	bin := config.DockerBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("docker binary not found in PATH: %s", bin),
			"install Docker Desktop or set binaries.docker in devbox.yml\nhttps://docs.docker.com/get-docker/",
		)}
	}
	return []validate.Diagnostic{ok(v.ID())}
}

type dockerDaemonValidator struct {
	cfg *config.DevboxConfig
}

func (v *dockerDaemonValidator) ID() string     { return "docker_daemon" }
func (v *dockerDaemonValidator) Domain() string { return "env" }

func (v *dockerDaemonValidator) Run(vctx validate.Context) []validate.Diagnostic {
	bin := config.DockerBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		// docker_bin will surface this; don't double-report.
		return nil
	}
	parent := vctx.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, dockerProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("docker daemon unreachable: %s", firstLine(string(out), err.Error())),
			"start Docker Desktop (or the docker daemon) and retry",
		)}
	}
	return []validate.Diagnostic{ok(v.ID())}
}

type dockerComposeValidator struct {
	cfg *config.DevboxConfig
}

func (v *dockerComposeValidator) ID() string     { return "docker_compose" }
func (v *dockerComposeValidator) Domain() string { return "env" }

func (v *dockerComposeValidator) Run(vctx validate.Context) []validate.Diagnostic {
	bin := config.DockerBin(v.cfg)
	if _, err := exec.LookPath(bin); err != nil {
		return nil
	}
	parent := vctx.Ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, dockerProbeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "compose", "version", "--short").CombinedOutput()
	if err != nil {
		return []validate.Diagnostic{fail(
			v.ID(),
			fmt.Sprintf("docker compose plugin not available: %s", firstLine(string(out), err.Error())),
			"install the Docker Compose plugin\nhttps://docs.docker.com/compose/install/",
		)}
	}
	if strings.TrimSpace(string(out)) == "" {
		return []validate.Diagnostic{fail(
			v.ID(),
			"docker compose plugin returned no version",
			"reinstall the Docker Compose plugin\nhttps://docs.docker.com/compose/install/",
		)}
	}
	return []validate.Diagnostic{ok(v.ID())}
}

// firstLine returns the first non-empty line of s, falling back to fallback.
func firstLine(s, fallback string) string {
	for line := range strings.SplitSeq(strings.TrimSpace(s), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return fallback
}
