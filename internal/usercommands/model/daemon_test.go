package model

import (
	"errors"
	"strings"
	"testing"
)

// happy path: daemon YAML round-trips and validates clean.
func TestDaemon_HappyPath(t *testing.T) {
	yaml := `
commands:
  queue:
    type: daemon
    description: "Laravel queue worker"
    service: app-main
    user: www-data
    argv:
      - php
      - artisan
      - queue:listen
    params:
      name:
        default: default
        pattern: "^[a-zA-Z0-9_-]+$"
    daemon:
      container_template: "php_queue_${param.name}"
      on_already_running: error
      auto_remove: true
      stop_timeout: 10s
      controls: [start, logs, stop, restart]
`
	cf := mustParse(t, yaml)
	cmd, ok := cf.Commands["queue"]
	if !ok {
		t.Fatal("command 'queue' not found")
	}
	if cmd.Type != CommandTypeDaemon {
		t.Errorf("Type = %q, want daemon", cmd.Type)
	}
	if cmd.Daemon == nil {
		t.Fatal("Daemon block is nil")
	}
	if cmd.Daemon.ContainerTemplate != "php_queue_${param.name}" {
		t.Errorf("ContainerTemplate = %q, unexpected", cmd.Daemon.ContainerTemplate)
	}
	if cmd.Daemon.OnAlreadyRunning != "error" {
		t.Errorf("OnAlreadyRunning = %q, unexpected", cmd.Daemon.OnAlreadyRunning)
	}
	if cmd.Daemon.AutoRemove == nil || !*cmd.Daemon.AutoRemove {
		t.Error("AutoRemove should be true")
	}
	if cmd.Daemon.StopTimeout != "10s" {
		t.Errorf("StopTimeout = %q, want 10s", cmd.Daemon.StopTimeout)
	}
	if len(cmd.Daemon.Controls) != 4 {
		t.Errorf("Controls len = %d, want 4", len(cmd.Daemon.Controls))
	}
	cmd.ID = "queue"
	if err := cmd.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestDaemon_Validate_TableDriven(t *testing.T) {
	tests := []struct {
		name        string
		yaml        string
		wantErrIs   []error // any sentinel must be matched via errors.Is
		wantErrSubs []string
	}{
		{
			name: "missing service",
			yaml: `
commands:
  d:
    type: daemon
    argv: [echo, hi]
    daemon:
      container_template: foo
`,
			wantErrIs: []error{ErrDaemonServiceRequired},
		},
		{
			name: "service with ${param.X} rejected",
			yaml: `
commands:
  d:
    type: daemon
    service: app-${param.name}
    argv: [echo]
    params:
      name: {default: x}
    daemon:
      container_template: foo
`,
			wantErrIs: []error{ErrDaemonServiceNotLiteral},
		},
		{
			name: "service with {{...}} rejected",
			yaml: `
commands:
  d:
    type: daemon
    service: "{{.queue.service}}"
    argv: [echo]
    daemon:
      container_template: foo
`,
			wantErrIs: []error{ErrDaemonServiceNotLiteral},
		},
		{
			name: "missing container_template",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      on_already_running: noop
`,
			wantErrIs: []error{ErrDaemonContainerTemplateRequired},
		},
		{
			name: "bad on_already_running",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      on_already_running: bogus
`,
			wantErrIs: []error{ErrDaemonOnAlreadyRunningInvalid},
		},
		{
			name: "stop_timeout: 10s accepted",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      stop_timeout: 10s
`,
		},
		{
			name: "stop_timeout garbage rejected",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      stop_timeout: "not-a-duration"
`,
			wantErrIs: []error{ErrDaemonStopTimeoutInvalid},
		},
		{
			name: "stop_timeout negative rejected",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      stop_timeout: "-5s"
`,
			wantErrIs:   []error{ErrDaemonStopTimeoutInvalid},
			wantErrSubs: []string{"must be positive"},
		},
		{
			name: "controls restart without stop",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      controls: [start, restart]
`,
			wantErrIs:   []error{ErrDaemonControlsInvalid},
			wantErrSubs: []string{"restart requires start and stop"},
		},
		{
			name: "controls unknown entry",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
    daemon:
      container_template: foo
      controls: [start, stop, foo]
`,
			wantErrIs:   []error{ErrDaemonControlsInvalid},
			wantErrSubs: []string{"unknown control"},
		},
		{
			name: "multi-field: missing service + bad on_already_running joined",
			yaml: `
commands:
  d:
    type: daemon
    argv: [echo]
    daemon:
      container_template: foo
      on_already_running: bogus
`,
			wantErrIs: []error{ErrDaemonServiceRequired, ErrDaemonOnAlreadyRunningInvalid},
		},
		{
			name: "missing daemon block",
			yaml: `
commands:
  d:
    type: daemon
    service: app-main
    argv: [echo]
`,
			wantErrIs: []error{ErrDaemonBlockRequired},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cf := mustParse(t, tt.yaml)
			cmd := cf.Commands["d"]
			cmd.ID = "d"
			err := cmd.Validate()
			if len(tt.wantErrIs) == 0 && len(tt.wantErrSubs) == 0 {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			for _, sentinel := range tt.wantErrIs {
				if !errors.Is(err, sentinel) {
					t.Errorf("errors.Is(%v, %v) = false", err, sentinel)
				}
			}
			for _, sub := range tt.wantErrSubs {
				if !strings.Contains(err.Error(), sub) {
					t.Errorf("error %q does not contain %q", err.Error(), sub)
				}
			}
		})
	}
}

// Daemon block on non-daemon types is rejected at parse time.
func TestDaemon_LeakedOnNonDaemonType(t *testing.T) {
	yaml := `
commands:
  d:
    type: shell
    cmd: echo hi
    daemon:
      container_template: foo
`
	_, err := ParseCommandFile([]byte(yaml))
	if err == nil {
		t.Fatal("ParseCommandFile() = nil, want error")
	}
	if !strings.Contains(err.Error(), "daemon") {
		t.Errorf("expected 'daemon' in parse error, got %v", err)
	}
}

// SourceDaemon is the expansion-time metadata field; populating it on a
// synthetic builtin command must not trigger leakage rejection. This protects
// the registry expander (task 4) from being unable to attach metadata.
func TestDaemon_SourceDaemonOnBuiltinIsAllowed(t *testing.T) {
	cmd := CommandDef{
		Type:         CommandTypeBuiltin,
		Cmd:          "docker_daemon_start",
		ID:           "queue.start",
		SourceDaemon: &DaemonSpec{ContainerTemplate: "foo"},
	}
	if err := cmd.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil (SourceDaemon should be invisible)", err)
	}
}

// DefaultDaemonControls contains all four control names.
func TestDaemon_DefaultControls(t *testing.T) {
	got := strings.Join(DefaultDaemonControls, ",")
	want := "start,logs,stop,restart"
	if got != want {
		t.Errorf("DefaultDaemonControls = %q, want %q", got, want)
	}
}

// CommandTypeDaemon constant.
func TestDaemon_TypeConstant(t *testing.T) {
	if string(CommandTypeDaemon) != "daemon" {
		t.Errorf("CommandTypeDaemon = %q, want daemon", CommandTypeDaemon)
	}
}
