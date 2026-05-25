package deploy

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/config"
	"devbox-cli/internal/pipeline"
	"devbox-cli/internal/usercommands/registry"
)

// ImplicitEnvStep is always the first step of any deploy plan.
// It regenerates .env from the current config before any phase runs.
var ImplicitEnvStep = config.DeployStep{
	Name:        "render-env",
	Type:        "devbox",
	Cmd:         "render env -o .env",
	Description: "Generate .env from config (implicit first step)",
}

// ResolvePlan builds the ordered list of active steps from cfg.Deploy.
// The implicit .env generation step is always prepended as step 0 (no phase).
// Steps whose when condition evaluates to false are excluded.
// Phases with deploy_services=true are expanded by inlining per-service
// deploy pipelines in topological dependency order.
// reg (registry) is used to validate files_gate directives and must be non-nil
// for runtime callers (deploy run, etc.).
func ResolvePlan(cfg *config.DevboxConfig, reg *registry.Registry) ([]pipeline.ResolvedStep, error) {
	implicit := pipeline.ResolvedStep{
		Phase: config.DeployPhase{Name: "env", Description: "Environment"},
		Step:  ImplicitEnvStep,
	}
	result := []pipeline.ResolvedStep{implicit}

	for _, phase := range cfg.Deploy.Phases {
		if phase.DeployServices {
			serviceSteps, err := ResolveServicesPlan(cfg, reg)
			if err != nil {
				return nil, fmt.Errorf("resolving services deploy: %w", err)
			}
			result = append(result, serviceSteps...)
			continue
		}
		resolved, err := pipeline.ResolvePhaseSteps(cfg, reg, phase, "")
		if err != nil {
			return nil, err
		}
		result = append(result, resolved...)
	}

	return result, nil
}

// FindStep looks up a step by address in the deploy config.
// Supports two forms:
//   - "<phase>/<step>"           — orchestrator step
//   - "<service>/<phase>/<step>" — per-service step (loaded from devbox/services/<service>/deploy.yml)
func FindStep(cfg *config.DevboxConfig, address string) (config.DeployPhase, config.DeployStep, error) {
	parts := strings.Split(address, "/")
	switch len(parts) {
	case 2:
		phaseName, stepName := parts[0], parts[1]
		for _, phase := range cfg.Deploy.Phases {
			if phase.Name != phaseName {
				continue
			}
			for _, step := range phase.Steps {
				if step.Name == stepName {
					return phase, step, nil
				}
			}
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q", stepName, phaseName)
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found", phaseName)

	case 3:
		serviceName, phaseName, stepName := parts[0], parts[1], parts[2]
		if _, ok := cfg.Services[serviceName]; !ok {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("service %q not found", serviceName)
		}
		cfgPath, ok := cfg.Raw["__configPath"].(string)
		if !ok {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("internal: __configPath missing from config")
		}
		baseDir := filepath.Dir(cfgPath)
		svcDeploy, err := config.LoadServiceDeployConfig(filepath.Join(baseDir, "devbox", "services", serviceName, "deploy.yml"))
		if err != nil {
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("loading deploy config for service %q: %w", serviceName, err)
		}
		for _, phase := range svcDeploy.Phases {
			if phase.Name != phaseName {
				continue
			}
			for _, step := range phase.Steps {
				if step.Name == stepName {
					return phase, step, nil
				}
			}
			return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("step %q not found in phase %q of service %q", stepName, phaseName, serviceName)
		}
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("phase %q not found in service %q", phaseName, serviceName)

	default:
		return config.DeployPhase{}, config.DeployStep{}, fmt.Errorf("invalid step address %q: expected <phase>/<step> or <service>/<phase>/<step>", address)
	}
}

// SourceDotEnv reads a .env file and sets each KEY=VALUE pair as an OS
// environment variable so that subsequent exec.Cmd calls (with Env: nil)
// inherit them. Blank lines and comments are skipped.
func SourceDotEnv(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val := strings.TrimSpace(value)
		if n := len(val); n >= 2 && val[0] == val[n-1] && (val[0] == '"' || val[0] == '\'') {
			val = val[1 : n-1]
		}
		if err := os.Setenv(strings.TrimSpace(key), val); err != nil {
			return fmt.Errorf("setenv %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	return nil
}

// IsRegularFile reports whether path exists and is a regular file.
func IsRegularFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
