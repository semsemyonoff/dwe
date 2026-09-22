package commands

// hide.go validates `hide:` expressions on commands and group metadata.
//
// `hide:` accepts the same expression syntax as workflow `when:` — Go template
// + builtin predicates (cmd:/builtin keys). This validator checks syntax, and
// for template-only expressions also renders them against the loaded config:
// runtime evaluation (registry.ApplyVisibility) is fail-open, so an expression
// that parses but cannot execute leaves the command visible, reported only as
// a log line at each invocation. Predicates are never executed here.

import (
	"fmt"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/execution/condition"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// hideDiagnostics emits a warning for a malformed or non-evaluating `hide:`
// expression on a command. The hide field is optional, so an empty value
// never produces a diagnostic. A nil cfg (config failed to load) skips the
// render check.
func hideDiagnostics(cmd model.CommandDef, relFile string, cfg *config.DweConfig) []validate.Diagnostic {
	return hideExprDiagnostics(cmd.Hide, fmt.Sprintf("commands:%s", cmd.ID), cmd.ID, relFile, cfg)
}

// groupHideDiagnostics is hideDiagnostics for a group's metadata block.
// Called once per command-file with a non-empty Group.Hide.
func groupHideDiagnostics(groupID, hideExpr, relFile string, cfg *config.DweConfig) []validate.Diagnostic {
	return hideExprDiagnostics(hideExpr, fmt.Sprintf("group:%s", groupID), fmt.Sprintf("group %q", groupID), relFile, cfg)
}

func hideExprDiagnostics(expr, target, label, relFile string, cfg *config.DweConfig) []validate.Diagnostic {
	if expr == "" {
		return nil
	}
	if err := tpl.CompileCommand(expr, tpl.SnapshotScopeNone); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("%s: hide: %v", label, err),
			Hint:     "fix the template syntax; same rules as workflow `when:`",
		}}
	}
	if cfg == nil || !isTemplateOnlyHide(expr) {
		return nil
	}
	if _, err := tpl.RenderCommand(expr, registry.HideRenderContext(cfg)); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("%s: hide: expression does not evaluate: %v", label, err),
			Hint:     "config is under .Raw, e.g. `index .Raw \"services\" \"db\" \"enabled\"`; until fixed the command stays visible",
		}}
	}
	return nil
}

// isTemplateOnlyHide reports whether expr is neither a `cmd:` predicate nor a
// builtin `when:` predicate. Those are left to runtime: their result depends
// on the host state they probe, not on the config the validator holds.
func isTemplateOnlyHide(expr string) bool {
	s := strings.TrimSpace(expr)
	if strings.HasPrefix(s, "cmd:") {
		return false
	}
	verb, _, _ := strings.Cut(s, " ")
	for _, p := range condition.Predicates() {
		if p.Name == verb {
			return false
		}
	}
	return true
}
