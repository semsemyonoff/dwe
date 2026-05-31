package shell

import (
	"fmt"
	"testing"

	"github.com/semsemyonoff/devbox/internal/core/project/config"
)

// --- pickService ---

func makeTestConfig(services map[string]config.ServiceConfig) *config.DevboxConfig {
	return &config.DevboxConfig{Services: services}
}

func TestPickService_explicitName_returnsDirect(t *testing.T) {
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main":   {Required: true},
		"second": {Enabled: true},
	})
	selectorCalled := false
	sel := func(_ *config.DevboxConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("should not call selector")
	}
	got, err := pickService(cfg, "main", sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "main" {
		t.Errorf("want %q, got %q", "main", got)
	}
	if selectorCalled {
		t.Error("selector must not be called when name is given explicitly")
	}
}

func TestPickService_singleEnabled_autoSelect(t *testing.T) {
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main": {Required: true},
	})
	selectorCalled := false
	sel := func(_ *config.DevboxConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("should not call selector")
	}
	got, err := pickService(cfg, "", sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "main" {
		t.Errorf("want %q, got %q", "main", got)
	}
	if selectorCalled {
		t.Error("selector must not be called when exactly one service is enabled")
	}
}

func TestPickService_noEnabled_error(t *testing.T) {
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main": {Required: false, Enabled: false},
	})
	sel := func(_ *config.DevboxConfig, _ []string) (string, error) {
		return "main", nil
	}
	_, err := pickService(cfg, "", sel)
	if err == nil {
		t.Fatal("expected error for no enabled services, got nil")
	}
}

func TestPickService_multipleEnabled_callsSelector(t *testing.T) {
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main":   {Required: true},
		"second": {Enabled: true},
	})
	selectorCalled := false
	sel := func(_ *config.DevboxConfig, names []string) (string, error) {
		selectorCalled = true
		return names[0], nil
	}
	_, err := pickService(cfg, "", sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !selectorCalled {
		t.Error("selector must be called when multiple services are enabled")
	}
}

func TestPickService_nonInteractiveSelector_multipleEnabled_returnsError(t *testing.T) {
	// When a non-TTY error selector is passed and multiple services are enabled,
	// pickService calls the selector which returns the non-interactive error.
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main":   {Required: true},
		"second": {Enabled: true},
	})
	nonTTYSelector := func(_ *config.DevboxConfig, _ []string) (string, error) {
		return "", fmt.Errorf("multiple services are enabled; pass a service name or run in an interactive terminal")
	}
	_, err := pickService(cfg, "", nonTTYSelector)
	if err == nil {
		t.Fatal("expected non-interactive error, got nil")
	}
}

func TestPickService_nonInteractiveSelector_singleEnabled_autoSelectsWithoutSelector(t *testing.T) {
	// Even with a non-TTY error selector, single-service auto-select works without calling selector.
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main": {Required: true},
	})
	selectorCalled := false
	nonTTYSelector := func(_ *config.DevboxConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("not interactive")
	}
	got, err := pickService(cfg, "", nonTTYSelector)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "main" {
		t.Errorf("want %q, got %q", "main", got)
	}
	if selectorCalled {
		t.Error("non-TTY selector must not be called for single-service auto-select")
	}
}
