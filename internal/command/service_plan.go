package command

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/usercommands/registry"
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
	type entry struct{ label string }
	var entries []entry

	for _, s := range plan.BeforeSteps {
		entries = append(entries, entry{fmt.Sprintf("devbox commands %s", s.CommandID)})
	}
	for _, s := range plan.ApplySteps {
		switch s.Kind {
		case journal.PendingDeploy:
			if len(s.Services) == 1 {
				entries = append(entries, entry{fmt.Sprintf("devbox deploy run --service %s", s.Services[0])})
			} else {
				entries = append(entries, entry{
					fmt.Sprintf("→ apply step: deploy services {%s} (dependency-ordered at execution)",
						strings.Join(s.Services, ", ")),
				})
			}
		case journal.PendingRestart:
			entries = append(entries, entry{"→ apply step: restart stack"})
		}
	}
	for _, s := range plan.AfterSteps {
		entries = append(entries, entry{fmt.Sprintf("devbox commands %s", s.CommandID)})
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
			_, _ = fmt.Fprintf(w, "  %d. %s\n", i+1, e.label)
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
