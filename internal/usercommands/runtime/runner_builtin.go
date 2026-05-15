package runtime

import (
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
func (r *BuiltinRunner) Run(ctx RunContext) error {
	name := ctx.Cmd.Cmd
	if name == "" {
		return fmt.Errorf("builtin: cmd (builtin name) is empty")
	}

	with, err := renderBuiltinWith(ctx.Cmd.With, ctx.Render)
	if err != nil {
		return fmt.Errorf("builtin %q: render with: %w", name, err)
	}

	if err := builtin.Validate(name, with); err != nil {
		return fmt.Errorf("builtin %q: %w", name, err)
	}

	stdin := stdinOrOS(ctx)
	if rc, ok := stdin.(*os.File); ok {
		stdin = rc
	}

	execCtx := builtin.ExecContext{
		Config:      ctx.Config,
		ProjectRoot: ctx.ProjectRoot,
		Output:      render.NewWriter(stdout(ctx)),
		Stdin:       stdin,
		SkipConfirm: ctx.SkipConfirm,
	}
	return builtin.Run(name, with, execCtx)
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
