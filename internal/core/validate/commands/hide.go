package commands

// hide.go validates `hide:` expressions on commands and group metadata.
//
// `hide:` accepts the same expression syntax as workflow `when:` — Go template
// + builtin predicates (cmd:/builtin keys). This validator only checks
// syntactic validity (template parses, ${...} balanced); it never executes
// shell predicates or accesses config. Runtime evaluation errors surface
// when `dwe commands` is invoked.

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// hideDiagnostics emits a warning per malformed `hide:` expression on a
// command. The hide field is optional, so an empty value never produces a
// diagnostic.
func hideDiagnostics(cmd model.CommandDef, relFile string) []validate.Diagnostic {
	if cmd.Hide == "" {
		return nil
	}
	if err := tpl.CompileCommand(cmd.Hide, tpl.SnapshotScopeNone); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "commands",
			Target:   fmt.Sprintf("commands:%s", cmd.ID),
			File:     relFile,
			Message:  fmt.Sprintf("%s: hide: %v", cmd.ID, err),
			Hint:     "fix the template syntax; same rules as workflow `when:`",
		}}
	}
	return nil
}

// groupHideDiagnostics emits a warning per malformed `hide:` expression on
// a group's metadata block. Called once per command-file with a non-empty
// Group.Hide.
func groupHideDiagnostics(groupID, hideExpr, relFile string) []validate.Diagnostic {
	if hideExpr == "" {
		return nil
	}
	if err := tpl.CompileCommand(hideExpr, tpl.SnapshotScopeNone); err != nil {
		return []validate.Diagnostic{{
			Severity: validate.SeverityWarning,
			Domain:   "commands",
			Target:   fmt.Sprintf("group:%s", groupID),
			File:     relFile,
			Message:  fmt.Sprintf("group %q: hide: %v", groupID, err),
			Hint:     "fix the template syntax; same rules as workflow `when:`",
		}}
	}
	return nil
}
