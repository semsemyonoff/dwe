package runtime

import (
	"errors"
	"fmt"
	"os"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/resolve"
)

// BuildRunContext constructs a RunContext for command execution by resolving
// params, context, and docker config. IO fields (Stdout, Stderr, Stdin,
// SkipConfirm, NonInteractive) are left at their zero values and must be
// set by the caller before invoking RunCommand.
func BuildRunContext(
	cfg *config.DevboxConfig,
	reg *registry.Registry,
	def *model.CommandDef,
	with map[string]any,
	workDir string,
) (RunContext, error) {
	// Convert With map[string]any → map[string]string for command param resolution.
	// String values are rendered through tpl.RenderCommand so that ${...} expressions
	// referencing project-level config (Raw) and host info resolve before pattern
	// validation. ${param.*} / ${context.*} are not available here — those belong to
	// the target command and are what this call is computing. Symmetric with the
	// workflow step path in runner_workflow.go.
	renderCtx := &tpl.RenderContext{Raw: cfg.Raw, Host: tpl.CurrentHostInfo()}
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
	rctx := &tpl.RenderContext{
		Raw:     cfg.Raw,
		Params:  params,
		Context: ctx,
		Host:    tpl.CurrentHostInfo(),
	}

	// Load docker config, tolerating missing file.
	dockerCfg, err := config.LoadDockerConfig(workDir, cfg)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return RunContext{}, fmt.Errorf("loading docker config: %w", err)
		}
		dockerCfg = &config.DockerConfig{}
	}

	// Return the populated context without IO fields.
	return RunContext{
		Cmd:          def,
		Params:       params,
		Context:      ctx,
		Render:       rctx,
		Config:       cfg,
		DockerConfig: dockerCfg,
		Registry:     reg,
		ProjectRoot:  workDir,
	}, nil
}
