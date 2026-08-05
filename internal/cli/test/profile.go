package test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/project"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/workflow/deploy"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// testCostProfileJSON describes what running one scenario would cost and how
// far its isolation reaches, as facts only — there is deliberately no
// cheap/expensive verdict field. The decision rule ("may an agent run this
// unattended?") lives in the dwe agent skill so it can change without a
// binary release.
//
// Two honest limits, stated here rather than discovered later:
//
//   - it reports whether there IS an image build, never what the build costs.
//     The dominant factor — whether the Docker layer cache is warm, seconds
//     versus many minutes — has no static source and is not modelled;
//   - isolation_findings carries only the shared-resource hazards (named /
//     external volumes and networks). The blocking kinds (container_name,
//     raw_host_port) are omitted on purpose: they abort the scenario before
//     deploy anyway, so they are not part of an "is this safe to run
//     unattended" decision.
type testCostProfileJSON struct {
	// EnabledServices counts the dwe services enabled AFTER this scenario's
	// env.services overlay — the number that actually differs between two
	// scenarios of the same project.
	EnabledServices int `json:"enabled_services"`
	// BuildServices are the compose services declaring build:, sorted.
	BuildServices []string `json:"build_services"`
	// ExternalImages are the distinct images of compose services that do not
	// build locally, sorted — what a cold run would have to pull.
	ExternalImages []string `json:"external_images"`
	// MaxStartPeriodSeconds is the largest healthcheck start_period across the
	// enabled chain (max, not sum: `up --wait` waits in parallel).
	MaxStartPeriodSeconds float64 `json:"max_start_period_seconds"`
	// SharedVolumes counts docker.yml volumes declared `shared: true`. These
	// resolve to their verbatim names and are written into by a test run.
	SharedVolumes int `json:"shared_volumes"`
	// IsolationFindings are the non-blocking compose isolation hazards —
	// resources shared with the working environment. Full messages come from
	// `dwe validate tests`.
	IsolationFindings []testIsolationFindingJSON `json:"isolation_findings"`
	// HostSteps counts the steps this scenario would run that execute on the
	// HOST, outside the container sandbox the disposable copy provides: its own
	// steps plus the deploy pipeline it triggers (project-wide + the enabled
	// services'). Host side effects (absolute paths, ~, binds outside the
	// project) are not sandboxed by the copy.
	//
	// `type: shell` is only one of the channels — see countHostSteps for the
	// full list and for the one place the count is deliberately conservative.
	HostSteps int `json:"host_steps"`
}

// testIsolationFindingJSON is one shared-resource hazard in the profile.
type testIsolationFindingJSON struct {
	Kind     string `json:"kind"`
	Resource string `json:"resource"`
}

// costProfiler holds the once-per-command project state every per-scenario
// profile derives from.
type costProfiler struct {
	baseDir       string
	cfg           *config.DweConfig
	reg           *usercommands.Registry // nil when the registry does not load
	sharedVolumes int
	projectHost   int            // host steps in the project-wide deploy pipeline
	serviceHost   map[string]int // host steps per service deploy pipeline
}

