package envtest

import (
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// HostPortOverride pins one service's declared host port to a freshly
// allocated free port in the copy's generated local.yml.
//
// A test copy runs under its own compose project name, so it never needs the
// original host ports. Remapping every enabled service's declared host port to
// a free one makes the `ports_free` preflight pass and — for projects that
// source their compose host bindings from `services.<name>.ports` (directly or
// via an `exports.env` entry `from: services.<name>.ports.<x>`) — moves the
// actual bind too. So a scenario runs alongside the working environment (and
// any other project holding those ports) with no port config in the scenario
// and no changes to the project. This is deliberately independent of the
// `env.vars: auto` mechanism, which only rewrites vars-routed ports and cannot
// touch a port declared in `services.<name>.ports` (a strict int the preflight
// reads literally).
type HostPortOverride struct {
	Service  string
	PortName string
	Port     int
	// Scheme is preserved from the original ServicePortSpec so a remapped port
	// keeps its http/https classification for URL rendering; "" writes a bare
	// integer.
	Scheme string
}

// hostPortKey identifies one declared host port (service + port name).
type hostPortKey struct {
	service  string
	portName string
}

// enabledHostPortKeys returns, sorted deterministically, every (service,
// portName) host port declared by a service that will be ENABLED in the test:
// the original merged enabled state, overridden by the scenario's
// env.services.enable/disable. Ports outside 1..65535 are skipped (mirrors the
// ports_free preflight's own guard in collectDeclaredPorts).
func enabledHostPortKeys(cfg *config.DweConfig, scn *Scenario) []hostPortKey {
	if cfg == nil {
		return nil
	}
	disable := map[string]bool{}
	enable := map[string]bool{}
	if scn != nil {
		for _, n := range scn.Env.Services.Disable {
			disable[n] = true
		}
		for _, n := range scn.Env.Services.Enable {
			enable[n] = true
		}
	}
	var keys []hostPortKey
	for name, svc := range cfg.Services {
		on := svc.Enabled
		if enable[name] {
			on = true
		}
		if disable[name] {
			on = false
		}
		if !on {
			continue
		}
		for portName, spec := range svc.Ports {
			if spec.Port <= 0 || spec.Port > 65535 {
				continue
			}
			keys = append(keys, hostPortKey{service: name, portName: portName})
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].service != keys[j].service {
			return keys[i].service < keys[j].service
		}
		return keys[i].portName < keys[j].portName
	})
	return keys
}

// buildHostPortOverrides pairs keys[i] with allocated[i], carrying the original
// port's scheme through. len(allocated) must be >= len(keys).
func buildHostPortOverrides(cfg *config.DweConfig, keys []hostPortKey, allocated []int) []HostPortOverride {
	out := make([]HostPortOverride, 0, len(keys))
	for i, k := range keys {
		var scheme string
		if svc, ok := cfg.Services[k.service]; ok {
			scheme = svc.Ports[k.portName].Scheme
		}
		out = append(out, HostPortOverride{
			Service:  k.service,
			PortName: k.portName,
			Port:     allocated[i],
			Scheme:   scheme,
		})
	}
	return out
}

// ApplyHostPortOverrides merges host-port overrides into overlay in place,
// writing services.<name>.ports.<portName> for each (a bare int, or a
// {port, scheme} mapping when the original carried a scheme). It runs AFTER
// BuildLocalOverlay so it composes with any services.<name>.enabled toggle the
// scenario set — the recursive merge keeps both keys under the same service.
func ApplyHostPortOverrides(overlay map[string]any, hostPorts []HostPortOverride) {
	if len(hostPorts) == 0 {
		return
	}
	services := map[string]any{}
	for _, hp := range hostPorts {
		svc, _ := services[hp.Service].(map[string]any)
		if svc == nil {
			svc = map[string]any{}
			services[hp.Service] = svc
		}
		portsMap, _ := svc["ports"].(map[string]any)
		if portsMap == nil {
			portsMap = map[string]any{}
			svc["ports"] = portsMap
		}
		if hp.Scheme != "" {
			portsMap[hp.PortName] = map[string]any{"port": hp.Port, "scheme": hp.Scheme}
		} else {
			portsMap[hp.PortName] = hp.Port
		}
	}
	localDeepMerge(overlay, map[string]any{"services": services})
}
