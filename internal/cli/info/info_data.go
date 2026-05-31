package info

import (
	"fmt"
	"slices"
	"strings"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
	"github.com/semsemyonoff/devbox/internal/shared/tpl"
)

// buildInfoData builds the structured JSON representation of the info dashboard.
// It mirrors the traversal logic of core/ui/render.Info but produces data instead
// of styled strings. The cli layer is the seam between data and rendering (CLAUDE.md).
func buildInfoData(cfg *config.DevboxConfig, infoCfg *config.InfoConfig) (infoJSON, error) {
	result := infoJSON{
		Title:    cfg.Project.FullName(),
		Sections: []infoSection{},
	}
	for _, section := range infoCfg.Sections {
		sec, contentCount, err := buildSectionData(cfg, section)
		if err != nil {
			return infoJSON{}, err
		}
		if section.HideOnEmpty && contentCount == 0 {
			continue
		}
		result.Sections = append(result.Sections, sec)
	}
	return result, nil
}

func buildSectionData(cfg *config.DevboxConfig, section config.InfoSection) (infoSection, int, error) {
	items, contentCount, err := buildItemsData(cfg, section.Items, section.ID)
	if err != nil {
		return infoSection{}, 0, err
	}
	if items == nil {
		items = []infoItem{}
	}
	sec := infoSection{
		ID:    section.ID,
		Title: section.Title,
		Items: items,
	}
	return sec, contentCount, nil
}

func buildItemsData(cfg *config.DevboxConfig, items []config.InfoItem, sectionID string) ([]infoItem, int, error) {
	var result []infoItem
	contentCount := 0
	for idx, item := range items {
		show, err := tpl.EvalCondition(item.When, cfg)
		if err != nil {
			return nil, 0, fmt.Errorf("section %q items[%d] when: %w", sectionID, idx, err)
		}
		if !show {
			continue
		}
		built, isContent, err := buildItemData(cfg, item, sectionID)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, built...)
		if isContent {
			contentCount++
		}
	}
	return result, contentCount, nil
}

func buildItemData(cfg *config.DevboxConfig, item config.InfoItem, sectionID string) ([]infoItem, bool, error) {
	switch item.Type {
	case "definition":
		value, err := tpl.Render(item.Value, cfg)
		if err != nil {
			return nil, false, fmt.Errorf("section %q definition %q: %w", sectionID, item.Name, err)
		}
		return []infoItem{{Type: "definition", Label: item.Name, Value: value}}, true, nil

	case "info":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return nil, false, fmt.Errorf("section %q info: %w", sectionID, err)
		}
		return []infoItem{{Type: "info", Value: text}}, true, nil

	case "warning":
		text, err := tpl.Render(item.Text, cfg)
		if err != nil {
			return nil, false, fmt.Errorf("section %q warning: %w", sectionID, err)
		}
		return []infoItem{{Type: "warning", Value: text}}, true, nil

	case "separator":
		return nil, false, nil // decorative; omit from JSON

	case "subgroup":
		renderedTitle, err := tpl.Render(item.Title, cfg)
		if err != nil {
			return nil, false, fmt.Errorf("section %q subgroup title: %w", sectionID, err)
		}
		subItems, subContent, err := buildItemsData(cfg, item.Items, sectionID)
		if err != nil {
			return nil, false, err
		}
		if item.SubgroupHideOnEmpty() && subContent == 0 {
			return nil, false, nil
		}
		var out []infoItem
		if renderedTitle != "" {
			out = append(out, infoItem{Type: "subgroup", Label: renderedTitle})
		}
		out = append(out, subItems...)
		return out, !item.IsDecorative(), nil

	case "auto-urls":
		if item.SourceAutoURLsSpec == nil {
			return nil, false, nil
		}
		urls := buildAutoURLsData(cfg, item.SourceAutoURLsSpec)
		return urls, len(urls) > 0, nil

	case "auto-hosts":
		if item.SourceAutoHostsSpec == nil {
			return nil, false, nil
		}
		hosts := buildAutoHostsData(cfg, item.SourceAutoHostsSpec)
		return hosts, len(hosts) > 0, nil

	default:
		return nil, false, fmt.Errorf("unknown item type %q", item.Type)
	}
}

