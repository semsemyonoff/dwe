package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devbox-cli/internal/config"
	coresnap "devbox-cli/internal/snapshot"
	"devbox-cli/internal/validate"
)

// snapshotEntry builds a coresnap.Entry on disk under root/snapshots/<name>
// with the given captured services in its manifest.
func snapshotEntry(t *testing.T, root, name string, captured []coresnap.ServiceSnapshot) coresnap.Entry {
	t.Helper()
	dir := filepath.Join(root, "snapshots", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := &coresnap.Manifest{
		Name:      name,
		CreatedAt: time.Now().UTC(),
		Project: coresnap.ProjectInfo{
			Name:     "testproj",
			Services: captured,
		},
	}
	if err := coresnap.SaveManifest(filepath.Join(dir, coresnap.ManifestFileName), m); err != nil {
		t.Fatal(err)
	}
	return coresnap.Entry{Dir: dir, Manifest: m}
}

func TestServicesDiffValidator(t *testing.T) {
	root := t.TempDir()
	captured := []coresnap.ServiceSnapshot{
		{Name: "db", Enabled: true},
		{Name: "main", Enabled: true},
	}
	entry := snapshotEntry(t, root, "snap", captured)

	tests := []struct {
		name     string
		cfg      *config.DevboxConfig
		manifest []coresnap.ServiceSnapshot
		wantDiag bool
		wantHint []string
	}{
		{
			name: "identical: no diag",
			cfg: &config.DevboxConfig{Services: map[string]config.ServiceConfig{
				"db":   {Enabled: true},
				"main": {Enabled: true},
			}},
			manifest: captured,
			wantDiag: false,
		},
		{
			name:     "nil cfg: silent",
			cfg:      nil,
			manifest: captured,
			wantDiag: false,
		},
		{
			name: "no services captured: silent",
			cfg: &config.DevboxConfig{Services: map[string]config.ServiceConfig{
				"db": {Enabled: true},
			}},
			manifest: nil,
			wantDiag: false,
		},
		{
			name: "only in snapshot",
			cfg: &config.DevboxConfig{Services: map[string]config.ServiceConfig{
				"main": {Enabled: true},
			}},
			manifest: captured,
			wantDiag: true,
			wantHint: []string{"only in snapshot: db"},
		},
		{
			name: "only local",
			cfg: &config.DevboxConfig{Services: map[string]config.ServiceConfig{
				"db":     {Enabled: true},
				"main":   {Enabled: true},
				"search": {Enabled: true},
			}},
			manifest: captured,
			wantDiag: true,
			wantHint: []string{"only local: search"},
		},
		{
			name: "enabled flipped",
			cfg: &config.DevboxConfig{Services: map[string]config.ServiceConfig{
				"db":   {Enabled: false},
				"main": {Enabled: true},
			}},
			manifest: captured,
			wantDiag: true,
			wantHint: []string{"enabled differs: db"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := entry
			if tc.manifest == nil {
				e.Manifest = &coresnap.Manifest{Name: "snap", Project: coresnap.ProjectInfo{Name: "testproj"}}
			} else {
				m := *entry.Manifest
				m.Project.Services = tc.manifest
				e.Manifest = &m
			}
			v := &servicesDiffValidator{name: "snap", entry: e, cfg: tc.cfg}
			got := v.Run(validate.Context{})
			if tc.wantDiag {
				if len(got) != 1 {
					t.Fatalf("want 1 diag, got %+v", got)
				}
				d := got[0]
				if d.Severity != validate.SeverityInfo {
					t.Errorf("severity = %v, want info", d.Severity)
				}
				if d.Target != "snap.services_diff" {
					t.Errorf("target = %q, want snap.services_diff", d.Target)
				}
				for _, want := range tc.wantHint {
					if !strings.Contains(d.Hint, want) {
						t.Errorf("hint missing %q: %q", want, d.Hint)
					}
				}
			} else if len(got) != 0 {
				t.Fatalf("want no diag, got %+v", got)
			}
		})
	}
}

// TestServicesDiffValidator_RegisteredInAll asserts the new validator is
// picked up by the All(...) aggregator alongside perSnapshotValidator.
func TestServicesDiffValidator_RegisteredInAll(t *testing.T) {
	root := t.TempDir()
	captured := []coresnap.ServiceSnapshot{
		{Name: "db", Enabled: true},
	}
	snapshotEntry(t, root, "s1", captured)

	cfg := &config.DevboxConfig{Services: map[string]config.ServiceConfig{
		"main": {Enabled: true},
	}}
	all := All(cfg, nil, nil, root, nil, false)
	var diags []validate.Diagnostic
	for _, v := range all {
		diags = append(diags, v.Run(validate.Context{})...)
	}
	if _, ok := findFirst(diags, "s1.services_diff"); !ok {
		t.Fatalf("expected s1.services_diff diagnostic, got %+v", diags)
	}
}
