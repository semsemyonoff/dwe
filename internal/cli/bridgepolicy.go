package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	bridgecore "github.com/semsemyonoff/dwe/internal/core/bridge"

	"github.com/spf13/cobra"
)

// Container command policy: when this dwe process was forked by the bridge
// daemon on behalf of a container (DWE_INVOKED_FROM=container — set by the
// daemon, not spoofable from the container: the daemon strips client-sent
// values and force-sets its own), only an allowlisted subset of the CLI is
// reachable. Default-deny: a new top-level command does not reach the
// container surface until explicitly added here.
//
// The motive is foot-gun first, blast radius second: `dwe stop` from inside a
// container kills the caller's own container (the terminal/IDE session dies
// before the exit code arrives), and a bridge token in a compromised
// container must not be able to destroy data (`reset run`, `snapshot
// restore`).
//
// All user commands live under the `commands` subtree, so the allowlist is
// static over top-level command names — no registry knowledge is needed.
var bridgeAllowedTopLevel = map[string]bool{
	"commands": true, // user commands — the primary bridge use case (hooks)
	"status":   true, // read-only diagnostics
	"info":     true,
	"logs":     true,
	"docs":     true, // read-only; useful to AI agents in the devcontainer
	"prompt":   true, // container terminal prompt
	"version":  true, // service commands
	"help":     true,
	// vars read subcommands (get/list/inspect) are always reachable; the
	// no-arg TUI auto-degrades to `vars list` via the non-interactive
	// dispatch. `vars set` is reachable too, but bridgeCommandAllowed is
	// prefix-wide and cannot see the var argument, so container writes are
	// deny-by-default and gated at runtime against bridge.vars_writable in
	// `vars set` itself.
	"vars": true,
	// `validate` and `completion` are deliberately absent: validation targets
	// the host workspace and completion scripts are installed on the host —
	// neither belongs to the container surface.
	//
	// cobra's hidden completion machinery is read-only and bypasses
	// PersistentPreRunE anyway; allowlisted so a completion script already
	// baked into an image keeps degrading silently instead of injecting a
	// policy error into the shell, even though the user-facing `completion`
	// generator is host-only.
	"__complete":       true,
	"__completeNoDesc": true,
}

// bridgeSuicidalReason explains, per top-level command, why running it from
// inside a container would kill the caller's own session.
var bridgeSuicidalReason = map[string]string{
	"stop":    "it would stop the container it was invoked from",
	"restart": "it would restart the container it was invoked from",
	"reset":   "it would stop and remove the container it was invoked from",
}

// bridgeInvokedFromContainer reports whether this process runs on behalf of
// a container (env set by the bridge daemon; host-controlled).
func bridgeInvokedFromContainer() bool {
	return os.Getenv(bridgecore.EnvInvokedFrom) == bridgecore.InvokedFromContainer
}

// bridgeCommandAllowed reports whether the resolved cobra command path (e.g.
// "dwe bridge status") is reachable from a container. Allowance is by
// top-level subtree, plus two nested exceptions:
//   - `bridge status` — the rest of the bridge subtree stays host-only
//     (`bridge stop` is suicide for the bridge itself).
//   - `render config` — a container may regenerate its config files after a
//     `vars set`; the other render subcommands (env/ide/ai/git) target host
//     state and stay host-only. The mutating `render config --harvest` path is
//     additionally rejected in render/config.go when invoked from a container.
func bridgeCommandAllowed(path string) bool {
	fields := strings.Fields(path)
	if len(fields) < 2 {
		return true // bare `dwe`: read-only project summary + help
	}
	if bridgeAllowedTopLevel[fields[1]] {
		return true
	}
	if fields[1] == "bridge" && len(fields) >= 3 && fields[2] == "status" {
		return true
	}
	return fields[1] == "render" && len(fields) >= 3 && fields[2] == "config"
}

// bridgePolicyGate enforces the container command policy on the resolved
// leaf command. It runs inside the root PersistentPreRunE (the single
// pre-RunE hook — cobra replaces instead of chaining, so no second hook may
// ever be added), after output-mode validation and before project
// resolution, so a blocked command fails with the policy error regardless of
// project state.
func bridgePolicyGate(cmd *cobra.Command) error {
	if !bridgeInvokedFromContainer() {
		return nil
	}
	path := cmd.CommandPath()
	if bridgeCommandAllowed(path) {
		return nil
	}
	return bridgeBlockedError(path)
}

// bridgeBlockedError builds the typed policy error for a blocked command
// path. Suicidal lifecycle commands carry the explanation; everything else a
// plain run-on-host hint.
func bridgeBlockedError(path string) *cmdctx.CodedError {
	rel := strings.TrimSpace(strings.TrimPrefix(path, "dwe"))
	err := cmdctx.Err("bridge_command_blocked",
		fmt.Sprintf("command %q is not available from inside a container", rel)).
		WithDetail("command", rel)
	top, _, _ := strings.Cut(rel, " ")
	if reason, ok := bridgeSuicidalReason[top]; ok {
		return err.WithHint(fmt.Sprintf("%s — run `dwe %s` on the host instead", reason, rel))
	}
	return err.WithHint(fmt.Sprintf("run `dwe %s` on the host instead", rel))
}

// applyBridgeContainerVisibility hides blocked commands from help listings
// and shell completion in container context — blocked commands are
// invisible, not "visible but failing". A command stays visible when it or
// any descendant is allowed, so a parent like `bridge` remains listed for
// its allowed `status` child.
func applyBridgeContainerVisibility(root *cobra.Command) {
	if !bridgeInvokedFromContainer() {
		return
	}
	for _, child := range root.Commands() {
		hideIfBridgeBlocked(child)
	}
}

// hideIfBridgeBlocked recursively hides cmd when neither it nor any
// descendant is allowed, and reports whether cmd stays visible.
func hideIfBridgeBlocked(cmd *cobra.Command) bool {
	if bridgeCommandAllowed(cmd.CommandPath()) {
		return true // allowance is prefix-based: the whole subtree is reachable
	}
	visible := false
	for _, child := range cmd.Commands() {
		if hideIfBridgeBlocked(child) {
			visible = true
		}
	}
	if !visible {
		cmd.Hidden = true
	}
	return visible
}
