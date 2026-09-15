// Package tests validates workspace/tests/*.yml integration-test scenario
// files: schema/name normalisation (via envtest.LoadScenario), the wall-clock
// timeout string, env.services references, whole-phase step resolution
// (reusing the exact runtime pipeline resolver), type: command references,
// and compose-isolation hazards, plus host scripts and shell steps that build
// their own compose project name (hostproject.go). Validate-only — never
// registered in preflight.Run.
package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/core/workflow/envtest"
)

// All returns the tests domain's validators.
func All() []validate.Validator {
	return []validate.Validator{&scenariosValidator{}, &hostProjectNameValidator{}}
}

// scenariosValidator statically validates every workspace/tests/*.yml
// scenario file and surfaces compose-isolation hazards as warnings.
type scenariosValidator struct{}

var _ validate.Validator = (*scenariosValidator)(nil)

func (v *scenariosValidator) ID() string     { return "scenarios" }
func (v *scenariosValidator) Domain() string { return "tests" }

func (v *scenariosValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}

	dir := envtest.TestsDir(ctx.ProjectRoot)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "tests",
			Target:   "tests.scenarios",
			Message:  fmt.Sprintf("reading %s: %v", dir, err),
		}}
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	reg, _ := ctx.CommandRegistry.(*registry.Registry)

	var diags []validate.Diagnostic
	scenarios := make([]scenarioView, 0, len(names))
	for _, name := range names {
		fileDiags, scn := v.validateFile(ctx, filepath.Join(dir, name), reg)
		scenarios = append(scenarios, newScenarioView(ctx.Cfg, ctx.ProjectRoot, envtest.ScenarioNameFromPath(name), scn))
		if len(fileDiags) == 0 {
			// Every other domain emits an OK row per clean file. Without one a
			// project whose scenarios all pass renders as "validation skipped
			// (no files found)" — FormatSummary's message for an empty
			// diagnostic set — which reads as "your scenario file is missing".
			fileDiags = []validate.Diagnostic{{
				Severity: validate.SeverityOK,
				Domain:   "tests",
				Target:   "tests." + envtest.ScenarioNameFromPath(name),
				File:     relPath(ctx.ProjectRoot, filepath.Join(dir, name)),
			}}
		}
		diags = append(diags, fileDiags...)
	}

	for _, f := range config.ScanComposeIsolation(ctx.Cfg, ctx.ProjectRoot) {
		// A volume the project declares shared: true in docker.yml is a
		// deliberate cross-project resource dwe creates itself — warning about
		// it every run would be permanent and unfixable noise.
		if f.Shared {
			continue
		}
		message := f.Message
		if f.Kind == config.KindInterpolatedHostPort {
			uncovered := uncoveredScenarios(ctx.Cfg, scenarios, f)
			switch {
			case f.VarPath != "" || f.SourceService != "":
				if len(uncovered) == 0 {
					continue
				}
				message += " (not covered in scenarios: " + strings.Join(uncovered, ", ") + ")"
			case len(scenarios) > 0 && len(uncovered) == 0:
				// No scenario's stack includes the file (every one disables
				// the service that brings it), so no test run binds the port.
				continue
			}
		}
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "tests",
			Target:   "tests.isolation",
			File:     relPath(ctx.ProjectRoot, f.File),
			Message:  message,
		})
	}

	return diags
}

// scenarioView pairs a scenario file's name with its loaded scenario and the
// compose files of its copy's stack (absolute, cleaned); scn is nil and
// composeFiles empty when the file failed to load.
type scenarioView struct {
	name         string
	scn          *envtest.Scenario
	composeFiles map[string]bool
}

// newScenarioView resolves the compose chain scn's copy would run.
// ScanComposeIsolation scans the project's chain, but envtest.ScenarioView
// gates each service's own files on the scenario's own enabled state (a
// disabled service stays enabled only when it is required) and drops the
// per-developer compose overlays the copy's seeded local.yml strips — so a
// file the scenario drops, or one only this developer's local.yml adds, binds
// nothing in that copy. Paths resolve as parseComposeFiles does, so they
// compare against IsolationFinding.File.
func newScenarioView(cfg *config.DweConfig, root, name string, scn *envtest.Scenario) scenarioView {
	v := scenarioView{name: name, scn: scn}
	if scn == nil {
		return v
	}
	v.composeFiles = make(map[string]bool)
	for _, f := range envtest.ScenarioView(cfg, scn.Env).ComposeFiles() {
		if !filepath.IsAbs(f) {
			f = filepath.Join(root, f)
		}
		v.composeFiles[filepath.Clean(f)] = true
	}
	return v
}

// uncoveredScenarios returns, in file order, the scenarios whose copy would
// bind the host port behind an interpolated finding: its compose file is in
// the scenario's stack and the scenario does not remap the port. validate has
// no scenario of its own, so the finding stays silent only when every scenario
// covers it — the same bar that keeps a fixed project green under --strict.
// An unloadable scenario covers nothing: checking a nil scenario would ask
// about the project's own enabled state instead.
func uncoveredScenarios(cfg *config.DweConfig, scenarios []scenarioView, f config.IsolationFinding) []string {
	var out []string
	for _, s := range scenarios {
		if s.scn != nil && (!s.composeFiles[filepath.Clean(f.File)] || envtest.CoversInterpolatedHostPort(cfg, s.scn, f)) {
			continue
		}
		out = append(out, s.name)
	}
	return out
}

