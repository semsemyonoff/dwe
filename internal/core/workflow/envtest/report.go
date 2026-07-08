package envtest

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/semsemyonoff/dwe/internal/shared/docker"
)

// reportLogTailLines bounds container-logs.txt: `--tail 200` on the compose
// path, `--tail 200` per container on the identity-label fallback.
const reportLogTailLines = 200

// ReportDeps holds the Docker-touching actions CollectReport drives, one seam
// per artifact, mirroring TeardownDeps — so tests can stub them without
// touching the real Docker daemon.
type ReportDeps struct {
	// PS captures `docker compose ps --all` output for the manifest's compose
	// project (or its identity-label fallback).
	PS func(ctx context.Context, m *Manifest) (string, error)
	// Logs captures combined container log tails for the manifest's compose
	// project (or its identity-label fallback).
	Logs func(ctx context.Context, m *Manifest) (string, error)
}

// NewReportDeps builds the real ReportDeps.
func NewReportDeps() ReportDeps {
	return ReportDeps{
		PS:   reportPSReal,
		Logs: reportLogsReal,
	}
}

// CollectReport writes a failure-artifact report into m.ReportDir: the
// scenario's pipeline log (copied from the copy's own .dwe/logs/test.log),
// `docker compose ps --all` output, and container log tails. The directory is
// cleared first — per-scenario overwrite, so the latest failure is what gets
// debugged. Every artifact is best-effort: a failure is reported via warn and
// never stops collection of the remaining artifacts. Returns m.ReportDir.
func CollectReport(ctx context.Context, m *Manifest, deps ReportDeps, warn func(string)) (string, error) {
	if m == nil {
		return "", fmt.Errorf("envtest: cannot collect a report for a nil manifest")
	}
	if warn == nil {
		warn = func(string) {}
	}

	if err := os.RemoveAll(m.ReportDir); err != nil {
		return "", fmt.Errorf("envtest: clearing report directory: %w", err)
	}
	if err := os.MkdirAll(m.ReportDir, 0o755); err != nil {
		return "", fmt.Errorf("envtest: creating report directory: %w", err)
	}

	src := filepath.Join(m.CopyPath, ".dwe", "logs", "test.log")
	if err := copyFile(src, filepath.Join(m.ReportDir, "pipeline.log")); err != nil {
		warn(fmt.Sprintf("report: copying pipeline log: %v", err))
	}

	if deps.PS != nil {
		out, err := deps.PS(ctx, m)
		if err != nil {
			warn(fmt.Sprintf("report: capturing compose ps: %v", err))
		}
		if err := os.WriteFile(filepath.Join(m.ReportDir, "compose-ps.txt"), []byte(out), 0o644); err != nil {
			warn(fmt.Sprintf("report: writing compose-ps.txt: %v", err))
		}
	}

	if deps.Logs != nil {
		out, err := deps.Logs(ctx, m)
		if err != nil {
			warn(fmt.Sprintf("report: capturing container logs: %v", err))
		}
		if err := os.WriteFile(filepath.Join(m.ReportDir, "container-logs.txt"), []byte(out), 0o644); err != nil {
			warn(fmt.Sprintf("report: writing container-logs.txt: %v", err))
		}
	}

	return m.ReportDir, nil
}

// copyFile copies src to dst, preserving src's file mode. A missing (or
// otherwise unreadable) src returns the underlying error — the caller treats
// it as skip+warn, not a hard failure.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// reportPSReal captures `docker compose ps --all` for the copy at m.CopyPath
// via BuildInternalArgs (never BuildArgs — a user args.ps policy override
// must not change a machine-readable capture), degrading to the
// identity-label `docker ps -a` fallback when the copy's own config can no
// longer be loaded (the same trigger Teardown's composeDownReal uses). --all
// is required: `compose ps` defaults to running-only, which would drop
// exactly the crashed service a failure report exists to surface.
func reportPSReal(ctx context.Context, m *Manifest) (string, error) {
	cfg, dockerCfg, err := loadCopyConfig(m.CopyPath)
	if err != nil {
		return reportPSIdentityFallback(ctx, m)
	}
	compose := docker.NewCompose(cfg, dockerCfg, m.CopyPath)
	args := compose.BuildInternalArgs("ps", "--all")
	out, err := captureCmdFn(ctx, compose.BinName(), args, compose.BuildEnv(), m.CopyPath)
	return string(out), err
}

// reportPSIdentityFallback runs `docker ps -a --filter
// label=<ComposeProjectLabel>=<proj>` — the manifest's exact compose project
// identity, never a name-pattern guess.
func reportPSIdentityFallback(ctx context.Context, m *Manifest) (string, error) {
	dockerBin := dockerBinForCopy(m.CopyPath)
	filterArg := fmt.Sprintf("label=%s=%s", docker.ComposeProjectLabel, m.ComposeProject)
	out, err := captureCmdFn(ctx, dockerBin, []string{"ps", "-a", "--filter", filterArg}, nil, "")
	return string(out), err
}

// reportLogsReal captures `docker compose logs --no-color --tail 200` for the
// copy at m.CopyPath via BuildInternalArgs (bypassing any user args.logs
// policy override — e.g. a project-configured `-f` would otherwise make
// report collection follow logs forever), degrading to per-container capture
// when the copy's own config can no longer be loaded.
func reportLogsReal(ctx context.Context, m *Manifest) (string, error) {
	cfg, dockerCfg, err := loadCopyConfig(m.CopyPath)
	if err != nil {
		return reportLogsIdentityFallback(ctx, m)
	}
	compose := docker.NewCompose(cfg, dockerCfg, m.CopyPath)
	args := compose.BuildInternalArgs("logs", "--no-color", "--tail", strconv.Itoa(reportLogTailLines))
	out, err := captureCmdFn(ctx, compose.BinName(), args, compose.BuildEnv(), m.CopyPath)
	return string(out), err
}

// reportLogsIdentityFallback lists containers by the manifest's exact compose
// project label, then captures each container's tail individually via
// `docker logs --tail 200 <id>`, prefixed with a `==== <id> ====` header.
func reportLogsIdentityFallback(ctx context.Context, m *Manifest) (string, error) {
	dockerBin := dockerBinForCopy(m.CopyPath)
	filterArg := fmt.Sprintf("label=%s=%s", docker.ComposeProjectLabel, m.ComposeProject)
	ids, err := listContainersFn(ctx, dockerBin, []string{"ps", "-aq", "--filter", filterArg})
	if err != nil {
		return "", err
	}

	var sb strings.Builder
	var errs []string
	for _, id := range ids {
		if id == "" {
			continue
		}
		out, capErr := captureCmdFn(ctx, dockerBin, []string{"logs", "--tail", strconv.Itoa(reportLogTailLines), id}, nil, "")
		fmt.Fprintf(&sb, "==== %s ====\n", id)
		sb.Write(out)
		sb.WriteString("\n")
		if capErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", id, capErr))
		}
	}
	if len(errs) > 0 {
		return sb.String(), fmt.Errorf("capturing container logs: %s", strings.Join(errs, "; "))
	}
	return sb.String(), nil
}

// captureCmdReal is the real subprocess capture behind captureCmdFn.
func captureCmdReal(ctx context.Context, bin string, args, env []string, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.Dir = dir
	cmd.Env = env
	return cmd.Output()
}

// captureCmdFn is a test seam over captureCmdReal: production runs the real
// subprocess; tests inject a recording stub to assert on the built
// bin/args/env/dir without spawning real processes. Shared by both the
// compose and identity-label docker captures.
var captureCmdFn = captureCmdReal
