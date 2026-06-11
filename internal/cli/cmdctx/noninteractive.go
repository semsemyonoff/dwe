package cmdctx

import "os"

// NonInteractiveEnv reports whether DWE_NONINTERACTIVE is truthy ("1" or
// "true"). The bridge daemon force-sets it for container invocations and CI
// pipelines set it by hand — both must behave identically to a non-TTY pipe.
// Shared by the bare `dwe commands` and bare `dwe docs` list fallbacks and by
// the command runner's prompt skipping.
func NonInteractiveEnv() bool {
	v := os.Getenv("DWE_NONINTERACTIVE")
	return v == "1" || v == "true"
}
