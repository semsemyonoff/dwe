package docs

import (
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
)

// docsCfgLang returns the language preference from the user config for the
// given project root. Returns "" when projectRoot is empty, the config is
// absent, or the config has no language set. Used by show/list/search/export/
// llmstxt to feed into i18n.ResolveLocale as the cfgLang argument.
// NOTE: docs.go's runDocsTUI uses a slightly different lookup (it also reads
// MermaidTheme) and keeps its own inline block.
func docsCfgLang(projectRoot string) string {
	if projectRoot == "" {
		return ""
	}
	ucfg, err := userpkg.Load(projectRoot)
	if err != nil || ucfg == nil {
		return ""
	}
	return ucfg.Language
}