// validateFile runs every check for a single scenario file, in load order.
// Each check appends its own diagnostic and continues, except a load failure
// (which also covers a bad scenario name — LoadScenario runs
// ValidateScenarioName internally) and a render failure, both of which abort
// the remaining checks for this file since nothing downstream can be trusted.
// It also returns the loaded scenario (nil on a load failure) for the
// project-wide isolation filter in Run.
func (v *scenariosValidator) validateFile(ctx validate.Context, path string, reg *registry.Registry) ([]validate.Diagnostic, *envtest.Scenario) {
	relFile := relPath(ctx.ProjectRoot, path)
	target := "tests." + envtest.ScenarioNameFromPath(path)

	scn, err := envtest.LoadScenario(path)
	if err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityError,
			Domain:   "tests",
			Target:   target,
			File:     relFile,
			Message:  err.Error(),
		}}, nil
	}

	var diags []validate.Diagnostic

	if scn.Timeout != "" {
		// Mirror resolveScenarioTimeout's runtime contract: a scenario timeout
		// must parse AND be strictly positive (a non-positive explicit value
		// would fail the run at resolve time, so surface it here instead).
		if d, err := time.ParseDuration(scn.Timeout); err != nil || d <= 0 {
			msg := fmt.Sprintf("invalid timeout %q: %v", scn.Timeout, err)
			if err == nil {
				msg = fmt.Sprintf("invalid timeout %q: must be positive", scn.Timeout)
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "tests",
				Target:   target,
				File:     relFile,
				Message:  msg,
			})
		}
	}

	for _, svc := range scn.Env.Services.Enable {
		if _, ok := ctx.Cfg.Services[svc]; !ok {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "tests",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("env.services.enable: unknown service %q", svc),
			})
		}
	}
	for _, svc := range scn.Env.Services.Disable {
		if _, ok := ctx.Cfg.Services[svc]; !ok {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "tests",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("env.services.disable: unknown service %q", svc),
			})
		}
	}

	// Overlay the scenario's env.vars AND env.services toggles onto the merged
	// config so both the render and resolve passes see the same config the
	// runtime does (runner.go loads one copyCfg carrying env.vars and the
	// enable/disable toggles — via the generated local.yml — and uses it for
	// both). ResolvePhaseSteps re-evaluates template `when:` conditions against
	// this cfg, so a scenario step whose `when:` references a scenario-only var
	// or a service's scenario-toggled enabled state must resolve against the
	// overlaid config, not the bare project config.
	//
	// ResolvePhaseSteps is also the only place the steps get rendered: it does
	// that once, per leaf step, before validating the step. A pre-pass here
	// would render a second time, and rendering is not idempotent — see
	// runSteps in envtest for the two ways that diverges from a real deploy.
	renderCfg := renderConfigFor(ctx.Cfg, scn.Env)

	phase := config.DeployPhase{Name: "tests", Steps: scn.Steps}
	if _, err := pipeline.ResolvePhaseSteps(renderCfg, reg, phase, ""); err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "tests",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("resolving steps: %v", err),
		})
	}

	if reg != nil {
		diags = append(diags, validateCommandRefs(renderCfg, scn.Steps, target, relFile, reg)...)
	}

	return diags, scn
}

// validateCommandRefs walks every step (recursing one level into parallel
// substeps, matching the step schema's own nesting limit) and flags a
// type: command step whose Cmd does not resolve in reg.
//
// These are the raw loaded steps, so a cmd: naming its target through a
// ${vars.*} reference has to be rendered before the lookup — the same read-a-
// raw-step-outside-ResolvePhaseSteps rule `dwe reset step` follows. A render
// error is left to the resolve pass above, which reports it.
func validateCommandRefs(cfg *config.DweConfig, steps []config.DeployStep, target, relFile string, reg *registry.Registry) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, step := range steps {
		if step.Parallel != nil {
			diags = append(diags, validateCommandRefs(cfg, step.Parallel.Steps, target, relFile, reg)...)
			continue
		}
		if step.Type != "command" {
			continue
		}
		rendered, err := pipeline.RenderStep(cfg, step)
		if err != nil {
			continue
		}
		step.Cmd = rendered.Cmd
		if _, err := reg.Get(step.Cmd); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "tests",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("unknown command %q", step.Cmd),
			})
		}
	}
	return diags
}

// renderConfigFor returns the config the scenario's copy would render steps
// against: envtest.ScenarioView applied to the project config. That gives the
// render and resolve passes the scenario's own env.vars (dot-paths rooted at
// vars:, with envtest.AutoPortSentinel substituted by
// envtest.AutoPortPlaceholder so a ${vars.x} reference used in a strict-int
// builtin param renders to a valid number) and its env.services toggles, so a
// step's template `when:` (e.g. `{{ (index .Services "x").Enabled }}` or
// `${services.x.enabled}`) resolves exactly as runtime does after loading the
// copy's generated local.yml.
//
// The view also drops the per-developer compose overlays, which is irrelevant
// to rendering — no step body reads compose.extra — and keeps this config
// identical to the one the isolation scan sees.
func renderConfigFor(cfg *config.DweConfig, env envtest.ScenarioEnv) *config.DweConfig {
	return envtest.ScenarioView(cfg, env)
}

// relPath returns path relative to root, falling back to path unchanged when
// root is empty or the path cannot be made relative.
func relPath(root, path string) string {
	if root == "" || path == "" {
		return path
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}
