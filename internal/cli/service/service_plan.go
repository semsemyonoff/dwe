package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/cli/deploy"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/runtime"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	"github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"github.com/spf13/cobra"
)

// renderNote evaluates a `notes.enable` / `notes.disable` string as a Go
// template against the toggled service's context. Available top-level keys:
//
//   - .name     — the service name being toggled
//   - .svc      — the resolved config.ServiceConfig for that service
//   - .services — the full cfg.Services map (resolved, enabled bits applied)
//   - .project  — cfg.Project
//
// The raw merged-config map (cfg.Raw) is intentionally NOT exposed: that map
// changes shape whenever the loader is refactored, and binding it to a
// user-visible template surface would freeze the layout.
//
// Strings without any `{{` markers are returned unchanged.
func renderNote(note string, cfg *config.DweConfig, name string) string {
	if note == "" || cfg == nil {
		return note
	}
	data := map[string]any{
		"name":     name,
		"svc":      cfg.Services[name],
		"services": cfg.Services,
		"project":  cfg.Project,
	}
	out, err := tpl.Render(note, data)
	if err != nil {
		return fmt.Sprintf("[note template error: %v] %s", err, note)
	}
	return out
}

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
// deployedServices is the set of services currently in StatusDeployed (from the
// journal); used to resolve `requires: deploy-or-restart`. Nil/empty means
// "nothing currently deployed" — deploy-or-restart resolves to deploy.
//
// Coverage rule for ApplySteps:
//   - all RequiresNone  → ApplySteps = nil
//   - all RequiresRestart → [{Restart}]
//   - all RequiresDeploy  → [{Deploy, sorted contributors}]
//   - mixed restart+deploy → [{Deploy, sorted contributors}, {Restart}]
func buildTogglePlan(
	cfg *config.DweConfig,
	reg *registry.Registry,
	svcDeploys map[string]*config.ServiceDeployConfig,
	toggles []ToggleAction,
	deployedServices map[string]bool,
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

		// Runtime guards must mirror service_hooks validator and run against the
		// RAW value (unspecified collapsed to its default) — never the post-Resolve
		// value — so journal state cannot make a statically invalid config pass.
		var rawRequires config.ToggleRequires
		if hooks != nil {
			rawRequires = hooks.Requires
		}
		raw := rawRequires.OrDefault()

		// on_disable: deploy / deploy-or-restart are both forbidden (the latter
		// resolves to deploy on never-deployed services).
		if t.Direction == DirectionDisable &&
			(raw == config.RequiresDeploy || raw == config.RequiresDeployOrRestart) {
			return TogglePlan{}, fmt.Errorf("%w: service %q", ErrDisableDeployForbidden, t.Service)
		}

		// deploy / deploy-or-restart need a deploy.yml at the time the toggle
		// is declared, regardless of current journal state.
		if (raw == config.RequiresDeploy || raw == config.RequiresDeployOrRestart) &&
			svcDeploys[t.Service] == nil {
			return TogglePlan{}, fmt.Errorf("%w: %s", ErrDeployRequiredNoDeployFile, t.Service)
		}

		// Now collapse deploy-or-restart against the journal for ApplyStep shaping.
		requires := raw.Resolve(deployedServices[t.Service])

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

		// Collect notes for this direction. Notes support Go templates against
		// the merged config (see renderNote for available keys).
		if svc.Notes != nil {
			var note string
			if t.Direction == DirectionEnable {
				note = svc.Notes.Enable
			} else {
				note = svc.Notes.Disable
			}
			if note != "" {
				notes = append(notes, renderNote(note, cfg, t.Service))
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

	// Validate hook command IDs against the registry before any mutation.
	if reg != nil {
		for _, step := range beforeSteps {
			if _, err := reg.Get(step.CommandID); err != nil {
				return TogglePlan{}, fmt.Errorf("before hook command %q not found: %w", step.CommandID, err)
			}
		}
		for _, step := range afterSteps {
			if _, err := reg.Get(step.CommandID); err != nil {
				return TogglePlan{}, fmt.Errorf("after hook command %q not found: %w", step.CommandID, err)
			}
		}
	}

	return TogglePlan{
		BeforeSteps: beforeSteps,
		ApplySteps:  applySteps,
		AfterSteps:  afterSteps,
		Notes:       notes,
	}, nil
}

// planEntry is the data for one rendered plan line. icon is the leading glyph
// (already styled), body is the human-readable literal to display, and stylize
// is a per-kind wrapper that applies the body's color/weight as a single span —
// keeping body unbroken in the output so existing substring-based tests still
// match.
type planEntry struct {
	icon    string
	body    string
	stylize func(string) string
}

// renderTogglePlan writes the numbered plan to w using the project's lipgloss
// palette so the output reads at a glance.
//
// Style choices:
//   - Before/after hook commands → accent "▶" icon, key style on the full command.
//   - Single-service deploy command → success "↑" icon, accent on the full command.
//   - Multi-service deploy apply step → success "↑" icon, accent body.
//   - Restart apply step → warning "↻" icon, accent body.
//   - Notes section → accent header, accent "•" bullets, body in default color.
//
// Each entry's body is rendered as a single styled span (no ANSI inside the
// literal) so the apply-step text remains greppable for tests and for users.
func renderTogglePlan(w io.Writer, plan TogglePlan) {
	cmdEntry := func(commandID string) planEntry {
		return planEntry{
			icon:    styles.StyleInfo("▶"),
			body:    fmt.Sprintf("dwe commands %s", commandID),
			stylize: styles.StyleKey,
		}
	}

	var entries []planEntry

	for _, s := range plan.BeforeSteps {
		entries = append(entries, cmdEntry(s.CommandID))
	}
	for _, s := range plan.ApplySteps {
		switch s.Kind {
		case journal.PendingDeploy:
			if len(s.Services) == 1 {
				entries = append(entries, planEntry{
					icon:    styles.RenderEnabled("↑"),
					body:    fmt.Sprintf("dwe deploy run --service %s", s.Services[0]),
					stylize: styles.StyleKey,
				})
			} else {
				body := fmt.Sprintf("→ apply step: deploy services {%s} (dependency-ordered at execution)",
					strings.Join(s.Services, ", "))
				entries = append(entries, planEntry{
					icon:    styles.RenderEnabled("↑"),
					body:    body,
					stylize: styles.StyleInfo,
				})
			}
		case journal.PendingRestart:
			entries = append(entries, planEntry{
				icon:    styles.StyleWarning("↻"),
				body:    "→ apply step: restart stack",
				stylize: styles.StyleInfo,
			})
		}
	}
	for _, s := range plan.AfterSteps {
		entries = append(entries, cmdEntry(s.CommandID))
	}

	if len(entries) == 0 && len(plan.Notes) == 0 {
		_, _ = fmt.Fprintln(w, styles.StyleMuted("No steps required."))
		return
	}

	if len(entries) > 0 {
		plural := "s"
		if len(entries) == 1 {
			plural = ""
		}
		header := fmt.Sprintf("Plan to apply (%d step%s):", len(entries), plural)
		_, _ = fmt.Fprintln(w, styles.StyleSubheader(header))

		// Width of the largest "N." index so dots align in multi-digit plans.
		idxWidth := len(fmt.Sprintf("%d", len(entries)))
		for i, e := range entries {
			idx := fmt.Sprintf("%*d.", idxWidth, i+1)
			body := e.body
			if e.stylize != nil {
				body = e.stylize(body)
			}
			_, _ = fmt.Fprintf(w, "  %s %s  %s\n",
				styles.StyleMuted(idx),
				e.icon,
				body,
			)
		}
	}

	if len(plan.Notes) > 0 {
		if len(entries) > 0 {
			_, _ = fmt.Fprintln(w, "")
		}
		_, _ = fmt.Fprintln(w, styles.StyleSubheader("Notes:"))
		bullet := styles.StyleInfo("•")
		for _, n := range plan.Notes {
			_, _ = fmt.Fprintf(w, "  %s %s\n", bullet, n)
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
	SkipHooks bool
	// Contributors is required for non-empty plans; used to build the
	// pending-clear slice after all apply steps succeed.
	Contributors []Contributor
}

// ExecuteDeps carries all injectable dependencies for executeTogglePlan.
// Production callers assign real implementations; tests inject stubs.
type ExecuteDeps struct {
	Cmd       *cobra.Command
	Flags     *cmdctx.RootFlags
	BaseDir   string
	StatePath string
	Cfg       *config.DweConfig
	CmdReg    *registry.Registry

	// Seams — signatures match real callees so no adapter is needed at the call site.
	RunDeploy  func(ctx context.Context, cmd *cobra.Command, flags *cmdctx.RootFlags, opts deploy.Opts) error
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
			stepErr = deps.RunDeploy(ctx, deps.Cmd, deps.Flags, deploy.Opts{
				Services:             step.Services,
				NonInteractive:       true,
				SuppressPendingClear: true,
			})
		case journal.PendingRestart:
			configPath := filepath.Join(deps.BaseDir, "workspace.yml")
			if deps.Flags != nil {
				configPath = deps.Flags.ConfigPath
			}
			var errOut io.Writer
			if deps.Cmd != nil {
				errOut = deps.Cmd.ErrOrStderr()
			}
			rctx := lifecycle.RunContext{
				Ctx:              ctx,
				ConfigPath:       configPath,
				Yes:              true,
				ErrOut:           errOut,
				SkipClearPending: true,
			}
			if deps.Flags != nil {
				rctx.Translator = deps.Flags.I18n
				rctx.Locale = deps.Flags.Locale
			}
			stepErr = deps.RunRestart(rctx)
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
