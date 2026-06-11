package commands

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// bridgeValidatorCfg declares two services: main (bridge on) and admin
// (bridge off — the default).
func bridgeValidatorCfg() *config.DweConfig {
	on := true
	return &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"main":  {Type: "app", Bridge: config.ServiceBridgeConfig{Enabled: &on}},
			"admin": {Type: "app"},
		},
	}
}

func TestBridgeDiagnostics_NilBlock_NoDiagnostic(t *testing.T) {
	if got := bridgeDiagnostics("commands:x", "x", "f.yml", nil, bridgeValidatorCfg()); len(got) != 0 {
		t.Errorf("nil block must be silent; got %+v", got)
	}
}

func TestBridgeDiagnostics_ValidServices_NoDiagnostic(t *testing.T) {
	on := true
	b := &model.BridgeDef{Enabled: &on, Services: []string{"main"}}
	if got := bridgeDiagnostics("commands:x", "x", "f.yml", b, bridgeValidatorCfg()); len(got) != 0 {
		t.Errorf("valid services must be silent; got %+v", got)
	}
}

func TestBridgeDiagnostics_UnknownService_Warning(t *testing.T) {
	on := true
	b := &model.BridgeDef{Enabled: &on, Services: []string{"mian"}} // typo
	diags := bridgeDiagnostics("commands:x", "x", "f.yml", b, bridgeValidatorCfg())
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != validate.SeverityWarning {
		t.Errorf("severity = %v, want warning", diags[0].Severity)
	}
	if !strings.Contains(diags[0].Message, `"mian"`) {
		t.Errorf("message must name the unknown service; got %q", diags[0].Message)
	}
}

func TestBridgeDiagnostics_BridgeDisabledService_Warning(t *testing.T) {
	on := true
	b := &model.BridgeDef{Enabled: &on, Services: []string{"admin"}}
	diags := bridgeDiagnostics("commands:x", "x", "f.yml", b, bridgeValidatorCfg())
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "bridge disabled") {
		t.Errorf("message must explain the dead opt-in; got %q", diags[0].Message)
	}
}

// TestBridgeDiagnostics_DisabledParentWithEnabledChild_Silent: listing a
// bridge-disabled parent stays effective when a bridge-enabled child extends
// it (the child inherits the parent's command rights), so no warning.
func TestBridgeDiagnostics_DisabledParentWithEnabledChild_Silent(t *testing.T) {
	on := true
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"base":  {Type: "app"}, // bridge off — the default
			"admin": {Type: "app", Extends: "base", Bridge: config.ServiceBridgeConfig{Enabled: &on}},
		},
	}
	b := &model.BridgeDef{Enabled: &on, Services: []string{"base"}}
	if got := bridgeDiagnostics("commands:x", "x", "f.yml", b, cfg); len(got) != 0 {
		t.Errorf("disabled parent with bridge-enabled child must be silent; got %+v", got)
	}
}

func TestBridgeDiagnostics_DisabledParentTransitiveChild_Silent(t *testing.T) {
	on := true
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"base":    {Type: "app"},
			"mid":     {Type: "app", Extends: "base"},
			"reports": {Type: "app", Extends: "mid", Bridge: config.ServiceBridgeConfig{Enabled: &on}},
		},
	}
	b := &model.BridgeDef{Enabled: &on, Services: []string{"base"}}
	if got := bridgeDiagnostics("commands:x", "x", "f.yml", b, cfg); len(got) != 0 {
		t.Errorf("transitive bridge-enabled grandchild must silence the warning; got %+v", got)
	}
}

func TestBridgeDiagnostics_DisabledParentDisabledChildren_StillWarns(t *testing.T) {
	on := true
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"base":  {Type: "app"},
			"admin": {Type: "app", Extends: "base"}, // bridge off too
		},
	}
	b := &model.BridgeDef{Enabled: &on, Services: []string{"base"}}
	diags := bridgeDiagnostics("commands:x", "x", "f.yml", b, cfg)
	if len(diags) != 1 || !strings.Contains(diags[0].Message, "bridge disabled") {
		t.Fatalf("expected the disabled warning to survive, got %+v", diags)
	}
}

func TestBridgeDiagnostics_EmptyServicesList_Warning(t *testing.T) {
	on := true
	b := &model.BridgeDef{Enabled: &on, Services: []string{}}
	diags := bridgeDiagnostics("commands:x", "x", "f.yml", b, bridgeValidatorCfg())
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d: %+v", len(diags), diags)
	}
	if !strings.Contains(diags[0].Message, "empty list") {
		t.Errorf("got %q", diags[0].Message)
	}
}

func TestBridgeDiagnostics_NilCfgSkipsCrossChecks(t *testing.T) {
	on := true
	b := &model.BridgeDef{Enabled: &on, Services: []string{"whatever"}}
	if got := bridgeDiagnostics("commands:x", "x", "f.yml", b, nil); len(got) != 0 {
		t.Errorf("nil cfg must skip service cross-checks; got %+v", got)
	}
}
