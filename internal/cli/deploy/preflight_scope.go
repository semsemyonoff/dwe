package deploy

import (
	"slices"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// preflightScope returns the services a per-service deploy actually brings up:
// the requested names plus the transitive closure of their service.yml
// depends_on. A per-service run resolves only render-env plus the requested
// services' own deploy.yml phases — it never runs the built-in start/up phase —
// but `dwe docker up <svc>` does not pass --no-deps, so compose starts the
// dependency chain too. Those are exactly the ports the run can fail to bind,
// and the only ones env.ports_free may check (see preflight.WithServices).
//
// Dependencies are followed only when they exist in cfg.Services and are
// enabled: a disabled service is never started, and collectDeclaredPorts skips
// it anyway. Requested names pass through as given, unfiltered — a disabled
// requested service contributes no declared ports for the same reason, and
// filtering here would hide a caller mistake behind a silent empty scope.
// A depends_on cycle terminates on the visited set. The result is sorted so the
// scope is stable across runs.
//
// Known limitation: the closure follows service.yml depends_on only. A
// dependency declared solely in a compose file, or a service a per-service
// deploy.yml step starts on its own, stays out of scope — a conflict on its host
// port then surfaces as a compose bind error mid-run instead of as the
// ports_free diagnostic. Widening the scope back to the whole project is the
// wrong trade: it is what made an untouched service's busy port block every
// per-service deploy.
func preflightScope(cfg *config.DweConfig, names []string) []string {
	if cfg == nil || len(names) == 0 {
		return nil
	}
	visited := make(map[string]bool, len(names))
	queue := make([]string, 0, len(names))
	for _, name := range names {
		if visited[name] {
			continue
		}
		visited[name] = true
		queue = append(queue, name)
	}
	for i := 0; i < len(queue); i++ {
		svc, ok := cfg.Services[queue[i]]
		if !ok {
			continue
		}
		for _, dep := range svc.DependsOn {
			if visited[dep] {
				continue
			}
			depCfg, known := cfg.Services[dep]
			if !known || !depCfg.Enabled {
				continue
			}
			visited[dep] = true
			queue = append(queue, dep)
		}
	}
	slices.Sort(queue)
	return queue
}
