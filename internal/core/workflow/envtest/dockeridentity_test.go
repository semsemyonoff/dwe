package envtest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestWriteDockerIdentity_WithExistingDockerYAML_WritesLocalOverlay(t *testing.T) {
	copyRoot := t.TempDir()
	workspaceDir := filepath.Join(copyRoot, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	baseYML := `
project_name: "${project.prefix}-${project.name}"
args:
  up: ["-d", "--remove-orphans"]
`
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.yml"), []byte(baseYML), 0o644); err != nil {
		t.Fatalf("write docker.yml: %v", err)
	}
	// Simulate a stray copied docker.local.yml with an unrelated project_name —
	// this branch must overwrite it, not merge with it.
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.local.yml"), []byte("project_name: stale\n"), 0o644); err != nil {
		t.Fatalf("write stale docker.local.yml: %v", err)
	}

	if err := WriteDockerIdentity(copyRoot, "myproj-t-scn-abc123"); err != nil {
		t.Fatalf("WriteDockerIdentity: %v", err)
	}

	localPath := filepath.Join(workspaceDir, "docker.local.yml")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("read docker.local.yml: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse docker.local.yml: %v", err)
	}
	if raw["project_name"] != "myproj-t-scn-abc123" {
		t.Errorf("docker.local.yml project_name = %v, want myproj-t-scn-abc123", raw["project_name"])
	}
	if _, hasArgs := raw["args"]; hasArgs {
		t.Errorf("docker.local.yml should hold only project_name, got args key too: %v", raw)
	}

	// The base docker.yml must be untouched.
	baseData, err := os.ReadFile(filepath.Join(workspaceDir, "docker.yml"))
	if err != nil {
		t.Fatalf("read docker.yml: %v", err)
	}
	if string(baseData) != baseYML {
		t.Errorf("docker.yml was modified, want untouched:\n%s", string(baseData))
	}

	cfg := &config.DweConfig{Raw: map[string]any{}}
	name, err := config.ResolveComposeProjectName(copyRoot, cfg)
	if err != nil {
		t.Fatalf("ResolveComposeProjectName: %v", err)
	}
	if name != "myproj-t-scn-abc123" {
		t.Errorf("ResolveComposeProjectName = %q, want myproj-t-scn-abc123", name)
	}
}

