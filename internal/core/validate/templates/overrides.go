package templates

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"devbox-cli/internal/core/validate"
)

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
		Hint:     fmt.Sprintf("sourced from devbox/templates/%s/%s.local/", kind, packName),
	}
}
