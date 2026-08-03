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
	return buildRunContext(cfg, reg, def, with, workDir, nil, tpl.SnapshotScopeNone)
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
	return buildRunContext(cfg, reg, def, with, workDir, snapshot, scope)
}

func buildRunContext(
	cfg *config.DweConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
	snapshot map[string]any,
	scope tpl.SnapshotScope,
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
