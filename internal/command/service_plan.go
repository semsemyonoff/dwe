package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/lifecycle"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/usercommands/registry"
	"devbox-cli/internal/usercommands/runtime"

	"github.com/spf13/cobra"
)

// Sentinel errors for toggle plan building.
var (
	// ErrUnknownToggleRequires is returned when a service has an unrecognised
	// requires value in its on_enable or on_disable block.
	ErrUnknownToggleRequires = errors.New("unknown toggle requires value")
	// ErrDisableDeployForbidden is returned when on_disable.requires is set to
	// "deploy", which is not a meaningful operation for disabling a service.
	ErrDisableDeployForbidden = errors.New("on_disable.requires: deploy is not allowed")
	// ErrDeployRequiredNoDeployFile is returned when a service declares
	// requires: deploy but has no deploy.yml.
	ErrDeployRequiredNoDeployFile = errors.New("service requires deploy but has no deploy.yml")
)

// ToggleDirection indicates whether a service is being enabled or disabled.
type ToggleDirection string

const (
	// DirectionUnspecified is the zero value; rejected by buildTogglePlan.
	DirectionUnspecified ToggleDirection = ""
	// DirectionEnable enables the service.
	DirectionEnable ToggleDirection = "enable"
	// DirectionDisable disables the service.
	DirectionDisable ToggleDirection = "disable"
)

// ToggleAction is a single service enable or disable operation.
type ToggleAction struct {
	Service   string
	Direction ToggleDirection
}

// PlanStep is a reference to a user command that runs as a before or after hook.
type PlanStep struct {
	CommandID string
}

// ApplyStep is a single apply operation in a toggle plan.
type ApplyStep struct {
	Kind     journal.PendingKind // PendingRestart or PendingDeploy
	Services []string            // deduped + alphabetical; nil for restart (stack-wide)
}

// TogglePlan holds all steps needed to complete a toggle operation.
type TogglePlan struct {
	BeforeSteps []PlanStep
	ApplySteps  []ApplyStep
	AfterSteps  []PlanStep
	Notes       []string
}

// buildTogglePlan resolves the toggle plan from a set of toggle actions.
//
// svcDeploys is the per-service deploy config map (from config.LoadServiceDeployConfigs).
// reg is used to validate hook command references at build time.
//
// Coverage rule for ApplySteps:
//   - all RequiresNone  → ApplySteps = nil
//   - all RequiresRestart → [{Restart}]
//   - all RequiresDeploy  → [{Deploy, sorted contributors}]
//   - mixed restart+deploy → [{Deploy, sorted contributors}, {Restart}]
func buildTogglePlan(
	cfg *config.DevboxConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.DeployConfig,
	toggles []ToggleAction,
) (TogglePlan, error) {
	// Validate all directions before doing any work.
	for _, t := range toggles {
		if t.Direction == DirectionUnspecified {
			return TogglePlan{}, fmt.Errorf("toggle action for service %q has unspecified direction", t.Service)
		}
	}

	// Sort toggles by service name for deterministic hook ordering.
	sorted := make([]ToggleAction, len(toggles))
	copy(sorted, toggles)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Service < sorted[j].Service
	})

	var (
		beforeSteps    []PlanStep
		afterSteps     []PlanStep
		notes          []string
		deployContribs []string // services with RequiresDeploy (deduplicated)
		needsRestart   bool     // any service with RequiresRestart
	)

	for _, t := range sorted {
		svc, ok := cfg.Services[t.Service]
		if !ok {
			return TogglePlan{}, fmt.Errorf("service %q not found in config", t.Service)
		}

		// Select the hook block for this direction.
		var hooks *config.ServiceToggleHooks
		if t.Direction == DirectionEnable {
			hooks = svc.OnEnable
		} else {
			hooks = svc.OnDisable
		}

		// Runtime guard: reject unknown requires values.
		if hooks != nil && !hooks.Requires.IsKnown() {
			return TogglePlan{}, fmt.Errorf("%w: service %q has requires=%q",
				ErrUnknownToggleRequires, t.Service, hooks.Requires)
		}

		// Resolve the effective requires (defaulting unspecified → restart).
		var rawRequires config.ToggleRequires
		if hooks != nil {
			rawRequires = hooks.Requires
		}
		requires := rawRequires.OrDefault()

		// Runtime guard: on_disable.requires: deploy is forbidden.
		if t.Direction == DirectionDisable && requires == config.RequiresDeploy {
			return TogglePlan{}, fmt.Errorf("%w: service %q", ErrDisableDeployForbidden, t.Service)
		}

		// Runtime guard: requires: deploy needs a deploy.yml.
		if requires == config.RequiresDeploy && svcDeploys[t.Service] == nil {
			return TogglePlan{}, fmt.Errorf("%w: %s", ErrDeployRequiredNoDeployFile, t.Service)
		}

		// Accumulate apply kinds.
		switch requires {
		case config.RequiresRestart:
			needsRestart = true
		case config.RequiresDeploy:
			deployContribs = append(deployContribs, t.Service)
		}
		// RequiresNone: no apply step.

		// Collect before/after hook steps (in declaration order within this service).
		if hooks != nil {
			for _, cmdID := range hooks.Before {
				beforeSteps = append(beforeSteps, PlanStep{CommandID: cmdID})
			}
			for _, cmdID := range hooks.After {
				afterSteps = append(afterSteps, PlanStep{CommandID: cmdID})
			}
		}

		// Collect notes for this direction.
		if svc.Notes != nil {
			var note string
			if t.Direction == DirectionEnable {
				note = svc.Notes.Enable
			} else {
				note = svc.Notes.Disable
			}
			if note != "" {
				notes = append(notes, note)
			}
		}
	}

	// Build ApplySteps per the coverage rule.
	// Deploy step (if any) always comes before restart.
	var applySteps []ApplyStep
	if len(deployContribs) > 0 {
		sort.Strings(deployContribs)
		applySteps = append(applySteps, ApplyStep{
			Kind:     journal.PendingDeploy,
			Services: deployContribs,
		})
	}
	if needsRestart {
		applySteps = append(applySteps, ApplyStep{Kind: journal.PendingRestart})
	}

	// Suppress nil slices to empty slices for consistency in callers.
	_ = reg // registry is carried through for Task 12 executor; unused here

	return TogglePlan{
		BeforeSteps: beforeSteps,
		ApplySteps:  applySteps,
		AfterSteps:  afterSteps,
		Notes:       notes,
	}, nil
}

