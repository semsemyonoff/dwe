package ui

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"devbox-cli/internal/config"
)

// renderAutoURLs renders the auto-urls info item block.
// Returns a string containing the rendered URLs section, or "" if no services match.
// The function reads services in deploy order to ensure deterministic output.
//
// Service iteration MUST use deploy-order logic — never range cfg.Services directly
// because Go map iteration is randomized and produces flaky tests.
func renderAutoURLs(cfg *config.DevboxConfig, spec *config.AutoURLsSpec) string {
	if cfg == nil || spec == nil {
		return ""
	}

	// Apply defaults
	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool"}
	}

	// Resolve port_via service and port
	var portViaService *config.ServiceConfig
	var portViaPort int
	if spec.PortVia != "" {
		// Explicit port_via: look up the service and use its http/https port
		if svc, ok := cfg.Services[spec.PortVia]; ok {
			portViaService = &svc
			// Use the HTTP or HTTPS port from the explicit port_via service
			if cfg.Runtime.UseHTTPS {
				portViaPort = svc.Ports["https"]
				if portViaPort == 0 {
					portViaPort = svc.Ports["http"]
				}
			} else {
				portViaPort = svc.Ports["http"]
				if portViaPort == 0 {
					portViaPort = svc.Ports["https"]
				}
			}
		}
	} else {
		// Auto-detect: single infra service with ports.http == 80 or ports.https == 443
		portViaService, portViaPort = autoDetectPortVia(cfg)
	}

	// Get ordered services (inline deploy-order logic to avoid import cycle with stack)
	ordered := getServicesInDeployOrder(cfg, include)
	if len(ordered) == 0 {
		return ""
	}

	// Build hide sets for fast lookup
	hideSet := make(map[string]bool)
	for _, h := range spec.Hide {
		hideSet[h] = true
	}

	var subgroups []string

	for _, svcName := range ordered {
		if hideSet[svcName] {
			continue
		}

		svc := cfg.Services[svcName]
		if !svc.Enabled {
			continue
		}

		hostKey := svc.DisplayHostKey()
		portKey := svc.DisplayPortKey()

		host := svc.Hosts[hostKey]
		port := svc.Ports[portKey]

		// Skip if service has no title and no paths (not intended for surface)
		// Services without an info block don't surface in the dashboard
		if svc.Info.Title == "" && len(svc.Info.Paths) == 0 {
			continue
		}

		// Skip if neither host nor port, and no paths
		if host == "" && port == 0 && len(svc.Info.Paths) == 0 {
			continue
		}

		// Build main URL row first (if we have host or port)
		var mainRow string
		if host != "" || port != 0 {
			mainRow = buildMainURLRow(cfg, svc, svcName, host, port, portViaService, portViaPort)
		}

		// Skip if we have no main row and no paths
		if mainRow == "" && len(svc.Info.Paths) == 0 {
			continue
		}

		// Build subgroup
		var subgroupLines []string

		// Subgroup title
		title := svc.DisplayTitle(svcName)
		subgroupLines = append(subgroupLines, "")
		subgroupLines = append(subgroupLines, title)

		// Main URL row
		if mainRow != "" {
			subgroupLines = append(subgroupLines, mainRow)
		}

		// Paths
		hidePaths := spec.HidePaths[svcName]
		hidePathsSet := make(map[string]bool)
		for _, p := range hidePaths {
			hidePathsSet[p] = true
		}

		for _, pathItem := range svc.Info.Paths {
			if hidePathsSet[pathItem.Name] {
				continue
			}
			pathRow := buildPathRow(cfg, pathItem, host, port, portViaService, portViaPort)
			if pathRow != "" {
				subgroupLines = append(subgroupLines, pathRow)
			}
		}

		if len(subgroupLines) > 0 {
			subgroups = append(subgroups, strings.Join(subgroupLines, "\n"))
		}
	}

	if len(subgroups) == 0 {
		return ""
	}

	return strings.Join(subgroups, "\n")
}

