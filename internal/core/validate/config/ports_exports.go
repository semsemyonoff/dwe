package config

import (
	"fmt"
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
