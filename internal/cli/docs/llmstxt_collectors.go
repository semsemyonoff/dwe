package docs

import (
	"path/filepath"
	"sort"

	"devbox-cli/internal/core/docs/llmstxt"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"
)

// collectServiceSummaries returns service summaries in deploy order.
// Iterates via config.DeployOrder per the service-iteration rule (never range cfg.Services).
func collectServiceSummaries(cfg *config.DevboxConfig, _ i18n.Translator, _ string) []llmstxt.ServiceSummary {
	if cfg == nil {
		return nil
	}
	names := config.DeployOrder(cfg, []string{"app", "tool", "infra"})
	result := make([]llmstxt.ServiceSummary, 0, len(names))
	for _, name := range names {
		svc := cfg.Services[name]
		result = append(result, llmstxt.ServiceSummary{
			Name:  name,
			Type:  string(svc.Type),
			Title: svc.Info.Title,
		})
	}
	return result
}

// collectCommandSummaries returns non-private command summaries sorted by ID.
// Descriptions are resolved via tr.CommandDescription for i18n support.
func collectCommandSummaries(reg *usercommands.Registry, tr i18n.Translator, locale string) []llmstxt.CommandSummary {
	if reg == nil {
		return nil
	}
	cmds := reg.List("")
	result := make([]llmstxt.CommandSummary, 0, len(cmds))
	for _, cmd := range cmds {
		result = append(result, llmstxt.CommandSummary{
			ID:          cmd.ID,
			Description: tr.CommandDescription(locale, cmd.ID, cmd.Description),
			Group:       cmd.Group,
		})
	}
	return result
}

// collectInfoSummary gathers project info: title from devbox.yml, primary URLs and
// hosts from service configs. Returns nil when cfg is nil or no meaningful data exists.
// root is the project root directory; info.yml is loaded from root/devbox/info.yml when
// non-empty (for potential future use of custom section titles).
func collectInfoSummary(cfg *config.DevboxConfig, root string) *llmstxt.InfoSummary {
	if cfg == nil {
		return nil
	}
	summary := &llmstxt.InfoSummary{
		Title: cfg.Project.FullName(),
	}

	// Load info.yml to check for a custom title in non-standard sections.
	if root != "" {
		infoPath := filepath.Join(root, "devbox", "info.yml")
		if ic, err := config.LoadInfoConfig(infoPath); err == nil {
			for _, sec := range ic.Sections {
				if sec.Title != "" && sec.ID != "urls" && sec.ID != "hosts" {
					summary.Title = sec.Title
					break
				}
			}
		}
	}

	// Collect primary URLs and hosts from services in deploy order.
	for _, name := range config.DeployOrder(cfg, []string{"app", "tool", "infra"}) {
		svc := cfg.Services[name]
		if svc.Info.PrimaryHost != "" {
			summary.URLs = append(summary.URLs, "http://"+svc.Info.PrimaryHost)
		}
		for _, k := range sortedStringKeys(svc.Hosts) {
			summary.Hosts = append(summary.Hosts, svc.Hosts[k])
		}
	}

	if summary.Title == "" && len(summary.URLs) == 0 && len(summary.Hosts) == 0 {
		return nil
	}
	return summary
}

// sortedStringKeys returns the keys of m sorted lexically.
func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
