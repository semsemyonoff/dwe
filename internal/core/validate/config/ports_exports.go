package config

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// portsExportsValidator warns when a service declares services.<name>.ports.<key>
// but no exports.env rule reads from it. Such a port is display-only: nothing
// pipes the resolved container binding out to a place a script, another
// service, or the developer can rely on. Two consequences make this worth
// flagging rather than silently accepted: a local.yml override of the port
// will not move the actual container binding anywhere visible, and dwe test's
// automatic host-port isolation (which remaps the port and expects an
// exports.env rule to pick up the remap — see docs/reference/config/tests.md)
// silently does not apply to it either.
type portsExportsValidator struct{}

var _ validate.Validator = (*portsExportsValidator)(nil)

func (v *portsExportsValidator) ID() string     { return "ports_exports" }
func (v *portsExportsValidator) Domain() string { return "config" }

func (v *portsExportsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	if ctx.Cfg == nil {
		return nil
	}

	exported := make(map[string]bool, len(ctx.Cfg.Exports.Env))
	for _, rule := range ctx.Cfg.Exports.Env {
		exported[rule.From] = true
	}

	var diags []validate.Diagnostic
	// DeployOrder — never range cfg.Services directly, map iteration order is
	// randomized and would make diagnostic ordering (and tests) flaky.
	for _, name := range config.DeployOrder(ctx.Cfg, []string{"app", "tool", "infra"}) {
		svc := ctx.Cfg.Services[name]
		// A service declaring no ports of its own inherits the parent's whole
		// port map through extends, so by the time the config is loaded the
		// child looks like it declares ports that only ever appear in the
		// parent's service.yml. Warning again here would duplicate the
		// parent's own finding and anchor it at a file that never mentions the
		// port. The test is whole-map, matching the loader's all-or-nothing
		// rule — see portsCoveredByEnabledAncestor.
		//
		// The suppression is valid ONLY when some ancestor holding that same
		// map is itself enabled: this loop runs over DeployOrder, which is
		// enabled-only, so a disabled ancestor never gets its turn and emits
		// nothing to defer to. An enabled child inheriting only through
		// disabled templates must therefore report the port itself.
		if portsCoveredByEnabledAncestor(ctx.Cfg, name) {
			continue
		}
		portNames := make([]string, 0, len(svc.Ports))
		for portName := range svc.Ports {
			portNames = append(portNames, portName)
		}
		slices.Sort(portNames)

		for _, portName := range portNames {
			from := fmt.Sprintf("services.%s.ports.%s", name, portName)
			if exported[from] {
				continue
			}
			diags = append(diags, validate.Diagnostic{
				Severity: validate.SeverityWarning,
				Domain:   "config",
				Target:   "config.ports_exports",
				File:     filepath.Join("workspace", "services", name, "service.yml"),
				Message: fmt.Sprintf(
					"service %s declares ports.%s but no exports.env rule reads from %s",
					name, portName, from,
				),
				Hint: fmt.Sprintf(
					"this port is display-only: a local.yml override of %s will not move the container "+
						"binding anywhere visible, and dwe test's host-port isolation will silently not apply "+
						"to it. Add an exports.env rule with `from: %s` if the port needs to be visible outside "+
						"the container, or ignore this if it is intentionally internal-only.",
					from, from,
				),
			})
		}
	}
	return diags
}

// portsCoveredByEnabledAncestor reports whether service name's whole resolved
// port map came from an `extends:` ancestor that is itself ENABLED — i.e. an
// ancestor DeployOrder will visit, which therefore emits the finding for that
// identical port set on its own service.yml. Suppressing the child is only
// correct in that case.
//
// Inheritance is all-or-nothing: ResolveServiceExtends clones the parent's map
// only when the child declares no ports at all
// (`if len(svc.Ports) == 0 { svc.Ports = maps.Clone(parent.Ports) }`), so a
// child declaring even one port keeps its own map entirely. The test has to
// match that rule — asking per port name whether SOME ancestor uses the same
// name would also swallow a genuinely unexported port the child declared
// itself, which is precisely the finding this validator exists to produce.
//
// So the test is map equality against the nearest ancestor that declares
// ports: that is exactly the map the clone would have produced. A child that
// re-declares a byte-identical map is indistinguishable from an inheriting one
// and is treated as inheriting — the right call either way, since the parent's
// own finding already covers the identical port set.
//
// Extends inheritance is preserved on the resolved config (ResolveServiceExtends
// never clears Extends), so the chain is still walkable here. The visited set
// guards against a cycle — the loader rejects those, but this validator must
// not hang on a config that somehow reached it.
//
// A disabled ancestor carrying the identical map is NOT the answer, but it is
// not the end of the walk either: ResolveServiceExtends runs in topological
// order, so a disabled intermediate template has already been given its own
// parent's map by the time the grandchild clones it. Stopping at the first
// ancestor with ports would then report "not covered" for
// base(enabled) <- template(disabled) <- worker(enabled) even though base
// emits the finding for exactly that port set. So keep climbing while the map
// stays equal, and answer on the first ancestor that is enabled.
func portsCoveredByEnabledAncestor(cfg *config.DweConfig, name string) bool {
	svc, ok := cfg.Services[name]
	if !ok || len(svc.Ports) == 0 {
		return false
	}
	visited := map[string]bool{name: true}
	cur := svc
	for cur.Extends != "" && !visited[cur.Extends] {
		parentName := cur.Extends
		visited[parentName] = true
		parent, ok := cfg.Services[parentName]
		if !ok {
			return false
		}
		if len(parent.Ports) > 0 {
			if !maps.Equal(svc.Ports, parent.Ports) {
				return false
			}
			if parent.Enabled {
				return true
			}
		}
		cur = parent
	}
	return false
}
