// Package envtest holds the declarative integration-test scenario format and
// its loader. A scenario lives at workspace/tests/<name>.yml and describes how a
// disposable copy of the project differs from the working environment (enabled/
// disabled services, var overrides), a wall-clock timeout, and an ordered list
// of pipeline steps run after an implicit clean-slate deploy.
//
// This package (stage 1a) owns only the schema, the strict loader, and scenario
// discovery/name validation. The runner, isolation, CLI, and teardown land in
// stage 1b; the loader-side ${...} rendering of step bodies lives in render.go.
package envtest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// AutoPortSentinel is the only magic value permitted for an env.vars entry: it
// asks the stage-1b runner to allocate a free host port before deploy and write
// the concrete number into the copy's local.yml. In stage 1a the value is kept
// as a raw string — no typed field, no allocation logic.
const AutoPortSentinel = "auto"

// scenarioNamePattern is the compose-project-name-fragment rule a scenario file
// basename (without .yml) must already satisfy. Names are never sanitised — a
// non-matching name is rejected so the eventual compose project name stays valid.
var scenarioNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Scenario is the parsed form of a workspace/tests/<name>.yml file. The scenario
// name is the file basename, not a field (symmetric with workspace/services/<name>/).
type Scenario struct {
	// Description is a human-readable summary shown by `dwe test list`.
	Description string `yaml:"description,omitempty"`
	// Env describes how the disposable copy differs from the working environment.
	Env ScenarioEnv `yaml:"env,omitempty"`
	// Timeout is the wall-clock budget for the whole scenario (e.g. "15m").
	// Kept as a raw string here (parsed by the stage-1b runner), matching the
	// string-timeout convention used elsewhere in config.
	Timeout string `yaml:"timeout,omitempty"`
	// Steps are ordinary pipeline steps run after the implicit deploy, in the
	// existing deploy-step schema. A scenario with no steps is valid.
	Steps []config.DeployStep `yaml:"steps,omitempty"`
}

// ScenarioEnv captures the per-scenario overrides applied to the copy.
type ScenarioEnv struct {
	// Services toggles which services are enabled/disabled in the copy.
	Services ScenarioServices `yaml:"services,omitempty"`
	// Vars overrides project vars in the copy's local.yml. Values are scalars,
	// plus the AutoPortSentinel string for host-port allocation (stage 1b).
	Vars map[string]any `yaml:"vars,omitempty"`
}

// ScenarioServices lists services to force-enable or force-disable in the copy.
type ScenarioServices struct {
	Enable  []string `yaml:"enable,omitempty"`
	Disable []string `yaml:"disable,omitempty"`
}

// LoadScenario reads and strict-decodes a single scenario file. Unlike the
// pipeline loaders, an empty / all-comment file is an error rather than an
// absent-and-defaulted document: a scenario has no meaningful default (spec §8).
// The scenario name (file basename without .yml) is validated, then step shape
// is checked via config.ValidateDeploySteps.
func LoadScenario(path string) (*Scenario, error) {
	name := ScenarioNameFromPath(path)
	if err := ValidateScenarioName(name); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var scn Scenario
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	// Deliberate divergence from the pipeline loaders: io.EOF (empty / all-comment
	// document) is NOT tolerated. An empty scenario is a user mistake, so surface it.
	if err := dec.Decode(&scn); err != nil {
		return nil, fmt.Errorf("scenario file is empty or invalid (%s): %w", path, err)
	}
	if err := config.ValidateDeploySteps(scn.Steps, "scenario "+name); err != nil {
		return nil, fmt.Errorf("scenario %q: %w", name, err)
	}
	return &scn, nil
}

// ScenarioNameFromPath returns the scenario name for a file path: the basename
// with its .yml/.yaml extension stripped.
func ScenarioNameFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(strings.TrimSuffix(base, ".yaml"), ".yml")
}

// ScenarioPath resolves a scenario name (basename without extension, as returned
// by ListScenarios) to its on-disk file under workspace/tests/, honoring BOTH
// .yml and .yaml — the same extensions ListScenarios discovers. Callers must use
// this instead of reconstructing name+".yml", which would fail to open a .yaml
// scenario. .yml wins when — pathologically — both are present. Returns an error
// if neither exists.
func ScenarioPath(baseDir, name string) (string, error) {
	dir := TestsDir(baseDir)
	for _, ext := range []string{".yml", ".yaml"} {
		p := filepath.Join(dir, name+ext)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("scenario %q: no %s.yml or %s.yaml under %s", name, name, name, dir)
}

// ValidateScenarioName rejects a name that does not already match the
// compose-project-name-fragment rule. Names are never case-folded or sanitised.
func ValidateScenarioName(name string) error {
	if !scenarioNamePattern.MatchString(name) {
		return fmt.Errorf("invalid scenario name %q: must match %s (lowercase alphanumerics, '-', '_'; no leading separator)", name, scenarioNamePattern.String())
	}
	return nil
}

// TestsDir returns the scenario directory for a project root.
func TestsDir(baseDir string) string {
	return filepath.Join(baseDir, "workspace", "tests")
}

// ListScenarios returns the sorted scenario names discovered under
// workspace/tests/*.yml. A file whose basename is not a valid scenario name is
// a hard error (no silent skipping). An absent tests directory yields an empty
// list and no error.
func ListScenarios(baseDir string) ([]string, error) {
	dir := TestsDir(baseDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".yml" && ext != ".yaml" {
			continue
		}
		name := ScenarioNameFromPath(e.Name())
		if err := ValidateScenarioName(name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}
