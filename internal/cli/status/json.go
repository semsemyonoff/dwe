package status

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/stack"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy/journal"

	"github.com/spf13/cobra"
)

// ── Top-level composite DTO ──────────────────────────────────────────────────

type statusJSON struct {
	Project  *projectJSON  `json:"project,omitempty"`
	Apps     []serviceJSON `json:"apps,omitempty"`
	Tools    []serviceJSON `json:"tools,omitempty"`
	Infra    []serviceJSON `json:"infra,omitempty"`
	Daemons  []daemonJSON  `json:"daemons,omitempty"`
	Deploy   *deployJSON   `json:"deploy,omitempty"`
	Topology *topologyJSON `json:"topology,omitempty"`
	Git      []gitRowJSON  `json:"git,omitempty"`
}

// ── Project ──────────────────────────────────────────────────────────────────

type projectJSON struct {
	Name   string `json:"name"`
	Health string `json:"health"` // "running"|"partial"|"stopped"
}

// ── Services (apps / tools / infra) ─────────────────────────────────────────

type serviceJSON struct {
	Name      string                            `json:"name"`
	Container string                            `json:"container_name"`
	Mandatory bool                              `json:"mandatory"`
	Enabled   bool                              `json:"enabled"`
	Running   bool                              `json:"running"`
	Status    string                            `json:"status"` // "running"|"stopped"|"disabled"
	Ports     map[string]config.ServicePortSpec `json:"ports,omitempty"`
	Hosts     map[string]string                 `json:"hosts,omitempty"`
}

// ── Daemons ──────────────────────────────────────────────────────────────────

