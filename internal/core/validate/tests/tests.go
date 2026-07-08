// Package tests validates workspace/tests/*.yml integration-test scenario
// files: schema/name normalisation (via envtest.LoadScenario), the wall-clock
// timeout string, env.services references, whole-phase step resolution
// (reusing the exact runtime pipeline resolver), type: command references,
// and compose-isolation hazards. Validate-only — never registered in
// preflight.Run.
package tests

import (
	"fmt"
	"maps"
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

// autoPortPlaceholder substitutes any scenario env.vars entry equal to
// envtest.AutoPortSentinel ("auto") when rendering steps at validate time, so
// a ${vars.x} reference used in a strict-int builtin param (e.g.
// tcp_reachable's port:) renders to a valid placeholder instead of the empty
// string. The concrete value is arbitrary — validate-time resolution never
// dials it, it only needs to satisfy an int/port-range check.
const autoPortPlaceholder = 1

// All returns the tests domain's validators.
func All() []validate.Validator {
	return []validate.Validator{&scenariosValidator{}}
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
	for _, name := range names {
		diags = append(diags, v.validateFile(ctx, filepath.Join(dir, name), reg)...)
	}

	for _, f := range config.ScanComposeIsolation(ctx.Cfg, ctx.ProjectRoot) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "tests",
			Target:   "tests.isolation",
			File:     relPath(ctx.ProjectRoot, f.File),
			Message:  f.Message,
		})
	}

	return diags
}

// validateFile runs every check for a single scenario file, in load order.
// Each check appends its own diagnostic and continues, except a load failure
// (which also covers a bad scenario name — LoadScenario runs
// ValidateScenarioName internally) and a render failure, both of which abort
// the remaining checks for this file since nothing downstream can be trusted.
func (v *scenariosValidator) validateFile(ctx validate.Context, path string, reg *registry.Registry) []validate.Diagnostic {
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
		}}
	}

	var diags []validate.Diagnostic

	if scn.Timeout != "" {
		if _, err := time.ParseDuration(scn.Timeout); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityError,
				Domain:   "tests",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("invalid timeout %q: %v", scn.Timeout, err),
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

	// Overlay the scenario's env.vars onto the merged config so both the render
	// and resolve passes see the same config the runtime does (runner.go builds
	// one copyCfg carrying env.vars and uses it for both). ResolvePhaseSteps
	// re-evaluates template `when:` conditions against this cfg, so a scenario
	// step whose `when:` references a scenario-only var must resolve against the
	// overlaid config, not the bare project config.
	renderCfg := renderConfigFor(ctx.Cfg, scn.Env.Vars)
	if err := envtest.RenderSteps(scn.Steps, renderCfg); err != nil {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "tests",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("rendering steps: %v", err),
		})
		return diags
	}

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
		diags = append(diags, validateCommandRefs(scn.Steps, target, relFile, reg)...)
	}

	return diags
}

// validateCommandRefs walks every step (recursing one level into parallel
// substeps, matching the step schema's own nesting limit) and flags a
// type: command step whose Cmd does not resolve in reg.
func validateCommandRefs(steps []config.DeployStep, target, relFile string, reg *registry.Registry) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for _, step := range steps {
		if step.Parallel != nil {
			diags = append(diags, validateCommandRefs(step.Parallel.Steps, target, relFile, reg)...)
			continue
		}
		if step.Type != "command" {
			continue
		}
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

// renderConfigFor builds a throwaway *config.DweConfig whose Raw carries cfg's
// merged config overlaid with the scenario's own env.vars (dot-paths rooted at
// vars:), substituting any value equal to envtest.AutoPortSentinel with
// autoPortPlaceholder. This lets ${vars.x} used in a strict-int builtin param
// render to a valid placeholder at validate time — the same "auto" magic value
// stage-1b's runner substitutes with a real allocated port, just resolved
// early since validate never allocates ports or deploys anything.
//
// The returned config is a shallow copy of cfg with only Raw replaced by a
// freshly built map (cfg.Raw and its "vars" entry are never mutated), safe to
// discard after the render pass.
func renderConfigFor(cfg *config.DweConfig, vars map[string]any) *config.DweConfig {
	overlayVars := make(map[string]any, len(vars))
	paths := make([]string, 0, len(vars))
	for path := range vars {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		value := vars[path]
		if s, ok := value.(string); ok && s == envtest.AutoPortSentinel {
			value = autoPortPlaceholder
		}
		setDotPath(overlayVars, path, value)
	}

	raw := make(map[string]any, len(cfg.Raw)+1)
	maps.Copy(raw, cfg.Raw)
	existingVars, _ := cfg.Raw["vars"].(map[string]any)
	raw["vars"] = mergeMaps(existingVars, overlayVars)

	cfgCopy := *cfg
	cfgCopy.Raw = raw
	return &cfgCopy
}

// setDotPath inserts value at the dot-path in m, creating intermediate maps as
// needed. A path segment already holding a non-map value is overwritten
// wholesale (mirrors envtest's own setDotPath collision rule). An empty path
// or path segment is silently dropped — a malformed scenario var path is
// reported downstream when the render/resolve pass sees the unresolved ref.
func setDotPath(m map[string]any, path string, value any) {
	if path == "" {
		return
	}
	parts := strings.Split(path, ".")
	for i, part := range parts {
		if part == "" {
			return
		}
		if i == len(parts)-1 {
			m[part] = value
			return
		}
		next, ok := m[part].(map[string]any)
		if !ok {
			next = make(map[string]any)
			m[part] = next
		}
		m = next
	}
}

// mergeMaps returns a new map holding dst's entries overlaid with src's
// (src wins on conflict; nested maps present on both sides merge
// recursively). Neither dst nor src is mutated.
func mergeMaps(dst, src map[string]any) map[string]any {
	out := make(map[string]any, len(dst)+len(src))
	maps.Copy(out, dst)
	for k, sv := range src {
		if dm, ok := out[k].(map[string]any); ok {
			if sm, ok := sv.(map[string]any); ok {
				out[k] = mergeMaps(dm, sm)
				continue
			}
		}
		out[k] = sv
	}
	return out
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