// getServicesInDeployOrder returns service names ordered by deployment dependencies, grouped by type.
// This inlines the logic from stack.DeployOrder to avoid import cycle with internal/stack.
// For each type in types (e.g., ["app", "tool", "infra"]):
//   - Filters enabled services of that type
//   - Sorts them by DependsOn relationships (topologically)
//   - Appends to result in order
func getServicesInDeployOrder(cfg *config.DevboxConfig, types []string) []string {
	if cfg == nil || cfg.Services == nil {
		return nil
	}

	var result []string

	for _, svcType := range types {
		// Collect enabled services of this type.
		var names []string
		for name, svc := range cfg.Services {
			if svc.Enabled && string(svc.Type) == svcType {
				names = append(names, name)
			}
		}

		// Alphabetically pre-sort for determinism before topological sort.
		slices.Sort(names)

		// Topologically sort by DependsOn.
		ordered, err := config.TopoSortServices(names, cfg.Services)
		if err != nil {
			// Cycle or missing dependency. Fall back to alphabetic silently
			// and log to stderr so debuggable but doesn't interrupt rendering.
			fmt.Fprintf(os.Stderr, "warning: service ordering for type %q: %v; using alphabetic fallback\n", svcType, err)
			ordered = names
		}

		result = append(result, ordered...)
	}

	return result
}

// autoDetectPortVia finds the single infra service with ports.http == 80 or ports.https == 443.
// Returns nil, 0 if 0 or >1 candidates found, or if cfg is nil.
func autoDetectPortVia(cfg *config.DevboxConfig) (*config.ServiceConfig, int) {
	if cfg == nil {
		return nil, 0
	}

	var candidates []*config.ServiceConfig
	var candidatePorts []int

	for _, svc := range cfg.Services {
		if svc.Type != config.ServiceTypeInfra || !svc.Enabled {
			continue
		}
		if svc.Ports["http"] == 80 {
			candidates = append(candidates, &svc)
			candidatePorts = append(candidatePorts, 80)
		} else if svc.Ports["https"] == 443 {
			candidates = append(candidates, &svc)
			candidatePorts = append(candidatePorts, 443)
		}
	}

	if len(candidates) == 1 {
		return candidates[0], candidatePorts[0]
	}

	return nil, 0
}

// buildMainURLRow assembles the main URL row for a service.
// Returns the formatted row or "" if no URL can be assembled.
// URL assembly rules:
// - hosts AND ports → proxied | direct
// - only hosts (app behind proxy) → proxied (requires port_via)
// - only ports → localhost:port
// - neither → skip silently
func buildMainURLRow(cfg *config.DevboxConfig, svc config.ServiceConfig, svcName, host string, port int,
	portVia *config.ServiceConfig, portViaPort int) string {

	var urls []string

	// Proxied URL (if host and port_via available)
	if host != "" && portVia != nil {
		scheme := "http"
		if cfg.Runtime.UseHTTPS {
			scheme = "https"
		}
		portViaURL := buildProxiedURL(scheme, host, portViaPort)
		urls = append(urls, portViaURL)
	}

	// Direct URL (if port available)
	if port > 0 {
		scheme := "http"
		if cfg.Runtime.UseHTTPS {
			scheme = "https"
		}
		directURL := fmt.Sprintf("%s://localhost:%d", scheme, port)
		urls = append(urls, directURL)
	}

	// If we have host but no port and no port_via, skip silently
	// (the service is intended for proxy-only rendering, but proxy not available)

	if len(urls) == 0 {
		return ""
	}

	urlStr := strings.Join(urls, " | ")
	icon := svc.DisplayIcon()
	row := fmt.Sprintf("  %s %s  — %s", icon, svc.DisplayTitle(svcName), urlStr)
	return row
}

// buildProxiedURL constructs a proxied URL with the given scheme and hostname.
// Omits port if it's 80 (http) or 443 (https).
func buildProxiedURL(scheme, host string, port int) string {
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) || port == 0 {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// buildPathRow assembles a sub-path row for a service.
// Returns the formatted row or "" if it cannot be assembled.
func buildPathRow(cfg *config.DevboxConfig, path config.ServiceInfoPath,
	host string, port int, portVia *config.ServiceConfig, portViaPort int) string {

	if path.Path == "" {
		return ""
	}

	// Determine base URL
	var baseURL string

	// Try proxied URL first
	if host != "" && portVia != nil {
		scheme := "http"
		if cfg.Runtime.UseHTTPS {
			scheme = "https"
		}
		baseURL = buildProxiedURL(scheme, host, portViaPort)
	} else if port > 0 {
		// Fall back to direct URL
		scheme := "http"
		if cfg.Runtime.UseHTTPS {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://localhost:%d", scheme, port)
	} else if host != "" {
		// Host-only (no port)
		scheme := "http"
		if cfg.Runtime.UseHTTPS {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://%s", scheme, host)
	}

	if baseURL == "" {
		return ""
	}

	fullURL := baseURL + path.Path
	icon := path.DisplayIcon()
	row := fmt.Sprintf("     %s %s  — %s", icon, path.Name, fullURL)
	return row
}