// renderTogglePlan writes the numbered plan to w.
// Lines that are runnable shell commands are printed in their exact shell form.
// Internal apply steps (multi-service deploy, restart) are prefixed with → to
// distinguish them from commands the user can copy-paste.
func renderTogglePlan(w io.Writer, plan TogglePlan) {
	var entries []string

	for _, s := range plan.BeforeSteps {
		entries = append(entries, fmt.Sprintf("devbox commands %s", s.CommandID))
	}
	for _, s := range plan.ApplySteps {
		switch s.Kind {
		case journal.PendingDeploy:
			if len(s.Services) == 1 {
				entries = append(entries, fmt.Sprintf("devbox deploy run --service %s", s.Services[0]))
			} else {
				entries = append(entries, fmt.Sprintf("→ apply step: deploy services {%s} (dependency-ordered at execution)",
					strings.Join(s.Services, ", ")))
			}
		case journal.PendingRestart:
			entries = append(entries, "→ apply step: restart stack")
		}
	}
	for _, s := range plan.AfterSteps {
		entries = append(entries, fmt.Sprintf("devbox commands %s", s.CommandID))
	}

	if len(entries) == 0 && len(plan.Notes) == 0 {
		_, _ = fmt.Fprintln(w, "No steps required.")
		return
	}

	if len(entries) > 0 {
		plural := "s"
		if len(entries) == 1 {
			plural = ""
		}
		_, _ = fmt.Fprintf(w, "Plan to apply (%d step%s):\n", len(entries), plural)
		for i, e := range entries {
			_, _ = fmt.Fprintf(w, "  %d. %s\n", i+1, e)
		}
	}

	if len(plan.Notes) > 0 {
		if len(entries) > 0 {
			_, _ = fmt.Fprintln(w, "")
		}
		_, _ = fmt.Fprintln(w, "Notes:")
		for _, n := range plan.Notes {
			_, _ = fmt.Fprintf(w, "  - %s\n", n)
		}
	}
}

// Contributor records the resolved requires for one toggled service so the
// executor knows which pending ops to clear on apply success.
type Contributor struct {
	Service  string
	Requires config.ToggleRequires // None / Restart / Deploy
}

// ExecuteOptions controls executor behaviour.
type ExecuteOptions struct {
	SkipHooks      bool
	NonInteractive bool
	// Contributors is required for non-empty plans; used to build the
	// pending-clear slice after all apply steps succeed.
	Contributors []Contributor
}

// ExecuteDeps carries all injectable dependencies for executeTogglePlan.
// Production callers assign real implementations; tests inject stubs.
type ExecuteDeps struct {
	Cmd       *cobra.Command
	Flags     *rootFlags
	BaseDir   string
	StatePath string
	Cfg       *config.DevboxConfig
	CmdReg    *registry.Registry

	// Seams — signatures match real callees so no adapter is needed at the call site.
	RunDeploy  func(ctx context.Context, cmd *cobra.Command, flags *rootFlags, opts DeployOpts) error
	RunRestart func(rctx lifecycle.RunContext) error
	RunUserCmd func(ctx context.Context, rc runtime.RunContext) error
}

