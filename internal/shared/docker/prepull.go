package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// BaseRef is an external FROM base image reference derived from a Dockerfile,
// paired with the platform pinned via "FROM --platform=<p>" (empty when the
// FROM pins none, or pins one that cannot be statically resolved). The
// platform must ride along to the inspect/pull calls: probing or pulling the
// wrong platform variant would leave buildkit to fetch the pinned one from the
// registry the prepull exists to avoid touching.
type BaseRef struct {
	Ref      string
	Platform string
}

// composeConfigJSON is the narrow subset of `compose config --format json`
// this package cares about: per-service build context needed to locate and
// parse the Dockerfile that produces that service's image.
type composeConfigJSON struct {
	Services map[string]struct {
		// Platform is the service-level target platform (`services.<name>.
		// platform`), e.g. "linux/amd64" — buildkit builds the service (and any
		// base with no FROM "--platform" of its own) for it.
		Platform string `json:"platform"`
		Build    *struct {
			Context          string             `json:"context"`
			Dockerfile       string             `json:"dockerfile"`
			DockerfileInline string             `json:"dockerfile_inline"`
			Args             map[string]*string `json:"args"`
			// Platforms is the build's explicit target-platform list
			// (`build.platforms`); when set it takes precedence over the
			// service-level Platform.
			Platforms []string `json:"platforms"`
		} `json:"build"`
	} `json:"services"`
}

// DeriveBuildBases returns the sorted, deduplicated set of external FROM base
// image references (each with any pinned "--platform") used by the given
// services' Dockerfiles. An empty services slice means all services in the
// compose config output.
//
// It runs `compose config --format json` via BuildInternalArgs (never
// BuildArgs — user-configured args.global must not corrupt the machine-
// readable output) on this Compose instance, then for each matching service
// with a build: block parses dockerfile_inline directly, or reads the
// Dockerfile at context/dockerfile (dockerfile taken as-is when already
// absolute — compose config emits absolute paths verbatim) and feeds it
// through externalBaseRefs, using the service's build.args (with null-valued
// entries dropped — compose config JSON encodes an unset override as a JSON
// null, which must fall back to the Dockerfile's own ARG default rather than
// overriding it with an empty string) as overrides. The service's resolved
// build target platform (`build.platforms`, else `services.<name>.platform`)
// is threaded in as the effective platform of any base whose FROM pins none of
// its own, so a service pinned to a non-default platform prepulls that variant
// instead of the host default.
//
// An unreadable Dockerfile skips just that service (with a trace warning),
// not the whole derivation — this function is advisory, like the rest of the
// prepull mechanism, and a single bad service must not blind the caller to
// every other service's bases.
func (c *Compose) DeriveBuildBases(services []string) ([]BaseRef, error) {
	out, err := c.output(c.BuildInternalArgs("config", "--format", "json"))
	if err != nil {
		return nil, fmt.Errorf("%s compose config: %w", c.BinName(), err)
	}

	var parsed composeConfigJSON
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("parsing compose config json: %w", err)
	}

	var filter map[string]struct{}
	if len(services) > 0 {
		filter = make(map[string]struct{}, len(services))
		for _, s := range services {
			filter[s] = struct{}{}
		}
	}

	seen := map[string]struct{}{}
	var refs []BaseRef
	for name, svc := range parsed.Services {
		if filter != nil {
			if _, ok := filter[name]; !ok {
				continue
			}
		}
		if svc.Build == nil {
			continue
		}

		buildArgs := make(map[string]string, len(svc.Build.Args))
		for k, v := range svc.Build.Args {
			if v != nil {
				buildArgs[k] = *v
			}
		}

		var content []byte
		if svc.Build.DockerfileInline != "" {
			content = []byte(svc.Build.DockerfileInline)
		} else {
			dockerfile := svc.Build.Dockerfile
			if dockerfile == "" {
				dockerfile = "Dockerfile"
			}
			if !filepath.IsAbs(dockerfile) {
				dockerfile = filepath.Join(svc.Build.Context, dockerfile)
			}
			content, err = os.ReadFile(dockerfile) //nolint:gosec
			if err != nil {
				trace.Debugf(context.Background(), "prepull: skipping service %q: reading dockerfile %q: %v", name, dockerfile, err)
				continue
			}
		}

		// Compose already resolved this service's build target platform(s):
		// build.platforms wins, else the service-level platform, else the
		// daemon default. Each drives the effective platform of bases whose
		// FROM pins none of their own — so prepull probes/pulls the same
		// variant buildkit will build, not the host default.
		targetPlatforms := []string{""}
		switch {
		case len(svc.Build.Platforms) > 0:
			targetPlatforms = svc.Build.Platforms
		case svc.Platform != "":
			targetPlatforms = []string{svc.Platform}
		}

		for _, tp := range targetPlatforms {
			for _, ref := range externalBaseRefs(content, buildArgs, tp) {
				key := ref.Ref + "\x00" + ref.Platform
				if _, dup := seen[key]; !dup {
					seen[key] = struct{}{}
					refs = append(refs, ref)
				}
			}
		}
	}

	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Ref != refs[j].Ref {
			return refs[i].Ref < refs[j].Ref
		}
		return refs[i].Platform < refs[j].Platform
	})
	return refs, nil
}

// ImageExists reports whether ref is present in the local image store for the
// given platform (empty = daemon default). It shells out to `docker image
// inspect` directly (not via compose) since image presence is daemon-scoped,
// not project-scoped. env/dir are set (unlike volumeExists, which sets
// neither) so DOCKER_HOST/context overrides from ProcessEnv apply — this is a
// deliberate deviation, not an oversight. A probe failure of any kind (missing
// binary, daemon unreachable, an inspect that predates `--platform` support,
// or a genuinely missing/wrong-platform image) is treated as "does not exist":
// this feeds the advisory prepull path, which must never hard-fail on a probe
// error — a false "missing" merely triggers a (safe) pull.
func (c *Compose) ImageExists(ref, platform string) bool {
	args := []string{"image", "inspect"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ref)
	cmd := exec.Command(c.BinName(), args...) //nolint:gosec
	cmd.Dir = c.BaseDir
	cmd.Env = c.BuildEnv()
	return cmd.Run() == nil
}

// PullImage pulls ref (for the given platform; empty = daemon default) via the
// daemon, streaming stdout/stderr to the terminal — base image pulls can take
// minutes, and silence would look like a hang. Mirrors Compose.Exec's
// env/dir/trace handling for a single `docker pull` invocation.
func (c *Compose) PullImage(ref, platform string) error {
	bin := c.BinName()
	args := []string{"pull"}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ref)
	cmd := exec.Command(bin, args...) //nolint:gosec
	cmd.Dir = c.BaseDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = c.BuildEnv()
	trace.Command(context.Background(), bin, args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", trace.FormatCommand(append([]string{bin}, args...)), err)
	}
	return nil
}
