package envtest

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

// portPlanFixture writes a project whose compose chain publishes four host
// ports through variables: one traced to a vars: path (twice, from two compose
// services), one traced to a declared service port, one traced to a vars: path
// by a rule gated on a service the project leaves disabled, and one no rule
// traces at all. Returns the loaded config and the project root.
func portPlanFixture(t *testing.T) (*config.DweConfig, string) {
	t.Helper()
	files := map[string]string{
		"workspace.yml": `project:
  name: demo
compose:
  base: compose.yaml
vars:
  ports:
    valkey: 6379
    redis: 6399
exports:
  env:
    - name: VALKEY_PORT
      from: vars.ports.valkey
    - name: APP_PORT
      from: services.app.ports.http
    - name: REDIS_PORT
      from: vars.ports.redis
      when: services.redis.enabled
`,
		"workspace/services/app/service.yml": `type: app
container: app
required: true
ports:
  http: 8080
`,
		"workspace/services/redis/service.yml": `type: infra
container: redis
compose:
  - compose/redis.yml
`,
		"compose.yaml": `services:
  valkey:
    image: valkey/valkey:8
    ports:
      - "${VALKEY_PORT:-6379}:6379"
  valkey-admin:
    image: nginx:1
    ports:
      - "${VALKEY_PORT:-6379}:6380"
  app:
    image: nginx:1
    ports:
      - "${APP_PORT:-8080}:80"
  other:
    image: nginx:1
    ports:
      - "${OTHER_PORT:-9000}:9000"
`,
		"compose/redis.yml": `services:
  redis:
    image: redis:7
    ports:
      - "${REDIS_PORT:-6399}:6379"
`,
	}

	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadConfig(filepath.Join(root, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg, root
}

func TestBuildPortPlanAutoPaths(t *testing.T) {
	tests := []struct {
		name string
		env  ScenarioEnv
		want []string
	}{
		{
			// One row covers three claims about the bare scenario, since they
			// share an input: the traced path is allocated without the scenario
			// mentioning it; valkey and valkey-admin both publish ${VALKEY_PORT},
			// so two findings de-duplicate to one path; and REDIS_PORT — published
			// only by compose/redis.yml, outside the view's -f chain while redis is
			// off — contributes nothing. The redis half is what the enable case
			// below flips.
			name: "a bare scenario allocates exactly the traced, enabled var paths",
			env:  ScenarioEnv{},
			want: []string{"ports.valkey"},
		},
		{
			name: "explicit auto and the traced path de-duplicate",
			env:  ScenarioEnv{Vars: map[string]any{"ports.valkey": AutoPortSentinel}},
			want: []string{"ports.valkey"},
		},
		{
			name: "an explicit number pins the path out of the plan",
			env:  ScenarioEnv{Vars: map[string]any{"ports.valkey": 6380}},
			want: nil,
		},
		{
			name: "a pinned path does not suppress an unrelated explicit auto",
			env: ScenarioEnv{Vars: map[string]any{
				"ports.valkey": 6380,
				"misc.port":    AutoPortSentinel,
			}},
			want: []string{"misc.port"},
		},
		{
			name: "enabling the service activates its when-gated rule and its port",
			env:  ScenarioEnv{Services: ScenarioServices{Enable: []string{"redis"}}},
			want: []string{"ports.redis", "ports.valkey"},
		},
		{
			name: "enabling the service still honours a pin on its path",
			env: ScenarioEnv{
				Services: ScenarioServices{Enable: []string{"redis"}},
				Vars:     map[string]any{"ports.redis": 6400},
			},
			want: []string{"ports.valkey"},
		},
		{
			name: "a nested env.vars form pins the same path a dot-path would",
			env:  ScenarioEnv{Vars: map[string]any{"ports": map[string]any{"valkey": 6380}}},
			want: nil,
		},
	}

	cfg, root := portPlanFixture(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := buildPortPlan(cfg, &Scenario{Env: tt.env}, root)
			if !reflect.DeepEqual(plan.autoPaths, tt.want) {
				t.Errorf("autoPaths = %v, want %v", plan.autoPaths, tt.want)
			}
		})
	}
}

