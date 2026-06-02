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
func renderAutoURLs(cfg *config.DweConfig, spec *config.AutoURLsSpec) string {
	if cfg == nil || spec == nil {
		return ""
	}

	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool"}
	}

	// Resolve port_via service. The listener port on the proxy is chosen
	// per-routed-service inside buildMainURL/buildPathURL, because the
	// routed service's info.scheme can pin the proxy listener key.
	var portViaService *config.ServiceConfig
	if spec.PortVia != "" {
		if svc, ok := cfg.Services[spec.PortVia]; ok {
			portViaService = &svc
		}
	} else {
		portViaService = autoDetectPortVia(cfg)
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
		port := svc.Port(portKey)

		if host == "" && port == 0 && len(svc.Info.Paths) == 0 {
			continue
		}

		var mainURL string
		if host != "" || port != 0 {
			mainURL = buildMainURL(cfg, svc, portKey, host, port, portViaService)
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
			pathURL := buildPathURL(cfg, svc, portKey, p, host, port, portViaService)
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
// Returns nil if 0 or >1 candidates found, or if cfg is nil. The chosen proxy
// listener port is resolved per-routed-service later via resolveProxyTarget.
func autoDetectPortVia(cfg *config.DweConfig) *config.ServiceConfig {
	if cfg == nil {
		return nil
	}

	var candidates []*config.ServiceConfig

	for _, name := range config.DeployOrder(cfg, []string{"infra"}) {
		svc := cfg.Services[name]
		if !svc.Enabled {
			continue
		}
		if svc.Port("http") == 80 || svc.Port("https") == 443 {
			candidates = append(candidates, &svc)
		}
	}

	if len(candidates) == 1 {
		return candidates[0]
	}

	return nil
}

// resolveProxyTarget picks the proxy listener key ("http" or "https") and the
// corresponding port number for routing `routed` through `proxy`. The key is
// chosen as:
//   - routed.Info.Scheme when it is "http" or "https" (per-service pin),
//   - else the global proxyPortKey(useHTTPS) default.
//
// This is the seam that lets a single shared reverse-proxy serve mixed-scheme
// stacks: a routed service can declare info.scheme: https to be looked up
// against the proxy's https listener while its sibling stays on http.
func resolveProxyTarget(routed, proxy config.ServiceConfig, useHTTPS bool) (string, int) {
	key := routed.Info.Scheme
	if key != "http" && key != "https" {
		key = proxyPortKey(useHTTPS)
	}
	return key, proxy.Port(key)
}

// buildMainURL returns the URL portion (without the row prefix) for a service.
// Rules:
//   - hosts AND ports → "<proxied> | <direct>"
//   - only hosts (app behind proxy) → "<proxied>" (requires port_via)
//   - only ports → "http(s)://localhost:<port>"
//   - neither → ""
//
// The proxied URL uses proxyScheme — derived from the routed service's
// info.scheme (per-service pin), then a per-port scheme override on the
// proxy's chosen listener, then runtime.use_https. The proxy service's own
// info.scheme is intentionally NOT consulted (it would otherwise leak to
// every routed-through service). The direct URL uses svc.EffectiveScheme so
// per-service info.scheme and per-port overrides on the routed service apply.
func buildMainURL(cfg *config.DweConfig, svc config.ServiceConfig, portKey, host string, port int,
	portVia *config.ServiceConfig) string {

	var urls []string

	if host != "" && portVia != nil {
		proxyKey, proxyPort := resolveProxyTarget(svc, *portVia, cfg.Runtime.UseHTTPS)
		urls = append(urls, buildProxiedURL(proxyScheme(svc, *portVia, proxyKey, cfg.Runtime.UseHTTPS), host, proxyPort))
	}

	if port > 0 {
		directScheme := svc.EffectiveScheme(portKey, cfg.Runtime.UseHTTPS)
		urls = append(urls, fmt.Sprintf("%s://localhost:%d", directScheme, port))
	}

	if len(urls) == 0 {
		return ""
	}
	return strings.Join(urls, " | ")
}

// proxyScheme returns the scheme used for proxied URLs when `routed` is routed
// through `proxy` on the listener identified by `key`. Precedence:
//  1. routed.Info.Scheme — per-service pin owned by the routed service;
//  2. proxy.PortScheme(key) — per-port override on the proxy's listener;
//  3. useHTTPS fallback.
//
// proxy.Info.Scheme is deliberately skipped: the proxy's service-level scheme
// describes the proxy's own dashboard URL row and must not leak onto every
// service routed through it.
func proxyScheme(routed, proxy config.ServiceConfig, key string, useHTTPS bool) string {
	if s := routed.Info.Scheme; s == "http" || s == "https" {
		return s
	}
	if sch := proxy.PortScheme(key); sch != "" {
		return sch
	}
	if useHTTPS {
		return "https"
	}
	return "http"
}

// proxyPortKey returns the well-known port key on a reverse-proxy service.
func proxyPortKey(useHTTPS bool) string {
	if useHTTPS {
		return "https"
	}
	return "http"
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
func buildPathURL(cfg *config.DweConfig, svc config.ServiceConfig, portKey string, path config.ServiceInfoPath,
	host string, port int, portVia *config.ServiceConfig) string {

	if path.Path == "" {
		return ""
	}

	hasHost := host != ""
	hasPort := port > 0
	hasPortVia := portVia != nil

	var baseURL string
	switch {
	case hasHost && hasPortVia:
		proxyKey, proxyPort := resolveProxyTarget(svc, *portVia, cfg.Runtime.UseHTTPS)
		baseURL = buildProxiedURL(proxyScheme(svc, *portVia, proxyKey, cfg.Runtime.UseHTTPS), host, proxyPort)
	case hasPort:
		directScheme := svc.EffectiveScheme(portKey, cfg.Runtime.UseHTTPS)
		baseURL = fmt.Sprintf("%s://localhost:%d", directScheme, port)
	case hasHost:
		// Host-only without portVia: skip silently (matches main-row behavior).
		return ""
	}

	if baseURL == "" {
		return ""
	}
	return baseURL + path.Path
}
