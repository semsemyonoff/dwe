package i18n

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	"github.com/semsemyonoff/dwe/internal/core/validate"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// orphanValidator emits a diagnostic for each orphaned command or group entry
// (one whose ID does not exist in the user-command registry).
type orphanValidator struct {
	pf  i18n.ProjectFile
	reg *registry.Registry
}

func (v *orphanValidator) ID() string {
	return fmt.Sprintf("%s/orphan", v.pf.Locale)
}

func (v *orphanValidator) Domain() string {
	return "i18n"
}

func (v *orphanValidator) Run(_ validate.Context) []validate.Diagnostic {
	if v.reg == nil || v.pf.Bundle == nil {
		return nil
	}

	// Build set of daemon base IDs from expanded virtual commands.
	// Daemon base commands are consumed during expansion and not present in
	// reg.Get(), so we must treat them as valid translation targets separately.
	daemonBases := make(map[string]struct{})
	for _, cmd := range v.reg.ListAll("") {
		if cmd.DerivedFromDaemon != "" {
			daemonBases[cmd.DerivedFromDaemon] = struct{}{}
		}
	}

	var diags []validate.Diagnostic

	// Check orphaned commands
	for cmdID := range v.pf.Bundle.Commands {
		if _, isDaemon := daemonBases[cmdID]; isDaemon {
			continue
		}
		if _, err := v.reg.Get(cmdID); err != nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "i18n",
				Target:   fmt.Sprintf("%s/%s", v.pf.Locale, cmdID),
				File:     v.pf.Path,
				Message:  fmt.Sprintf("translation references a command that no longer exists: %s", cmdID),
				Hint:     "rename or remove the entry",
			})
		}
	}

	// Check orphaned groups
	for groupID := range v.pf.Bundle.Groups {
		if findGroupNode(v.reg.Groups(), groupID) == nil {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "i18n",
				Target:   fmt.Sprintf("%s/%s", v.pf.Locale, groupID),
				File:     v.pf.Path,
				Message:  fmt.Sprintf("translation references a group that no longer exists: %s", groupID),
				Hint:     "rename or remove the entry",
			})
		}
	}

	return diags
}

// findGroupNode searches the registry tree for a node with the given dot-separated ID.
func findGroupNode(node *registry.GroupNode, id string) *registry.GroupNode {
	if node == nil {
		return nil
	}
	if node.ID == id {
		return node
	}
	for _, child := range node.Children {
		if found := findGroupNode(child, id); found != nil {
			return found
		}
	}
	return nil
}
