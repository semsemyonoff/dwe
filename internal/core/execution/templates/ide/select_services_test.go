package ide

import (
	"testing"

	"devbox-cli/internal/core/project/config"
)

func TestSelectServices_typeDefaults(t *testing.T) {
	t.Run("apps selected by default; tool and infra dropped as ide-policy", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"app1":    {Enabled: true, Type: config.ServiceTypeApp, Dir: "services/app1"},
			"db":      {Enabled: true, Type: config.ServiceTypeInfra, Dir: "services/db"},
			"adminer": {Enabled: true, Type: config.ServiceTypeTool, Dir: "services/adminer"},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "app1" {
			t.Errorf("selected=%v want [app1]", selected)
		}
		policies := map[string]string{}
		for _, sk := range skipped {
			policies[sk.Name] = sk.Reason
		}
		if policies["db"] != "ide-policy" || policies["adminer"] != "ide-policy" {
			t.Errorf("expected ide-policy for db+adminer; got skipped=%v", skipped)
		}
	})

	t.Run("tool with explicit ide.enabled=true is selected", func(t *testing.T) {
		bTrue := true
		svcs := map[string]config.ServiceConfig{
			"adminer": {
				Enabled: true, Type: config.ServiceTypeTool, Dir: "services/adminer",
				Render: config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: &bTrue}},
			},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "adminer" {
			t.Errorf("selected=%v want [adminer]", selected)
		}
	})

	t.Run("app with explicit ide.enabled=false reports ide-disabled", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"app1": {
				Enabled: true, Type: config.ServiceTypeApp, Dir: "services/app1",
				Render: config.ServiceRenderConfig{IDE: config.ServiceIDEConfig{Enabled: new(bool)}},
			},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 0 {
			t.Errorf("selected=%v want none", selected)
		}
		if len(skipped) != 1 || skipped[0].Reason != "ide-disabled" {
			t.Errorf("skipped=%v want [{app1 ide-disabled}]", skipped)
		}
	})
}
