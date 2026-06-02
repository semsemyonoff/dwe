package ai

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestSelectServices_typeDefaults(t *testing.T) {
	t.Run("only app type selected by default; non-app skipped as ai-policy", func(t *testing.T) {
		svcs := map[string]config.ServiceConfig{
			"app1":    {Enabled: true, Type: config.ServiceTypeApp, Dir: "services/app1"},
			"db":      {Enabled: true, Type: config.ServiceTypeInfra, Dir: "services/db"},
			"adminer": {Enabled: true, Type: config.ServiceTypeTool, Dir: "services/adminer"},
		}
		selected, skipped := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "app1" {
			t.Errorf("selected=%v want [app1]", selected)
		}
		var policySkips []string
		for _, s := range skipped {
			if s.Reason == "ai-policy" {
				policySkips = append(policySkips, s.Name)
			}
		}
		if len(policySkips) != 2 {
			t.Errorf("ai-policy skips=%v want [adminer db]", policySkips)
		}
	})

	t.Run("non-app explicitly opted in is selected", func(t *testing.T) {
		on := true
		svcs := map[string]config.ServiceConfig{
			"db": {
				Enabled: true, Type: config.ServiceTypeInfra, Dir: "services/db",
				Render: config.ServiceRenderConfig{AI: config.ServiceAIConfig{Enabled: &on}},
			},
		}
		selected, _ := SelectServices(svcs)
		if len(selected) != 1 || selected[0] != "db" {
			t.Errorf("selected=%v want [db]", selected)
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
