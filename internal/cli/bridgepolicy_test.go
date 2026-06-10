package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	bridgecore "github.com/semsemyonoff/dwe/internal/core/bridge"

	"github.com/spf13/cobra"
)

// --- allowlist predicate ---------------------------------------------------

func TestBridgeCommandAllowed_table(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		// Bare root: read-only project summary + help.
		{"dwe", true},

		// Allowed top-level subtrees.
		{"dwe commands", true},
		{"dwe commands list", true},
		{"dwe status", true},
		{"dwe status services", true},
		{"dwe info", true},
		{"dwe validate", true},
		{"dwe logs", true},
		{"dwe docs", true},
		{"dwe docs llms-txt", true},
		{"dwe prompt", true},
		{"dwe version", true},
		{"dwe completion", true},
		{"dwe completion bash", true},
		{"dwe help", true},
		{"dwe __complete", true},

		// The single nested exception inside an otherwise blocked subtree.
		{"dwe bridge status", true},
		{"dwe bridge", false},
		{"dwe bridge start", false},
		{"dwe bridge stop", false},
		{"dwe bridge daemon", false},

		// Suicidal lifecycle commands.
		{"dwe stop", false},
		{"dwe restart", false},
		{"dwe reset", false},
		{"dwe reset run", false},

		// Stack mutations and destructive / interactive commands.
		{"dwe deploy", false},
		{"dwe deploy run", false},
		{"dwe run", false},
		{"dwe services", false},
		{"dwe services enable", false},
		{"dwe snapshot", false},
		{"dwe snapshot list", false},
		{"dwe render", false},
		{"dwe init", false},
		{"dwe shell", false},
		{"dwe docker", false},
		{"dwe compose", false},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.path, " ", "_"), func(t *testing.T) {
			if got := bridgeCommandAllowed(tc.path); got != tc.want {
				t.Errorf("bridgeCommandAllowed(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// --- blocked error shape ----------------------------------------------------

func TestBridgeBlockedError_suicideExplanation(t *testing.T) {
	for _, top := range []string{"stop", "restart", "reset"} {
		t.Run(top, func(t *testing.T) {
			err := bridgeBlockedError("dwe " + top)
			if err.Code != "bridge_command_blocked" {
				t.Errorf("code: want bridge_command_blocked, got %q", err.Code)
			}
			if !strings.Contains(err.Hint, "container it was invoked from") {
				t.Errorf("hint must carry the suicide explanation, got %q", err.Hint)
			}
			if !strings.Contains(err.Hint, "on the host") {
				t.Errorf("hint must point at running on the host, got %q", err.Hint)
			}
		})
	}
}

func TestBridgeBlockedError_genericHint(t *testing.T) {
	err := bridgeBlockedError("dwe deploy run")
	if err.Code != "bridge_command_blocked" {
		t.Errorf("code: want bridge_command_blocked, got %q", err.Code)
	}
	if strings.Contains(err.Hint, "container it was invoked from") {
		t.Errorf("non-suicidal command must not carry the suicide explanation, got %q", err.Hint)
	}
	if !strings.Contains(err.Hint, "run `dwe deploy run` on the host") {
		t.Errorf("hint must name the blocked command path, got %q", err.Hint)
	}
	if err.Details["command"] != "deploy run" {
		t.Errorf("details.command: want %q, got %v", "deploy run", err.Details["command"])
	}
}

// --- gate wiring ------------------------------------------------------------

func TestBridgePolicyGate_inactiveOutsideContainer(t *testing.T) {
	root := NewRootCmd()
	stopCmd, _, err := root.Find([]string{"stop"})
	if err != nil {
		t.Fatalf("finding stop command: %v", err)
	}

	for _, val := range []string{"", "host", "ci"} {
		t.Run("env="+val, func(t *testing.T) {
			t.Setenv(bridgecore.EnvInvokedFrom, val)
			if err := bridgePolicyGate(stopCmd); err != nil {
				t.Errorf("gate must be inactive for %q, got %v", val, err)
			}
		})
	}
}

func TestBridgePolicyGate_blockedViaExecute(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(bridgecore.EnvInvokedFrom, bridgecore.InvokedFromContainer)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"stop"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected bridge_command_blocked error, got nil")
	}
	ce, ok := errors.AsType[*cmdctx.CodedError](err)
	if !ok {
		t.Fatalf("expected *cmdctx.CodedError, got %T: %v", err, err)
	}
	if ce.Code != "bridge_command_blocked" {
		t.Errorf("code: want bridge_command_blocked, got %q", ce.Code)
	}
	if !strings.Contains(ce.Hint, "container it was invoked from") {
		t.Errorf("stop must carry the suicide explanation, got %q", ce.Hint)
	}
}

// TestBridgePolicyGate_flagsBeforeSubcommand verifies the gate keys on the
// RESOLVED command path, not argv shape — persistent flags before the
// subcommand must not confuse it — and that the typed error serializes to the
// standard JSON error envelope.
func TestBridgePolicyGate_flagsBeforeSubcommand_jsonEnvelope(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(bridgecore.EnvInvokedFrom, bridgecore.InvokedFromContainer)

	root, flags := NewRootCmdWithFlags()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--output", "json", "deploy", "run"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected bridge_command_blocked error, got nil")
	}
	ce, ok := errors.AsType[*cmdctx.CodedError](err)
	if !ok || ce.Code != "bridge_command_blocked" {
		t.Fatalf("expected bridge_command_blocked CodedError, got %T: %v", err, err)
	}

	// Mirror main.go's JSON error handler and verify the envelope shape.
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	cmdctx.WriteError(flags, root, err)

	var envelope struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Hint    string         `json:"hint"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if uerr := json.Unmarshal(stderr.Bytes(), &envelope); uerr != nil {
		t.Fatalf("stderr is not a JSON envelope: %v\n---\n%s", uerr, stderr.String())
	}
	if envelope.Error.Code != "bridge_command_blocked" {
		t.Errorf("envelope code: want bridge_command_blocked, got %q", envelope.Error.Code)
	}
	if envelope.Error.Details["command"] != "deploy run" {
		t.Errorf("envelope details.command: want %q, got %v", "deploy run", envelope.Error.Details["command"])
	}
	if envelope.Error.Hint == "" {
		t.Error("envelope hint must be present")
	}
}

func TestBridgePolicyGate_allowedCommandRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv(bridgecore.EnvInvokedFrom, bridgecore.InvokedFromContainer)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"version"})

	if err := root.Execute(); err != nil {
		t.Fatalf("allowlisted `dwe version` must run from container context: %v", err)
	}
}

// --- help/completion visibility ----------------------------------------------

func findTopLevel(t *testing.T, root *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("top-level command %q not found", name)
	return nil
}

func TestApplyBridgeContainerVisibility_realTree(t *testing.T) {
	t.Setenv(bridgecore.EnvInvokedFrom, bridgecore.InvokedFromContainer)
	root := NewRootCmd()

	hidden := []string{"stop", "restart", "reset", "run", "deploy", "services", "snapshot", "render", "init", "shell", "docker", "compose"}
	for _, name := range hidden {
		if cmd := findTopLevel(t, root, name); !cmd.Hidden {
			t.Errorf("blocked command %q must be hidden in container context", name)
		}
	}
	// `prompt` is allowlisted but excluded here: it is Hidden by design on
	// the host too (prompt hot-path pattern), independent of bridge context.
	visible := []string{"commands", "status", "info", "validate", "logs", "docs", "version", "completion"}
	for _, name := range visible {
		if cmd := findTopLevel(t, root, name); cmd.Hidden {
			t.Errorf("allowlisted command %q must stay visible in container context", name)
		}
	}
	// The bridge subtree: the parent-with-allowed-child rule keeps `bridge`
	// visible for the allowlisted `bridge status`, while its blocked
	// siblings (start/stop/logs) hide.
	bridgeCmd := findTopLevel(t, root, "bridge")
	if bridgeCmd.Hidden {
		t.Error("bridge subtree must stay visible: `bridge status` is allowed")
	}
	for _, sub := range bridgeCmd.Commands() {
		switch sub.Name() {
		case "status":
			if sub.Hidden {
				t.Error("bridge status must stay visible in container context")
			}
		case "start", "stop", "logs":
			if !sub.Hidden {
				t.Errorf("bridge %s must be hidden in container context", sub.Name())
			}
		}
	}
}

func TestApplyBridgeContainerVisibility_hostNoop(t *testing.T) {
	t.Setenv(bridgecore.EnvInvokedFrom, "")
	root := NewRootCmd()
	for _, name := range []string{"stop", "deploy", "services"} {
		if cmd := findTopLevel(t, root, name); cmd.Hidden {
			t.Errorf("command %q must stay visible outside container context", name)
		}
	}
}

// TestHideIfBridgeBlocked_parentWithAllowedChild pins the structural rule: a
// blocked parent with an allowed descendant stays visible while its blocked
// siblings hide (the `bridge status` vs `bridge stop` shape from task 10).
func TestHideIfBridgeBlocked_parentWithAllowedChild(t *testing.T) {
	root := &cobra.Command{Use: "dwe"}
	bridge := &cobra.Command{Use: "bridge"}
	status := &cobra.Command{Use: "status"}
	stop := &cobra.Command{Use: "stop"}
	bridge.AddCommand(status, stop)
	root.AddCommand(bridge)

	if !hideIfBridgeBlocked(bridge) {
		t.Fatal("bridge must report visible: it has an allowed descendant")
	}
	if bridge.Hidden {
		t.Error("bridge must stay visible for `bridge status`")
	}
	if status.Hidden {
		t.Error("bridge status must stay visible")
	}
	if !stop.Hidden {
		t.Error("bridge stop must be hidden")
	}
}
