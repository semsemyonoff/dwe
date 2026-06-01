package docs

import (
	"sort"

	"github.com/semsemyonoff/dwe/internal/core/docs/llmstxt"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// collectServiceSummaries returns service summaries in deploy order.
// Iterates via config.DeployOrder per the service-iteration rule (never range cfg.Services).
func collectServiceSummaries(cfg *config.DweConfig) []llmstxt.ServiceSummary {
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
		})
	}
	return result
}

// collectInfoSummary gathers project URLs and hosts from service configs.
// Returns nil when cfg is nil or no service contributes a URL or host.
func collectInfoSummary(cfg *config.DweConfig) *llmstxt.InfoSummary {
	if cfg == nil {
		return nil
	}
	summary := &llmstxt.InfoSummary{}

	// Collect primary URLs and hosts from services in deploy order.
	// svc.Info.PrimaryHost is a KEY into svc.Hosts; resolve it to the actual hostname.
	for _, name := range config.DeployOrder(cfg, []string{"app", "tool", "infra"}) {
		svc := cfg.Services[name]
		hostKeys := sortedStringKeys(svc.Hosts)
		for _, k := range hostKeys {
			summary.Hosts = append(summary.Hosts, svc.Hosts[k])
		}
		if host := svc.Hosts[svc.DisplayHostKey()]; host != "" {
			summary.URLs = append(summary.URLs, "http://"+host)
		}
	}

	if len(summary.URLs) == 0 && len(summary.Hosts) == 0 {
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
