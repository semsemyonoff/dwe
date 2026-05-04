package localconfig

import (
	"testing"

	"devbox-cli/internal/config"
)

func TestDiffServiceSelection_NothingChanged(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "main", Mandatory: true, Enabled: true},
		{Name: "second", Mandatory: false, Enabled: true},
		{Name: "third", Mandatory: false, Enabled: false},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{"second"})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty, got %v", toDisable)
	}
}

func TestDiffServiceSelection_OneEnabled(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "second", Mandatory: false, Enabled: false},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{"second"})
	if len(toEnable) != 1 || toEnable[0] != "second" {
		t.Errorf("toEnable = %v, want [second]", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty, got %v", toDisable)
	}
}

func TestDiffServiceSelection_OneDisabled(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "second", Mandatory: false, Enabled: true},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 1 || toDisable[0] != "second" {
		t.Errorf("toDisable = %v, want [second]", toDisable)
	}
}

func TestDiffServiceSelection_MandatoryInKeptIsNoop(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "main", Mandatory: true, Enabled: true},
		{Name: "second", Mandatory: false, Enabled: false},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{"main"})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty (mandatory not toggleable), got %v", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty, got %v", toDisable)
	}
}

func TestDiffServiceSelection_MandatoryMissingFromKeptIsNoop(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "main", Mandatory: true, Enabled: true},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{})
	if len(toEnable) != 0 {
		t.Errorf("toEnable should be empty, got %v", toEnable)
	}
	if len(toDisable) != 0 {
		t.Errorf("toDisable should be empty (mandatory never disabled), got %v", toDisable)
	}
}

func TestDiffServiceSelection_MultipleChanges(t *testing.T) {
	selections := []ServiceSelection{
		{Name: "main", Mandatory: true, Enabled: true},
		{Name: "alpha", Mandatory: false, Enabled: false},
		{Name: "beta", Mandatory: false, Enabled: true},
		{Name: "gamma", Mandatory: false, Enabled: true},
	}
	toEnable, toDisable := DiffServiceSelection(selections, []string{"alpha", "gamma"})
	if len(toEnable) != 1 || toEnable[0] != "alpha" {
		t.Errorf("toEnable = %v, want [alpha]", toEnable)
	}
	if len(toDisable) != 1 || toDisable[0] != "beta" {
		t.Errorf("toDisable = %v, want [beta]", toDisable)
	}
}

func TestValidateServiceToggle_UnknownService(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {Mandatory: false},
		},
	}
	if err := ValidateServiceToggle(cfg, "unknown", true); err == nil {
		t.Error("expected error for unknown service, got nil")
	}
}

func TestValidateServiceToggle_MandatoryService(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main": {Mandatory: true},
		},
	}
	if err := ValidateServiceToggle(cfg, "main", false); err == nil {
		t.Error("expected error for mandatory service, got nil")
	}
}

func TestValidateServiceToggle_OptionalService(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"second": {Mandatory: false},
		},
	}
	if err := ValidateServiceToggle(cfg, "second", true); err != nil {
		t.Errorf("unexpected error for optional service: %v", err)
	}
}

func TestApplyServiceTogglesToYAML_AllOrNothing(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"main":   {Mandatory: true},
			"second": {Mandatory: false},
		},
	}
	local := map[string]any{}

	// "second" is valid; "main" is mandatory — batch must reject before writing.
	err := ApplyServiceTogglesToYAML(cfg, local, []string{"second"}, []string{"main"})
	if err == nil {
		t.Fatal("expected error for mandatory toggle, got nil")
	}

	// local must not have been modified.
	if _, ok := local["services"]; ok {
		t.Error("local map must not be modified when batch validation fails")
	}
}

func TestApplyServiceTogglesToYAML_AppliesChanges(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"second": {Mandatory: false},
			"third":  {Mandatory: false},
		},
	}
	local := map[string]any{}

	if err := ApplyServiceTogglesToYAML(cfg, local, []string{"second"}, []string{"third"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	svcMap, ok := local["services"].(map[string]any)
	if !ok {
		t.Fatal("local[services] missing or wrong type")
	}
	if secondEntry, _ := svcMap["second"].(map[string]any); secondEntry["enabled"] != true {
		t.Errorf("second should be enabled=true, got %v", secondEntry)
	}
	if thirdEntry, _ := svcMap["third"].(map[string]any); thirdEntry["enabled"] != false {
		t.Errorf("third should be enabled=false, got %v", thirdEntry)
	}
}