// executeTogglePlan runs all steps in plan using deps.
// The apply phase is: run before-hooks → apply steps in order → (on success) clear pending → run after-hooks.
// On any apply-step failure: stop, do NOT run after-hooks, do NOT clear pending.
func executeTogglePlan(ctx context.Context, deps ExecuteDeps, plan TogglePlan, opts ExecuteOptions) error {
	// Empty plan is a no-op.
	if len(plan.ApplySteps) == 0 && len(plan.BeforeSteps) == 0 && len(plan.AfterSteps) == 0 {
		return nil
	}

	// Contributors required when there are apply steps.
	if len(plan.ApplySteps) > 0 && len(opts.Contributors) == 0 {
		return fmt.Errorf("executeTogglePlan: Contributors required for non-empty apply plan")
	}

	// Before hooks.
	if !opts.SkipHooks {
		for _, step := range plan.BeforeSteps {
			if err := runToggleHook(ctx, deps, step); err != nil {
				return fmt.Errorf("before hook %q: %w", step.CommandID, err)
			}
		}
	}

	// Apply steps — stop on first failure; do NOT clear pending on failure.
	for i, step := range plan.ApplySteps {
		var stepErr error
		switch step.Kind {
		case journal.PendingDeploy:
			stepErr = deps.RunDeploy(ctx, deps.Cmd, deps.Flags, DeployOpts{
				Services:       step.Services,
				NonInteractive: true,
			})
		case journal.PendingRestart:
			configPath := filepath.Join(deps.BaseDir, "devbox.yml")
			if deps.Flags != nil {
				configPath = deps.Flags.configPath
			}
			var errOut io.Writer
			if deps.Cmd != nil {
				errOut = deps.Cmd.ErrOrStderr()
			}
			stepErr = deps.RunRestart(lifecycle.RunContext{
				Ctx:        ctx,
				ConfigPath: configPath,
				Yes:        true,
				ErrOut:     errOut,
			})
		}
		if stepErr != nil {
			return fmt.Errorf("applying %s (step %d/%d): %w", step.Kind, i+1, len(plan.ApplySteps), stepErr)
		}
	}

	// All apply steps succeeded — clear pending atomically under a fresh lock.
	if len(plan.ApplySteps) > 0 {
		clears := buildPendingClears(opts.Contributors)
		if len(clears) > 0 {
			releaseLock, err := lock.AcquireProjectLocks(deps.BaseDir)
			if err != nil {
				return fmt.Errorf("acquiring locks for pending clear: %w", err)
			}
			clearErr := journal.ClearPendingOps(deps.StatePath, clears)
			releaseLock()
			if clearErr != nil {
				return fmt.Errorf("clearing pending after apply: %w", clearErr)
			}
		}
	}

	// After hooks.
	if !opts.SkipHooks {
		for _, step := range plan.AfterSteps {
			if err := runToggleHook(ctx, deps, step); err != nil {
				return fmt.Errorf("after hook %q: %w", step.CommandID, err)
			}
		}
	}

	return nil
}

// buildPendingClears translates a Contributors slice into the minimal set of
// PendingClear entries needed to remove exactly the contributor-owned pending ops.
func buildPendingClears(contributors []Contributor) []journal.PendingClear {
	var deployServices []string
	hasRestart := false
	for _, c := range contributors {
		switch c.Requires {
		case config.RequiresDeploy:
			deployServices = append(deployServices, c.Service)
		case config.RequiresRestart:
			hasRestart = true
		}
	}

	var clears []journal.PendingClear
	if len(deployServices) > 0 {
		sort.Strings(deployServices)
		clears = append(clears, journal.PendingClear{Kind: journal.PendingDeploy, Services: deployServices})
	}
	if hasRestart {
		clears = append(clears, journal.PendingClear{Kind: journal.PendingRestart})
	}
	return clears
}

// runToggleHook looks up a command by ID and runs it non-interactively.
func runToggleHook(ctx context.Context, deps ExecuteDeps, step PlanStep) error {
	if deps.CmdReg == nil {
		return fmt.Errorf("command registry required for hook execution")
	}
	cmdDef, err := deps.CmdReg.Get(step.CommandID)
	if err != nil {
		return fmt.Errorf("looking up command %q: %w", step.CommandID, err)
	}
	var stdout, stderr io.Writer
	var stdin io.Reader
	if deps.Cmd != nil {
		stdout = deps.Cmd.OutOrStdout()
		stderr = deps.Cmd.ErrOrStderr()
		stdin = deps.Cmd.InOrStdin()
	}
	rc := runtime.RunContext{
		Cmd:            cmdDef,
		Config:         deps.Cfg,
		Registry:       deps.CmdReg,
		ProjectRoot:    deps.BaseDir,
		Stdout:         stdout,
		Stderr:         stderr,
		Stdin:          stdin,
		SkipConfirm:    true,
		NonInteractive: true,
		SkipNotify:     true,
	}
	return deps.RunUserCmd(ctx, rc)
}
