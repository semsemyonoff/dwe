package bridgeclient

import "strings"

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
// anything else is returned as-is (the daemon then rejects it with a
// containment error). Matching is path-boundary aware ("/workspace" does not
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
