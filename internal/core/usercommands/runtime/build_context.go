package runtime

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/resolve"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// BuildRunContext constructs a RunContext for command execution by resolving
// params, context, and docker config. IO fields (Stdout, Stderr, Stdin,
// SkipConfirm, NonInteractive) are left at their zero values and must be
// set by the caller before invoking RunCommand.
func BuildRunContext(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
) (RunContext, error) {
	return buildRunContext(cfg, reg, def, with, workDir, nil, tpl.SnapshotScopeNone, false)
}

// BuildPreRenderedRunContext mirrors BuildRunContext for callers whose with:
// map has ALREADY been rendered through tpl.RenderCommand — the pipeline,
// which renders every step's cmd/with/check leaves once at resolve time
// (pipeline.renderStepFields). Rendering such a map a second time is not
// idempotent: a value that resolved to text containing a literal `{{` (a var
// holding a `docker inspect -f` format string) would be parsed as a Go
// template on the second pass and fail the step, and a value that resolved to
// another `${vars.*}` reference would be expanded a second time. Values are
// stringified but never re-rendered.
func BuildPreRenderedRunContext(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
) (RunContext, error) {
	return buildRunContext(cfg, reg, def, with, workDir, nil, tpl.SnapshotScopeNone, true)
}

// BuildSnapshotRunContext constructs a RunContext for command execution inside
// a snapshot workflow scope. It mirrors BuildRunContext but injects the
// snapshot variable map and scope into the resulting Render context so that
// ${snapshot.*} resolves and is scope-validated for both `with:` rendering at
// the top of this call and downstream `when:`/script env expansion.
func BuildSnapshotRunContext(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
	snapshot map[string]any,
	scope tpl.SnapshotScope,
) (RunContext, error) {
	return buildRunContext(cfg, reg, def, with, workDir, snapshot, scope, false)
}

func buildRunContext(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
	snapshot map[string]any,
	scope tpl.SnapshotScope,
	withPreRendered bool,
) (RunContext, error) {
	// Convert With map[string]any → map[string]string for command param resolution.
	// String values are rendered through tpl.RenderCommand so that ${...} expressions
	// referencing project-level config (Raw) and host info resolve before pattern
	// validation. ${param.*} / ${context.*} are not available here — those belong to
	// the target command and are what this call is computing. Symmetric with the
	// workflow step path in runner_workflow.go.
	renderCtx := &tpl.RenderContext{
		Raw:           cfg.Raw,
		Host:          tpl.CurrentHostInfo(),
		Snapshot:      snapshot,
		SnapshotScope: scope,
	}
	strWith := make(map[string]string, len(with))
	for k, v := range with {
		raw := fmt.Sprintf("%v", v)
		if withPreRendered {
			strWith[k] = raw
			continue
		}
		rendered, err := tpl.RenderCommand(raw, renderCtx)
		if err != nil {
			return RunContext{}, fmt.Errorf("rendering with[%q]: %w", k, err)
		}
		strWith[k] = rendered
	}

	// Resolve parameters using the converted with map.
	params, err := resolve.Params(def.Params, strWith, cfg)
	if err != nil {
		return RunContext{}, fmt.Errorf("resolving params: %w", err)
	}

	// Resolve context.
	ctx, err := resolve.Context(def.Context, cfg)
	if err != nil {
		return RunContext{}, fmt.Errorf("resolving context: %w", err)
	}

	// Create the render context.
	//
	// Args carries the command's own args.default. Doing it here rather than at
	// the CLI call site is load-bearing: a command is also invoked from workflow
	// steps, pipeline actions and validate checks, none of which go through
	// `dwe cmd`. Without this, `argv: [go, test, -race, "${args}"]` with
	// args.default ["./..."] would render as `go test -race` on those paths —
	// a silently different, wrong command line. The CLI overwrites Args with the
	// caller's actual pass-through arguments after construction.
	rctx := &tpl.RenderContext{
		Raw:           cfg.Raw,
		Params:        params,
		Context:       ctx,
		Host:          tpl.CurrentHostInfo(),
		Snapshot:      snapshot,
		Args:          def.Args.Resolve(nil),
		SnapshotScope: scope,
	}

	// Load docker config, tolerating missing file.
	dockerCfg, err := config.LoadDockerConfigOrEmpty(workDir, cfg)
	if err != nil {
		return RunContext{}, err
	}

	// Return the populated context without IO fields.
	// Translator defaults to NopTranslator; callers wire in the real translator
	// and locale after construction if needed.
	return RunContext{
		Cmd:          def,
		Params:       params,
		Context:      ctx,
		Render:       rctx,
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		ProjectRoot:  workDir,
		Translator:   i18n.NopTranslator{},
		Locale:       "",
	}, nil
}
