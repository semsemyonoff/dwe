package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	configtmpl "github.com/semsemyonoff/dwe/internal/core/execution/templates/config"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// fieldNamePattern is the set of valid ${generated.<name>} identifiers. It
// mirrors the ${...} var grammar (tpl.varPattern) minus the dot: a generated
// field name is a single dot-free identifier so ${generated.<name>} resolves to
// exactly that key (a dot would be parsed as a nested path and never match).
var fieldNamePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// generatedMissingVerb is the predicate verb whose two-arg payload references a
// declared generated field. Kept in sync with the runtime switch in
// internal/core/execution/condition.
const generatedMissingVerb = "generated-missing"

// generatedValidator validates the per-service `generated:` declarations and the
// optional `render.config.template` pin in service.yml, plus a cross-check that
// every `generated-missing <svc> <field>` predicate in the project's pipelines
// references a declared generated field.
type generatedValidator struct{}

func (v *generatedValidator) ID() string     { return "generated" }
func (v *generatedValidator) Domain() string { return "config" }

func (v *generatedValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	services, ok := resolveServices(ctx)
	if !ok || len(services) == 0 {
		return diags
	}

	servicesDir := filepath.Join(ctx.ProjectRoot, "workspace", "services")

	for _, name := range slices.Sorted(maps.Keys(services)) {
		svc := services[name]
		svcFile := relPath(ctx.ProjectRoot, filepath.Join(servicesDir, name, "service.yml"))
		target := "config.generated:" + name

		diags = append(diags, validateGeneratedFields(name, svc, svcFile, target)...)
		diags = append(diags, validateRenderConfigTemplate(ctx, name, svc, services, svcFile, target)...)
	}

	diags = append(diags, validateGeneratedMissingRefs(ctx, services)...)

	return diags
}

// validateGeneratedFields checks every declared generated field for a valid name,
// a contained-relative file path, and a regex pattern with at least one capture
// group.
func validateGeneratedFields(name string, svc config.ServiceConfig, svcFile, target string) []validate.Diagnostic {
	var diags []validate.Diagnostic

	emit := func(msg, hint string) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   target,
			File:     svcFile,
			Message:  msg,
			Hint:     hint,
		})
	}

	for _, field := range slices.Sorted(maps.Keys(svc.Generated)) {
		gf := svc.Generated[field]

		if !fieldNamePattern.MatchString(field) {
			emit(
				fmt.Sprintf("service %q generated field %q: invalid name; must match a ${generated.<name>} identifier (letters, digits, underscore; no leading digit or dot)", name, field),
				"rename the field, e.g. app_key",
			)
		}

		if strings.TrimSpace(gf.File) == "" {
			emit(
				fmt.Sprintf("service %q generated field %q: file is required (the path the service writes the value into)", name, field),
				"set generated."+field+".file, e.g. configs/.env",
			)
		} else if !isContainedRel(gf.File) {
			emit(
				fmt.Sprintf("service %q generated field %q: file %q must be a relative path contained in the service dir (no leading / or ..)", name, field, gf.File),
				"use a path relative to the service hub dir, e.g. configs/.env",
			)
		}

		switch {
		case strings.TrimSpace(gf.Pattern) == "":
			emit(
				fmt.Sprintf("service %q generated field %q: pattern is required (a regex whose first capture group extracts the value)", name, field),
				"set generated."+field+".pattern, e.g. '^APP_KEY=(.*)$'",
			)
		default:
			re, err := regexp.Compile(gf.Pattern)
			if err != nil {
				emit(
					fmt.Sprintf("service %q generated field %q: pattern %q does not compile: %v", name, field, gf.Pattern, err),
					"fix the regular expression",
				)
			} else if re.NumSubexp() < 1 {
				emit(
					fmt.Sprintf("service %q generated field %q: pattern %q has no capture group; capture group 1 supplies the value", name, field, gf.Pattern),
					"wrap the value in parentheses, e.g. '^APP_KEY=(.*)$'",
				)
			}
		}
	}

	return diags
}

// validateRenderConfigTemplate warns when an explicit render.config.template pin
// does not resolve to a usable config template pack (mirrors the ide/ai/git
// template-pin discipline). Implicit (unpinned) resolution emits nothing — a
// missing pack just means config rendering is opt-out for that service.
func validateRenderConfigTemplate(
	ctx validate.Context,
	name string,
	svc config.ServiceConfig,
	services map[string]config.ServiceConfig,
	svcFile, target string,
) []validate.Diagnostic {
	if svc.Render.Config == nil || svc.Render.Config.Template == "" {
		return nil
	}

	_, _, found, err := configtmpl.ResolveTemplatePack(svc, services, ctx.ProjectRoot, name)
	if err == nil && found {
		return nil
	}

	msg := fmt.Sprintf("service %q render.config.template %q does not resolve to a config template pack", name, svc.Render.Config.Template)
	if err != nil {
		msg = fmt.Sprintf("service %q render.config.template %q: %v", name, svc.Render.Config.Template, err)
	}
	return []validate.Diagnostic{{
		Severity: validate.SeverityWarning,
		Domain:   "config",
		Target:   target,
		File:     svcFile,
		Message:  msg,
		Hint:     "create workspace/templates/config/" + svc.Render.Config.Template + "/ or remove the pin to use convention-based resolution",
	}}
}