func TestWriteDockerIdentity_WithoutDockerYAML_WritesNeutralBaseFile(t *testing.T) {
	copyRoot := t.TempDir()
	workspaceDir := filepath.Join(copyRoot, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	// A stray copied docker.local.yml with no base docker.yml must be removed —
	// otherwise it would activate for the first time in the generated copy.
	if err := os.WriteFile(filepath.Join(workspaceDir, "docker.local.yml"), []byte("project_name: stale\n"), 0o644); err != nil {
		t.Fatalf("write stray docker.local.yml: %v", err)
	}

	if err := WriteDockerIdentity(copyRoot, "myproj-t-scn-abc123"); err != nil {
		t.Fatalf("WriteDockerIdentity: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workspaceDir, "docker.local.yml")); !os.IsNotExist(err) {
		t.Fatalf("stray docker.local.yml should have been removed, stat err = %v", err)
	}

	cfg := &config.DweConfig{Raw: map[string]any{}}
	name, err := config.ResolveComposeProjectName(copyRoot, cfg)
	if err != nil {
		t.Fatalf("ResolveComposeProjectName: %v", err)
	}
	if name != "myproj-t-scn-abc123" {
		t.Errorf("ResolveComposeProjectName = %q, want myproj-t-scn-abc123", name)
	}

	// Semantics-equivalence: LoadDockerConfig over the generated docker.yml must
	// yield DockerArgs identical to LoadDockerConfigOrEmpty's missing-file
	// zero-value — no up/logs/run/down defaults leaking in.
	generated, err := config.LoadDockerConfig(copyRoot, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfig(generated): %v", err)
	}

	emptyDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(emptyDir, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace/ (empty): %v", err)
	}
	baseline, err := config.LoadDockerConfigOrEmpty(emptyDir, cfg)
	if err != nil {
		t.Fatalf("LoadDockerConfigOrEmpty(missing): %v", err)
	}

	if len(generated.Args.Up) != len(baseline.Args.Up) ||
		len(generated.Args.Logs) != len(baseline.Args.Logs) ||
		len(generated.Args.Run) != len(baseline.Args.Run) ||
		len(generated.Args.Down) != len(baseline.Args.Down) ||
		len(generated.Args.Global) != len(baseline.Args.Global) ||
		len(generated.Args.Stop) != len(baseline.Args.Stop) ||
		len(generated.Args.Restart) != len(baseline.Args.Restart) ||
		len(generated.Args.Ps) != len(baseline.Args.Ps) ||
		len(generated.Args.Exec) != len(baseline.Args.Exec) ||
		len(generated.Args.Pull) != len(baseline.Args.Pull) ||
		len(generated.Args.Build) != len(baseline.Args.Build) {
		t.Errorf("generated DockerArgs %+v not semantics-neutral vs missing-file baseline %+v", generated.Args, baseline.Args)
	}
}

func TestWriteDockerIdentity_WithoutDockerYAML_NoStrayLocalFile(t *testing.T) {
	copyRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(copyRoot, "workspace"), 0o755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}

	if err := WriteDockerIdentity(copyRoot, "proj-t-scn-000000"); err != nil {
		t.Fatalf("WriteDockerIdentity: %v", err)
	}

	if _, err := os.Stat(filepath.Join(copyRoot, "workspace", "docker.local.yml")); !os.IsNotExist(err) {
		t.Fatalf("no docker.local.yml should exist, stat err = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(copyRoot, "workspace", "docker.yml"))
	if err != nil {
		t.Fatalf("read generated docker.yml: %v", err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("parse generated docker.yml: %v", err)
	}
	argsRaw, ok := raw["args"].(map[string]any)
	if !ok {
		t.Fatalf("generated docker.yml args: got %T, want map", raw["args"])
	}
	for _, key := range dockerArgsKeys {
		v, present := argsRaw[key]
		if !present {
			t.Errorf("generated docker.yml args missing key %q", key)
			continue
		}
		list, ok := v.([]any)
		if !ok || len(list) != 0 {
			t.Errorf("generated docker.yml args[%q] = %v, want explicit []", key, v)
		}
	}
}

// TestDockerArgsKeys_CoversAllDockerArgsFields guards the hand-maintained
// dockerArgsKeys list against silent drift from config.DockerArgs. If a field is
// added to DockerArgs without a matching entry here, the generated docker.yml's
// args: block would omit that key, so LoadDockerConfig's per-key default (not the
// empty-value neutral one) would silently apply in every disposable test copy —
// a compile-time-invisible regression. Keep the two exactly in sync.
func TestDockerArgsKeys_CoversAllDockerArgsFields(t *testing.T) {
	var want []string
	for _, f := range reflect.VisibleFields(reflect.TypeFor[config.DockerArgs]()) {
		tag := f.Tag.Get("yaml")
		if comma := strings.IndexByte(tag, ','); comma >= 0 {
			tag = tag[:comma]
		}
		if tag == "" || tag == "-" {
			continue
		}
		want = append(want, tag)
	}

	have := make(map[string]bool, len(dockerArgsKeys))
	for _, k := range dockerArgsKeys {
		have[k] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("dockerArgsKeys is missing config.DockerArgs YAML key %q — add it so the generated docker.yml keeps that key semantics-neutral", w)
		}
	}
	if len(dockerArgsKeys) != len(want) {
		t.Errorf("dockerArgsKeys has %d keys, config.DockerArgs has %d YAML fields; keep them in sync (dockerArgsKeys=%v, DockerArgs=%v)",
			len(dockerArgsKeys), len(want), dockerArgsKeys, want)
	}
}
