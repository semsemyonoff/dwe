package envtest

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// dockerArgsKeys lists every config.DockerArgs YAML key, in the order they are
// emitted into a generated docker.yml's args: block.
var dockerArgsKeys = []string{
	"global", "up", "down", "stop", "restart", "logs", "ps", "exec", "run", "pull", "build",
}

// WriteDockerIdentity stamps the disposable copy at copyRoot with projectName
// as its compose project identity (spec §5), choosing the branch that keeps
// the copy semantics-neutral relative to the original project:
//
//   - copy has workspace/docker.yml: write workspace/docker.local.yml holding
//     only project_name — the local layer always wins over the copied base
//     file's own (possibly templated) project_name, per
//     config.ResolveComposeProjectName's precedence.
//   - copy has no workspace/docker.yml: write workspace/docker.yml holding
//     project_name plus an explicit empty list ([]) for every
//     config.DockerArgs key. An args: block with no keys at all would let
//     config.LoadDockerConfig's per-key defaults (up/logs/run/down) kick in —
//     defaults a docker.yml-less project never had. Explicit [] marks every
//     key present (config's detectPresentArgsKeys), opting all of them out,
//     which reproduces config.LoadDockerConfigOrEmpty's missing-file
//     zero-value exactly. Any stray copied docker.local.yml is removed in
//     this branch — a sibling local file would otherwise silently override
//     the generated base file DWE itself just wrote.
func WriteDockerIdentity(copyRoot, projectName string) error {
	workspaceDir := filepath.Join(copyRoot, "workspace")
	dockerPath := filepath.Join(workspaceDir, "docker.yml")
	localPath := filepath.Join(workspaceDir, "docker.local.yml")

	if _, err := os.Stat(dockerPath); err == nil {
		if err := writeYAMLFile(localPath, map[string]any{
			"project_name": projectName,
		}); err != nil {
			return fmt.Errorf("envtest: writing docker.local.yml: %w", err)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("envtest: checking for docker.yml: %w", err)
	}

	args := make(map[string]any, len(dockerArgsKeys))
	for _, key := range dockerArgsKeys {
		args[key] = []string{}
	}
	if err := writeYAMLFile(dockerPath, map[string]any{
		"project_name": projectName,
		"args":         args,
	}); err != nil {
		return fmt.Errorf("envtest: writing docker.yml: %w", err)
	}

	if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("envtest: removing stray docker.local.yml: %w", err)
	}
	return nil
}

// writeYAMLFile atomically marshals v as YAML into path (write-temp + rename
// into the same directory), creating the parent directory as needed.
func writeYAMLFile(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	data, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshalling: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".docker-identity-*.yml")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op once renamed
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp file: %w", err)
	}
	return nil
}
