package stack

import (
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"
	sharedrender "github.com/semsemyonoff/dwe/internal/shared/render"
)

// CollectDeployStatus builds the Deploy Status section's rows from in.State
// joined against current config hashes. Returns nil when in.State is nil or
// no tracked service yields a row. Pure — no Docker probe involved.
func CollectDeployStatus(in StatusInput) []render.DeployStatusRow {
	if in.State == nil {
		return nil
	}
	view := BuildDeployStatusView(in.State, in.Cfg, in.SvcDeploys, in.Tracked)
	if len(view.Rows) == 0 {
		return nil
	}

	uiRows := make([]render.DeployStatusRow, len(view.Rows))
	for i, row := range view.Rows {
		uiRows[i] = render.DeployStatusRow{
			Service:         row.Service,
			Status:          string(row.Status),
			ConfigDelta:     string(row.ConfigDelta),
			PrevHashShort:   row.PrevHashShort,
			CurrHashShort:   row.CurrHashShort,
			LastFailedPhase: row.LastFailedPhase,
			LastFailedStep:  row.LastFailedStep,
		}
	}
	return uiRows
}

// RenderDeployStatusRows renders a previously-collected deploy-status
// snapshot as the section title + table, at the given width (0 = unbounded).
// The pure counterpart to CollectDeployStatus, used by the status TUI which
// already knows its panel width.
func RenderDeployStatusRows(rows []render.DeployStatusRow, width int) string {
	if len(rows) == 0 {
		return ""
	}
	return wrapSection("Deploy Status", render.DeployStatusAt(rows, width))
}

// DeployStatus returns the Deploy Status section title + table as a
// single string. Returns empty string when in.State is nil or no tracked
// service yields a row.
func DeployStatus(in StatusInput) string {
	rows := CollectDeployStatus(in)
	if len(rows) == 0 {
		return ""
	}
	if in.Width > 0 {
		return RenderDeployStatusRows(rows, in.Width)
	}
	return wrapSection("Deploy Status", render.DeployStatus(rows))
}

// BuildDeployStatusView assembles a view model joining current config hashes
// against persisted state for each tracked service.
func BuildDeployStatusView(state *journal.ProjectState, cfg *config.DweConfig, svcDeploys map[string]*config.ServiceDeployConfig, tracked []string) *statusview.DeployStatusView {
	view := &statusview.DeployStatusView{}
	if state.Project != nil {
		view.ProjectStatus = state.Project.Status
		view.ProjectDeployedAt = state.Project.DeployedAt
	}

	for _, serviceName := range tracked {
		svcCfg, ok := cfg.Services[serviceName]
		if !ok {
			continue
		}

		svcDeploy := svcDeploys[serviceName]
		currHash := journal.ServiceConfigHash(svcCfg, svcDeploy)

		delta := statusview.ConfigDeltaOK
		var prevHashShort string

		svcState, exists := state.Services[serviceName]
		if !exists {
			delta = statusview.ConfigDeltaMissing
		} else {
			prevHashShort = journal.ShortHash(svcState.ConfigHash)
			if svcState.ConfigHash != currHash {
				delta = statusview.ConfigDeltaChanged
			}
		}

		row := statusview.DeployStatusRow{
			Service:       serviceName,
			Status:        journal.StatusNotDeployed,
			ConfigDelta:   delta,
			CurrHashShort: journal.ShortHash(currHash),
			PrevHashShort: prevHashShort,
		}

		if svcState != nil {
			row.Status = svcState.Status
			if svcState.LastRun != nil && svcState.LastRun.Status != journal.StatusOk {
				phase, step := findLatestFailedStep(svcState)
				row.LastFailedPhase = phase
				row.LastFailedStep = step
			}
		}

		view.Rows = append(view.Rows, row)
	}

	return view
}

// findLatestFailedStep returns the (phase, step) pair of the most recently
// failed step in svcState, comparing by FinishedAt.
func findLatestFailedStep(svcState *journal.ServiceState) (phase, step string) {
	var latest time.Time
	for phaseName, p := range svcState.Phases {
		for stepName, s := range p.Steps {
			if s.Status == journal.StatusFailed && s.FinishedAt.After(latest) {
				latest = s.FinishedAt
				phase = phaseName
				step = stepName
			}
		}
	}
	return phase, step
}

// RenderServiceDeployDetail prints the per-phase/step breakdown for a single
// tracked service. It returns an error when the service is not tracked.
func RenderServiceDeployDetail(w io.Writer, state *journal.ProjectState, tracked []string, serviceName string) error {
	if state == nil {
		_, _ = fmt.Fprintf(w, "Deploy state not available\n")
		return nil
	}
	if !slices.Contains(tracked, serviceName) {
		return fmt.Errorf("service %q is not tracked (not deployed)", serviceName)
	}

	rw := sharedrender.NewWriter(w)
	svcState, ok := state.Services[serviceName]
	if !ok {
		_, _ = fmt.Fprintf(rw.Writer(), "Service %q not deployed yet\n", serviceName)
		return nil
	}

	_, _ = fmt.Fprintf(rw.Writer(), "Deploy status for service %q:\n\n", serviceName)
	_, _ = fmt.Fprintf(rw.Writer(), "Overall status: %s\n", svcState.Status)
	_, _ = fmt.Fprintf(rw.Writer(), "Config hash: %s\n", svcState.ConfigHash)
	if svcState.LastRun != nil {
		_, _ = fmt.Fprintf(rw.Writer(), "Last run: %s\n", svcState.LastRun.Status)
	}
	_, _ = fmt.Fprintf(rw.Writer(), "\n%s\n", render.SectionTitle("Phases"))

	phaseNames := make([]string, 0, len(svcState.Phases))
	for phaseName := range svcState.Phases {
		phaseNames = append(phaseNames, phaseName)
	}
	slices.Sort(phaseNames)
	for _, phaseName := range phaseNames {
		phase := svcState.Phases[phaseName]
		_, _ = fmt.Fprintf(rw.Writer(), "  %s: %s\n", phaseName, phase.Status)
		stepNames := make([]string, 0, len(phase.Steps))
		for stepName := range phase.Steps {
			stepNames = append(stepNames, stepName)
		}
		slices.Sort(stepNames)
		for _, stepName := range stepNames {
			step := phase.Steps[stepName]
			_, _ = fmt.Fprintf(rw.Writer(), "    %s: %s (hash=%s, duration=%dms)\n",
				stepName, step.Status, journal.ShortHash(step.ActionHash), step.DurationMs)
		}
	}

	return nil
}
