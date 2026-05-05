package builtin

import (
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

func (serviceConfigsCheckBuiltin) Run(with map[string]any, ctx ExecContext) error {
	serviceName := getStringParam(with, "service", "")

	svc, ok := ctx.Config.Services[serviceName]
	if !ok {
		return fmt.Errorf("service %q not found in config", serviceName)
	}

	destDir := filepath.Join(ctx.ProjectRoot, svc.Dir, "configs")

	var missing []string
	for _, entry := range svc.Configs {
		dest := filepath.Join(destDir, entry.File)
		fi, err := os.Stat(dest)
		if err != nil || !fi.Mode().IsRegular() {
			missing = append(missing, filepath.Join(svc.Dir, "configs", entry.File))
		}
	}

	if len(missing) > 0 {
		if ctx.Output != nil {
			for _, f := range missing {
				ctx.Output.Error("missing config: " + f)
			}
		}
		return fmt.Errorf("missing config files: %s", strings.Join(missing, ", "))
	}
	return nil
}
