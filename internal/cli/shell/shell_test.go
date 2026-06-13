package shell

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// --- pickService ---

func makeTestConfig(services map[string]config.ServiceConfig) *config.DweConfig {
	return &config.DweConfig{Services: services}
}

func TestPickService_explicitName_returnsDirect(t *testing.T) {
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main":   {Required: true},
		"second": {Enabled: true},
	})
	selectorCalled := false
	sel := func(_ *config.DweConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("should not call selector")
	}
	got, err := pickService(cfg, "main", "", "", sel)
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
	sel := func(_ *config.DweConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("should not call selector")
	}
	got, err := pickService(cfg, "", "", "", sel)
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
	sel := func(_ *config.DweConfig, _ []string) (string, error) {
		return "main", nil
	}
	_, err := pickService(cfg, "", "", "", sel)
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
	sel := func(_ *config.DweConfig, names []string) (string, error) {
		selectorCalled = true
		return names[0], nil
	}
	_, err := pickService(cfg, "", "", "", sel)
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
	nonTTYSelector := func(_ *config.DweConfig, _ []string) (string, error) {
		return "", fmt.Errorf("multiple services are enabled; pass a service name or run in an interactive terminal")
	}
	_, err := pickService(cfg, "", "", "", nonTTYSelector)
	if err == nil {
		t.Fatal("expected non-interactive error, got nil")
	}
}

func TestPickService_cwdInsideServiceDir_selectsThatService(t *testing.T) {
	// Two services; cwd is inside "api"'s dir. Even though "web" is also enabled
	// (so auto-select would be ambiguous), the cwd match wins without a selector.
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"web": {Enabled: true},
		"api": {Enabled: true, Dir: "services/api"},
	})
	selectorCalled := false
	sel := func(_ *config.DweConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("should not call selector")
	}
	root := "/proj"
	cwd := "/proj/services/api/src"
	got, err := pickService(cfg, "", root, cwd, sel)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "api" {
		t.Errorf("want %q, got %q", "api", got)
	}
	if selectorCalled {
		t.Error("selector must not be called when cwd resolves the service")
	}
}

func TestPickService_cwdOutsideAnyServiceDir_fallsThrough(t *testing.T) {
	// cwd is not under any service dir → falls through to single-enabled auto-select.
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"api": {Required: true, Dir: "services/api"},
	})
	got, err := pickService(cfg, "", "/proj", "/proj/elsewhere", func(_ *config.DweConfig, _ []string) (string, error) {
		return "", fmt.Errorf("should not call selector")
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "api" {
		t.Errorf("want %q (single-enabled fallback), got %q", "api", got)
	}
}

// --- -c / --command flag wiring ---

func TestNewCmd_commandFlag_registered(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	flag := cmd.Flags().Lookup("command")
	if flag == nil {
		t.Fatal("expected --command flag to be registered")
	}
	if flag.Shorthand != "c" {
		t.Errorf("want shorthand %q, got %q", "c", flag.Shorthand)
	}
	if flag.DefValue != "" {
		t.Errorf("want default %q, got %q", "", flag.DefValue)
	}
}

func TestNewCmd_commandFlag_validation(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantErr  bool
		errMatch string
	}{
		{
			name:     "explicit empty errors",
			args:     []string{"-c", ""},
			wantErr:  true,
			errMatch: "cannot be empty or whitespace-only",
		},
		{
			name:     "whitespace only errors",
			args:     []string{"-c", "   "},
			wantErr:  true,
			errMatch: "cannot be empty or whitespace-only",
		},
		{
			name:     "tabs and newlines errors",
			args:     []string{"-c", "\t \n"},
			wantErr:  true,
			errMatch: "cannot be empty or whitespace-only",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewCmd("", &cmdctx.RootFlags{})
			cmd.SetArgs(tc.args)
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			err := cmd.Execute()
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tc.errMatch) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.errMatch)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestNewCmd_commandFlag_validationGate confirms the validator is gated on
// `Changed("command")`, not on an empty string. An unset flag must NOT trigger
// the "empty or whitespace-only" error; instead the command continues into
// LoadConfig and fails there (proving the gate works).
func TestNewCmd_commandFlag_unsetSkipsValidation(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{ConfigPath: "/nonexistent/path/workspace.yml"})
	var stderr bytes.Buffer
	cmd.SetArgs([]string{}) // no -c
	cmd.SetOut(io.Discard)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error from config load, got nil")
	}
	if strings.Contains(err.Error(), "cannot be empty or whitespace-only") {
		t.Errorf("validator fired without -c flag: %v", err)
	}
}

func TestNewCmd_helpText_describesOneShotMode(t *testing.T) {
	cmd := NewCmd("", &cmdctx.RootFlags{})
	if !strings.Contains(cmd.Long, "-c") {
		t.Errorf("cmd.Long should mention the -c flag, got: %q", cmd.Long)
	}
	if !strings.Contains(cmd.Long, "exit code") {
		t.Errorf("cmd.Long should mention 'exit code', got: %q", cmd.Long)
	}
	if !strings.Contains(cmd.Example, `dwe shell main -c "composer install"`) {
		t.Errorf("cmd.Example should include the composer install example, got: %q", cmd.Example)
	}
}

func TestPickService_nonInteractiveSelector_singleEnabled_autoSelectsWithoutSelector(t *testing.T) {
	// Even with a non-TTY error selector, single-service auto-select works without calling selector.
	cfg := makeTestConfig(map[string]config.ServiceConfig{
		"main": {Required: true},
	})
	selectorCalled := false
	nonTTYSelector := func(_ *config.DweConfig, _ []string) (string, error) {
		selectorCalled = true
		return "", fmt.Errorf("not interactive")
	}
	got, err := pickService(cfg, "", "", "", nonTTYSelector)
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
