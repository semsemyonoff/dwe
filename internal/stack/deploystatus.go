package stack

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
	"devbox-cli/internal/deploy/journal"
	"devbox-cli/internal/shared/render"
	"devbox-cli/internal/ui"
)

// RenderDeployStatus returns the Deploy Status section title + table as a
// single string. Returns empty string when in.State is nil or no tracked
// service yields a row.
func RenderDeployStatus(in StatusInput) string {
	if in.State == nil {
		return ""
	}
	view := BuildDeployStatusView(in.State, in.Cfg, in.SvcDeploys, in.Tracked)
	if len(view.Rows) == 0 {
		return ""
	}

	uiRows := make([]ui.DeployStatusRow, len(view.Rows))
	for i, row := range view.Rows {
		uiRows[i] = ui.DeployStatusRow{
			Service:         row.Service,
			Status:          string(row.Status),
			ConfigDelta:     string(row.ConfigDelta),
			PrevHashShort:   row.PrevHashShort,
			CurrHashShort:   row.CurrHashShort,
			LastFailedPhase: row.LastFailedPhase,
			LastFailedStep:  row.LastFailedStep,
		}
	}
	var b strings.Builder
	b.WriteString(ui.RenderSectionTitle("Deploy Status"))
	b.WriteByte('\n')
	b.WriteString(ui.RenderDeployStatus(uiRows))
	b.WriteByte('\n')
	return b.String()
}

// BuildDeployStatusView assembles a view model joining current config hashes
// against persisted state for each tracked service.
func BuildDeployStatusView(state *journal.ProjectState, cfg *config.DevboxConfig, svcDeploys map[string]*config.ServiceDeployConfig, tracked []string) *statusview.DeployStatusView {
	view := &statusview.DeployStatusView{
		ProjectStatus:     state.Project.Status,
		ProjectDeployedAt: state.Project.DeployedAt,
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

	rw := render.NewWriter(w)
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
	_, _ = fmt.Fprintf(rw.Writer(), "\n%s\n", ui.RenderSectionTitle("Phases"))

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
