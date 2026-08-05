package test

import (
	"errors"
	"maps"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/project/project"
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
	// ShellSteps counts `type: shell` steps this scenario would run: its own
	// steps plus the deploy pipeline it triggers (project-wide + the enabled
	// services'). Host side effects of a shell step are not sandboxed.
	ShellSteps int `json:"shell_steps"`
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
	sharedVolumes int
	projectShell  int            // shell steps in the project-wide deploy pipeline
	serviceShell  map[string]int // shell steps per service deploy pipeline
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
	serviceShell := make(map[string]int, len(serviceDeploys))
	for name, sd := range serviceDeploys {
		serviceShell[name] = countShellStepsInPhases(sd.Phases)
	}

	return &costProfiler{
		baseDir:       baseDir,
		cfg:           cfg,
		sharedVolumes: shared,
		projectShell:  countShellStepsInPhases(effective.Phases),
		serviceShell:  serviceShell,
	}
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
	shell := countShellSteps(scn.Steps) + p.projectShell
	for name, svc := range services {
		if !svc.Enabled {
			continue
		}
		enabled++
		shell += p.serviceShell[name]
	}

	return &testCostProfileJSON{
		EnabledServices:       enabled,
		BuildServices:         costs.BuildServices,
		ExternalImages:        costs.ExternalImages,
		MaxStartPeriodSeconds: costs.MaxStartPeriod.Seconds(),
		SharedVolumes:         p.sharedVolumes,
		IsolationFindings:     findings,
		ShellSteps:            shell,
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

func countShellStepsInPhases(phases []config.DeployPhase) int {
	n := 0
	for _, phase := range phases {
		n += countShellSteps(phase.Steps)
	}
	return n
}

// countShellSteps counts `type: shell` steps, descending into parallel groups
// (nested groups are rejected at plan time, but the recursion costs nothing).
func countShellSteps(steps []config.DeployStep) int {
	n := 0
	for _, step := range steps {
		if step.Parallel != nil {
			n += countShellSteps(step.Parallel.Steps)
			continue
		}
		if step.Type == "shell" {
			n++
		}
	}
	return n
}
