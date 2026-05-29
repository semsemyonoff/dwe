// Package containers groups the docker/daemon/container builtin
// implementations: daemon lifecycle (start/logs/stop/reap), container
// state probes and reap (containers_running, docker_wait_healthy,
// docker_stop_remove_container), and project-scoped volume cleanup
// (docker_remove_project_volumes).
package containers

import "devbox-cli/internal/core/execution/builtin/spec"

// Builtins returns the containers builtin entries keyed by their registered name.
func Builtins() map[string]spec.Entry {
	return map[string]spec.Entry{
		"docker_daemon_start":           {Impl: DaemonStart{}, Kind: spec.KindInternal},
		"docker_daemon_logs":            {Impl: DaemonLogs{}, Kind: spec.KindInternal},
		"docker_daemon_stop":            {Impl: DaemonStop{}, Kind: spec.KindInternal},
		"docker_stop_remove_container":  {Impl: StopRemoveContainer{}, Kind: spec.KindInternal},
		"daemons_reap":                  {Impl: DaemonsReap{}, Kind: spec.KindInternal},
		"containers_running":            {Impl: ContainersRunning{}, Kind: spec.KindPredicate},
		"docker_wait_healthy":           {Impl: WaitHealthy{}, Kind: spec.KindAction},
		"docker_remove_project_volumes": {Impl: RemoveProjectVolumes{}, Kind: spec.KindAction},
	}
}
