package registry

import (
	"os"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
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

// extendsChainCfg declares main ← admin ← reports (each extends the previous)
// plus an unrelated standalone service.
func extendsChainCfg() *config.DweConfig {
	return &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"main":       {Type: "app"},
			"admin":      {Type: "app", Extends: "main"},
			"reports":    {Type: "app", Extends: "admin"},
			"standalone": {Type: "app"},
		},
	}
}

func TestApplyBridgeVisibility_ExtendsChildInheritsParentRights(t *testing.T) {
	containerEnv(t, "admin")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(extendsChainCfg(), ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":        false, // restricted to main; admin extends main
		"cs.host-only":  true,  // explicit false still wins
		"cs.admin-only": false, // direct match unaffected
	})
}

func TestApplyBridgeVisibility_ExtendsTransitiveGrandchild(t *testing.T) {
	containerEnv(t, "reports")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(extendsChainCfg(), ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":        false, // reports → admin → main
		"cs.admin-only": false, // admin is on the chain too
	})
}

func TestApplyBridgeVisibility_ExtendsUnrelatedServiceStillRejected(t *testing.T) {
	containerEnv(t, "standalone")
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(extendsChainCfg(), ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.all":       true,  // no extends path to main
		"plain.marked": false, // unrestricted untouched
	})
}

func TestApplyBridgeVisibility_ExtendsParentNotAdmittedForChildList(t *testing.T) {
	containerEnv(t, "main") // parent calling a command scoped to its child
	reg := bridgeTestRegistry(t)
	if err := reg.ApplyVisibility(extendsChainCfg(), ""); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	assertBridgeHidden(t, reg, map[string]bool{
		"cs.admin-only": true, // rights flow child←parent, never parent←child
	})
}

func TestCallerExtendsChain(t *testing.T) {
	cfg := extendsChainCfg()
	tests := []struct {
		name   string
		cfg    *config.DweConfig
		caller string
		want   []string
	}{
		{"nil cfg degrades to exact match", nil, "admin", []string{"admin"}},
		{"empty caller", cfg, "", []string{""}},
		{"no extends", cfg, "main", []string{"main"}},
		{"single hop", cfg, "admin", []string{"admin", "main"}},
		{"two hops", cfg, "reports", []string{"reports", "admin", "main"}},
		{"unknown caller", cfg, "ghost", []string{"ghost"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := callerExtendsChain(tt.cfg, tt.caller)
			if len(got) != len(tt.want) {
				t.Fatalf("chain = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("chain = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestCallerExtendsChain_CycleSafe(t *testing.T) {
	cfg := &config.DweConfig{
		Services: map[string]config.ServiceConfig{
			"a": {Type: "app", Extends: "b"},
			"b": {Type: "app", Extends: "a"},
		},
	}
	got := callerExtendsChain(cfg, "a")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("cycle chain = %v, want [a b]", got)
	}
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
