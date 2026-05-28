package snapshot

import (
	"context"
	"fmt"
	"io"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/usercommands/registry"
	"devbox-cli/internal/core/usercommands/runtime"
	"devbox-cli/internal/shared/tpl"
)

// ExecParams describes one invocation of a snapshot workflow (create, restore,
// remove, rollback). The workflow body is reused from the parsed snapshot
// config and executed by the existing user-command workflow runner via a
// synthetic *model.CommandDef built at runtime.
type ExecParams struct {
	// Cfg is the loaded devbox config used for ${...} resolution and to look
	// up registered user commands referenced from workflow steps.
	Cfg *config.DevboxConfig
	// Registry is the loaded user-command registry. Workflow steps reference
	// user commands by ID through it.
	Registry *registry.Registry
	// BaseDir is the project root (the directory that holds devbox/).
	BaseDir string
	// Workflow is the chosen create/restore/remove block (or its variant).
	Workflow *config.SnapshotWorkflow
	// Vars is the ${snapshot.*} map (typically built via BuildSnapshotVars).
	Vars map[string]any
	// Scope governs which ${snapshot.*} keys are valid at compile.
	Scope tpl.SnapshotScope
	// Stdout / Stderr receive workflow output. nil falls back to os.Stdout /
	// os.Stderr inside the runner.
	Stdout io.Writer
	Stderr io.Writer
	// SkipConfirm forwards through to confirm steps (e.g. -y was passed).
	SkipConfirm bool
	// NonInteractive forces non-interactive code paths even on a TTY.
	NonInteractive bool
	// StepObserverFactory, when non-nil, is invoked after the synthetic
	// CommandDef is built so the per-workflow live UI can install itself
	// before the runner sees the first step. A nil factory (or one that
	// returns nil) leaves rc.StepObserver nil so the runner falls back to
	// the existing plain-stdout output.
	StepObserverFactory StepObserverFactory
}

// RunWorkflow executes p.Workflow under the snapshot variable scope.
//
// The snapshot workflow is wrapped in a synthetic type=workflow CommandDef so
// the entire existing user-command execution pipeline (when/confirm/parallel/
// continue_on_error, sub-step rendering, notifications suppression) handles
// the run. The synthetic command's ID is "<internal>.snapshot.<scope>".
func RunWorkflow(ctx context.Context, p ExecParams) error {
	if p.Workflow == nil {
		return fmt.Errorf("snapshot: RunWorkflow called with nil workflow")
	}
	if len(p.Workflow.Steps) == 0 {
		return fmt.Errorf("snapshot: workflow has no steps")
	}
	if p.Registry == nil {
		return fmt.Errorf("snapshot: registry is required to run workflows")
	}
	if p.Cfg == nil {
		return fmt.Errorf("snapshot: devbox config is required to run workflows")
	}

	cmd := &model.CommandDef{
		ID:    "<internal>.snapshot." + p.Scope.String(),
		Type:  model.CommandTypeWorkflow,
		Steps: p.Workflow.Steps,
	}
	if err := cmd.Validate(); err != nil {
		return fmt.Errorf("snapshot: synthetic workflow invalid: %w", err)
	}

	var obs StepObserverCloser
	if p.StepObserverFactory != nil {
		obs = p.StepObserverFactory(p.Workflow.Steps)
		if obs != nil {
			defer obs.Close()
		}
	}

	rc := runtime.RunContext{
		Cmd: cmd,
		Render: &tpl.RenderContext{
			Raw:           p.Cfg.Raw,
			Host:          tpl.CurrentHostInfo(),
			Snapshot:      p.Vars,
			SnapshotScope: p.Scope,
		},
		Config:         p.Cfg,
		Registry:       p.Registry,
		ProjectRoot:    p.BaseDir,
		Stdout:         p.Stdout,
		Stderr:         p.Stderr,
		SkipConfirm:    p.SkipConfirm,
		NonInteractive: p.NonInteractive,
		// Snapshot workflows are the operator's "command" — the outer
		// subcommand layer owns end-of-run notifications, so suppress
		// any per-leaf notification opt-ins.
		SkipNotify: true,
	}
	if obs != nil {
		rc.StepObserver = obs
	}

	return runtime.RunCommand(ctx, rc)
}

// SelectWorkflow picks the workflow body to run for a given subcommand.
//
// Semantics:
//
//   - kind names the top-level block ("create", "restore", "remove").
//   - empty variant → return the default block (error when missing).
//   - non-empty variant on "create" → return Variants[variant] or error
//     when the variant is not defined.
//   - non-empty variant on "restore"/"remove" → return Variants[variant]
//     when present; otherwise fall back to the default block. This mirrors
//     the create/restore asymmetry in the spec: a snapshot captured with a
//     variant may be restored by a workflow that has no matching variant.
//
// A nil cfg returns a clear error so callers don't have to nil-check before
// dispatching.
func SelectWorkflow(cfg *config.SnapshotConfig, kind, variant string) (*config.SnapshotWorkflow, error) {
	if cfg == nil {
		return nil, fmt.Errorf("snapshot: no snapshot config loaded")
	}
	var base *config.SnapshotWorkflow
	switch kind {
	case "create":
		base = cfg.Create
	case "restore":
		base = cfg.Restore
	case "remove":
		base = cfg.Remove
	default:
		return nil, fmt.Errorf("snapshot: unknown workflow kind %q", kind)
	}

	if variant == "" {
		if base == nil {
			return nil, fmt.Errorf("snapshot: %s workflow is not defined", kind)
		}
		return base, nil
	}

	if base != nil {
		if v, ok := base.Variants[variant]; ok {
			return &config.SnapshotWorkflow{
				Description: v.Description,
				Steps:       v.Steps,
			}, nil
		}
	}

	if kind == "create" {
		return nil, fmt.Errorf("snapshot: create variant %q is not defined", variant)
	}

	if base == nil {
		return nil, fmt.Errorf("snapshot: %s workflow is not defined", kind)
	}
	return base, nil
}
