package commands

// bridge.go validates `bridge:` blocks on commands and group metadata: the
// services list must reference declared workspace services that actually have
// the bridge enabled, otherwise the opt-in can never take effect. All
// diagnostics are warnings — a stale services entry must not block deploys.

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// bridgeDiagnostics checks one `bridge:` block. target is the diagnostic
// target label ("commands:<id>" or "group:<id>"); label names the holder in
// messages. nil cfg (broken project config) skips the service cross-checks.
func bridgeDiagnostics(target, label, relFile string, b *model.BridgeDef, cfg *config.DweConfig) []validate.Diagnostic {
	if b == nil {
		return nil
	}
	var diags []validate.Diagnostic
	if b.Services != nil && len(b.Services) == 0 {
		diags = append(diags, validate.Diagnostic{
			Severity: validate.SeverityWarning,
			Domain:   "commands",
			Target:   target,
			File:     relFile,
			Message:  fmt.Sprintf("%s: bridge.services is an empty list — treated as \"all services\"", label),
			Hint:     "remove the field to make the intent explicit, or list the services to restrict to",
		})
	}
	if cfg == nil {
		return diags
	}
	for _, name := range b.Services {
		svc, ok := cfg.Services[name]
		if !ok {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("%s: bridge.services references unknown service %q", label, name),
				Hint:     "use a workspace/services/<name> folder name; a typo here silently hides the command from every container",
			})
			continue
		}
		if !svc.BridgeEnabled() && !hasBridgeEnabledDescendant(cfg, name) {
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "commands",
				Target:   target,
				File:     relFile,
				Message:  fmt.Sprintf("%s: bridge.services lists %q, but that service has the bridge disabled", label, name),
				Hint:     fmt.Sprintf("set `bridge: enabled: true` in workspace/services/%s/service.yml or drop it from the list", name),
			})
		}
	}
	return diags
}

// hasBridgeEnabledDescendant reports whether any bridge-enabled service
// extends (transitively) the named one. Listing a bridge-disabled parent in
// bridge.services is still effective — its children inherit the parent's
// command rights through the extends chain — so it must not warn.
func hasBridgeEnabledDescendant(cfg *config.DweConfig, parent string) bool {
	for childName, child := range cfg.Services {
		if childName == parent || !child.BridgeEnabled() {
			continue
		}
		visited := map[string]bool{childName: true}
		for cur := child.Extends; cur != "" && !visited[cur]; {
			if cur == parent {
				return true
			}
			visited[cur] = true
			next, ok := cfg.Services[cur]
			if !ok {
				break
			}
			cur = next.Extends
		}
	}
	return false
}
