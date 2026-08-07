package envtest

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Manifest is the durable, on-disk record of a single scenario run. It is
// written BEFORE any Docker interaction (spec §6) so a half-dead run — killed
// mid-deploy, or left behind by --keep — is still sweepable from the manifest
// alone: teardown (and `dwe test clean`) consume only the manifest + copy
// contents, never the original scenario definition.
type Manifest struct {
	// Scenario is the scenario name this run belongs to.
	Scenario string `yaml:"scenario"`
	// RunID is the 6-hex-char identifier disambiguating this run.
	RunID string `yaml:"run_id"`
	// ComposeProject is the exact compose project name used for this run's
	// disposable copy — the sole identity teardown uses to reap containers/
	// volumes (never a name pattern guess).
	ComposeProject string `yaml:"compose_project"`
	// CopyPath is the absolute path to the disposable project copy.
	CopyPath string `yaml:"copy_path"`
	// BridgeDir is the copy's bridge runtime directory (bridge.DefaultBridgeDir(CopyPath)).
	BridgeDir string `yaml:"bridge_dir"`
	// ReportDir is the failure-report directory for this scenario.
	ReportDir string `yaml:"report_dir"`
	// CreatedAt is the UTC creation timestamp, RFC3339.
	CreatedAt time.Time `yaml:"created_at"`
}

// WriteManifest atomically writes m to path (write-temp + rename into the
// same directory, mirroring internal/shared/generatedstore.Save), creating
// the parent directory as needed.
func WriteManifest(path string, m *Manifest) error {
	if m == nil {
		return fmt.Errorf("envtest: cannot write nil manifest")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("envtest: creating manifest directory: %w", err)
	}

	data, err := yaml.Marshal(m)
	if err != nil {
		return fmt.Errorf("envtest: marshalling manifest: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".manifest-*.yml")
	if err != nil {
		return fmt.Errorf("envtest: creating temp manifest file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op once renamed
	}()

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("envtest: writing temp manifest file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("envtest: closing temp manifest file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("envtest: renaming temp manifest file: %w", err)
	}
	return nil
}

// LoadManifest reads and parses a manifest file. A missing file surfaces as
// the underlying os.ErrNotExist (callers use os.IsNotExist), a malformed file
// as a wrapped decode error — no silent defaulting.
func LoadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("envtest: parsing manifest (%s): %w", path, err)
	}
	return &m, nil
}

// DeleteManifest removes a manifest file. A missing file is not an error
// (idempotent — teardown may be retried).
func DeleteManifest(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("envtest: deleting manifest (%s): %w", path, err)
	}
	return nil
}
