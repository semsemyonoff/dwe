package registry

import (
	"strings"
	"testing"

	"devbox-cli/internal/core/usercommands/model"
)

const daemonYAML = `
commands:
  queue:
    type: daemon
    description: Laravel queue worker
    service: app-main
    workdir_from: services.main.work_dir_internal
    user: www-data
    env:
      QUEUE_CONNECTION: redis
    params:
      name:
        default: default
        pattern: ^[a-zA-Z0-9_-]+$
    argv:
      - php
      - artisan
      - queue:listen
      - --queue=${param.name}
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error
      stop_timeout: 10s
`

func TestLoadRegistry_Daemon_ExpandsFourSynthetics(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})

	for _, suffix := range []string{".start", ".logs", ".stop", ".restart"} {
		id := "services.main.queue" + suffix
		cmd, err := reg.Get(id)
		if err != nil {
			t.Errorf("expected %q to exist: %v", id, err)
			continue
		}
		if cmd.DerivedFromDaemon != "services.main.queue" {
			t.Errorf("%s: DerivedFromDaemon = %q, want services.main.queue",
				id, cmd.DerivedFromDaemon)
		}
		if cmd.SourceDaemon == nil {
			t.Errorf("%s: SourceDaemon is nil", id)
		}
		if cmd.Daemon != nil {
			t.Errorf("%s: Daemon should be nil on synthetic; got %#v", id, cmd.Daemon)
		}
		if cmd.Private {
			t.Errorf("%s: synthetic should be public", id)
		}
		// Synthetics must pass cmd.Validate() — no leakage check should fire.
		if err := cmd.Validate(); err != nil {
			t.Errorf("%s: synthetic.Validate(): %v", id, err)
		}
	}
}

func TestLoadRegistry_Daemon_SourceNotRunnable(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	if _, err := reg.Get("services.main.queue"); err == nil {
		t.Error("source daemon should NOT be runnable; Get returned no error")
	}
}

func TestLoadRegistry_Daemon_SourceNotInParentGroup(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	root := reg.Groups()
	// Walk to services.main
	var mainNode *GroupNode
	for _, ch := range root.Children {
		if ch.Name == "services" {
			for _, sub := range ch.Children {
				if sub.Name == "main" {
					mainNode = sub
				}
			}
		}
	}
	if mainNode == nil {
		t.Fatal("services.main group node missing")
	}
	for _, cmd := range mainNode.Commands {
		if cmd.ID == "services.main.queue" || cmd.LocalName == "queue" {
			t.Errorf("source daemon command leaked into parent group: %q", cmd.ID)
		}
	}
}

func TestLoadRegistry_Daemon_GroupNodeForBase(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	// Walk to services.main.queue group node
	gn, ok := reg.groups["services.main.queue"]
	if !ok {
		t.Fatal("group node for daemon base ID not created")
	}
	if gn.Meta.Title != "queue" {
		t.Errorf("group Meta.Title = %q, want queue", gn.Meta.Title)
	}
	if gn.Meta.Description != "Laravel queue worker" {
		t.Errorf("group Meta.Description = %q, want propagated from daemon", gn.Meta.Description)
	}
	if len(gn.Commands) != 4 {
		t.Errorf("group should hold 4 synthetic commands, got %d", len(gn.Commands))
	}
}

func TestLoadRegistry_Daemon_StartHasExecutionFields(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	cmd, err := reg.Get("services.main.queue.start")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Type != model.CommandTypeBuiltin {
		t.Errorf("type = %q, want builtin", cmd.Type)
	}
	if cmd.Cmd != "docker_daemon_start" {
		t.Errorf("Cmd = %q, want docker_daemon_start", cmd.Cmd)
	}
	if cmd.With["service"] != "app-main" {
		t.Errorf("with[service] = %v", cmd.With["service"])
	}
	if cmd.With["container_template"] != "php_queue_${param.name}" {
		t.Errorf("with[container_template] = %v", cmd.With["container_template"])
	}
	if cmd.With["daemon_id"] != "services.main.queue" {
		t.Errorf("with[daemon_id] = %v", cmd.With["daemon_id"])
	}
	if cmd.With["on_already_running"] != "error" {
		t.Errorf("with[on_already_running] = %v, want \"error\"", cmd.With["on_already_running"])
	}
	// argv must be []any so renderBuiltinWith can template each element.
	argv, ok := cmd.With["argv"].([]any)
	if !ok {
		t.Fatalf("with[argv] type = %T, want []any", cmd.With["argv"])
	}
	if len(argv) != 4 {
		t.Errorf("argv length = %d, want 4", len(argv))
	}
	// label_params keys map to ${param.<n>} template literals.
	lp, ok := cmd.With["label_params"].(map[string]any)
	if !ok {
		t.Fatalf("label_params type = %T, want map[string]any", cmd.With["label_params"])
	}
	if lp["name"] != "${param.name}" {
		t.Errorf("label_params[name] = %v, want ${param.name}", lp["name"])
	}
	// env must be map[string]any so renderBuiltinWith walks values.
	env, ok := cmd.With["env"].(map[string]any)
	if !ok {
		t.Fatalf("env type = %T, want map[string]any", cmd.With["env"])
	}
	if env["QUEUE_CONNECTION"] != "redis" {
		t.Errorf("env[QUEUE_CONNECTION] = %v", env["QUEUE_CONNECTION"])
	}
}

