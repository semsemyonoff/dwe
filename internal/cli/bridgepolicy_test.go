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
		{"dwe logs", true},
		{"dwe docs", true},
		{"dwe docs llms-txt", true},
		{"dwe prompt", true},
		{"dwe version", true},
		{"dwe help", true},

		// vars subtree: read subcommands always reachable; `set` reachable but
		// runtime-gated by bridge.vars_writable inside the command itself.
		{"dwe vars", true},
		{"dwe vars get", true},
		{"dwe vars list", true},
		{"dwe vars inspect", true},
		{"dwe vars set", true},

		// The `render config` nested exception: a container may regenerate its
		// config after a `vars set`. Every other render subcommand stays
		// host-only, and bare `render` is blocked.
		{"dwe render config", true},
		{"dwe render config main", true},
		{"dwe render env", false},
		{"dwe render ide", false},
		{"dwe render ai", false},
		{"dwe render git", false},
		// The hidden machinery stays allowed (baked-in completion scripts must
		// degrade silently), the user-facing generator does not.
		{"dwe __complete", true},
		{"dwe completion", false},
		{"dwe completion bash", false},

		// Host-side concerns removed from the container surface.
		{"dwe validate", false},
		{"dwe validate config", false},

		// The whole `secrets` subtree is host-only: no container may mint,
		// rekey or export the project identity. Decrypted READS stay reachable
		// through `vars get` (above) — the same exposure the container already
		// has through its rendered .env.
		{"dwe secrets", false},
		{"dwe secrets init", false},
		{"dwe secrets status", false},
		{"dwe secrets set", false},
		{"dwe secrets get", false},
		{"dwe secrets key export", false},
		{"dwe secrets key import", false},
		{"dwe secrets key list", false},
		{"dwe secrets key remove", false},
		{"dwe secrets rekey", false},

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
		{"dwe test", false},
		{"dwe test run", false},
		{"dwe test list", false},
		{"dwe test clean", false},
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

	hidden := []string{"stop", "restart", "reset", "run", "deploy", "services", "snapshot", "test", "init", "shell", "docker", "compose", "validate", "completion"}
	for _, name := range hidden {
		if cmd := findTopLevel(t, root, name); !cmd.Hidden {
			t.Errorf("blocked command %q must be hidden in container context", name)
		}
	}
	// `prompt` is allowlisted but excluded here: it is Hidden by design on
	// the host too (prompt hot-path pattern), independent of bridge context.
	// `render` stays visible for its allowed `config` child (verified below).
	visible := []string{"commands", "status", "info", "logs", "docs", "version", "vars"}
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

	// The render subtree: the parent-with-allowed-child rule keeps `render`
	// visible for the allowlisted `render config`, while its host-only
	// siblings (env/ide/ai/git) hide.
	renderCmd := findTopLevel(t, root, "render")
	if renderCmd.Hidden {
		t.Error("render subtree must stay visible: `render config` is allowed")
	}
	for _, sub := range renderCmd.Commands() {
		switch sub.Name() {
		case "config":
			if sub.Hidden {
				t.Error("render config must stay visible in container context")
			}
		case "env", "ide", "ai", "git":
			if !sub.Hidden {
				t.Errorf("render %s must be hidden in container context", sub.Name())
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
// siblings hide (the `bridge status` vs `bridge stop` shape).
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
