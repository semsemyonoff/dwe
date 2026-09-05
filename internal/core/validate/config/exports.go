package config

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// exportsValidator warns on an exports.env rule whose from: or when: dot-path
// does not resolve in the merged config.
//
// Nothing catches this today: `from: vars.db.passwrod` is a well-formed string,
// the loader accepts it, and `dwe render env` writes DB_PASSWORD= — an empty
// value that reaches every container as if it had been declared that way. The
// when: variant is quieter still: an unresolvable path is falsy, so the rule is
// skipped and the variable simply never appears.
//
// The criterion is config.ResolvePath reporting not-found — literally the
// function envfile.BuildContent calls (render.go), so this validator and the
// renderer cannot drift. A present key holding nil is NOT a finding: the path
// exists, the author wrote it, and the empty render is then intentional.
//
// Warning, not error: `from:` paired with `default:` is a legitimate
// optional-path pattern, and a path may be declared in a local.yml that is not
// on this machine. The hint says which of the three shapes the rule has, since
// the consequence differs (renders empty / default always wins / render fails).
type exportsValidator struct{}

var _ validate.Validator = (*exportsValidator)(nil)

func (v *exportsValidator) ID() string     { return "exports" }
func (v *exportsValidator) Domain() string { return "config" }

func (v *exportsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil || len(ctx.Cfg.Exports.Env) == 0 {
		return nil
	}

	configPath := ctx.ConfigPath
	if configPath == "" {
		// The preflight/menu Context constructions carry no ConfigPath. They
		// never register this validator, but the fallback is cheap and
		// workspace.yml is the one layer guaranteed to exist.
		configPath = filepath.Join(ctx.ProjectRoot, "workspace.yml")
	}
	declaredIn := exportRuleFiles(ctx.ProjectRoot, configPath)
	fallbackFile := relPath(ctx.ProjectRoot, configPath)

	var diags []validate.Diagnostic
	for _, rule := range ctx.Cfg.Exports.Env {
		// Reserved names are rejected at load; skip defensively, matching
		// BuildContent's own defense-in-depth branch.
		if config.IsReservedExportName(rule.Name) {
			continue
		}
		file := fallbackFile
		if f, ok := declaredIn[rule.Name]; ok {
			file = f
		}

		if rule.From != "" {
			if _, found := config.ResolvePath(ctx.Cfg.Raw, rule.From); !found {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Domain:   "config",
					Target:   "config.exports",
					File:     file,
					Message: fmt.Sprintf(
						"exports.env[%s]: from %q does not resolve in the merged config",
						rule.Name, rule.From,
					),
					Hint: unresolvedFromHint(rule),
				})
			}
		}

		if rule.When != "" {
			if _, found := config.ResolvePath(ctx.Cfg.Raw, rule.When); !found {
				diags = append(diags, validate.Diagnostic{
					Severity: validate.SeverityWarning,
					Domain:   "config",
					Target:   "config.exports",
					File:     file,
					Message: fmt.Sprintf(
						"exports.env[%s]: when %q does not resolve in the merged config",
						rule.Name, rule.When,
					),
					Hint: "an unresolvable when: is falsy, so the rule is always skipped and " +
						"the variable never reaches .env — check for a typo",
				})
			}
		}
	}
	return diags
}

// unresolvedFromHint names the consequence of the miss, which depends on what
// else the rule declares — the three branches mirror BuildContent's own.
func unresolvedFromHint(rule config.ExportRule) string {
	switch {
	case rule.Required && rule.Default == "":
		return "the rule is required, so `dwe render env` fails on it — check for a typo in the path"
	case rule.Default != "":
		return "the default is always used — check for a typo in the path, or drop from: if the default is the intent"
	default:
		return "the variable renders empty — check for a typo in the path, or add a default:"
	}
}

// exportRuleFiles maps an export rule name to the project-relative path of the
// layer that declares it, so a rule overridden in local.yml is reported there
// rather than at workspace.yml. Layers are scanned highest-precedence first:
// that is the declaration the merged config actually carries.
//
// A read failure yields an empty map — the layer error is surfaced by the
// workspace validator, and this one falls back to the config path.
func exportRuleFiles(projectRoot, configPath string) map[string]string {
	layers, err := config.LoadRawLayers(configPath)
	if err != nil {
		return nil
	}
	files := make(map[string]string)
	for _, layer := range slices.Backward(layers) {
		for _, name := range exportNamesIn(layer.Data) {
			if _, seen := files[name]; !seen {
				files[name] = relPath(projectRoot, layer.Path)
			}
		}
	}
	return files
}

// exportNamesIn lists the exports.env rule names declared in one raw layer.
func exportNamesIn(data map[string]any) []string {
	exports, ok := data["exports"].(map[string]any)
	if !ok {
		return nil
	}
	env, ok := exports["env"].([]any)
	if !ok {
		return nil
	}
	var names []string
	for _, item := range env {
		rule, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := rule["name"].(string); ok && name != "" {
			names = append(names, name)
		}
	}
	return names
}
