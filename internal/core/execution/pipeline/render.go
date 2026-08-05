package pipeline

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// hasKnownVarRef reports whether s contains at least one ${...} reference
// whose head namespace is known to tpl.CompileVarSyntax. A bare
// tpl.VarPattern.MatchString is not enough: a shell-style ${CONTAINER} beside
// an untouched Go-template idiom would still pull the whole string into
// tpl.RenderCommand and then fail on the {{ }} part. Gating on a KNOWN head
// keeps strings that only use shell-style ${VAR} (or none at all) out of the
// renderer entirely — see resolveLeafStep for the call site.
//
// Membership goes through tpl.IsKnownVarHead rather than a local index over
// tpl.KnownVarHeads: the gate must agree with what CompileVarSyntax actually
// rewrites, and a second copy of the set is a second thing to keep in sync.
func hasKnownVarRef(s string) bool {
	for _, m := range tpl.VarPattern.FindAllStringSubmatch(s, -1) {
		head, _, _ := strings.Cut(m[1], ".")
		if tpl.IsKnownVarHead(head) {
			return true
		}
	}
	return false
}

// renderIfKnown renders s through tpl.RenderCommand only when it carries a
// known-head ${...} reference (hasKnownVarRef); otherwise s is returned
// unchanged, never touched by the template engine.
func renderIfKnown(s string, ctx *tpl.RenderContext) (string, error) {
	if s == "" || !hasKnownVarRef(s) {
		return s, nil
	}
	return tpl.RenderCommand(s, ctx)
}

// renderWithLeaf renders a single string leaf of a with: map. When wide is
// false the gate is renderIfKnown's — a known-head ${...} reference and
// nothing else. When wide is true the leaf ALSO enters the engine on a literal
// `{{`.
//
// wide is set exactly where usercommands.BuildRunContext used to render the
// map unconditionally at exec time and no longer does (the pipeline now hands
// it to BuildPreRenderedRunContext, which never re-renders): a `type: command`
// step/action, and a files_gate probe. There, gating on a known ${...} head
// alone would silently pass a Go-template value — `{{ resolve .Raw
// "vars.db.host" }}`, the form varsusage already recognizes inside with: —
// through as literal text.
//
// Everywhere else wide stays false, and deliberately so. A `type: builtin`
// with: map was never rendered by this engine at all, and its values are
// consumed raw by the builtin — the `message` builtin renders its own text
// against DweConfig (`{{ .Project.Name }}`, a documented parameter form), and
// the `shell` predicate takes a `docker inspect -f "{{.State.Status}}"` format
// string. Both parse as Go templates but not in *this* template space, so a
// wide gate here turns a working, documented pipeline into a hard resolve
// failure. Same argument as the one that keeps cmd:/when: on the narrow gate.
func renderWithLeaf(s string, ctx *tpl.RenderContext, wide bool) (string, error) {
	if s == "" {
		return s, nil
	}
	if hasKnownVarRef(s) || (wide && strings.Contains(s, "{{")) {
		return tpl.RenderCommand(s, ctx)
	}
	return s, nil
}

// renderValue renders every string leaf reachable from v through
// renderWithLeaf (see there for what wide gates), recursing into nested maps
// and sequences. Non-string scalars, and strings the gate rejects, are
// returned unchanged. v is never mutated — new map/slice containers are
// allocated for any branch reached during recursion.
func renderValue(v any, ctx *tpl.RenderContext, wide bool) (any, error) {
	switch val := v.(type) {
	case string:
		return renderWithLeaf(val, ctx, wide)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			rendered, err := renderValue(inner, ctx, wide)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = rendered
		}
		return out, nil
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			rendered, err := renderValue(inner, ctx, wide)
			if err != nil {
				return nil, fmt.Errorf("[%d]: %w", i, err)
			}
			out[i] = rendered
		}
		return out, nil
	default:
		return v, nil
	}
}

// renderWith renders every string leaf of a with: map into a freshly
// allocated copy (see renderWithLeaf for the gate); the input map is never
// mutated.
func renderWith(with map[string]any, ctx *tpl.RenderContext, wide bool) (map[string]any, error) {
	if len(with) == 0 {
		return with, nil
	}
	out := make(map[string]any, len(with))
	for k, v := range with {
		rendered, err := renderValue(v, ctx, wide)
		if err != nil {
			return nil, fmt.Errorf("with.%s: %w", k, err)
		}
		out[k] = rendered
	}
	return out, nil
}

// renderAction renders an Action's cmd/with into a freshly allocated copy.
// A nil action renders to nil. The input action is never mutated. Only a
// `type: command` action's with: map gets the wide gate — a builtin check:
// consumes its with: values raw, so a Go-template value there belongs to the
// builtin's own template space, not this one.
func renderAction(action *config.Action, ctx *tpl.RenderContext) (*config.Action, error) {
	if action == nil {
		return nil, nil
	}
	cmd, err := renderIfKnown(action.Cmd, ctx)
	if err != nil {
		return nil, fmt.Errorf("cmd: %w", err)
	}
	with, err := renderWith(action.With, ctx, action.Type == "command")
	if err != nil {
		return nil, err
	}
	out := *action
	out.Cmd = cmd
	out.With = with
	return &out, nil
}

