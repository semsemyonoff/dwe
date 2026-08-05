package envtest

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// RenderSteps renders the ${...} substrate in a scenario's steps against the
// project config, in place. For every step (recursing one level into parallel
// substeps) it rewrites the string cmd: field and every string leaf reachable
// under with: — including strings nested inside with: maps and lists. Non-string
// YAML values (ints, bools, and the map/list containers themselves) are
// preserved untouched, so a `with:` value declared as 200 stays the int 200.
//
// Resolution goes through internal/shared/tpl against cfg.Raw, so scenarios may
// reference ${vars.x}, ${project.name}, the curated ${services.<name>...} subset,
// and the other Raw-backed namespaces. An absent path resolves to the empty
// string, matching the substrate's lenient semantics everywhere else.
//
// Order contract: RenderSteps MUST run BEFORE ResolvePhaseSteps so that
// plan-time builtin.Validate sees fully rendered params (spec §4). The stage-1b
// runner calls it on the freshly loaded scenario steps before handing them to
// the pipeline resolver.
func RenderSteps(steps []config.DeployStep, cfg *config.DweConfig) error {
	ctx := renderContext(cfg)
	for i := range steps {
		if err := renderStep(&steps[i], ctx); err != nil {
			return err
		}
	}
	return nil
}

// renderContext builds the ${...} evaluation context for scenario rendering.
// Only Raw-backed namespaces are meaningful for scenarios; command params,
// files, and generated values have no scenario source, so they stay nil — and
// renderScalar rejects any reference to them rather than letting the lenient
// resolvers substitute "".
func renderContext(cfg *config.DweConfig) *tpl.RenderContext {
	ctx := &tpl.RenderContext{Host: tpl.CurrentHostInfo()}
	if cfg != nil {
		ctx.Raw = cfg.Raw
	}
	return ctx
}

// renderStep renders a single step's cmd:, with:, and check: in place, then
// recurses one level into parallel substeps (deeper nesting is schema-rejected).
func renderStep(step *config.DeployStep, ctx *tpl.RenderContext) error {
	if step.Cmd != "" {
		rendered, err := renderScalar(step.Cmd, ctx)
		if err != nil {
			return fmt.Errorf("render step cmd %q: %w", step.Cmd, err)
		}
		step.Cmd = rendered
	}
	for key, val := range step.With {
		rendered, err := renderValue(val, ctx)
		if err != nil {
			return fmt.Errorf("render step with.%s: %w", key, err)
		}
		step.With[key] = rendered
	}
	if step.Check != nil {
		if err := renderAction(step.Check, ctx); err != nil {
			return err
		}
	}
	if step.Parallel != nil {
		for i := range step.Parallel.Steps {
			if err := renderStep(&step.Parallel.Steps[i], ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

// renderAction renders an action's cmd: and with: in place. A step's check:
// clause reuses the deploy-step action shape (same type/cmd/with), so it must go
// through the identical ${...} substrate — otherwise plan-time builtin.Validate
// and runtime ExecAction would see unresolved ${...} in check params.
func renderAction(action *config.Action, ctx *tpl.RenderContext) error {
	if action.Cmd != "" {
		rendered, err := renderScalar(action.Cmd, ctx)
		if err != nil {
			return fmt.Errorf("render check cmd %q: %w", action.Cmd, err)
		}
		action.Cmd = rendered
	}
	for key, val := range action.With {
		rendered, err := renderValue(val, ctx)
		if err != nil {
			return fmt.Errorf("render check with.%s: %w", key, err)
		}
		action.With[key] = rendered
	}
	return nil
}

// renderScalar renders one string through the ${...} substrate after checking
// it against what renderContext can actually offer. A scenario has no command
// params, resolved files, or harvested generated values, but ${param.*} and
// friends are known heads: without the pre-scan they would be rewritten into a
// template call over a nil map and render to "", erasing the reference before
// the pipeline resolver (which runs the identical check) ever sees it.
func renderScalar(s string, ctx *tpl.RenderContext) (string, error) {
	if err := tpl.ValidateRawScope(s); err != nil {
		return "", err
	}
	return tpl.RenderCommand(s, ctx)
}

// renderValue renders every string leaf reachable from v, preserving the shape
// and types of everything else. Maps and lists are walked recursively; strings
// are rendered; all other scalars are returned unchanged.
func renderValue(v any, ctx *tpl.RenderContext) (any, error) {
	switch val := v.(type) {
	case string:
		return renderScalar(val, ctx)
	case map[string]any:
		for k, inner := range val {
			rendered, err := renderValue(inner, ctx)
			if err != nil {
				return nil, err
			}
			val[k] = rendered
		}
		return val, nil
	case []any:
		for i, inner := range val {
			rendered, err := renderValue(inner, ctx)
			if err != nil {
				return nil, err
			}
			val[i] = rendered
		}
		return val, nil
	default:
		return v, nil
	}
}
