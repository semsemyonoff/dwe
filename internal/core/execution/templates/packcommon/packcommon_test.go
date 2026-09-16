package packcommon

import (
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
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

func commandIDs(cmds []model.CommandSummary) []string {
	var out []string
	for _, c := range cmds {
		out = append(out, c.ID)
	}
	return out
}

func groupIDs(groups []model.CommandGroupSummary) []string {
	var out []string
	for _, g := range groups {
		out = append(out, g.ID)
	}
	return out
}

func TestTemplateDataServiceCommands(t *testing.T) {
	tests := []struct {
		name string
		data TemplateData
		want []string
	}{
		{
			name: "key equals container",
			data: TemplateData{
				Resolved:   "admin",
				ServiceCfg: config.ServiceConfig{Container: "admin"},
				Commands: []model.CommandSummary{
					{ID: "admin.lint", Service: "admin"},
					{ID: "api.test", Service: "api"},
				},
			},
			want: []string{"admin.lint"},
		},
		{
			name: "key differs from container (magento)",
			data: TemplateData{
				Resolved:   "magento",
				ServiceCfg: config.ServiceConfig{Container: "app-magento"},
				Commands: []model.CommandSummary{
					{ID: "services.magento.cache.flush", Service: "app-magento"},
					{ID: "services.magento.db.dump", Service: "app-magento"},
					{ID: "services.other.x", Service: "magento"},
				},
			},
			want: []string{"services.magento.cache.flush", "services.magento.db.dump"},
		},
		{
			name: "key differs from container (podlapka map)",
			data: TemplateData{
				Resolved:   "map",
				ServiceCfg: config.ServiceConfig{Container: "map-edit"},
				Commands: []model.CommandSummary{
					{ID: "map.build", Service: "map-edit"},
					{ID: "map.serve", Service: "map"},
				},
			},
			want: []string{"map.build"},
		},
		{
			name: "unrendered param service excluded",
			data: TemplateData{
				Resolved:   "admin",
				ServiceCfg: config.ServiceConfig{Container: "app-admin"},
				Commands: []model.CommandSummary{
					{ID: "generic.exec", Service: "app-${param.service}"},
					{ID: "admin.lint", Service: "app-admin"},
				},
			},
			want: []string{"admin.lint"},
		},
		{
			name: "command without service excluded",
			data: TemplateData{
				Resolved:   "admin",
				ServiceCfg: config.ServiceConfig{Container: "admin"},
				Commands: []model.CommandSummary{
					{ID: "admin.pull", Type: "shell"},
					{ID: "admin.lint", Service: "admin"},
				},
			},
			want: []string{"admin.lint"},
		},
		{
			name: "extends chain joins on rendering service container",
			data: TemplateData{
				Service:    "base",
				Resolved:   "child",
				ServiceCfg: config.ServiceConfig{Container: "app-child", Extends: "base"},
				Commands: []model.CommandSummary{
					{ID: "base.run", Service: "base"},
					{ID: "child.run", Service: "child"},
					{ID: "child.test", Service: "app-child"},
				},
			},
			want: []string{"child.test"},
		},
		{
			name: "empty container matches nothing",
			data: TemplateData{
				Commands: []model.CommandSummary{{ID: "host.task"}},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandIDs(tt.data.ServiceCommands()); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ServiceCommands() = %v, want %v", got, tt.want)
			}
		})
	}
}

// magentoHub and hubServices model magento's layout: magento-debug extends
// magento and shares its hub directory, node is a second hub, db has no hub.
var (
	magentoHub  = config.ServiceConfig{Container: "app-magento", Dir: "./services/magento"}
	hubServices = map[string]config.ServiceConfig{
		"magento":       magentoHub,
		"magento-debug": {Container: "app-magento-debug", Dir: "./services/magento", Extends: "magento"},
		"node":          {Container: "app-node", Dir: "./services/node"},
		"db":            {Container: "db"},
	}
)

