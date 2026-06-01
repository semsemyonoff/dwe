package render

import (
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// renderAutoHosts renders the auto-hosts info item block.
// Returns a string containing the rendered hosts section, or "" if no hosts match.
// The function reads services in deploy order to ensure deterministic output.
//
// Service iteration MUST use deploy-order logic — never range cfg.Services directly
// because Go map iteration is randomized and produces flaky tests.
func renderAutoHosts(cfg *config.DweConfig, spec *config.AutoHostsSpec) string {
	if cfg == nil || spec == nil {
		return ""
	}

	// Apply defaults
	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool", "infra"}
	}

	ip := spec.IP
	if ip == "" {
		ip = "127.0.0.1"
	}

	// Validate IP (warn-level — validation framework handles this, render is silent)
	// Invalid IP proceeds silently; output will be malformed but won't crash

	// Build hide set for fast lookup
	hideSet := make(map[string]bool)
	for _, h := range spec.Hide {
		hideSet[h] = true
	}

	ordered := config.DeployOrder(cfg, include)
	if len(ordered) == 0 {
		return ""
	}

	// Collect all hostnames from services, preserving order and deduping
	seen := make(map[string]bool)
	var hostnames []string

	for _, svcName := range ordered {
		if hideSet[svcName] {
			continue
		}

		svc := cfg.Services[svcName]

		// Collect all hosts.<key> values from this service's hosts map
		// We need deterministic iteration, so sort the keys first
		var hostKeys []string
		for key := range svc.Hosts {
			hostKeys = append(hostKeys, key)
		}
		slices.Sort(hostKeys)

		for _, key := range hostKeys {
			hostname := svc.Hosts[key]

			// Filter: drop empty, drop localhost, drop any *.localhost
			// (browsers and most resolvers route .localhost to 127.0.0.1 without
			// an /etc/hosts entry), drop duplicates.
			if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
				continue
			}

			if !seen[hostname] {
				hostnames = append(hostnames, hostname)
				seen[hostname] = true
			}
		}
	}

	if len(hostnames) == 0 {
		return ""
	}

	// Build output: each hostname on its own line as "IP\thostname".
	// No leading indent — users copy-paste these straight into /etc/hosts.
	var lines []string
	for _, hostname := range hostnames {
		lines = append(lines, ip+"\t"+hostname)
	}

	return strings.Join(lines, "\n")
}
