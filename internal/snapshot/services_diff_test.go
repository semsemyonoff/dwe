package snapshot

import (
	"reflect"
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func TestDiffServices(t *testing.T) {
	tests := []struct {
		name     string
		manifest []ServiceSnapshot
		current  map[string]config.ServiceConfig
		want     ServicesDiff
	}{
		{
			name: "identical",
			manifest: []ServiceSnapshot{
				{Name: "db", Enabled: true},
				{Name: "main", Enabled: true},
			},
			current: map[string]config.ServiceConfig{
				"db":   {Enabled: true},
				"main": {Enabled: true},
			},
			want: ServicesDiff{},
		},
		{
			name: "only in snapshot",
			manifest: []ServiceSnapshot{
				{Name: "cdn", Enabled: false},
				{Name: "main", Enabled: true},
			},
			current: map[string]config.ServiceConfig{
				"main": {Enabled: true},
			},
			want: ServicesDiff{OnlyInSnapshot: []string{"cdn"}},
		},
		{
			name: "only local",
			manifest: []ServiceSnapshot{
				{Name: "main", Enabled: true},
			},
			current: map[string]config.ServiceConfig{
				"main":   {Enabled: true},
				"search": {Enabled: true},
			},
			want: ServicesDiff{OnlyLocal: []string{"search"}},
		},
		{
			name: "enabled flipped",
			manifest: []ServiceSnapshot{
				{Name: "db", Enabled: true},
				{Name: "main", Enabled: false},
			},
			current: map[string]config.ServiceConfig{
				"db":   {Enabled: false},
				"main": {Enabled: true},
			},
			want: ServicesDiff{
				EnabledDiff: []ServiceEnabledDiff{
					{Name: "db", ManifestEnabled: true, LocalEnabled: false},
					{Name: "main", ManifestEnabled: false, LocalEnabled: true},
				},
			},
		},
		{
			name: "deterministic ordering across all groups",
			manifest: []ServiceSnapshot{
				{Name: "zeta", Enabled: true},
				{Name: "alpha", Enabled: true},
				{Name: "beta", Enabled: false},
			},
			current: map[string]config.ServiceConfig{
				"beta":  {Enabled: true},
				"omega": {Enabled: true},
				"gamma": {Enabled: true},
			},
			want: ServicesDiff{
				OnlyInSnapshot: []string{"alpha", "zeta"},
				OnlyLocal:      []string{"gamma", "omega"},
				EnabledDiff: []ServiceEnabledDiff{
					{Name: "beta", ManifestEnabled: false, LocalEnabled: true},
				},
			},
		},
		{
			name:     "empty both sides",
			manifest: nil,
			current:  nil,
			want:     ServicesDiff{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DiffServices(tc.manifest, tc.current)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("DiffServices() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestServicesDiffIsEmpty(t *testing.T) {
	if !(ServicesDiff{}).IsEmpty() {
		t.Fatal("zero ServicesDiff should be empty")
	}
	if (ServicesDiff{OnlyLocal: []string{"x"}}).IsEmpty() {
		t.Fatal("non-empty ServicesDiff reported empty")
	}
}

func TestFormatServicesDiff(t *testing.T) {
	if got := FormatServicesDiff(ServicesDiff{}); got != "" {
		t.Fatalf("empty diff format = %q, want \"\"", got)
	}
	d := ServicesDiff{
		OnlyInSnapshot: []string{"cdn"},
		OnlyLocal:      []string{"search"},
		EnabledDiff: []ServiceEnabledDiff{
			{Name: "db", ManifestEnabled: true, LocalEnabled: false},
		},
	}
	got := FormatServicesDiff(d)
	for _, want := range []string{
		"only in snapshot: cdn",
		"only local: search",
		"enabled differs: db (snapshot=enabled, local=disabled)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("FormatServicesDiff() = %q, missing %q", got, want)
		}
	}
}
