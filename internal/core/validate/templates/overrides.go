package templates

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// sanitizedCfg returns the config the ide/ai/git dry-run renders must see.
//
// Those three renderers load LoadConfigSanitized at run time (their outputs are
// git-tracked, so a secret must reach a template as its ENC[age:…] marker, never
// as plaintext). The validator dry-runs the very same templates, so it loads the
// same shape — otherwise `dwe validate` would exercise data the renderer never
// produces. A failed sanitized load falls back to ctx.Cfg: the validator's job
// is to report template problems, not to re-report a config load the caller has
// already handled.
func sanitizedCfg(ctx validate.Context) *config.DweConfig {
	if ctx.ConfigPath == "" {
		return ctx.Cfg
	}
	cfg, err := config.LoadConfigSanitized(ctx.ConfigPath)
	if err != nil || cfg == nil {
		return ctx.Cfg
	}
	return cfg
}

// overrideSink returns a sink and a getter that collects rels with fromOverride=true.
func overrideSink() (sink func(rel string, fromOverride bool), get func() []string) {
	var hits []string
	return func(rel string, fromOverride bool) {
			if fromOverride {
				hits = append(hits, rel)
			}
		}, func() []string {
			return hits
		}
}

// overrideDiagnostic builds a single info diagnostic listing override hits.
// Returns nil when there are no hits. Listed basenames are capped at 5 with
// "..." truncation to keep the diagnostics table cell width predictable.
func overrideDiagnostic(domain, kind, packName, target string, hits []string) *validate.Diagnostic {
	if len(hits) == 0 {
		return nil
	}
	bases := make([]string, len(hits))
	for i, h := range hits {
		bases[i] = filepath.Base(h)
	}
	sort.Strings(bases)
	display := bases
	truncated := false
	if len(display) > 5 {
		display = display[:5]
		truncated = true
	}
	listing := strings.Join(display, ", ")
	if truncated {
		listing += ", ..."
	}
	return &validate.Diagnostic{
		Severity: validate.SeverityInfo,
		Domain:   domain,
		Target:   target,
		Message:  fmt.Sprintf("using %d local override(s): %s", len(hits), listing),
		Hint:     fmt.Sprintf("sourced from workspace/templates/%s/%s.local/", kind, packName),
	}
}
