package commands

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestNotifyValidator_DaemonWithNotify(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    notify: true
    service: app
    daemon:
      container_template: "q"
    argv: [echo]
`
	diags := runValidator(t, content)
	d := findDiag(diags, "notify is not allowed on type: daemon")
	require.NotNil(t, d, "expected daemon+notify diagnostic; got: %#v", diags)
	require.Equal(t, validate.SeverityError, d.Severity)
	require.Equal(t, "commands:daemons.queue", d.Target)
	require.NotEmpty(t, d.Hint)
	require.Equal(t, 1, countContains(diags, "notify is not allowed on type: daemon"))
}

func TestNotifyValidator_DaemonWithoutNotify(t *testing.T) {
	content := `commands:
  queue:
    type: daemon
    service: app
    daemon:
      container_template: "q"
    argv: [echo]
`
	diags := runValidator(t, content)
	require.Nil(t, findDiag(diags, "notify is not allowed"))
}

func TestNotifyValidator_ParallelSubStepWithNotify(t *testing.T) {
	content := `commands:
  build:
    type: shell
    notify: true
    cmd: "echo build"
  test:
    type: shell
    notify: true
    cmd: "echo test"
  ci:
    type: workflow
    steps:
      - parallel:
          steps:
            - command: daemons.build
            - command: daemons.test
`
	diags := runValidator(t, content)
	infos := 0
	for _, d := range diags {
		if d.Severity == validate.SeverityInfo &&
			containsAll(d.Message, "notify on a direct sub-step", "parallel") {
			infos++
		}
	}
	require.Equal(t, 2, infos, "expected one info per notify-true sub-step; got diags: %#v", diags)
}

func TestNotifyValidator_TopLevelNotifyNoDiag(t *testing.T) {
	content := `commands:
  build:
    type: shell
    notify: true
    cmd: "echo build"
`
	diags := runValidator(t, content)
	for _, d := range diags {
		require.NotContains(t, d.Message, "notify", "no notify diagnostic expected; got %#v", d)
	}
}

func TestNotifyValidator_ParallelSubStepWithoutNotify(t *testing.T) {
	content := `commands:
  build:
    type: shell
    cmd: "echo build"
  test:
    type: shell
    cmd: "echo test"
  ci:
    type: workflow
    steps:
      - parallel:
          steps:
            - command: daemons.build
            - command: daemons.test
`
	diags := runValidator(t, content)
	for _, d := range diags {
		require.NotContains(t, d.Message, "notify on a direct sub-step")
	}
}

func TestNotifyValidator_ParallelSubStepUnknownCommand(t *testing.T) {
	// Cross-ref validator handles the missing-ref case; the notify validator
	// must not also emit a notify diagnostic for a sub-step that can't be resolved.
	content := `commands:
  ci:
    type: workflow
    steps:
      - parallel:
          steps:
            - command: missing-a
            - command: missing-b
`
	diags := runValidator(t, content)
	for _, d := range diags {
		require.NotContains(t, d.Message, "notify on a direct sub-step")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
