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
