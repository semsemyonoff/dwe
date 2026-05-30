package runtime

import (
	"context"
	"fmt"
	"os"

	"devbox-cli/internal/core/execution/builtin"
	"devbox-cli/internal/core/usercommands/runtime/internal/runio"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/shared/tpl"
)

// BuiltinRunner executes type=builtin commands by invoking an engine-internal
// builtin action by name. The command's cmd: field holds the builtin name and
// with: holds its parameters. String values inside with: are rendered against
// the command template context so callers can use ${...} / {{ }} expressions.
type BuiltinRunner struct{}

// Run dispatches the builtin and surfaces any validation or execution error.
func (r *BuiltinRunner) Run(ctx context.Context, rc RunContext) error {
	name := rc.Cmd.Cmd
	if name == "" {
		return fmt.Errorf("builtin: cmd (builtin name) is empty")
	}

	with, err := renderBuiltinWith(rc.Cmd.With, rc.Render)
	if err != nil {
		return fmt.Errorf("builtin %q: render with: %w", name, err)
	}

	// Guard: interactive builtins (confirm, docker_daemon_logs) cannot run
	// inside a parallel group — they would compete for shared stdin / TTY.
	// SkipConfirm semantics are asymmetric: `confirm` is safe under parallel
	// when pre-approved (--yes auto-yes), but `docker_daemon_logs` tails a
	// foreground process and has no auto-skip — it always rejects under
	// parallel regardless of -y. The guard runs before Validate so the
	// surfaced error is the architectural reason, not a downstream
	// missing-field complaint.
	if rc.UnderParallel && builtin.IsInteractive(name) {
		skipOK := name == "confirm" && (rc.SkipConfirm || rc.NonInteractive || isNonInteractive())
		if !skipOK {
			return fmt.Errorf("%w: builtin %s in command %q", ErrConfirmInsideParallel, name, rc.Cmd.ID)
		}
	}

	// Daemon-generated virtual commands (.start, .logs, .stop) invoke KindInternal
	// builtins and are NOT user-authored YAML — they are expanded from type: daemon.
	callerCtx := builtin.CtxUserYAML
	if rc.Cmd.DerivedFromDaemon != "" && rc.Cmd.SourceDaemon != nil {
		callerCtx = builtin.CtxInternal
	}

	if err := builtin.Validate(name, with, callerCtx); err != nil {
		return fmt.Errorf("builtin %q: %w", name, err)
	}

	stdin := runio.StdinOrOS(rc)
	if f, ok := stdin.(*os.File); ok {
		stdin = f
	}

	execCtx := builtin.ExecContext{
		Config:       rc.Config,
		DockerConfig: rc.DockerConfig,
		ProjectRoot:  rc.ProjectRoot,
		Output:       render.NewWriter(runio.StdoutOf(rc)),
		Stdin:        stdin,
		SkipConfirm:  rc.SkipConfirm || rc.NonInteractive || isNonInteractive(),
	}
	return builtin.Run(ctx, name, with, execCtx, callerCtx)
}

// renderBuiltinWith walks the with map and renders any string values via the
// command template engine. Lists and nested maps are walked recursively so
// callers can use ${...} or {{ }} inside e.g. services list entries.
func renderBuiltinWith(in map[string]any, rctx *tpl.RenderContext) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		rv, err := renderAny(v, rctx)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = rv
	}
	return out, nil
}

func renderAny(v any, rctx *tpl.RenderContext) (any, error) {
	switch val := v.(type) {
	case string:
		return tpl.RenderCommand(val, rctx)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			rv, err := renderAny(item, rctx)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = rv
		}
		return out, nil
	case map[string]any:
		return renderBuiltinWith(val, rctx)
	default:
		return v, nil
	}
}
