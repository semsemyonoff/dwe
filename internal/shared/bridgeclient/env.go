package bridgeclient

import (
	"os"
	"strings"
)

// Host-controlled variables of the bridge env contract (design D7): the
// daemon strips any client-sent value and force-sets its own on every forked
// subprocess, so a container can never spoof them. Defined here (the leaf
// both core/ and cli/ import) so consumers like the command registry need no
// dependency on core/bridge; core/bridge aliases them for daemon-side code.
const (
	// EnvInvokedFrom marks a dwe process as bridge-forked; the CLI command
	// policy and command bridge-visibility key off InvokedFromContainer.
	EnvInvokedFrom = "DWE_INVOKED_FROM"
	// InvokedFromContainer is the EnvInvokedFrom value set by the daemon.
	InvokedFromContainer = "container"
	// EnvNonInteractive forces the existing non-interactive contract (as in
	// CI) on every bridged invocation — the bridge never allocates a pty.
	EnvNonInteractive = "DWE_NONINTERACTIVE"
)

// EnvBridgeService names the service whose shim initiated this bridged
// invocation. The compose overlay injects it per service (the value is the
// workspace/services/<name> key); the shim forwards it and the daemon passes
// it through — its consumer is the host-side dwe, which filters per-command
// bridge visibility by it. Intentionally NOT in the strip set. ADVISORY ONLY:
// a container controls its own environment and can claim another service's
// name — per-service command visibility is a UX boundary between containers
// of one project, not a security boundary (that remains the top-level command
// allowlist plus the daemon env hardening).
const EnvBridgeService = "DWE_BRIDGE_SERVICE"

// InContainer reports whether this dwe process runs on behalf of a container
// shim (the daemon force-sets EnvInvokedFrom on every bridged subprocess).
func InContainer() bool {
	return os.Getenv(EnvInvokedFrom) == InvokedFromContainer
}

// CallingService returns the service name whose shim initiated this bridged
// invocation, or "" outside the bridge / when the overlay predates the
// variable. See EnvBridgeService for the trust caveat.
func CallingService() string {
	return os.Getenv(EnvBridgeService)
}

// strippedEnvNames are bridge-internal variables the shim never forwards to
// the host (design D7). The daemon re-filters the same set defense-in-depth
// and force-sets the host-controlled DWE_INVOKED_FROM / DWE_NONINTERACTIVE.
var strippedEnvNames = map[string]struct{}{
	"DWE_BRIDGE_DIR":          {},
	"DWE_HOST_WORKSPACE":      {},
	"DWE_CONTAINER_WORKSPACE": {},
	"DWE_BRIDGE_PROJECT":      {},
	"DWE_BRIDGE_UNREACHABLE":  {},
}

// strippedEnvPrefix removes DWE_PROJECT_ROOT and any DWE_PROJECT_ROOT* variant
// so a container cannot override host-side project discovery.
const strippedEnvPrefix = "DWE_PROJECT_ROOT"

// StripEnv returns env (KEY=VALUE entries) without the bridge-internal
// variables listed in strippedEnvNames and without any DWE_PROJECT_ROOT*
// entry. Entries with no '=' are dropped as malformed.
func StripEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if _, stripped := strippedEnvNames[name]; stripped {
			continue
		}
		if strings.HasPrefix(name, strippedEnvPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ForceColorEnv returns env with CLICOLOR_FORCE=1 (and COLORTERM=truecolor)
// appended when nothing in env already decides the matter: the host-side dwe
// renders through lipgloss, whose stdout over the bridge is a pipe (never a
// tty), so without forcing it silently downgrades to no-color even when the
// shim's stdout in the container IS a terminal. The shim calls this only
// after a positive tty probe; an explicit NO_COLOR or CLICOLOR_FORCE in the
// container env wins unchanged, and piped shim output (`dwe cmd foo | grep …`)
// never forces. COLORTERM upgrades the host color profile to truecolor —
// `docker exec` typically leaves the container with a bare TERM=xterm, which
// would quantize the host palette down to 16 ANSI colors and visibly shift
// hues versus the same command run on the host; any terminal modern enough
// to run dev containers handles 24-bit SGR. An explicit container COLORTERM
// is respected.
func ForceColorEnv(env []string) []string {
	hasColorTerm := false
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "NO_COLOR" || name == "CLICOLOR_FORCE" {
			return env
		}
		if name == "COLORTERM" {
			hasColorTerm = true
		}
	}
	env = append(env, "CLICOLOR_FORCE=1")
	if !hasColorTerm {
		env = append(env, "COLORTERM=truecolor")
	}
	return env
}

// EnvBridgeStdinTTY signals the host-side dwe that the shim's stdin in the
// container is a real terminal. Together with CLICOLOR_FORCE it lets the
// host runners give child processes a full PTY (compose exec demands a
// terminal stdin before it will allocate a container TTY) without breaking
// piped-stdin invocations like `cat dump.sql | dwe cmd db.import`.
// Intentionally NOT in the strip set — the host subprocess is its consumer.
const EnvBridgeStdinTTY = "DWE_BRIDGE_STDIN_TTY"

// SetStdinTTYEnv force-owns the EnvBridgeStdinTTY signal: any client-supplied
// value is dropped (a spoofed "1" would wire a PTY onto a piped stdin and
// hang it) and the probed truth is appended when stdin IS a terminal.
func SetStdinTTYEnv(env []string, stdinIsTTY bool) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if name, _, ok := strings.Cut(kv, "="); ok && name == EnvBridgeStdinTTY {
			continue
		}
		out = append(out, kv)
	}
	if stdinIsTTY {
		out = append(out, EnvBridgeStdinTTY+"=1")
	}
	return out
}

// TranslateCwd rewrites a container working directory to the host path per
// design D7: a cwd inside containerWS gets its prefix replaced with hostWS;
// anything else is returned as-is (the daemon then falls back to running
// from its project root). Matching is path-boundary aware ("/workspace" does not
// match "/workspaces") and paths are container-side, hence always
// slash-separated.
func TranslateCwd(cwd, containerWS, hostWS string) string {
	if containerWS == "" || hostWS == "" {
		return cwd
	}
	containerWS = strings.TrimSuffix(containerWS, "/")
	hostWS = strings.TrimSuffix(hostWS, "/")
	if cwd == containerWS {
		return hostWS
	}
	if strings.HasPrefix(cwd, containerWS+"/") {
		return hostWS + strings.TrimPrefix(cwd, containerWS)
	}
	return cwd
}
