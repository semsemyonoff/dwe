package templates

import "devbox-cli/internal/core/project/config"

func appServices(services map[string]config.ServiceConfig) map[string]config.ServiceConfig {
	apps := make(map[string]config.ServiceConfig)
	for name, svc := range services {
		if svc.IsApp() {
			apps[name] = svc
		}
	}
	return apps
}
