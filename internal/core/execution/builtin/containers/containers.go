// Package containers groups the docker/daemon/container builtin
// implementations: daemon lifecycle (start/logs/stop/reap), container
// state probes and reap (containers_running, docker_wait_healthy,
// docker_stop_remove_container), and project-scoped volume cleanup
// (docker_remove_project_volumes).
package containers

import "github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"

// Builtins returns the containers builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"docker_daemon_start":           {Impl: DaemonStart{}, Kind: spec.KindInternal, Summary: "start a named daemon container (docker compose run -d)"},
		"docker_daemon_logs":            {Impl: DaemonLogs{}, Kind: spec.KindInternal, Summary: "tail a daemon container's logs in the foreground (interactive)"},
		"docker_daemon_stop":            {Impl: DaemonStop{}, Kind: spec.KindInternal, Summary: "stop a named daemon container (idempotent)"},
		"docker_stop_remove_container":  {Impl: StopRemoveContainer{}, Kind: spec.KindInternal, Summary: "stop and remove a named container; per-service reset baseline"},
		"daemons_reap":                  {Impl: DaemonsReap{}, Kind: spec.KindInternal, Summary: "stop every project daemon container; auto-injected as _auto_reap_daemons"},
		"containers_running":            {Impl: ContainersRunning{}, Kind: spec.KindPredicate, Summary: "report whether the named containers are running"},
		"docker_wait_healthy":           {Impl: WaitHealthy{}, Kind: spec.KindAction, Summary: "wait until the named containers report healthy"},
		"docker_remove_project_volumes": {Impl: RemoveProjectVolumes{}, Kind: spec.KindAction, Summary: "remove every Docker volume belonging to the project"},
	}
}