// buildAutoURLsData extracts structured URL data from config.
// Mirrors the traversal in core/ui.renderAutoURLs but returns data instead of styled strings.
// The URL assembly helpers below are duplicated from core/ui/info_auto_urls.go because
// core/ui is a string-only sink layer and cannot be imported by cli for data extraction.
func buildAutoURLsData(cfg *config.DevboxConfig, spec *config.AutoURLsSpec) []infoItem {
	if cfg == nil || spec == nil {
		return nil
	}

	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool"}
	}

	portViaService, portViaPort := autoDetectPortViaData(cfg)
	if spec.PortVia != "" {
		if svc, ok := cfg.Services[spec.PortVia]; ok {
			portViaService = &svc
			if cfg.Runtime.UseHTTPS {
				portViaPort = svc.Ports["https"]
			} else {
				portViaPort = svc.Ports["http"]
			}
		}
	}

	ordered := config.DeployOrder(cfg, include)
	hideSet := make(map[string]bool, len(spec.Hide))
	for _, h := range spec.Hide {
		hideSet[h] = true
	}

	var items []infoItem
	for _, svcName := range ordered {
		if hideSet[svcName] {
			continue
		}
		svc := cfg.Services[svcName]
		host := svc.Hosts[svc.DisplayHostKey()]
		port := svc.Ports[svc.DisplayPortKey()]

		if host == "" && port == 0 && len(svc.Info.Paths) == 0 {
			continue
		}

		title := svc.DisplayTitle(svcName)
		if host != "" || port != 0 {
			mainURL := buildMainURLData(cfg, host, port, portViaService, portViaPort)
			if mainURL != "" {
				items = append(items, infoItem{Type: "url", Label: title, Value: mainURL})
			}
		}

		hidePathsSet := make(map[string]bool)
		for _, p := range spec.HidePaths[svcName] {
			hidePathsSet[p] = true
		}
		for _, p := range svc.Info.Paths {
			if hidePathsSet[p.Name] {
				continue
			}
			pathURL := buildPathURLData(cfg, p, host, port, portViaService, portViaPort)
			if pathURL == "" {
				continue
			}
			items = append(items, infoItem{Type: "url", Label: title + "/" + p.Name, Value: pathURL})
		}
	}
	return items
}

// buildAutoHostsData extracts hostname data from config.
// Mirrors the traversal in core/ui.renderAutoHosts but returns data instead of styled strings.
func buildAutoHostsData(cfg *config.DevboxConfig, spec *config.AutoHostsSpec) []infoItem {
	if cfg == nil || spec == nil {
		return nil
	}
	include := spec.Include
	if len(include) == 0 {
		include = []string{"app", "tool", "infra"}
	}
	ip := spec.IP
	if ip == "" {
		ip = "127.0.0.1"
	}
	hideSet := make(map[string]bool, len(spec.Hide))
	for _, h := range spec.Hide {
		hideSet[h] = true
	}
	ordered := config.DeployOrder(cfg, include)
	seen := make(map[string]bool)
	var items []infoItem
	for _, svcName := range ordered {
		if hideSet[svcName] {
			continue
		}
		svc := cfg.Services[svcName]
		var hostKeys []string
		for key := range svc.Hosts {
			hostKeys = append(hostKeys, key)
		}
		slices.Sort(hostKeys)
		for _, key := range hostKeys {
			hostname := svc.Hosts[key]
			if hostname == "" || hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
				continue
			}
			if !seen[hostname] {
				items = append(items, infoItem{Type: "host", Label: hostname, Value: ip})
				seen[hostname] = true
			}
		}
	}
	return items
}

// URL building helpers — logic duplicated from core/ui/info_auto_urls.go.
// core/ui is a string-only sink (returns styled strings); the cli layer owns data extraction.

func autoDetectPortViaData(cfg *config.DevboxConfig) (*config.ServiceConfig, int) {
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

func getSchemeData(useHTTPS bool) string {
	if useHTTPS {
		return "https"
	}
	return "http"
}

func buildMainURLData(cfg *config.DevboxConfig, host string, port int,
	portVia *config.ServiceConfig, portViaPort int) string {
	scheme := getSchemeData(cfg.Runtime.UseHTTPS)
	var urls []string
	if host != "" && portVia != nil {
		urls = append(urls, buildProxiedURLData(scheme, host, portViaPort))
	}
	if port > 0 {
		urls = append(urls, fmt.Sprintf("%s://localhost:%d", scheme, port))
	}
	if len(urls) == 0 {
		return ""
	}
	return strings.Join(urls, " | ")
}

func buildProxiedURLData(scheme, host string, port int) string {
	if (scheme == "http" && port == 80) || (scheme == "https" && port == 443) || port == 0 {
		return fmt.Sprintf("%s://%s", scheme, host)
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

func buildPathURLData(cfg *config.DevboxConfig, path config.ServiceInfoPath,
	host string, port int, portVia *config.ServiceConfig, portViaPort int) string {
	if path.Path == "" {
		return ""
	}
	scheme := getSchemeData(cfg.Runtime.UseHTTPS)
	var baseURL string
	switch {
	case host != "" && portVia != nil:
		baseURL = buildProxiedURLData(scheme, host, portViaPort)
	case port > 0:
		baseURL = fmt.Sprintf("%s://localhost:%d", scheme, port)
	default:
		return ""
	}
	return baseURL + path.Path
}