func TestTemplateDataServiceCommandGroups(t *testing.T) {
	tests := []struct {
		name string
		data TemplateData
		want []string
	}{
		{
			name: "two-level collapse",
			data: TemplateData{
				ServiceCfg: config.ServiceConfig{Container: "admin"},
				Commands: []model.CommandSummary{
					{ID: "admin.build", Service: "admin"},
					{ID: "admin.lint.eslint", Service: "admin"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "admin", Description: "Admin SPA", Count: 13},
					{ID: "admin.lint", Title: "Lint", Count: 5},
				},
			},
			want: []string{"admin"},
		},
		{
			name: "two independent groups both kept",
			data: TemplateData{
				ServiceCfg: config.ServiceConfig{Container: "admin"},
				Commands: []model.CommandSummary{
					{ID: "admin.build", Service: "admin"},
					{ID: "db.migrate", Service: "admin"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "admin", Title: "admin", Count: 1},
					{ID: "db", Title: "db", Count: 10},
				},
			},
			want: []string{"admin", "db"},
		},
		{
			name: "dot boundary: admin does not own administration",
			data: TemplateData{
				ServiceCfg: config.ServiceConfig{Container: "backoffice"},
				Commands: []model.CommandSummary{
					{ID: "administration.users", Service: "backoffice"},
					{ID: "admin.build", Service: "admin"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "admin", Title: "admin", Count: 1},
					{ID: "administration", Title: "administration", Count: 1},
				},
			},
			want: []string{"administration"},
		},
		{
			name: "shared parent over another hub's subgroup does not absorb",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.magento.reindex", Service: "app-magento"},
					{ID: "services.magento.db.dump", Service: "app-magento"},
					{ID: "services.node.build", Service: "app-node"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services", Description: "Per-service commands", Count: 3},
					{ID: "services.magento", Title: "magento", Count: 2},
					{ID: "services.magento.db", Title: "db", Count: 1},
					{ID: "services.node", Title: "node", Count: 1},
				},
			},
			want: []string{"services.magento"},
		},
		{
			// magento's real shape: config.set targets db, which has no hub.
			name: "commands for a container without a hub do not block the collapse",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.magento.bootstrap", Type: "workflow"},
					{ID: "services.magento.dbdump", Service: "db"},
					{ID: "services.magento.cache.flush", Service: "app-magento"},
					{ID: "services.magento.config.get", Service: "app-magento"},
					{ID: "services.magento.config.set", Service: "db"},
					{ID: "services.magento.dbtools.dump", Service: "db"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services.magento", Description: "Magento", Count: 6},
					{ID: "services.magento.cache", Description: "Cache", Count: 1},
					{ID: "services.magento.config", Description: "Config", Count: 2},
					{ID: "services.magento.dbtools", Description: "DB tools", Count: 1},
				},
			},
			want: []string{"services.magento"},
		},
		{
			name: "shared parent declaring another hub's command itself does not absorb",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.node-build", Service: "app-node"},
					{ID: "services.magento.reindex", Service: "app-magento"},
					{ID: "services.magento.cache.flush", Service: "app-magento"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services", Description: "Per-service commands", Count: 3},
					{ID: "services.magento", Description: "Magento", Count: 2},
					{ID: "services.magento.cache", Description: "Cache", Count: 1},
				},
			},
			want: []string{"services.magento"},
		},
		{
			// A file mixing both hubs' commands must not read as this hub's.
			name: "shared parent mixing this hub's and another hub's commands does not absorb",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.magento-status", Service: "app-magento"},
					{ID: "services.node-build", Service: "app-node"},
					{ID: "services.magento.reindex", Service: "app-magento"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services", Description: "Per-service commands", Count: 3},
					{ID: "services.magento", Description: "Magento", Count: 1},
				},
			},
			want: []string{"services", "services.magento"},
		},
		{
			name: "extends sibling sharing the hub directory is not another hub",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.magento.reindex", Service: "app-magento"},
					{ID: "services.magento.debug.xdebug", Service: "app-magento-debug"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services.magento", Description: "Magento", Count: 2},
					{ID: "services.magento.debug", Description: "Debug", Count: 1},
				},
			},
			want: []string{"services.magento"},
		},
		{
			name: "shared parent kept for a hub command no descendant covers",
			data: TemplateData{
				ServiceCfg: magentoHub,
				Services:   hubServices,
				Commands: []model.CommandSummary{
					{ID: "services.status", Service: "app-magento"},
					{ID: "services.magento.reindex", Service: "app-magento"},
					{ID: "services.node.build", Service: "app-node"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "services", Description: "Per-service commands", Count: 3},
					{ID: "services.magento", Title: "magento", Count: 1},
					{ID: "services.node", Title: "node", Count: 1},
				},
			},
			want: []string{"services", "services.magento"},
		},
		{
			name: "no qualifying group",
			data: TemplateData{
				ServiceCfg: config.ServiceConfig{Container: "admin"},
				Commands: []model.CommandSummary{
					{ID: "admin.pull", Type: "shell"},
					{ID: "generic.exec", Service: "app-${param.service}"},
				},
				CommandGroups: []model.CommandGroupSummary{
					{ID: "admin", Title: "admin", Count: 1},
					{ID: "generic", Title: "generic", Count: 1},
				},
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupIDs(tt.data.ServiceCommandGroups()); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ServiceCommandGroups() = %v, want %v", got, tt.want)
			}
		})
	}
}