// renderFilesGate renders a FilesGate's command/with into a freshly allocated
// copy. A nil gate renders to nil. The input gate is never mutated; Require
// and State (not template-bearing) are copied through unchanged by the
// struct-value copy.
func renderFilesGate(fg *filesgate.FilesGate, ctx *tpl.RenderContext) (*filesgate.FilesGate, error) {
	if fg == nil {
		return nil, nil
	}
	cmd, err := renderIfKnown(fg.Command, ctx)
	if err != nil {
		return nil, fmt.Errorf("files_gate.command: %w", err)
	}
	// A files_gate with: map is always destined for a user command
	// (BuildPreRenderedRunContext in evalFilesGate), so it takes the wide gate
	// regardless of the host step's own type.
	with, err := renderWith(fg.With, ctx, true)
	if err != nil {
		return nil, fmt.Errorf("files_gate.%w", err)
	}
	out := *fg
	out.Command = cmd
	out.With = with
	return &out, nil
}

// renderWhen renders a runtime condition's Cmd into a freshly allocated copy.
// A nil condition, or one whose type is not runtime (template conditions carry
// Expr, not Cmd, and are evaluated separately), is returned unchanged. When is
// a pointer shared with the loaded config — the same reference-type hazard as
// With/Check/FilesGate — so this never mutates the input; callers at every
// scope (phase, parallel-group parent, leaf step) route through this before
// storing the condition on a ResolvedStep.
func renderWhen(when *condition.Condition, ctx *tpl.RenderContext) (*condition.Condition, error) {
	if when == nil || !when.IsRuntime() {
		return when, nil
	}
	cmd, err := renderIfKnown(when.Cmd, ctx)
	if err != nil {
		return nil, fmt.Errorf("when.cmd: %w", err)
	}
	out := *when
	out.Cmd = cmd
	return &out, nil
}

// renderStepFields renders a DeployStep's template-bearing fields — cmd,
// with (recursively), check, files_gate, and timeout — into a freshly
// resolved copy of the step. The input step's reference-typed fields (With,
// Check, FilesGate) are never mutated: With and Check are reference types
// shared with the loaded config, so rendering in place would make
// journal.ProjectConfigHash depend on deploy scope and a second resolve
// double-render.
//
// cmd, check.cmd, files_gate.command and timeout are gated on hasKnownVarRef
// before they reach tpl.RenderCommand, so a command with no known-head ${...}
// reference (a bare Go-template idiom like `{{.State.Status}}`, or
// shell-style ${VAR} only) never enters the template engine. with: leaves take
// the wider renderWithLeaf gate only where the map used to be rendered by
// usercommands.BuildRunContext at exec time — a `type: command` step, or any
// step carrying a files_gate (whose probe falls back to step.With when
// files_gate.with is empty). See renderWithLeaf for why widening it further
// breaks builtin with: maps.
func renderStepFields(step config.DeployStep, ctx *tpl.RenderContext) (config.DeployStep, error) {
	out := step

	cmd, err := renderIfKnown(step.Cmd, ctx)
	if err != nil {
		return config.DeployStep{}, fmt.Errorf("cmd: %w", err)
	}
	out.Cmd = cmd

	with, err := renderWith(step.With, ctx, step.Type == "command" || step.FilesGate != nil)
	if err != nil {
		return config.DeployStep{}, err
	}
	out.With = with

	check, err := renderAction(step.Check, ctx)
	if err != nil {
		return config.DeployStep{}, fmt.Errorf("check: %w", err)
	}
	out.Check = check

	fg, err := renderFilesGate(step.FilesGate, ctx)
	if err != nil {
		return config.DeployStep{}, err
	}
	out.FilesGate = fg

	timeout, err := renderIfKnown(step.Timeout, ctx)
	if err != nil {
		return config.DeployStep{}, fmt.Errorf("timeout: %w", err)
	}
	out.Timeout = timeout

	return out, nil
}

// RenderStep renders a DeployStep's template-bearing fields (cmd, with,
// check, files_gate, timeout) against cfg into a freshly resolved copy — the
// same rendering ResolvePhaseSteps applies to every step it resolves. Callers
// that read a step outside ResolvePhaseSteps (e.g. `dwe reset step`, which
// looks a step up by address via reset.FindStep) must call this before
// reading any of those fields, so the address path and ResolvePhaseSteps
// never disagree about what a step's command actually is.
func RenderStep(cfg *config.DweConfig, step config.DeployStep) (config.DeployStep, error) {
	return renderStepFields(step, renderContextFor(cfg))
}

// RenderWhen renders a runtime condition's Cmd against cfg into a freshly
// resolved copy, mirroring the rendering ResolvePhaseSteps applies to a
// step's/phase's runtime when condition before storing it on a ResolvedStep.
// A nil condition, or one that is not a runtime condition, is returned
// unchanged.
func RenderWhen(cfg *config.DweConfig, when *condition.Condition) (*condition.Condition, error) {
	return renderWhen(when, renderContextFor(cfg))
}

// renderContextFor builds the ${...} evaluation context for pipeline step
// rendering. Only Raw-backed namespaces and host info are meaningful on this
// path — command params, files, snapshot vars, and generated values have no
// pipeline-step source, so they stay nil/zero (SnapshotScope defaults to
// SnapshotScopeNone: any ${snapshot.*} reference here is a resolve error,
// matching envtest's scenario rendering context).
func renderContextFor(cfg *config.DweConfig) *tpl.RenderContext {
	ctx := &tpl.RenderContext{Host: tpl.CurrentHostInfo()}
	if cfg != nil {
		ctx.Raw = cfg.Raw
	}
	return ctx
}
