package builtin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type serviceConfigsCheckBuiltin struct{}

func (serviceConfigsCheckBuiltin) Validate(with map[string]any) error {
	service := getStringParam(with, "service", "")
	if service == "" {
		return fmt.Errorf("builtin service_configs_check: missing required param 'service'")
	}
	return nil
}

func (serviceConfigsCheckBuiltin) Describe(with map[string]any) string {
	service := getStringParam(with, "service", "")
	return fmt.Sprintf("builtin: service_configs_check(service=%s)", service)
}

func (serviceConfigsCheckBuiltin) Run(_ context.Context, with map[string]any, ectx ExecContext) error {
	serviceName := getStringParam(with, "service", "")

	svc, ok := ectx.Config.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found in config", serviceName)
	}
	if svc.Dir == "" {
		return fmt.Errorf("service %q: dir is not set", serviceName)
	}

	destDir := filepath.Join(ectx.ProjectRoot, svc.Dir, "configs")
	cleanDestDir := filepath.Clean(destDir)

	var missing []string
	for _, entry := range svc.Configs {
		dest := filepath.Join(destDir, entry.File)
		cleanDest := filepath.Clean(dest)
		if cleanDest == cleanDestDir || !strings.HasPrefix(cleanDest, cleanDestDir+string(filepath.Separator)) {
			return fmt.Errorf("service %q: config %q escapes the configs directory", serviceName, entry.File)
		}
		fi, err := os.Stat(dest)
		if err != nil || !fi.Mode().IsRegular() {
			missing = append(missing, filepath.Join(svc.Dir, "configs", entry.File))
		}
	}

	if len(missing) > 0 {
		if ectx.Output != nil {
			for _, f := range missing {
				ectx.Output.Error("missing config: " + f)
			}
		}
		return fmt.Errorf("missing config files: %s", strings.Join(missing, ", "))
	}
	return nil
}