// validateGeneratedMissingRefs scans the project's pipelines for
// `generated-missing <svc> <field>` predicates and flags any that reference an
// undeclared service or generated field. It reuses condition.ParseGeneratedMissing
// (the same two-arg parser the runtime evaluator uses) so arity rules stay in one
// place. Predicates with bad arity are left to the runtime evaluator — this
// cross-check only reports unresolved references.
func validateGeneratedMissingRefs(ctx validate.Context, services map[string]config.ServiceConfig) []validate.Diagnostic {
	var diags []validate.Diagnostic

	emit := func(file, location, msg, hint string) {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityError,
			Domain:   "config",
			Target:   "config.generated.predicate:" + location,
			File:     file,
			Message:  msg,
			Hint:     hint,
		})
	}

	for _, src := range collectPredicateSources(ctx, services) {
		for _, ref := range scanPhasesForGeneratedMissing(src.phases) {
			svcArg, field, err := condition.ParseGeneratedMissing(ref.args)
			if err != nil {
				// Arity errors are surfaced at runtime; static cross-check only
				// reports unresolved references.
				continue
			}
			svc, exists := services[svcArg]
			if !exists {
				emit(src.file, src.label,
					fmt.Sprintf("generated-missing predicate references unknown service %q", svcArg),
					"reference a service declared under workspace/services/")
				continue
			}
			if _, declared := svc.Generated[field]; !declared {
				emit(src.file, src.label,
					fmt.Sprintf("generated-missing predicate references undeclared generated field %q on service %q", field, svcArg),
					fmt.Sprintf("declare generated.%s in workspace/services/%s/service.yml", field, svcArg))
			}
		}
	}

	return diags
}

// predicateSource bundles a set of pipeline phases with the file and label used
// for diagnostics.
type predicateSource struct {
	phases []config.DeployPhase
	file   string
	label  string
}

// collectPredicateSources gathers every pipeline phase set that may carry a
// when-condition: the project deploy/reset pipelines and each service's deploy
// pipeline. Sources that fail to load are skipped (other validators surface load
// errors).
func collectPredicateSources(ctx validate.Context, services map[string]config.ServiceConfig) []predicateSource {
	var sources []predicateSource

	if ctx.Cfg != nil && ctx.Cfg.Deploy != nil {
		sources = append(sources, predicateSource{
			phases: ctx.Cfg.Deploy.Phases,
			file:   relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "deploy.yml")),
			label:  "deploy",
		})
	}

	if resetCfg, err := config.LoadResetConfig(filepath.Join(ctx.ProjectRoot, "workspace", "reset.yml")); err == nil {
		sources = append(sources, predicateSource{
			phases: resetCfg.Phases,
			file:   relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "reset.yml")),
			label:  "reset",
		})
	}

	if len(services) > 0 {
		if svcDeploys, err := config.LoadServiceDeployConfigs(ctx.ProjectRoot, services); err == nil {
			for _, name := range slices.Sorted(maps.Keys(svcDeploys)) {
				sources = append(sources, predicateSource{
					phases: svcDeploys[name].Phases,
					file:   relPath(ctx.ProjectRoot, filepath.Join(ctx.ProjectRoot, "workspace", "services", name, "deploy.yml")),
					label:  "service-deploy[" + name + "]",
				})
			}
		}
	}

	return sources
}

// generatedMissingRef records the argument string of one generated-missing
// predicate found in a pipeline (the text after the verb).
type generatedMissingRef struct {
	args string
}

// scanPhasesForGeneratedMissing walks phase- and step-level builtin when
// conditions and returns the argument string of each generated-missing predicate.
func scanPhasesForGeneratedMissing(phases []config.DeployPhase) []generatedMissingRef {
	var refs []generatedMissingRef

	collect := func(c *condition.Condition) {
		if c == nil || c.Type != condition.TypeBuiltin {
			return
		}
		if args, ok := generatedMissingArgs(c.Cmd); ok {
			refs = append(refs, generatedMissingRef{args: args})
		}
	}

	for _, phase := range phases {
		collect(phase.When)
		for _, step := range phase.Steps {
			collect(step.When)
			if step.Parallel != nil {
				for _, sub := range step.Parallel.Steps {
					collect(sub.When)
				}
			}
		}
	}

	return refs
}

// generatedMissingArgs reports whether cmd is a generated-missing predicate and,
// if so, returns the argument string (everything after the verb).
func generatedMissingArgs(cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	verb, rest, hasRest := strings.Cut(cmd, " ")
	if verb != generatedMissingVerb {
		return "", false
	}
	if !hasRest {
		return "", true
	}
	return strings.TrimSpace(rest), true
}

// isContainedRel reports whether a service-relative file path stays inside the
// service hub dir (no absolute path, no `..` escape). It uses a synthetic base
// so the check is filesystem-free.
func isContainedRel(p string) bool {
	if filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	if clean == "." || clean == ".." {
		return false
	}
	if strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}