// TestBuildPortPlanUntracedAndServicePortFindings pins what the plan does NOT
// allocate: a variable no active rule traces keeps its finding and contributes
// no path, and a variable reading a declared service port is covered by the
// service remap instead.
func TestBuildPortPlanUntracedAndServicePortFindings(t *testing.T) {
	cfg, root := portPlanFixture(t)
	plan := buildPortPlan(cfg, &Scenario{}, root)

	byVar := map[string]config.IsolationFinding{}
	for _, f := range plan.findings {
		if f.Kind == config.KindInterpolatedHostPort {
			byVar[f.EnvVar] = f
		}
	}
	if f, ok := byVar["OTHER_PORT"]; !ok || f.VarPath != "" || f.SourceService != "" {
		t.Errorf("OTHER_PORT finding = %+v (present=%v), want an untraced finding", f, ok)
	}
	if f, ok := byVar["APP_PORT"]; !ok || f.SourceService != "app" {
		t.Errorf("APP_PORT finding = %+v (present=%v), want SourceService app", f, ok)
	}
	if _, ok := byVar["REDIS_PORT"]; ok {
		t.Errorf("redis is disabled, its compose file must not be scanned: %+v", byVar["REDIS_PORT"])
	}
	if want := []string{"ports.valkey"}; !reflect.DeepEqual(plan.autoPaths, want) {
		t.Errorf("autoPaths = %v, want %v", plan.autoPaths, want)
	}
}

// TestBuildPortPlanMalformedExportPath pins that a truncated `from: vars.<path>`
// never becomes an allocation. The plan feeds BuildLocalOverlay, whose
// setDotPath rejects an empty segment with an error — so classifying such a rule
// as traced would abort EVERY scenario of the project over a typo `dwe validate`
// only warns about. It must classify as untraced instead: nothing is allocated,
// and the isolation warning still fires because the copy really does bind the
// live stack's port.
func TestBuildPortPlanMalformedExportPath(t *testing.T) {
	files := map[string]string{
		"workspace.yml": `project:
  name: demo
compose:
  base: compose.yaml
vars:
  ports:
    valkey: 6379
exports:
  env:
    - name: VALKEY_PORT
      from: vars.ports.
`,
		"compose.yaml": `services:
  valkey:
    image: valkey/valkey:8
    ports:
      - "${VALKEY_PORT:-6379}:6379"
`,
	}
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadConfig(filepath.Join(root, "workspace.yml"))
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	scn := &Scenario{}
	plan := buildPortPlan(cfg, scn, root)
	if len(plan.autoPaths) != 0 {
		t.Errorf("autoPaths = %v, want none for a malformed from:", plan.autoPaths)
	}

	var finding config.IsolationFinding
	for _, f := range plan.findings {
		if f.EnvVar == "VALKEY_PORT" {
			finding = f
		}
	}
	if finding.Kind != config.KindInterpolatedHostPort {
		t.Fatalf("no interpolated finding for VALKEY_PORT: %+v", plan.findings)
	}
	if finding.VarPath != "" {
		t.Errorf("VarPath = %q, want empty for a malformed from:", finding.VarPath)
	}
	if CoversInterpolatedHostPort(cfg, scn, finding) {
		t.Error("a malformed from: must leave the finding uncovered — nothing remaps the port")
	}

	// The end-to-end guarantee: the plan's paths are all writable, so the copy's
	// local.yml still generates.
	ports := map[string]int{}
	for i, p := range plan.autoPaths {
		ports[p] = 41234 + i
	}
	if _, err := BuildLocalOverlay(nil, scn, "demo-t-s-abc", ports, nil); err != nil {
		t.Errorf("BuildLocalOverlay: %v", err)
	}
}

// TestBuildPortPlanStableAcrossRuns pins that neither the scan nor the union
// leaks map iteration order into the plan.
func TestBuildPortPlanStableAcrossRuns(t *testing.T) {
	cfg, root := portPlanFixture(t)
	scn := &Scenario{Env: ScenarioEnv{
		Services: ScenarioServices{Enable: []string{"redis"}},
		Vars:     map[string]any{"misc.port": AutoPortSentinel},
	}}

	want := buildPortPlan(cfg, scn, root)
	if got := []string{"misc.port", "ports.redis", "ports.valkey"}; !reflect.DeepEqual(want.autoPaths, got) {
		t.Fatalf("autoPaths = %v, want %v", want.autoPaths, got)
	}
	for i := range 5 {
		got := buildPortPlan(cfg, scn, root)
		if !reflect.DeepEqual(got.autoPaths, want.autoPaths) {
			t.Fatalf("run %d: autoPaths = %v, want %v", i, got.autoPaths, want.autoPaths)
		}
		if !reflect.DeepEqual(got.keys, want.keys) {
			t.Fatalf("run %d: keys = %v, want %v", i, got.keys, want.keys)
		}
	}
}

// TestBuildPortPlanKeys pins that the plan carries the same declared host-port
// keys the allocator used before it existed.
func TestBuildPortPlanKeys(t *testing.T) {
	cfg, root := portPlanFixture(t)
	plan := buildPortPlan(cfg, &Scenario{}, root)

	want := []hostPortKey{{service: "app", portName: "http"}}
	if !reflect.DeepEqual(plan.keys, want) {
		t.Errorf("keys = %v, want %v", plan.keys, want)
	}
	if !plan.hasAllocatedPorts() {
		t.Error("hasAllocatedPorts() = false, want true")
	}
}

