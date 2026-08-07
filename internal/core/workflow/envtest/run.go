package envtest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// runIDBytes is the raw entropy of a run ID (3 bytes = 6 lowercase hex chars).
const runIDBytes = 3

// NewRunID returns a fresh run identifier: 6 lowercase hex characters. It
// disambiguates concurrent/successive runs of the same scenario sharing the
// same compose-project-name prefix.
func NewRunID() (string, error) {
	var b [runIDBytes]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("envtest: generating run ID: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// composeNameDisallowed matches any character outside the compose
// project-name charset ([a-z0-9_-]); each match is collapsed to a single "-".
var composeNameDisallowed = regexp.MustCompile(`[^a-z0-9_-]+`)

// projectBaseName returns the base fragment used to derive a scenario run's
// compose project name: cfg.Project.Prefix when set, else cfg.Project.Name.
// Shared by ComposeProjectName and Clean's orphan-scan prefix so the two can
// never drift apart.
func projectBaseName(cfg *config.DweConfig) string {
	if cfg.Project.Prefix != "" {
		return cfg.Project.Prefix
	}
	return cfg.Project.Name
}

// normalizeComposeName lowercases s and collapses every run of characters
// outside the compose project-name charset ([a-z0-9_-]) into a single "-".
func normalizeComposeName(s string) string {
	s = strings.ToLower(s)
	return composeNameDisallowed.ReplaceAllString(s, "-")
}

// ComposeProjectName derives the compose project name for a scenario run:
// "<base>-t-<scenario>-<runID>", where base is projectBaseName(cfg). Scenario
// names and run IDs are already valid fragments, but the base (arbitrary
// project name/prefix) is not, so the whole result is normalised.
func ComposeProjectName(cfg *config.DweConfig, scenario, runID string) string {
	return normalizeComposeName(projectBaseName(cfg) + "-t-" + scenario + "-" + runID)
}

// testsRootDir returns the .dwe/tests root for a project.
func testsRootDir(baseDir string) string {
	return filepath.Join(baseDir, ".dwe", "tests")
}

// RunDir returns the disposable copy directory for a scenario:
// .dwe/tests/runs/<scenario>.
func RunDir(baseDir, scenario string) string {
	return filepath.Join(testsRootDir(baseDir), "runs", scenario)
}

// LockPath returns the per-scenario flock path:
// .dwe/tests/locks/<scenario>.lock.
func LockPath(baseDir, scenario string) string {
	return filepath.Join(testsRootDir(baseDir), "locks", scenario+".lock")
}

// ManifestPath returns the durable run-manifest path for a scenario run:
// .dwe/tests/manifests/<scenario>-<runID>.yml.
func ManifestPath(baseDir, scenario, runID string) string {
	return filepath.Join(testsRootDir(baseDir), "manifests", scenario+"-"+runID+".yml")
}

// ManifestsDir returns the manifest directory for a project (used to glob
// existing manifests for a scenario, e.g. the kept-run guard).
func ManifestsDir(baseDir string) string {
	return filepath.Join(testsRootDir(baseDir), "manifests")
}

// manifestFileName matches "<scenario>-<6-hex-char-run-id>.yml" — any
// scenario, generalising manifestRunIDSuffix (which is anchored to one known
// scenario prefix) for a directory-wide listing.
var manifestFileName = regexp.MustCompile(`^.+-[0-9a-f]{6}\.yml$`)

// ListManifests returns every manifest file under baseDir's manifests
// directory, across all scenarios, sorted. An absent manifests directory
// yields (nil, nil) — nothing to sweep. Used by Clean to enumerate the full
// candidate set before any scenario filtering is applied.
func ListManifests(baseDir string) ([]string, error) {
	dir := ManifestsDir(baseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("envtest: reading manifests directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if e.IsDir() || !manifestFileName.MatchString(e.Name()) {
			continue
		}
		matches = append(matches, filepath.Join(dir, e.Name()))
	}
	sort.Strings(matches)
	return matches, nil
}

// ReportsDir returns the failure-report directory for a scenario:
// .dwe/tests/reports/<scenario>.
func ReportsDir(baseDir, scenario string) string {
	return filepath.Join(testsRootDir(baseDir), "reports", scenario)
}

// ScrubComposeEnv unsets every COMPOSE_* environment variable in the current
// process, leaving DOCKER_* untouched (daemon/context selection must still
// apply). It must be called exactly once, at the very top of `dwe test run`,
// before the per-scenario flock, any goroutine, UI, or subprocess spawn (spec
// §3): the disposable copy's compose invocations must never inherit an
// ambient COMPOSE_PROJECT_NAME/COMPOSE_FILE from the developer's shell. This
// is unrelated to the bridge daemon's dangerous-env strip set (session.go) —
// there is no trust boundary here, only environment hygiene.
func ScrubComposeEnv() {
	for _, kv := range os.Environ() {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(name, "COMPOSE_") {
			_ = os.Unsetenv(name)
		}
	}
}