type daemonJSON struct {
	ID            string `json:"id"`
	Container     string `json:"container"`
	Params        string `json:"params,omitempty"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	StartedAt     string `json:"started_at,omitempty"` // RFC3339
}

// ── Deploy status table ──────────────────────────────────────────────────────

type deployJSON struct {
	ProjectStatus     string              `json:"project_status"`
	ProjectDeployedAt string              `json:"project_deployed_at,omitempty"` // RFC3339
	Services          []deployServiceJSON `json:"services"`
}

type deployServiceJSON struct {
	Service         string `json:"service"`
	Status          string `json:"status"`
	DeployedAt      string `json:"deployed_at,omitempty"` // RFC3339
	ConfigDelta     string `json:"config_delta"`
	PrevHash        string `json:"prev_hash,omitempty"`
	CurrHash        string `json:"curr_hash"`
	LastFailedPhase string `json:"last_failed_phase,omitempty"`
	LastFailedStep  string `json:"last_failed_step,omitempty"`
}

// ── Deploy per-service detail (status deploy <name>) ─────────────────────────

type deployDetailJSON struct {
	Service    string                     `json:"service"`
	Status     string                     `json:"status"`
	ConfigHash string                     `json:"config_hash,omitempty"`
	DeployedAt string                     `json:"deployed_at,omitempty"` // RFC3339
	Phases     map[string]phaseDetailJSON `json:"phases,omitempty"`
}

type phaseDetailJSON struct {
	Status string                    `json:"status"`
	Steps  map[string]stepDetailJSON `json:"steps,omitempty"`
}

type stepDetailJSON struct {
	Status     string `json:"status"`
	FinishedAt string `json:"finished_at,omitempty"` // RFC3339
	ActionHash string `json:"action_hash,omitempty"`
	DurationMs int64  `json:"duration_ms"`
}

// ── Topology ─────────────────────────────────────────────────────────────────

type topologyJSON struct {
	Nodes []topologyNodeJSON `json:"nodes"`
}

type topologyNodeJSON struct {
	Name         string   `json:"name"`
	Status       string   `json:"status"` // "running"|"stopped"|"disabled"|"unknown"
	Dependencies []string `json:"dependencies,omitempty"`
}

// ── Git workspace ─────────────────────────────────────────────────────────────

type gitRowJSON struct {
	Service     string `json:"service"`
	Dir         string `json:"dir"`
	Branch      string `json:"branch,omitempty"`
	SHA         string `json:"sha,omitempty"`
	Dirty       bool   `json:"dirty"`
	AheadBehind string `json:"ahead_behind,omitempty"`
}

// ── Per-subcommand wrappers (key always present) ─────────────────────────────

type appsStatusJSON struct {
	Apps []serviceJSON `json:"apps"`
}

type toolsStatusJSON struct {
	Tools []serviceJSON `json:"tools"`
}

type infraStatusJSON struct {
	Infra []serviceJSON `json:"infra"`
}

type daemonsStatusJSON struct {
	Daemons []daemonJSON `json:"daemons"`
}

type deployTableStatusJSON struct {
	Deploy *deployJSON `json:"deploy"`
}

type deployDetailStatusJSON struct {
	Deploy *deployDetailJSON `json:"deploy"`
}

type topologyStatusJSON struct {
	Topology *topologyJSON `json:"topology"`
}

type gitStatusJSON struct {
	Git []gitRowJSON `json:"git"`
}

// ── Builders ─────────────────────────────────────────────────────────────────

func buildProjectJSON(sc *statusContext) *projectJSON {
	if sc.Cfg == nil {
		return nil
	}
	in := sc.statusInput()
	var h stack.Health
	if stack.HasRuntimeStatuses(in.TopoStatus) {
		h = stack.AggregateHealthFromTopo(in.TopoStatus)
	} else {
		rows := stack.CollectServiceRows(in, nil)
		h = stack.AggregateHealth(rows)
	}
	return &projectJSON{
		Name:   sc.Cfg.Project.Name,
		Health: healthStr(h),
	}
}

func buildServicesJSON(sc *statusContext, svcType config.ServiceType) []serviceJSON {
	in := sc.statusInput()
	rows := stack.CollectServiceRows(in, &svcType)
	if len(rows) == 0 {
		return nil
	}
	result := make([]serviceJSON, len(rows))
	for i, row := range rows {
		j := serviceJSON{
			Name:      row.Name,
			Container: row.Container,
			Mandatory: row.Mandatory,
			Enabled:   row.Enabled,
			Running:   row.Running,
			Status:    serviceStatusStr(row),
		}
		if svc, ok := sc.Cfg.Services[row.Name]; ok && len(svc.Ports) > 0 {
			j.Ports = svc.Ports
		}
		if len(row.Hosts) > 0 {
			j.Hosts = row.Hosts
		}
		result[i] = j
	}
	return result
}

func buildDaemonsJSON(ctx context.Context, sc *statusContext) []daemonJSON {
	rows, _ := stack.CollectDaemons(ctx, sc.Cfg, sc.normalisedDockerCfg(), sc.ProjectRoot)
	if len(rows) == 0 {
		return nil
	}
	result := make([]daemonJSON, len(rows))
	for i, row := range rows {
		d := daemonJSON{
			ID:            row.ID,
			Container:     row.Container,
			Params:        row.Params,
			UptimeSeconds: int64(row.Uptime / time.Second),
		}
		if !row.StartedAt.IsZero() {
			d.StartedAt = row.StartedAt.UTC().Format(time.RFC3339)
		}
		result[i] = d
	}
	return result
}

func buildDeployTableJSON(sc *statusContext) *deployJSON {
	if sc.State == nil {
		return nil
	}
	view := stack.BuildDeployStatusView(sc.State, sc.Cfg, sc.SvcDeploys, sc.Tracked)
	if len(view.Rows) == 0 {
		return nil
	}
	projectStatus := string(view.ProjectStatus)
	if projectStatus == "" {
		projectStatus = string(journal.StatusNotDeployed)
	}
	d := &deployJSON{
		ProjectStatus: projectStatus,
		Services:      make([]deployServiceJSON, len(view.Rows)),
	}
	if !view.ProjectDeployedAt.IsZero() {
		d.ProjectDeployedAt = view.ProjectDeployedAt.UTC().Format(time.RFC3339)
	}
	for i, row := range view.Rows {
		sj := deployServiceJSON{
			Service:         row.Service,
			Status:          string(row.Status),
			ConfigDelta:     string(row.ConfigDelta),
			PrevHash:        row.PrevHashShort,
			CurrHash:        row.CurrHashShort,
			LastFailedPhase: row.LastFailedPhase,
			LastFailedStep:  row.LastFailedStep,
		}
		if !row.DeployedAt.IsZero() {
			sj.DeployedAt = row.DeployedAt.UTC().Format(time.RFC3339)
		}
		d.Services[i] = sj
	}
	return d
}

func buildDeployDetailJSON(sc *statusContext, serviceName string) (*deployDetailJSON, error) {
	if sc.State == nil {
		return &deployDetailJSON{Service: serviceName, Status: "unknown"}, nil
	}
	if !slices.Contains(sc.Tracked, serviceName) {
		return nil, cmdctx.Err("service_not_tracked",
			fmt.Sprintf("service %q is not tracked (not deployed)", serviceName)).
			WithDetail("service", serviceName)
	}
	svcState, ok := sc.State.Services[serviceName]
	if !ok {
		return &deployDetailJSON{Service: serviceName, Status: string(journal.StatusNotDeployed)}, nil
	}
	detail := &deployDetailJSON{
		Service:    serviceName,
		Status:     string(svcState.Status),
		ConfigHash: svcState.ConfigHash,
	}
	if !svcState.DeployedAt.IsZero() {
		detail.DeployedAt = svcState.DeployedAt.UTC().Format(time.RFC3339)
	}
	if len(svcState.Phases) > 0 {
		detail.Phases = make(map[string]phaseDetailJSON, len(svcState.Phases))
		for phaseName, phase := range svcState.Phases {
			pd := phaseDetailJSON{Status: string(phase.Status)}
			if len(phase.Steps) > 0 {
				pd.Steps = make(map[string]stepDetailJSON, len(phase.Steps))
				for stepName, step := range phase.Steps {
					sd := stepDetailJSON{
						Status:     string(step.Status),
						ActionHash: step.ActionHash,
						DurationMs: step.DurationMs,
					}
					if !step.FinishedAt.IsZero() {
						sd.FinishedAt = step.FinishedAt.UTC().Format(time.RFC3339)
					}
					pd.Steps[stepName] = sd
				}
			}
			detail.Phases[phaseName] = pd
		}
	}
	return detail, nil
}

func buildTopologyJSON(sc *statusContext) *topologyJSON {
	if len(sc.Topo) == 0 {
		return nil
	}
	names := make([]string, 0, len(sc.Topo))
	for name := range sc.Topo {
		names = append(names, name)
	}
	sort.Strings(names)

	nodes := make([]topologyNodeJSON, len(names))
	for i, name := range names {
		deps := sc.Topo[name]
		statusStr := "unknown"
		if nodeStatus, ok := sc.TopoStatus[name]; ok {
			switch nodeStatus {
			case render.NodeRunning:
				statusStr = "running"
			case render.NodeStopped:
				statusStr = "stopped"
			case render.NodeDisabled:
				statusStr = "disabled"
			}
		}
		node := topologyNodeJSON{
			Name:   name,
			Status: statusStr,
		}
		if len(deps) > 0 {
			sorted := make([]string, len(deps))
			copy(sorted, deps)
			sort.Strings(sorted)
			node.Dependencies = sorted
		}
		nodes[i] = node
	}
	return &topologyJSON{Nodes: nodes}
}

func buildGitJSON(ctx context.Context, sc *statusContext) []gitRowJSON {
	rows := stack.CollectGitWorkspace(ctx, sc.Cfg, sc.ProjectRoot)
	if len(rows) == 0 {
		return nil
	}
	result := make([]gitRowJSON, len(rows))
	for i, row := range rows {
		result[i] = gitRowJSON{
			Service:     row.Service,
			Dir:         row.Dir,
			Branch:      row.Branch,
			SHA:         row.SHA,
			Dirty:       row.Dirty,
			AheadBehind: row.AheadBehind,
		}
	}
	return result
}

// ── JSON renderers ────────────────────────────────────────────────────────────

// renderStatusJSON emits the full composite status as a JSON object.
func renderStatusJSON(cmd *cobra.Command, sc *statusContext, no *noSectionFlags, flags *cmdctx.RootFlags) error {
	ctx := cmd.Context()
	data := statusJSON{
		Project: buildProjectJSON(sc),
	}
	if !no.noApps {
		data.Apps = buildServicesJSON(sc, config.ServiceTypeApp)
	}
	if !no.noTools {
		data.Tools = buildServicesJSON(sc, config.ServiceTypeTool)
	}
	if !no.noInfra {
		data.Infra = buildServicesJSON(sc, config.ServiceTypeInfra)
	}
	if !no.noDaemons {
		data.Daemons = buildDaemonsJSON(ctx, sc)
	}
	if !no.noDeploy {
		data.Deploy = buildDeployTableJSON(sc)
	}
	if !no.noTopology {
		data.Topology = buildTopologyJSON(sc)
	}
	if !no.noGit {
		data.Git = buildGitJSON(ctx, sc)
	}
	return cmdctx.WriteData(flags, cmd, data, func(statusJSON) string { return "" })
}

// renderStatusSectionJSON emits a single status section as a JSON object with
// the section key always present (e.g. `{"apps": [...]}`).
func renderStatusSectionJSON(cmd *cobra.Command, sc *statusContext, s section, flags *cmdctx.RootFlags) error {
	ctx := cmd.Context()
	switch s {
	case sectionApps:
		rows := buildServicesJSON(sc, config.ServiceTypeApp)
		if rows == nil {
			rows = []serviceJSON{}
		}
		return cmdctx.WriteData(flags, cmd, appsStatusJSON{Apps: rows}, func(appsStatusJSON) string { return "" })
	case sectionTools:
		rows := buildServicesJSON(sc, config.ServiceTypeTool)
		if rows == nil {
			rows = []serviceJSON{}
		}
		return cmdctx.WriteData(flags, cmd, toolsStatusJSON{Tools: rows}, func(toolsStatusJSON) string { return "" })
	case sectionInfra:
		rows := buildServicesJSON(sc, config.ServiceTypeInfra)
		if rows == nil {
			rows = []serviceJSON{}
		}
		return cmdctx.WriteData(flags, cmd, infraStatusJSON{Infra: rows}, func(infraStatusJSON) string { return "" })
	case sectionDaemons:
		daemons := buildDaemonsJSON(ctx, sc)
		if daemons == nil {
			daemons = []daemonJSON{}
		}
		return cmdctx.WriteData(flags, cmd, daemonsStatusJSON{Daemons: daemons}, func(daemonsStatusJSON) string { return "" })
	case sectionDeploy:
		return cmdctx.WriteData(flags, cmd,
			deployTableStatusJSON{Deploy: buildDeployTableJSON(sc)},
			func(deployTableStatusJSON) string { return "" })
	case sectionTopology:
		return cmdctx.WriteData(flags, cmd,
			topologyStatusJSON{Topology: buildTopologyJSON(sc)},
			func(topologyStatusJSON) string { return "" })
	case sectionGit:
		rows := buildGitJSON(ctx, sc)
		if rows == nil {
			rows = []gitRowJSON{}
		}
		return cmdctx.WriteData(flags, cmd, gitStatusJSON{Git: rows}, func(gitStatusJSON) string { return "" })
	}
	return nil
}

// renderDeployDetailJSON emits the per-service deploy detail as JSON.
func renderDeployDetailJSON(cmd *cobra.Command, sc *statusContext, serviceName string, flags *cmdctx.RootFlags) error {
	detail, err := buildDeployDetailJSON(sc, serviceName)
	if err != nil {
		return err
	}
	return cmdctx.WriteData(flags, cmd,
		deployDetailStatusJSON{Deploy: detail},
		func(deployDetailStatusJSON) string { return "" })
}

// ── Local helpers ─────────────────────────────────────────────────────────────

func healthStr(h stack.Health) string {
	switch h {
	case stack.HealthRunning:
		return "running"
	case stack.HealthPartial:
		return "partial"
	default:
		return "stopped"
	}
}

func serviceStatusStr(row render.ServiceTableRow) string {
	switch {
	case row.Running:
		return "running"
	case !row.Mandatory && !row.Enabled:
		return "disabled"
	default:
		return "stopped"
	}
}
