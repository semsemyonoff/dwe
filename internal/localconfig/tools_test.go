package localconfig

import (
	"strings"
	"testing"
)

var testKnownTools = map[string]bool{
	"adminer":       true,
	"redis_insight": true,
	"mailpit":       true,
}

func TestDiffToolSelection_NothingChanged(t *testing.T) {
	selections := []ToolSelection{
		{Name: "adminer", Enabled: true},
		{Name: "mailpit", Enabled: false},
	}
	toEnable, toDisable := DiffToolSelection(selections, []string{"adminer"})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty, got %v", toDisable)
	}
}

func TestDiffToolSelection_OneEnabled(t *testing.T) {
	selections := []ToolSelection{
		{Name: "adminer", Enabled: false},
	}
	toEnable, toDisable := DiffToolSelection(selections, []string{"adminer"})
	if len(toEnable) != 1 || toEnable[0] != "adminer" {
		t.Errorf("toEnable = %v, want [adminer]", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty, got %v", toDisable)
	}
}

func TestDiffToolSelection_OneDisabled(t *testing.T) {
	selections := []ToolSelection{
		{Name: "adminer", Enabled: true},
	}
	toEnable, toDisable := DiffToolSelection(selections, []string{})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 1 || toDisable[0] != "adminer" {
		t.Errorf("toDisable = %v, want [adminer]", toDisable)
	}
}

func TestDiffToolSelection_MultipleChanges(t *testing.T) {
	selections := []ToolSelection{
		{Name: "adminer", Enabled: false},
		{Name: "redis_insight", Enabled: true},
		{Name: "mailpit", Enabled: true},
	}
	toEnable, toDisable := DiffToolSelection(selections, []string{"adminer", "mailpit"})
	if len(toEnable) != 1 || toEnable[0] != "adminer" {
		t.Errorf("toEnable = %v, want [adminer]", toEnable)
	}
	if len(toDisable) != 1 || toDisable[0] != "redis_insight" {
		t.Errorf("toDisable = %v, want [redis_insight]", toDisable)
	}
}

func TestDiffToolSelection_AllUnchecked(t *testing.T) {
	selections := []ToolSelection{
		{Name: "adminer", Enabled: true},
		{Name: "redis_insight", Enabled: true},
		{Name: "mailpit", Enabled: false},
	}
	toEnable, toDisable := DiffToolSelection(selections, []string{})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 2 {
		t.Errorf("toDisable = %v, want [adminer redis_insight]", toDisable)
	}
}

func TestValidateToolToggle_UnknownTool(t *testing.T) {
	err := ValidateToolToggle(testKnownTools, "unknown")
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	msg := err.Error()
	for _, available := range []string{"adminer", "redis_insight", "mailpit"} {
		if !strings.Contains(msg, available) {
			t.Errorf("error should mention available tool %q, got: %q", available, msg)
		}
	}
}

func TestValidateToolToggle_KnownTool(t *testing.T) {
	if err := ValidateToolToggle(testKnownTools, "adminer"); err != nil {
		t.Errorf("unexpected error for known tool: %v", err)
	}
}

func TestApplyToolTogglesToYAML_UnknownToolRejectsAll(t *testing.T) {
	local := map[string]any{}
	err := ApplyToolTogglesToYAML(testKnownTools, local, []string{"unknown"}, nil)
	if err == nil {
		t.Fatal("expected error for unknown tool, got nil")
	}
	if _, ok := local["tools"]; ok {
		t.Error("local map must not be modified when validation fails")
	}
}

func TestApplyToolTogglesToYAML_AppliesChanges(t *testing.T) {
	local := map[string]any{}
	if err := ApplyToolTogglesToYAML(testKnownTools, local, []string{"adminer"}, []string{"mailpit"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	toolsMap, ok := local["tools"].(map[string]any)
	if !ok {
		t.Fatal("local[tools] missing or wrong type")
	}
	if adminerEntry, _ := toolsMap["adminer"].(map[string]any); adminerEntry["enabled"] != true {
		t.Errorf("adminer should be enabled=true, got %v", adminerEntry)
	}
	if mailpitEntry, _ := toolsMap["mailpit"].(map[string]any); mailpitEntry["enabled"] != false {
		t.Errorf("mailpit should be enabled=false, got %v", mailpitEntry)
	}
}