func TestLoadRegistry_Daemon_StopCarriesTimeout(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	cmd, err := reg.Get("services.main.queue.stop")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.With["stop_timeout"] != "10s" {
		t.Errorf("stop synth missing stop_timeout: %v", cmd.With)
	}
}

func TestLoadRegistry_Daemon_RestartPassesParams(t *testing.T) {
	reg := mustRegistry(t, map[string]string{
		"services/main.yml": daemonYAML,
	})
	cmd, err := reg.Get("services.main.queue.restart")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Type != model.CommandTypeWorkflow {
		t.Errorf("restart type = %q, want workflow", cmd.Type)
	}
	if len(cmd.Steps) != 2 {
		t.Fatalf("restart steps len = %d, want 2", len(cmd.Steps))
	}
	if cmd.Steps[0].Command != "services.main.queue.stop" {
		t.Errorf("step[0] command = %q, want .stop", cmd.Steps[0].Command)
	}
	if cmd.Steps[1].Command != "services.main.queue.start" {
		t.Errorf("step[1] command = %q, want .start", cmd.Steps[1].Command)
	}
	for i, step := range cmd.Steps {
		if step.With["name"] != "${param.name}" {
			t.Errorf("step[%d].with[name] = %q, want ${param.name}", i, step.With["name"])
		}
	}
	if len(cmd.Params) != 1 {
		t.Errorf("restart params should be copied from source, got %v", cmd.Params)
	}
}

func TestLoadRegistry_Daemon_AlwaysGeneratesAllFour(t *testing.T) {
	yml := `
commands:
  queue:
    type: daemon
    description: always generates all four
    service: app-main
    daemon:
      container_template: q
    params:
      name:
        default: default
        pattern: ^[a-z]+$
`
	reg := mustRegistry(t, map[string]string{"main.yml": yml})
	expected := []string{"main.queue.start", "main.queue.logs", "main.queue.stop", "main.queue.restart"}
	for _, id := range expected {
		if _, err := reg.Get(id); err != nil {
			t.Errorf("%q missing: %v", id, err)
		}
	}
}

func TestLoadRegistry_Daemon_CollisionWithExplicit(t *testing.T) {
	collide := `
commands:
  queue:
    type: daemon
    description: d
    service: app-main
    daemon:
      container_template: q
`
	// Explicit command at services.main.queue.start collides with the
	// daemon-derived synthetic of the same ID.
	explicit := `
commands:
  start:
    type: shell
    cmd: "echo hi"
`
	_, err := buildTestRegistry(t, map[string]string{
		"services/main.yml":       collide,
		"services/main/queue.yml": explicit,
	})
	if err == nil {
		t.Fatal("expected collision error, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "services.main.queue.start") {
		t.Errorf("error should mention synthetic ID, got: %v", err)
	}
	if !strings.Contains(msg, "services.main.queue") {
		t.Errorf("error should mention source daemon base, got: %v", err)
	}
}

func TestLoadRegistry_Daemon_NoParamsSingleInstance(t *testing.T) {
	yml := `
commands:
  cron:
    type: daemon
    description: cron worker
    service: app-main
    daemon:
      container_template: cron
`
	reg := mustRegistry(t, map[string]string{"main.yml": yml})
	cmd, err := reg.Get("main.cron.start")
	if err != nil {
		t.Fatal(err)
	}
	lp, ok := cmd.With["label_params"].(map[string]any)
	if !ok {
		t.Fatalf("label_params missing or wrong type: %T", cmd.With["label_params"])
	}
	if len(lp) != 0 {
		t.Errorf("label_params should be empty when daemon has no params, got %v", lp)
	}
	restart, err := reg.Get("main.cron.restart")
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range restart.Steps {
		if len(s.With) != 0 {
			t.Errorf("step[%d].With should be empty for no-params daemon, got %v", i, s.With)
		}
	}
}
