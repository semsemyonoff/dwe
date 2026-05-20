package runtime

import (
	"context"
	"fmt"
	"os"

	"devbox-cli/internal/builtin"
	"devbox-cli/internal/render"
	"devbox-cli/internal/tpl"
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

	if err := builtin.Validate(name, with); err != nil {
		return fmt.Errorf("builtin %q: %w", name, err)
	}

	// Guard: the confirm builtin is interactive. Reject it inside a parallel
	// group when confirmation has not been pre-approved, so it never tries to
	// read from shared stdin and never reaches the huh prompt.
	if name == "confirm" && rc.UnderParallel && !rc.SkipConfirm && !rc.NonInteractive && !isNonInteractive() {
		return fmt.Errorf("%w: builtin confirm in command %q", ErrConfirmInsideParallel, rc.Cmd.ID)
	}

	stdin := stdinOrOS(rc)
	if f, ok := stdin.(*os.File); ok {
		stdin = f
	}

	execCtx := builtin.ExecContext{
		Config:      rc.Config,
		ProjectRoot: rc.ProjectRoot,
		Output:      render.NewWriter(stdout(rc)),
		Stdin:       stdin,
		SkipConfirm: rc.SkipConfirm || rc.NonInteractive || isNonInteractive(),
	}
	return builtin.Run(ctx, name, with, execCtx)
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
