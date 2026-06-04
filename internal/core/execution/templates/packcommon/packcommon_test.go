package packcommon

import (
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestExtendsDepth(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"root":  {},
		"mid":   {Extends: "root"},
		"leaf":  {Extends: "mid"},
		"loopA": {Extends: "loopB"},
		"loopB": {Extends: "loopA"},
	}
	tests := []struct {
		name      string
		wantDepth int
		wantCap   bool
	}{
		{"root", 0, false},
		{"mid", 1, false},
		{"leaf", 2, false},
		{"unknown", 0, false},
		{"loopA", 32, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			depth, capped := ExtendsDepth(services, tt.name)
			if depth != tt.wantDepth || capped != tt.wantCap {
				t.Fatalf("ExtendsDepth(%q) = (%d,%v), want (%d,%v)", tt.name, depth, capped, tt.wantDepth, tt.wantCap)
			}
		})
	}
}

func TestExtendsRoot(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"root": {},
		"mid":  {Extends: "root"},
		"leaf": {Extends: "mid"},
	}
	for name, want := range map[string]string{
		"root":    "root",
		"mid":     "root",
		"leaf":    "root",
		"unknown": "unknown",
	} {
		if got := ExtendsRoot(services, name); got != want {
			t.Errorf("ExtendsRoot(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestImplicitPackCandidates(t *testing.T) {
	services := map[string]config.ServiceConfig{
		"web":  {Extends: "base"},
		"base": {},
	}
	got := ImplicitPackCandidates(services, "web")
	want := []string{"web", "base", "default"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ImplicitPackCandidates = %v, want %v", got, want)
	}

	// Invalid names in the chain are skipped silently; "default" still appended.
	bad := map[string]config.ServiceConfig{"x/y": {}}
	if got := ImplicitPackCandidates(bad, "x/y"); !reflect.DeepEqual(got, []string{"default"}) {
		t.Fatalf("ImplicitPackCandidates(invalid) = %v, want [default]", got)
	}

	// A cyclic extends chain must terminate (maxDepth bound) and the seen-set
	// must dedup so each name appears once before "default".
	cyclic := map[string]config.ServiceConfig{
		"loopA": {Extends: "loopB"},
		"loopB": {Extends: "loopA"},
	}
	if got := ImplicitPackCandidates(cyclic, "loopA"); !reflect.DeepEqual(got, []string{"loopA", "loopB", "default"}) {
		t.Fatalf("ImplicitPackCandidates(cyclic) = %v, want [loopA loopB default]", got)
	}
}

func TestTemplateDataServiceAccessors(t *testing.T) {
	d := TemplateData{Services: map[string]config.ServiceConfig{
		"app1":  {Type: config.ServiceTypeApp},
		"tool1": {Type: config.ServiceTypeTool},
		"infra": {Type: config.ServiceTypeInfra},
	}}
	if got := d.AppServices(); len(got) != 1 || got["app1"].Type != config.ServiceTypeApp {
		t.Errorf("AppServices = %v", got)
	}
	if got := d.ToolServices(); len(got) != 1 || got["tool1"].Type != config.ServiceTypeTool {
		t.Errorf("ToolServices = %v", got)
	}
	if got := d.InfraServices(); len(got) != 1 || got["infra"].Type != config.ServiceTypeInfra {
		t.Errorf("InfraServices = %v", got)
	}
}

func TestDryRunRenderNilGuards(t *testing.T) {
	if got := DryRunRender("ai", "/tmp", "pack", nil, TemplateData{Cfg: &config.DweConfig{}}); got != nil {
		t.Errorf("nil manifest should return nil, got %v", got)
	}
}