func TestPortPlanHasAllocatedPorts(t *testing.T) {
	tests := []struct {
		name string
		plan portPlan
		want bool
	}{
		{name: "empty", plan: portPlan{}, want: false},
		{name: "host ports only", plan: portPlan{keys: []hostPortKey{{service: "app"}}}, want: true},
		{name: "var paths only", plan: portPlan{autoPaths: []string{"ports.valkey"}}, want: true},
		{
			name: "findings alone allocate nothing",
			plan: portPlan{findings: []config.IsolationFinding{{Kind: config.KindContainerName}}},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.plan.hasAllocatedPorts(); got != tt.want {
				t.Errorf("hasAllocatedPorts() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBuildPortPlanNilConfig pins the defensive guard: no config, no scan, no
// panic.
func TestBuildPortPlanNilConfig(t *testing.T) {
	plan := buildPortPlan(nil, &Scenario{Env: ScenarioEnv{Vars: map[string]any{"a.b": AutoPortSentinel}}}, t.TempDir())
	if plan.keys != nil || plan.autoPaths != nil || plan.findings != nil {
		t.Errorf("buildPortPlan(nil) = %+v, want an empty plan", plan)
	}
}

func TestPinnedVarPath(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]any
		path string
		want bool
	}{
		{name: "int pins", vars: map[string]any{"ports.valkey": 6380}, path: "ports.valkey", want: true},
		{name: "numeric string pins", vars: map[string]any{"ports.valkey": "6380"}, path: "ports.valkey", want: true},
		{name: "float pins", vars: map[string]any{"ports.valkey": float64(6380)}, path: "ports.valkey", want: true},
		{name: "zero pins", vars: map[string]any{"ports.valkey": 0}, path: "ports.valkey", want: true},
		{name: "bool pins", vars: map[string]any{"ports.valkey": false}, path: "ports.valkey", want: true},
		{name: "auto does not pin", vars: map[string]any{"ports.valkey": AutoPortSentinel}, path: "ports.valkey", want: false},
		{name: "null does not pin", vars: map[string]any{"ports.valkey": nil}, path: "ports.valkey", want: false},
		{name: "empty string does not pin", vars: map[string]any{"ports.valkey": ""}, path: "ports.valkey", want: false},
		{name: "absent path does not pin", vars: map[string]any{"ports.other": 6380}, path: "ports.valkey", want: false},
		{name: "no scenario vars at all", vars: nil, path: "ports.valkey", want: false},
		{
			name: "nested form pins the same path",
			vars: map[string]any{"ports": map[string]any{"valkey": 6380}},
			path: "ports.valkey",
			want: true,
		},
		{
			name: "a map at the path is structure, not a pin",
			vars: map[string]any{"ports": map[string]any{"valkey": map[string]any{"port": 6380}}},
			path: "ports.valkey",
			want: false,
		},
		{
			name: "a list at the path is not a pin",
			vars: map[string]any{"ports.valkey": []any{6380}},
			path: "ports.valkey",
			want: false,
		},
		{
			name: "a scalar shadowing a prefix does not resolve deeper",
			vars: map[string]any{"ports": 6380},
			path: "ports.valkey",
			want: false,
		},
		{name: "empty path never pins", vars: map[string]any{"ports.valkey": 6380}, path: "", want: false},
		{name: "empty segment never pins", vars: map[string]any{"ports.valkey": 6380}, path: "ports..valkey", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pinnedVarPath(expandVarPaths(tt.vars, nil), tt.path); got != tt.want {
				t.Errorf("pinnedVarPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// TestPinnedVarPathIgnoresSeedLocalYAML pins that only the scenario's own
// env.vars pin a path: a developer's local.yml value must never make the copy
// bind the developer's port.
func TestPinnedVarPathIgnoresSeedLocalYAML(t *testing.T) {
	cfg, root := portPlanFixture(t)
	// The fixture's workspace.yml already declares vars.ports.valkey: 6379 —
	// the same shape a seeded local.yml override would produce in cfg.
	if cfg.Raw["vars"].(map[string]any)["ports"].(map[string]any)["valkey"] != 6379 {
		t.Fatalf("fixture lost its vars.ports.valkey declaration: %v", cfg.Raw["vars"])
	}
	plan := buildPortPlan(cfg, &Scenario{}, root)
	if want := []string{"ports.valkey"}; !reflect.DeepEqual(plan.autoPaths, want) {
		t.Errorf("autoPaths = %v, want %v — a project/seed value must not pin", plan.autoPaths, want)
	}
}
