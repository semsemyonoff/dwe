package registry

import (
	"os"
	"testing"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

// containerEnv pins the bridged-invocation environment for one test.
func containerEnv(t *testing.T, service string) {
	t.Helper()
	t.Setenv(bridgeclient.EnvInvokedFrom, bridgeclient.InvokedFromContainer)
	if service == "" {
		t.Setenv(bridgeclient.EnvBridgeService, "x")
		_ = os.Unsetenv(bridgeclient.EnvBridgeService)
		return
	}
	t.Setenv(bridgeclient.EnvBridgeService, service)
}

// hostEnv guarantees the bridge variables are absent.
func hostEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{bridgeclient.EnvInvokedFrom, bridgeclient.EnvBridgeService} {
		t.Setenv(name, "x")
		_ = os.Unsetenv(name)
	}
}

const bridgeCSYAML = `
group:
  title: Code style
  bridge:
    enabled: true
    services: [main]

commands:
  all:
    type: shell
    cmd: echo cs
  host-only:
    type: shell
    cmd: echo host
    bridge:
      enabled: false
  admin-only:
    type: shell
    cmd: echo admin
    bridge:
      services: [admin]
`

const bridgePlainYAML = `
commands:
  unmarked:
    type: shell
    cmd: echo unmarked
  marked:
    type: shell
    cmd: echo marked
    bridge:
      enabled: true
`

func bridgeTestRegistry(t *testing.T) *Registry {
	t.Helper()
	return mustRegistry(t, map[string]string{
		"cs.yml":    bridgeCSYAML,
		"plain.yml": bridgePlainYAML,
	})
}

func assertBridgeHidden(t *testing.T, reg *Registry, want map[string]bool) {
	t.Helper()
	for id, hidden := range want {
		def, err := reg.Get(id)
		if err != nil {
			t.Fatalf("get %q: %v", id, err)
		}
		if def.BridgeHidden != hidden {
			t.Errorf("%s: BridgeHidden = %v, want %v", id, def.BridgeHidden, hidden)
		}
	}
}

func TestApplyBridgeVisibility_HostAllVisible(t *testing.T) {
	hostEnv(t)
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":         false,
		"cs.host-only":   false,
		"cs.admin-only":  false,
		"plain.unmarked": false,
		"plain.marked":   false,
	})
}

func TestApplyBridgeVisibility_ContainerOptIn(t *testing.T) {
	containerEnv(t, "main")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":         false, // group enables for main
		"cs.host-only":   true,  // explicit false wins over group
		"cs.admin-only":  true,  // narrowed to admin, caller is main
		"plain.unmarked": true,  // no block anywhere → host-only
		"plain.marked":   false, // command-level enable, all services
	})
}

func TestApplyBridgeVisibility_ServiceMismatch(t *testing.T) {
	containerEnv(t, "admin")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":        true,  // group restricts to main
		"cs.admin-only": false, // command widened services to admin
		"plain.marked":  false, // unrestricted
	})
}

func TestApplyBridgeVisibility_UnknownCallerSafeDegradation(t *testing.T) {
	containerEnv(t, "") // overlay predating DWE_BRIDGE_SERVICE
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":       true,  // service-restricted needs an identity
		"plain.marked": false, // unrestricted stays reachable
	})
}

func TestApplyBridgeVisibility_ReapplyOnHostResets(t *testing.T) {
	containerEnv(t, "admin")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	hostEnv(t)
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("re-ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":         false,
		"plain.unmarked": false,
	})
}

func TestApplyBridgeVisibility_DaemonSyntheticsInherit(t *testing.T) {
	containerEnv(t, "main")
	reg := mustRegistry(t, map[string]string{
		"workers.yml": `
commands:
  queue:
    type: daemon
    service: app
    argv: [php, artisan, queue:work]
    bridge:
      enabled: true
    daemon:
      container_template: "{project}-queue"
`,
	})
	if err := reg.ApplyVisibility(nil, ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"workers.queue.start":   false,
		"workers.queue.logs":    false,
		"workers.queue.stop":    false,
		"workers.queue.restart": false,
	})
}
