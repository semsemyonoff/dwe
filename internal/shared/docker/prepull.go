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

// composeConfigJSON is the narrow subset of `compose config --format json`
// this package cares about: per-service build context needed to locate and
// parse the Dockerfile that produces that service's image.
type composeConfigJSON struct {
	Services map[string]struct {
		Build *struct {
			Context          string             `json:"context"`
			Dockerfile       string             `json:"dockerfile"`
			DockerfileInline string             `json:"dockerfile_inline"`
			Args             map[string]*string `json:"args"`
		} `json:"build"`
	} `json:"services"`
}

// DeriveBuildBases returns the sorted, deduplicated set of external FROM base
// image references used by the given services' Dockerfiles. An empty
// services slice means all services in the compose config output.
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
// overriding it with an empty string) as overrides.
//
// An unreadable Dockerfile skips just that service (with a trace warning),
// not the whole derivation — this function is advisory, like the rest of the
// prepull mechanism, and a single bad service must not blind the caller to
// every other service's bases.
func (c *Compose) DeriveBuildBases(services []string) ([]string, error) {
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
	var refs []string
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

		for _, ref := range externalBaseRefs(content, buildArgs) {
			if _, dup := seen[ref]; !dup {
				seen[ref] = struct{}{}
				refs = append(refs, ref)
			}
		}
	}

	sort.Strings(refs)
	return refs, nil
}

// ImageExists reports whether ref is present in the local image store.
// It shells out to `docker image inspect` directly (not via compose) since
// image presence is daemon-scoped, not project-scoped. env/dir are set
// (unlike volumeExists, which sets neither) so DOCKER_HOST/context overrides
// from ProcessEnv apply — this is a deliberate deviation, not an oversight.
// A probe failure of any kind (missing binary, daemon unreachable, or a
// genuinely missing image) is treated as "does not exist": this feeds the
// advisory prepull path, which must never hard-fail on a probe error.
func (c *Compose) ImageExists(ref string) bool {
	cmd := exec.Command(c.BinName(), "image", "inspect", ref) //nolint:gosec
	cmd.Dir = c.BaseDir
	cmd.Env = c.BuildEnv()
	return cmd.Run() == nil
}

// PullImage pulls ref via the daemon, streaming stdout/stderr to the
// terminal — base image pulls can take minutes, and silence would look like
// a hang. Mirrors Compose.Exec's env/dir/trace handling for a single
// `docker pull` invocation.
func (c *Compose) PullImage(ref string) error {
	bin := c.BinName()
	args := []string{"pull", ref}
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
