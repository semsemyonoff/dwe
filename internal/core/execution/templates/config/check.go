package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	projectconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/pathsafe"
)

// CheckRendered verifies that every config-pack render target for serviceName
// exists on disk as a regular file under the service hub dir (svc.Dir). It
// resolves the pack and manifest exactly like RenderConfigs but writes nothing.
//
// It returns the project-root-relative paths of any missing targets and whether
// a config pack resolved. When no pack resolves (config rendering is opt-in) it
// returns found=false with no missing list — there is nothing to check.
//
// This backs the service_configs_render_check builtin: pairing it on the render
// step as a check: forces the render to re-run every deploy (the hasCheck → Run
// lever), mirroring service_configs_copy + service_configs_check.
func CheckRendered(projectRoot string, cfg *projectconfig.DweConfig, serviceName string) ([]string, bool, error) {
	if cfg == nil {
		return nil, false, errors.New("config check: nil cfg")
	}
	svc, ok := cfg.Services[serviceName]
	if !ok {
		return nil, false, fmt.Errorf("config check: unknown service %q", serviceName)
	}

	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return nil, false, fmt.Errorf("resolve project root: %w", err)
	}

	packDir, packName, found, err := ResolveTemplatePack(svc, cfg.Services, absRoot, serviceName)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}

	if filepath.Clean(svc.Dir) == "." || svc.Dir == "" {
		return nil, false, fmt.Errorf("config check: service %q has no dir", serviceName)
	}
	absHubDir := filepath.Join(absRoot, svc.Dir)
	if _, err := pathsafe.ContainedRel(absRoot, absHubDir); err != nil {
		return nil, false, fmt.Errorf("config check: service dir %q escapes project root: %w", svc.Dir, err)
	}

	m, err := LoadManifest(packDir)
	if err != nil {
		return nil, false, err
	}
	if err := ValidateManifest(m, absRoot, packName, absHubDir); err != nil {
		return nil, false, err
	}

	var missing []string
	for _, entry := range m.Render {
		absDest := filepath.Join(absHubDir, entry.To)
		if _, err := pathsafe.ContainedRel(absHubDir, absDest); err != nil {
			return nil, false, fmt.Errorf("config check: dest %q escapes service dir: %w", entry.To, err)
		}
		fi, err := os.Stat(absDest)
		if err != nil || !fi.Mode().IsRegular() {
			missing = append(missing, filepath.Join(svc.Dir, entry.To))
		}
	}
	return missing, true, nil
}
