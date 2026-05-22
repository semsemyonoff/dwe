package ai

import (
	"testing"

	"devbox-cli/internal/config"
)

func TestSelectServices_typeDefaults(t *testing.T) {
	t.Run("all types selected by default when dir is set", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"app1":    {Enabled: true, Type: config.ServiceTypeApp, Dir: "services/app1"},
			"db":      {Enabled: true, Type: config.ServiceTypeInfra, Dir: "services/db"},
			"adminer": {Enabled: true, Type: config.ServiceTypeTool, Dir: "services/adminer"},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 3 {
			t.Errorf("selected=%v want all three", selected)
		}
	})

	t.Run("explicit ai.enabled=false drops as ai-disabled", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"app1": {
				Enabled: true, Type: config.ServiceTypeApp, Dir: "services/app1",
				Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: new(bool)}},
			},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 0 {
			t.Errorf("selected=%v want none", selected)
		}
		if len(skipped) != 1 || skipped[0].Reason != "ai-disabled" {
			t.Errorf("skipped=%v want [{app1 ai-disabled}]", skipped)
		}
	})

	t.Run("disabled service dropped regardless of type", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"db": {Enabled: false, Type: config.ServiceTypeInfra, Dir: "services/db"},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 0 {
			t.Errorf("selected=%v", selected)
		}
		if len(skipped) != 1 || skipped[0].Reason != "service-disabled" {
			t.Errorf("skipped=%v", skipped)
		}
	})

	t.Run("shallowest extends wins on dir collision (apps)", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"parent": {Enabled: true, Type: config.ServiceTypeApp, Dir: "services/main"},
			"child":  {Enabled: true, Type: config.ServiceTypeApp, Dir: "services/main", Extends: "parent"},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "parent" {
			t.Errorf("selected=%v want [parent]", selected)
		}
	})
}
