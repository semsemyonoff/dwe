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
