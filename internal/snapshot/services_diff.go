package snapshot

import (
	"sort"
	"strings"

	"devbox-cli/internal/config"
)

// ServiceEnabledDiff records a service whose Enabled flag differs between the
// manifest's captured state and the current project's effective config.
type ServiceEnabledDiff struct {
	Name            string
	ManifestEnabled bool
	LocalEnabled    bool
}

// ServicesDiff is the typed result of comparing a snapshot manifest's captured
// service set against the current project's effective service map. All three
// slices are sorted by service Name for deterministic output.
type ServicesDiff struct {
	// OnlyInSnapshot lists service names present in the manifest but absent
	// from the current project — restoring is likely to leave dangling
	// artifacts referencing services that no longer exist.
	OnlyInSnapshot []string
	// OnlyLocal lists service names present in the current project but
	// absent from the manifest — restore will not touch them, but the user
	// may expect them to be reset to a known state.
	OnlyLocal []string
	// EnabledDiff lists services present in both sides whose Enabled flag
	// differs.
	EnabledDiff []ServiceEnabledDiff
}

// IsEmpty reports whether all three diff groups are empty.
func (d ServicesDiff) IsEmpty() bool {
	return len(d.OnlyInSnapshot) == 0 && len(d.OnlyLocal) == 0 && len(d.EnabledDiff) == 0
}

// DiffServices compares the manifest's captured services against the current
// project's effective service map. The comparison is config-blind beyond the
// service Name and Enabled flag — it does not inspect ports, hosts, or any
// other ServiceConfig field. The current map shape mirrors
// config.DevboxConfig.Services so callers can pass cfg.Services directly.
func DiffServices(manifest []ServiceSnapshot, current map[string]config.ServiceConfig) ServicesDiff {
	manifestByName := make(map[string]ServiceSnapshot, len(manifest))
	for _, s := range manifest {
		manifestByName[s.Name] = s
	}

	currentNames := make([]string, 0, len(current))
	for name := range current {
		currentNames = append(currentNames, name)
	}
	sort.Strings(currentNames)

	manifestNames := make([]string, 0, len(manifest))
	for _, s := range manifest {
		manifestNames = append(manifestNames, s.Name)
	}
	sort.Strings(manifestNames)

	var d ServicesDiff
	for _, name := range manifestNames {
		if _, ok := current[name]; !ok {
			d.OnlyInSnapshot = append(d.OnlyInSnapshot, name)
		}
	}
	for _, name := range currentNames {
		if _, ok := manifestByName[name]; !ok {
			d.OnlyLocal = append(d.OnlyLocal, name)
		}
	}
	for _, name := range manifestNames {
		cur, ok := current[name]
		if !ok {
			continue
		}
		ms := manifestByName[name]
		if ms.Enabled != cur.Enabled {
			d.EnabledDiff = append(d.EnabledDiff, ServiceEnabledDiff{
				Name:            name,
				ManifestEnabled: ms.Enabled,
				LocalEnabled:    cur.Enabled,
			})
		}
	}
	return d
}

// FormatServicesDiff renders a human-readable summary of the diff suitable for
// CLI prompts, inspect output, and validator hint text. Returns an empty
// string when the diff is empty.
func FormatServicesDiff(d ServicesDiff) string {
	if d.IsEmpty() {
		return ""
	}
	var parts []string
	if len(d.OnlyInSnapshot) > 0 {
		parts = append(parts, "only in snapshot: "+strings.Join(d.OnlyInSnapshot, ", "))
	}
	if len(d.OnlyLocal) > 0 {
		parts = append(parts, "only local: "+strings.Join(d.OnlyLocal, ", "))
	}
	if len(d.EnabledDiff) > 0 {
		flips := make([]string, 0, len(d.EnabledDiff))
		for _, e := range d.EnabledDiff {
			flips = append(flips, e.Name+" (snapshot="+enabledLabel(e.ManifestEnabled)+", local="+enabledLabel(e.LocalEnabled)+")")
		}
		parts = append(parts, "enabled differs: "+strings.Join(flips, ", "))
	}
	return strings.Join(parts, "; ")
}

func enabledLabel(b bool) string {
	if b {
		return "enabled"
	}
	return "disabled"
}
