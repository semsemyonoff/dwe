package render

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
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

	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool"}
	}

	// Resolve port_via service and port.
	var portViaService *config.ServiceConfig
	var portViaPort int
	if spec.PortVia != "" {
		if svc, ok := cfg.Services[spec.PortVia]; ok {
			portViaService = &svc
			if cfg.Runtime.UseHTTPS {
				portViaPort = svc.Ports["https"]
			} else {
				portViaPort = svc.Ports["http"]
			}
		}
	} else {
		portViaService, portViaPort = autoDetectPortVia(cfg)
	}

	ordered := config.DeployOrder(cfg, include)
	if len(ordered) == 0 {
		return ""
	}

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

		hostKey := svc.DisplayHostKey()
		portKey := svc.DisplayPortKey()

		host := svc.Hosts[hostKey]
		port := svc.Ports[portKey]

		if host == "" && port == 0 && len(svc.Info.Paths) == 0 {
			continue
		}

		var mainURL string
		if host != "" || port != 0 {
			mainURL = buildMainURL(cfg, host, port, portViaService, portViaPort)
		}

		hidePathsSet := make(map[string]bool)
		for _, p := range spec.HidePaths[svcName] {
			hidePathsSet[p] = true
		}

		// Collect aligned rows (main + paths).
		type row struct {
			prefix string
			url    string
		}
		var rows []row

		if mainURL != "" {
			prefix := fmt.Sprintf("  %s%s", styles.IconPrefix(svc.DisplayIcon()), svc.DisplayTitle(svcName))
			rows = append(rows, row{prefix, mainURL})
		}

		for _, p := range svc.Info.Paths {
			if hidePathsSet[p.Name] {
				continue
			}
			pathURL := buildPathURL(cfg, p, host, port, portViaService, portViaPort)
			if pathURL == "" {
				continue
			}
			prefix := fmt.Sprintf("     %s%s", styles.IconPrefix(p.DisplayIcon()), p.Name)
			rows = append(rows, row{prefix, pathURL})
		}

		if len(rows) == 0 {
			continue
		}

		// Column-align ' — ' across rows in this subgroup.
		maxW := 0
		for _, r := range rows {
			if w := lipgloss.Width(r.prefix); w > maxW {
				maxW = w
			}
		}

		title := styles.AccentStyle().Bold(true).Render(svc.DisplayTitle(svcName))
		lines := []string{"", title}
		for _, r := range rows {
			pad := strings.Repeat(" ", maxW-lipgloss.Width(r.prefix))
			lines = append(lines, r.prefix+pad+" — "+r.url)
		}

		subgroups = append(subgroups, strings.Join(lines, "\n"))
	}

	if len(subgroups) == 0 {
		return ""
	}

	return strings.Join(subgroups, "\n")
}

// autoDetectPortVia finds the single infra service with ports.http == 80 or ports.https == 443.
// Returns nil, 0 if 0 or >1 candidates found, or if cfg is nil.
func autoDetectPortVia(cfg *config.DevboxConfig) (*config.ServiceConfig, int) {
	if cfg == nil {
		return nil, 0
	}

	var candidates []*config.ServiceConfig

	for _, name := range config.DeployOrder(cfg, []string{"infra"}) {
		svc := cfg.Services[name]
		if !svc.Enabled {
			continue
		}
		if svc.Ports["http"] == 80 || svc.Ports["https"] == 443 {
			candidates = append(candidates, &svc)
		}
	}

	if len(candidates) == 1 {
		// Re-select port based on use_https — detection uses http==80 or https==443
		// as a proxy filter, but the actual port for URL construction must match
		// the scheme. Do NOT fall through to the other key (per plan spec).
		var port int
		if cfg.Runtime.UseHTTPS {
			port = candidates[0].Ports["https"]
		} else {
			port = candidates[0].Ports["http"]
		}
		return candidates[0], port
	}

	return nil, 0
}

func getScheme(useHTTPS bool) string {
	if useHTTPS {
		return "https"
	}
	return "http"
}

// buildMainURL returns the URL portion (without the row prefix) for a service.
// Rules:
//   - hosts AND ports → "<proxied> | <direct>"
//   - only hosts (app behind proxy) → "<proxied>" (requires port_via)
//   - only ports → "http(s)://localhost:<port>"
//   - neither → ""
func buildMainURL(cfg *config.DevboxConfig, host string, port int,
	portVia *config.ServiceConfig, portViaPort int) string {

	scheme := getScheme(cfg.Runtime.UseHTTPS)
	var urls []string

	if host != "" && portVia != nil {
		urls = append(urls, buildProxiedURL(scheme, host, portViaPort))
	}

	if port > 0 {
		urls = append(urls, fmt.Sprintf("%s://localhost:%d", scheme, port))
	}

	if len(urls) == 0 {
		return ""
	}
	return strings.Join(urls, " | ")
}

// buildProxiedURL constructs a proxied URL with the given scheme and hostname.
// Omits port if it's 80 (http) or 443 (https).
func buildProxiedURL(scheme, host string, port int) string {
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) || port == 0 {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// buildPathURL returns the full URL for a sub-path row, or "" if it cannot be
// assembled (e.g. host-only without portVia and no direct port).
func buildPathURL(cfg *config.DevboxConfig, path config.ServiceInfoPath,
	host string, port int, portVia *config.ServiceConfig, portViaPort int) string {

	if path.Path == "" {
		return ""
	}

	scheme := getScheme(cfg.Runtime.UseHTTPS)
	hasHost := host != ""
	hasPort := port > 0
	hasPortVia := portVia != nil

	var baseURL string
	switch {
	case hasHost && hasPortVia:
		baseURL = buildProxiedURL(scheme, host, portViaPort)
	case hasPort:
		baseURL = fmt.Sprintf("%s://localhost:%d", scheme, port)
	case hasHost:
		// Host-only without portVia: skip silently (matches main-row behavior).
		return ""
	}

	if baseURL == "" {
		return ""
	}
	return baseURL + path.Path
}