// newCostProfiler assembles the profiler, or returns nil when the project
// state it needs is unavailable.
//
// Every failure degrades to nil rather than to an error: `dwe test list` takes
// no locks, touches no Docker and requires no loadable config — it is the
// command you reach for while the config is mid-edit — so an unavailable
// profile is silently omitted and the listing still works.
func newCostProfiler(baseDir, configPath string) *costProfiler {
	if configPath == "" {
		if baseDir == "" {
			return nil
		}
		configPath = filepath.Join(baseDir, project.ConfigFilename)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil
	}

	dockerCfg, err := config.LoadDockerConfigOrEmpty(baseDir, cfg)
	if err != nil {
		return nil
	}
	shared := 0
	for _, vol := range dockerCfg.Resources.Volumes {
		if vol.Shared {
			shared++
		}
	}

	projectDeploy, err := config.LoadProjectDeployConfig(filepath.Join(baseDir, "workspace", "deploy.yml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// An absent file resolves to the built-in default pipeline — the same one
	// a real deploy would run, so the count stays truthful either way.
	effective, _ := deploy.EnsureDeployConfig(projectDeploy)

	serviceDeploys, err := config.LoadServiceDeployConfigs(baseDir, cfg.Services)
	if err != nil {
		return nil
	}

	// Best-effort: the registry resolves a `type: command` step to the command
	// that would run, which is what decides host vs container. A registry that
	// does not load leaves it nil and countHostSteps falls back to the
	// conservative reading — `list` still works on a broken commands tree.
	reg, _ := usercommands.LoadRegistryFromConfigPath(configPath)

	p := &costProfiler{
		baseDir:       baseDir,
		cfg:           cfg,
		reg:           reg,
		sharedVolumes: shared,
	}
	p.projectHost = p.countHostStepsInPhases(effective.Phases)
	p.serviceHost = make(map[string]int, len(serviceDeploys))
	for name, sd := range serviceDeploys {
		p.serviceHost[name] = p.countHostStepsInPhases(sd.Phases)
	}
	return p
}

// profile computes one scenario's cost profile. A nil profiler yields nil, so
// the caller never has to branch on availability.
func (p *costProfiler) profile(scn *envtest.Scenario) *testCostProfileJSON {
	if p == nil || scn == nil {
		return nil
	}

	services := p.scenarioServices(scn)

	// A shallow view over the loaded config: everything except the enabled
	// state is shared, so the compose chain resolves exactly as it would in
	// the copy this scenario deploys.
	view := *p.cfg
	view.Services = services

	costs := config.ScanComposeCost(&view, p.baseDir)

	findings := make([]testIsolationFindingJSON, 0)
	for _, f := range config.ScanComposeIsolation(&view, p.baseDir) {
		if f.Blocking {
			continue
		}
		findings = append(findings, testIsolationFindingJSON{Kind: string(f.Kind), Resource: f.Resource})
	}

	enabled := 0
	host := p.countHostSteps(scn.Steps) + p.projectHost
	for name, svc := range services {
		if !svc.Enabled {
			continue
		}
		enabled++
		host += p.serviceHost[name]
	}

	return &testCostProfileJSON{
		EnabledServices:       enabled,
		BuildServices:         costs.BuildServices,
		ExternalImages:        costs.ExternalImages,
		MaxStartPeriodSeconds: costs.MaxStartPeriod.Seconds(),
		SharedVolumes:         p.sharedVolumes,
		IsolationFindings:     findings,
		HostSteps:             host,
	}
}

// scenarioServices returns the project's services with this scenario's
// env.services overlay applied. A `required: true` service stays enabled even
// when the scenario disables it — the same precedence the config loader
// applies to the generated local.yml the runner writes.
func (p *costProfiler) scenarioServices(scn *envtest.Scenario) map[string]config.ServiceConfig {
	out := make(map[string]config.ServiceConfig, len(p.cfg.Services))
	maps.Copy(out, p.cfg.Services)

	for _, name := range scn.Env.Services.Enable {
		if svc, ok := out[name]; ok {
			svc.Enabled = true
			out[name] = svc
		}
	}
	for _, name := range scn.Env.Services.Disable {
		svc, ok := out[name]
		if !ok || svc.Required {
			continue
		}
		svc.Enabled = false
		out[name] = svc
	}

	return out
}

func (p *costProfiler) countHostStepsInPhases(phases []config.DeployPhase) int {
	n := 0
	for _, phase := range phases {
		// A phase-level shell `when:` runs sh -c in the project root before any
		// of the phase's steps do, so it is host execution in its own right.
		// Counting it as one keeps the > 0 gate honest; the exact number only
		// ever has to distinguish "none" from "some".
		if isHostCondition(phase.When) {
			n++
		}
		n += p.countHostSteps(phase.Steps)
	}
	return n
}

// countHostSteps counts the steps that execute something on the HOST, descending
// into parallel groups (nested groups are rejected at plan time, but the
// recursion costs nothing). A step is counted at most once no matter how many
// host channels it uses.
//
// What the field is after is PROJECT-AUTHORED code running on the host, and
// `type: shell` is only the most obvious of its channels — a counter that saw
// only that one would report 0 for a pipeline running arbitrary host commands
// through any of the others:
//
//   - type: shell — sh -c in the project root;
//   - type: builtin, cmd: shell — the shell builtin, same sh -c;
//   - type: command — host unless the referenced command runs in a container
//     (service_exec / service_run). Resolved through the registry; an
//     unresolvable reference counts as host, which is the safe direction for a
//     field that gates an unattended run;
//   - type: dwe — only when the subcommand re-enters project-authored code
//     (see dweSubcommandRunsProjectCode);
//   - a shell `when:` or a host-executing `check:` on any step, including the
//     `check: auto` sentinel (it derives to the shell builtin).
//
// dwe's own subcommands (`docker up --wait`, `info`, `render …` — the whole
// built-in default pipeline) are deliberately NOT counted: they are dwe
// machinery acting on the disposable copy, which is exactly what the scenario
// exists to run. Counting them would leave every project at host_steps > 0 and
// the gate permanently shut, which reports nothing.
func (p *costProfiler) countHostSteps(steps []config.DeployStep) int {
	n := 0
	for _, step := range steps {
		if step.Parallel != nil {
			if isHostCondition(step.When) {
				n++
			}
			n += p.countHostSteps(step.Parallel.Steps)
			continue
		}
		if p.stepRunsOnHost(step) {
			n++
		}
	}
	return n
}

func (p *costProfiler) stepRunsOnHost(step config.DeployStep) bool {
	if isHostCondition(step.When) {
		return true
	}
	if step.Check != nil {
		// The `check: auto` sentinel resolves to {type: builtin, cmd: shell}.
		if config.IsAutoCheck(step.Check) || p.actionRunsOnHost(step.Check.Type, step.Check.Cmd) {
			return true
		}
	}
	return p.actionRunsOnHost(step.Type, step.Cmd)
}

func (p *costProfiler) actionRunsOnHost(actionType, cmd string) bool {
	switch actionType {
	case "shell":
		return true
	case "dwe":
		return dweSubcommandRunsProjectCode(cmd)
	case "builtin":
		return cmd == "shell"
	case "command":
		return p.commandRunsOnHost(cmd, map[string]bool{})
	default:
		return false
	}
}

// dweSubcommandsRunningProjectCode are the dwe subcommands that execute
// project-authored code rather than dwe's own machinery: the user-command
// dispatcher and every entry point that drives a project pipeline. The check is
// on the first token only — deliberately coarse, since a read-only sub-verb
// (`deploy plan`) counted as host merely closes the gate, while missing a
// mutating one would open it wrongly.
var dweSubcommandsRunningProjectCode = map[string]bool{
	"cmd":     true,
	"deploy":  true,
	"reset":   true,
	"restart": true,
	"run":     true,
	"test":    true,
}

func dweSubcommandRunsProjectCode(cmd string) bool {
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return false
	}
	return dweSubcommandsRunningProjectCode[fields[0]]
}

// commandRunsOnHost reports whether the user command id would execute
// project-authored code on the host. service_exec / service_run run inside a
// container; dwe and builtin are classified by the same rules their pipeline
// step counterparts get; a workflow is host-executing when any sub-step it
// references is; everything else (shell, script, …) is a host process. An
// unresolvable id (registry absent, unknown command, a cmd: still carrying an
// unrendered ${...} reference) counts as host — the profile gates an unattended
// run, so the unknown case must close the gate, not open it.
//
// seen breaks a reference cycle; the loader rejects one, but this must not hang
// on a config it never validated.
func (p *costProfiler) commandRunsOnHost(id string, seen map[string]bool) bool {
	if p.reg == nil {
		return true
	}
	if seen[id] {
		// Already counted on the way in; a cycle adds no new channel.
		return false
	}
	seen[id] = true

	def, err := p.reg.Get(id)
	if err != nil || def == nil {
		return true
	}
	switch def.Type {
	case model.CommandTypeServiceExec, model.CommandTypeServiceRun:
		return false
	case model.CommandTypeDwe:
		// Same rule as a `type: dwe` step — dwe's own subcommands are not
		// project code.
		return dweSubcommandRunsProjectCode(def.Cmd)
	case model.CommandTypeBuiltin:
		return def.Cmd == "shell"
	case model.CommandTypeWorkflow:
		return slices.ContainsFunc(flattenWorkflowSteps(def.Steps), func(s model.WorkflowStep) bool {
			return s.Command != "" && p.commandRunsOnHost(s.Command, seen)
		})
	default:
		return true
	}
}

// flattenWorkflowSteps returns the workflow's leaf sub-steps, unwrapping
// parallel containers (the schema allows exactly one level of nesting).
func flattenWorkflowSteps(steps []model.WorkflowStep) []model.WorkflowStep {
	out := make([]model.WorkflowStep, 0, len(steps))
	for _, s := range steps {
		if s.Parallel != nil {
			out = append(out, s.Parallel.Steps...)
			continue
		}
		out = append(out, s)
	}
	return out
}

// isHostCondition reports whether c is a shell condition — sh -c in the project
// root. builtin predicates only stat the filesystem and template conditions are
// evaluated in-process, so neither runs anything.
func isHostCondition(c *condition.Condition) bool {
	return c != nil && c.Type == condition.TypeShell
}
