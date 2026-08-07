package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// sampleWorkspaceYML reflects the lean workspace.yml (project identity only, schema_version silently ignored).
const sampleWorkspaceYML = `
schema_version: "1"
project:
  name: laravel
  prefix: dwe
`

// sampleToolsYML declares the standard tools as type=tool services with
// compose overlays for two. Post-unification the file is a services.yml
// fragment, not a separate tools.yml. Kept under the legacy name so existing
// call sites stay terse.
const sampleToolsYML = sampleToolsServicesYML

// minimalToolsYML declares the standard tools as type=tool services without
// compose overlays. Post-unification the file is a services.yml fragment.
const minimalToolsYML = `
services:
  adminer:
    type: tool
    container: adminer
    ports:
      main: 8080
    hosts:
      main: adminer.localhost
  redis_insight:
    type: tool
    container: redis_insight
    ports:
      main: 5540
    hosts:
      main: redis.localhost
  mailpit:
    type: tool
    container: mailpit
    ports:
      main: 8025
    hosts:
      main: mail.localhost
`

// sampleToolsServicesYML declares the standard tools as type=tool services
// with per-service ports/hosts. Two of them carry compose overlays.
const sampleToolsServicesYML = `
services:
  adminer:
    type: tool
    container: adminer
    ports:
      main: 8080
    hosts:
      main: adminer.localhost
  redis_insight:
    type: tool
    container: redis_insight
    compose:
      - compose/tools/redis_insight.yml
    ports:
      main: 5540
    hosts:
      main: redis.localhost
  mailpit:
    type: tool
    container: mailpit
    compose:
      - compose/tools/mailpit.yml
    ports:
      main: 8025
    hosts:
      main: mail.localhost
`

// minimalDefaultsYML carries enable overlays for the sample tools, all disabled.
const minimalDefaultsYML = `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: false
  mailpit:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
state: ""
`

// sampleDefaultsYML enables redis_insight and mailpit; disables adminer.
const sampleDefaultsYML = `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
state: ""
`

// writeServicesDir creates per-folder service files under <baseDir>/workspace/services/
// from a YAML fragment shaped like `services: {name: {...}}`.
func writeServicesDir(t *testing.T, baseDir, servicesYML string) {
	t.Helper()
	if servicesYML == "" {
		return
	}
	type wrap struct {
		Services map[string]any `yaml:"services"`
	}
	var w wrap
	if err := yaml.Unmarshal([]byte(servicesYML), &w); err != nil {
		t.Fatalf("writeServicesDir: parse: %v", err)
	}
	for name, svc := range w.Services {
		dir := filepath.Join(baseDir, "workspace", "services", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("writeServicesDir: mkdir %s: %v", dir, err)
		}
		data, err := yaml.Marshal(svc)
		if err != nil {
			t.Fatalf("writeServicesDir: marshal %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "service.yml"), data, 0644); err != nil {
			t.Fatalf("writeServicesDir: write %s: %v", name, err)
		}
	}
}

// writeServiceYAML writes a service.yml file at <baseDir>/workspace/services/<name>/
func writeServiceYAML(t *testing.T, baseDir, name, content string) {
	t.Helper()
	dir := filepath.Join(baseDir, "workspace", "services", name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("writeServiceYAML: mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "service.yml"), []byte(content), 0644); err != nil {
		t.Fatalf("writeServiceYAML: write %s: %v", name, err)
	}
}

func writeTempYML(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "dwe-*.yml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	return f.Name()
}

// writeLayeredFixture creates the file layout used by LoadConfig:
//
//	<tmp>/workspace.yml
//	<tmp>/workspace/defaults.yml   (optional)
//	<tmp>/workspace/local.yml      (optional)
//	<tmp>/workspace/services/<name>/service.yml  (from sampleToolsYML)
//
// Tests that need a different service set should call writeFullFixture with an
// explicit tools argument; tests that want no tool services at all should pass
// the noToolsYML sentinel.
func writeLayeredFixture(t *testing.T, wsContent, defaults, user string) string {
	t.Helper()
	return writeFullFixture(t, wsContent, defaults, user, "", sampleToolsYML)
}

// noToolsYML is a sentinel passed as the tools argument to writeFullFixture to
// suppress creation of the tool service folders entirely.
const noToolsYML = "<NONE>"

// writeFullFixture creates the complete file layout used by LoadConfig,
// including optional per-folder services and tool services.
//
// Pass tools=noToolsYML to suppress creating tool service folders. Empty
// string falls back to sampleToolsYML so existing layered tests stay terse.
func writeFullFixture(t *testing.T, wsContent, defaults, user, services, tools string) string {
	t.Helper()
	dir := t.TempDir()

	workspacePath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(workspacePath, []byte(wsContent), 0644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}

	workspaceDir := filepath.Join(dir, "workspace")

	writeTools := tools != noToolsYML
	toolsContent := ""
	if writeTools {
		toolsContent = tools
		if toolsContent == "" {
			toolsContent = sampleToolsYML
		}
	}

	if defaults != "" || user != "" {
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			t.Fatalf("mkdir workspace/: %v", err)
		}
	}

	if defaults != "" {
		if err := os.WriteFile(filepath.Join(workspaceDir, "defaults.yml"), []byte(defaults), 0644); err != nil {
			t.Fatalf("write defaults.yml: %v", err)
		}
	}

	if user != "" {
		if err := os.WriteFile(filepath.Join(workspaceDir, "local.yml"), []byte(user), 0644); err != nil {
			t.Fatalf("write local.yml: %v", err)
		}
	}

	// Write per-folder services (services and tools are independent fragments).
	writeServicesDir(t, dir, services)
	writeServicesDir(t, dir, toolsContent)

	return workspacePath
}

// --- LoadDweConfig (single-file loader) ---

// fullSingleYML is a self-contained workspace.yml with all fields, used to test
// LoadDweConfig in isolation.
const fullSingleYML = `
schema_version: "1"
project:
  name: laravel
  prefix: dwe
tools:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
runtime:
  use_https: false
  ports:
    app: 80
    db: 13306
    redis: 6379
    adminer: 8080
    redis_insight: 5540
    mailpit: 8025
  hosts:
    main: app.localhost
    adminer: adminer.localhost
    redis_insight: redis.localhost
    mailpit: mail.localhost
  spx:
    path: ""
state: ""
`

func TestLoadDweConfig(t *testing.T) {
	path := writeTempYML(t, fullSingleYML)
	cfg, err := LoadDweConfig(path)
	if err != nil {
		t.Fatalf("LoadDweConfig: %v", err)
	}

	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
	if cfg.Project.FullName() != "dwe-laravel" {
		t.Errorf("FullName = %q, want dwe-laravel", cfg.Project.FullName())
	}
}

func TestLoadDweConfig_notFound(t *testing.T) {
	_, err := LoadDweConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadDweConfig_invalidYAML(t *testing.T) {
	path := writeTempYML(t, "{ invalid yaml ][")
	_, err := LoadDweConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

// --- LoadConfig (layered loader) ---

func TestLoadConfig_mergesDefaultsAndUser(t *testing.T) {
	userYML := `
services:
  adminer:
    enabled: true
state: staging
`
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, userYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// From workspace.yml
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}

	// From defaults.yml: redis_insight enabled; mailpit's per-service port is 8025
	if !cfg.Services["redis_insight"].Enabled {
		t.Error("services.redis_insight.enabled should be true (from defaults)")
	}
	if cfg.Services["mailpit"].Port("main") != 8025 {
		t.Errorf("services.mailpit.ports.main = %d, want 8025", cfg.Services["mailpit"].Port("main"))
	}

	// Overridden by local.yml: adminer flipped enabled=true
	if !cfg.Services["adminer"].Enabled {
		t.Error("services.adminer.enabled should be true (overridden by user)")
	}
	if cfg.State != "staging" {
		t.Errorf("state = %q, want staging (from user)", cfg.State)
	}
}

// --- strict root allowlist + vars: sandbox (Task 1) ---

func TestLoadConfig_strictRoot_allowedKeysLoad(t *testing.T) {
	// All formalized top-level keys + schema_version + vars: load without error.
	wsYML := `
schema_version: "1"
project:
  name: laravel
  prefix: dwe
runtime:
  use_https: false
state: ""
vars:
  db:
    user: root
    password: secret
  my_custom:
    timeout: 30
  # Names that are root-forbidden (legacy or unknown top-level keys) are perfectly
  # fine NESTED inside the vars: sandbox — the strict-root check applies only one
  # level up. This pins the "unvalidated/nestable inside" half of the contract.
  tools:
    php: docker
  binaries:
    docker: /usr/local/bin/docker
`
	path := writeFullFixture(t, wsYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// vars: survives into Raw and resolves by dot-path.
	if v, ok := ResolvePath(cfg.Raw, "vars.db.password"); !ok || v != "secret" {
		t.Errorf("vars.db.password = %v (ok=%v), want secret", v, ok)
	}
	if v, ok := ResolvePath(cfg.Raw, "vars.my_custom.timeout"); !ok || v != 30 {
		t.Errorf("vars.my_custom.timeout = %v (ok=%v), want 30", v, ok)
	}
	// Root-forbidden names nested under vars: are accepted and resolvable.
	if v, ok := ResolvePath(cfg.Raw, "vars.tools.php"); !ok || v != "docker" {
		t.Errorf("vars.tools.php = %v (ok=%v), want docker", v, ok)
	}
	if v, ok := ResolvePath(cfg.Raw, "vars.binaries.docker"); !ok || v != "/usr/local/bin/docker" {
		t.Errorf("vars.binaries.docker = %v (ok=%v), want /usr/local/bin/docker", v, ok)
	}
	// The typed Vars field is also populated.
	if cfg.Vars == nil {
		t.Fatal("cfg.Vars should be populated")
	}
	if db, ok := cfg.Vars["db"].(map[string]any); !ok || db["user"] != "root" {
		t.Errorf("cfg.Vars[db][user] = %v, want root", cfg.Vars["db"])
	}
}

func TestLoadConfig_strictRoot_unknownKeyRejected(t *testing.T) {
	// An unknown top-level key in each layer is rejected with the vars: hint.
	cases := []struct {
		name             string
		ws, defaults, lc string
		wantFile         string
	}{
		{
			name:     "workspace.yml",
			ws:       sampleWorkspaceYML + "\ndb:\n  user: root\n",
			wantFile: "workspace.yml",
		},
		{
			name:     "defaults.yml",
			ws:       sampleWorkspaceYML,
			defaults: "schema_version: \"1\"\nmy_custom:\n  x: 1\n",
			wantFile: "defaults.yml",
		},
		{
			name:     "local.yml",
			ws:       sampleWorkspaceYML,
			lc:       "app:\n  log_level: debug\n",
			wantFile: "local.yml",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFullFixture(t, tc.ws, tc.defaults, tc.lc, "", noToolsYML)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatal("expected error for unknown top-level key")
			}
			if !strings.Contains(err.Error(), "unknown top-level key") {
				t.Errorf("error = %q, want 'unknown top-level key'", err)
			}
			if !strings.Contains(err.Error(), "vars:") {
				t.Errorf("error = %q, want vars: hint", err)
			}
			if !strings.Contains(err.Error(), tc.wantFile) {
				t.Errorf("error = %q, want layer file %q", err, tc.wantFile)
			}
		})
	}
}

func TestLoadConfig_strictRoot_varsFromDefaultsMerges(t *testing.T) {
	// vars: declared in defaults.yml survives the 3-layer merge and is reachable.
	defaults := "schema_version: \"1\"\nvars:\n  db:\n    host: dbhost\n"
	path := writeFullFixture(t, sampleWorkspaceYML, defaults, "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if v, ok := ResolvePath(cfg.Raw, "vars.db.host"); !ok || v != "dbhost" {
		t.Errorf("vars.db.host = %v (ok=%v), want dbhost (from defaults)", v, ok)
	}
}

func TestLoadConfig_strictRoot_legacyKeysKeepDedicatedMessages(t *testing.T) {
	// binaries:/tools: must still emit their dedicated migration messages — the
	// generic allowlist error must not clobber them.
	t.Run("tools", func(t *testing.T) {
		ws := sampleWorkspaceYML + "\ntools:\n  web:\n    image: nginx\n"
		path := writeFullFixture(t, ws, "", "", "", noToolsYML)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "tools: no longer supported") {
			t.Fatalf("want dedicated tools message, got %v", err)
		}
	})
	t.Run("binaries", func(t *testing.T) {
		ws := sampleWorkspaceYML + "\nbinaries:\n  docker: /usr/bin/docker\n"
		path := writeFullFixture(t, ws, "", "", "", noToolsYML)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "binaries: moved") {
			t.Fatalf("want dedicated binaries message, got %v", err)
		}
	})
	// A layer carrying ONLY a bare (null) legacy key is dropped by deepMerge and
	// never reaches the merged map; the per-layer pass is the only place that
	// catches it. Guards against a removed top-level block loading silently.
	t.Run("tools bare in defaults layer", func(t *testing.T) {
		defaults := "schema_version: \"1\"\ntools:\n"
		path := writeFullFixture(t, sampleWorkspaceYML, defaults, "", "", noToolsYML)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "tools: no longer supported") {
			t.Fatalf("want dedicated tools message from defaults layer, got %v", err)
		}
	})
	t.Run("binaries bare in local layer", func(t *testing.T) {
		lc := "binaries:\n"
		path := writeFullFixture(t, sampleWorkspaceYML, "", lc, "", noToolsYML)
		_, err := LoadConfig(path)
		if err == nil || !strings.Contains(err.Error(), "binaries: moved") {
			t.Fatalf("want dedicated binaries message from local layer, got %v", err)
		}
	})
}

func TestUpdateConfig_EffectiveMode(t *testing.T) {
	cases := []struct {
		name string
		cfg  *UpdateConfig
		want string
	}{
		{name: "nil block", cfg: nil, want: "off"},
		{name: "present empty mode", cfg: &UpdateConfig{}, want: "on"},
		{name: "explicit on", cfg: &UpdateConfig{Mode: "on"}, want: "on"},
		{name: "explicit off", cfg: &UpdateConfig{Mode: "off"}, want: "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.EffectiveMode(); got != tc.want {
				t.Errorf("EffectiveMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStopPortReleaseTimeout(t *testing.T) {
	tests := []struct {
		name string
		cfg  *DweConfig
		want time.Duration
	}{
		{name: "nil cfg → default", cfg: nil, want: DefaultStopPortReleaseTimeout},
		{name: "nil block → default", cfg: &DweConfig{}, want: DefaultStopPortReleaseTimeout},
		{name: "empty value → default", cfg: &DweConfig{Stop: &StopConfig{}}, want: DefaultStopPortReleaseTimeout},
		{name: "duration string", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "2m"}}, want: 2 * time.Minute},
		{name: "compound duration", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "1m30s"}}, want: 90 * time.Second},
		{name: "bare seconds", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "90"}}, want: 90 * time.Second},
		{name: "zero disables", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "0"}}, want: 0},
		{name: "invalid falls back to default", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "1minute"}}, want: DefaultStopPortReleaseTimeout},
		{name: "negative duration falls back to default", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "-5s"}}, want: DefaultStopPortReleaseTimeout},
		{name: "negative seconds falls back to default", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "-5"}}, want: DefaultStopPortReleaseTimeout},
		{name: "overflowing bare seconds falls back to default", cfg: &DweConfig{Stop: &StopConfig{PortReleaseTimeout: "18446744074"}}, want: DefaultStopPortReleaseTimeout},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := StopPortReleaseTimeout(tc.cfg); got != tc.want {
				t.Errorf("StopPortReleaseTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLoadConfig_stopPortReleaseTimeout_valid(t *testing.T) {
	ws := sampleWorkspaceYML + "\nstop:\n  port_release_timeout: 2m\n"
	path := writeFullFixture(t, ws, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := StopPortReleaseTimeout(cfg); got != 2*time.Minute {
		t.Errorf("StopPortReleaseTimeout() = %v, want 2m", got)
	}
}

func TestLoadConfig_stopPortReleaseTimeout_invalid(t *testing.T) {
	for _, bad := range []string{"1minute", "-5s", "-30", "18446744074"} {
		t.Run(bad, func(t *testing.T) {
			ws := sampleWorkspaceYML + "\nstop:\n  port_release_timeout: \"" + bad + "\"\n"
			path := writeFullFixture(t, ws, "", "", "", noToolsYML)
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("expected load error for stop.port_release_timeout %q", bad)
			}
			if !strings.Contains(err.Error(), "stop.port_release_timeout") {
				t.Errorf("error should name the offending key, got: %v", err)
			}
		})
	}
}

func TestLoadConfig_stopPortReleaseTimeout_nilOverrideAttribution(t *testing.T) {
	// defaults.yml sets a bad value; local.yml sets a null override. deepMerge
	// drops the null, so the bad merged value comes from defaults.yml — the error
	// must blame defaults.yml, NOT the local.yml null layer.
	ws := sampleWorkspaceYML
	defaults := "schema_version: \"1\"\nstop:\n  port_release_timeout: \"1minute\"\n"
	local := "stop:\n  port_release_timeout: null\n"
	path := writeFullFixture(t, ws, defaults, local, "", noToolsYML)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected load error for invalid stop.port_release_timeout")
	}
	if !strings.Contains(err.Error(), "defaults.yml") {
		t.Errorf("error should blame defaults.yml (origin of the bad value), got: %v", err)
	}
	if strings.Contains(err.Error(), "local.yml") {
		t.Errorf("error must not blame local.yml (its null override is dropped by deepMerge), got: %v", err)
	}
}

func TestLoadConfig_update_absentBlockIsOff(t *testing.T) {
	// No update: block at all → EffectiveMode off, Update field nil.
	path := writeFullFixture(t, sampleWorkspaceYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Update != nil {
		t.Errorf("cfg.Update = %+v, want nil", cfg.Update)
	}
	if got := cfg.Update.EffectiveMode(); got != "off" {
		t.Errorf("EffectiveMode() = %q, want off", got)
	}
}

func TestLoadConfig_update_threeLayerMerge(t *testing.T) {
	// defaults on → workspace off → local on. local.yml wins (last-layer-wins
	// scalar merge): final resolved mode is on.
	ws := sampleWorkspaceYML + "\nupdate:\n  mode: off\n"
	defaults := "schema_version: \"1\"\nupdate:\n  mode: on\n"
	lc := "update:\n  mode: on\n"
	path := writeFullFixture(t, ws, defaults, lc, "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Update == nil {
		t.Fatal("cfg.Update should be populated")
	}
	if got := cfg.Update.EffectiveMode(); got != "on" {
		t.Errorf("EffectiveMode() = %q, want on (local.yml override)", got)
	}
}

func TestLoadConfig_update_presentEmptyMode(t *testing.T) {
	// update: with no mode → EffectiveMode on (writing the key is the opt-in).
	ws := sampleWorkspaceYML + "\nupdate: {}\n"
	path := writeFullFixture(t, ws, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Update.EffectiveMode(); got != "on" {
		t.Errorf("EffectiveMode() = %q, want on", got)
	}
}

func TestLoadConfig_update_presentNullValue(t *testing.T) {
	// A bare `update:` (null value) is also the opt-in: writing the key is
	// itself the opt-in (present-but-empty → on). deepMerge drops the nil value,
	// so this guards the presence-normalization that keeps the contract.
	ws := sampleWorkspaceYML + "\nupdate:\n"
	path := writeFullFixture(t, ws, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Update == nil {
		t.Fatal("Update should be a present (empty) block, got nil")
	}
	if got := cfg.Update.EffectiveMode(); got != "on" {
		t.Errorf("EffectiveMode() = %q, want on", got)
	}
}

func TestLoadConfig_update_explicitModeSurvivesNullOverride(t *testing.T) {
	// defaults.yml sets mode: off; local.yml writes a bare `update:` (null).
	// The explicit mode survives the merge (the merged block is not empty), so
	// EffectiveMode stays off — a null override does not silently re-enable.
	defaults := "schema_version: \"1\"\nupdate:\n  mode: off\n"
	lc := "update:\n"
	path := writeFullFixture(t, sampleWorkspaceYML, defaults, lc, "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if got := cfg.Update.EffectiveMode(); got != "off" {
		t.Errorf("EffectiveMode() = %q, want off (explicit mode survives null override)", got)
	}
}

func TestLoadConfig_update_invalidModeRejected(t *testing.T) {
	// A bad value must hard-error at load time, not silently fall through.
	ws := sampleWorkspaceYML + "\nupdate:\n  mode: yes-please\n"
	path := writeFullFixture(t, ws, "", "", "", noToolsYML)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid update.mode")
	}
	if !strings.Contains(err.Error(), "update.mode") {
		t.Errorf("error = %q, want 'update.mode'", err)
	}
}

func TestLoadConfig_update_invalidModeNamesSourceLayer(t *testing.T) {
	// An invalid override in local.yml must be attributed to local.yml, not the
	// top-level workspace.yml path — the message names the layer that supplied it.
	lc := "update:\n  mode: bogus\n"
	path := writeFullFixture(t, sampleWorkspaceYML, "", lc, "", noToolsYML)
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid update.mode in local.yml")
	}
	if !strings.Contains(err.Error(), "update.mode") || !strings.Contains(err.Error(), "local.yml") {
		t.Errorf("error = %q, want it to mention update.mode and local.yml", err)
	}
}

func TestLoadConfig_noOptionalFiles(t *testing.T) {
	// Works fine when defaults.yml, local.yml, and tools.yml are absent.
	path := writeFullFixture(t, sampleWorkspaceYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Project.Name != "laravel" {
		t.Errorf("Project.Name = %q", cfg.Project.Name)
	}
}

func TestLoadConfig_noDefaultsFile(t *testing.T) {
	// local.yml present, defaults.yml absent. Skip tools.yml to avoid requiring
	// a runtime block solely to satisfy tool host/port validation.
	userYML := `state: demo`
	path := writeFullFixture(t, sampleWorkspaceYML, "", userYML, "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.State != "demo" {
		t.Errorf("state = %q, want demo", cfg.State)
	}
}

func TestLoadConfig_notFound(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if err == nil {
		t.Error("expected error for missing workspace.yml")
	}
}

func TestLoadConfigOrWrap_success(t *testing.T) {
	path := writeFullFixture(t, sampleWorkspaceYML, "", "", "", noToolsYML)
	cfg, err := LoadConfigOrWrap(path)
	if err != nil {
		t.Fatalf("LoadConfigOrWrap: %v", err)
	}
	if cfg == nil || cfg.Project.Name != "laravel" {
		t.Fatalf("LoadConfigOrWrap returned unexpected cfg: %+v", cfg)
	}
}

func TestLoadConfigOrWrap_wrapsError(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nonexistent.yml")
	cfg, err := LoadConfigOrWrap(missing)
	if err == nil {
		t.Fatal("expected error for missing workspace.yml")
	}
	if cfg != nil {
		t.Errorf("cfg = %+v, want nil on error", cfg)
	}
	if !strings.HasPrefix(err.Error(), "loading config: ") {
		t.Errorf("error %q missing canonical %q prefix", err.Error(), "loading config: ")
	}
	// The wrap must preserve the underlying error chain for errors.Is callers.
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("wrapped error does not unwrap to os.ErrNotExist: %v", err)
	}
}

// --- helpers ---

func TestProjectFullName_noPrefix(t *testing.T) {
	p := ProjectConfig{Name: "myapp"}
	if p.FullName() != "myapp" {
		t.Errorf("FullName = %q, want myapp", p.FullName())
	}
}

// Tools no longer have a dedicated AnyEnabled helper; the equivalent is to
// walk cfg.Services and filter by IsTool().

func TestDeepMerge_nonConflicting(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"b": 2}
	deepMerge(dst, src)
	if dst["a"] != 1 || dst["b"] != 2 {
		t.Errorf("unexpected result: %v", dst)
	}
}

func TestDeepMerge_srcWins(t *testing.T) {
	dst := map[string]any{"a": 1}
	src := map[string]any{"a": 99}
	deepMerge(dst, src)
	if dst["a"] != 99 {
		t.Errorf("expected src to win: got %v", dst["a"])
	}
}

func TestDeepMerge_recursiveMaps(t *testing.T) {
	dst := map[string]any{
		"m": map[string]any{"x": 1, "y": 2},
	}
	src := map[string]any{
		"m": map[string]any{"y": 99, "z": 3},
	}
	deepMerge(dst, src)
	m := dst["m"].(map[string]any)
	if m["x"] != 1 || m["y"] != 99 || m["z"] != 3 {
		t.Errorf("unexpected nested merge result: %v", m)
	}
}

// --- ResolvePath ---

func TestResolvePath_topLevel(t *testing.T) {
	m := map[string]any{"state": "staging"}
	v, ok := ResolvePath(m, "state")
	if !ok || v != "staging" {
		t.Errorf("got %v, %v", v, ok)
	}
}

func TestResolvePath_nested(t *testing.T) {
	m := map[string]any{
		"services": map[string]any{
			"app": map[string]any{
				"ports": map[string]any{"http": 8080},
			},
		},
	}
	v, ok := ResolvePath(m, "services.app.ports.http")
	if !ok || v != 8080 {
		t.Errorf("got %v, %v", v, ok)
	}
}

func TestResolvePath_missing(t *testing.T) {
	m := map[string]any{"a": 1}
	_, ok := ResolvePath(m, "a.b.c")
	if ok {
		t.Error("expected not found for non-map intermediate")
	}
}

func TestResolvePath_emptyPath(t *testing.T) {
	m := map[string]any{"a": 1}
	_, ok := ResolvePath(m, "")
	if ok {
		t.Error("expected not found for empty path")
	}
}

func TestResolvePath_nilMap(t *testing.T) {
	_, ok := ResolvePath(nil, "a")
	if ok {
		t.Error("expected not found for nil map")
	}
}

// --- LookupDotPath ---

func TestLookupDotPath_stringLeaf(t *testing.T) {
	cfg := &DweConfig{Raw: map[string]any{
		"services": map[string]any{
			"main": map[string]any{"work_dir_internal": "/var/www"},
		},
	}}
	v, err := LookupDotPath(cfg, "services.main.work_dir_internal")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != "/var/www" {
		t.Errorf("got %v, want /var/www", v)
	}
}

func TestLookupDotPath_missingReturnsNil(t *testing.T) {
	cfg := &DweConfig{Raw: map[string]any{}}
	v, err := LookupDotPath(cfg, "a.b.c")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if v != nil {
		t.Errorf("got %v, want nil", v)
	}
}

func TestLookupDotPath_nonStringErrors(t *testing.T) {
	cfg := &DweConfig{Raw: map[string]any{"port": 8080}}
	_, err := LookupDotPath(cfg, "port")
	if err == nil {
		t.Fatal("expected error for non-string leaf")
	}
}

func TestLookupDotPath_nilConfig(t *testing.T) {
	v, err := LookupDotPath(nil, "any.path")
	if err != nil || v != nil {
		t.Errorf("got v=%v, err=%v; want nil, nil", v, err)
	}
}

// --- LoadConfig populates Raw ---

func TestLoadConfig_rawPopulated(t *testing.T) {
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Raw == nil {
		t.Fatal("cfg.Raw should be populated")
	}
	v, ok := ResolvePath(cfg.Raw, "services.adminer.ports.main")
	if !ok {
		t.Error("services.adminer.ports.main not found in Raw")
	}
	if fmt.Sprintf("%v", v) != "8080" {
		t.Errorf("services.adminer.ports.main = %v, want 8080", v)
	}
}

// --- ComposeConfig loading ---

func TestLoadConfig_composeBasePresent(t *testing.T) {
	defaultsWithCompose := sampleDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeLayeredFixture(t, sampleWorkspaceYML, defaultsWithCompose, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Compose.Base != "compose.yaml" {
		t.Errorf("Compose.Base = %q, want compose.yaml", cfg.Compose.Base)
	}
}

func TestLoadConfig_toolComposesLoaded(t *testing.T) {
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Check that tool compose files are loaded from tool entries.
	if want := []string{"compose/tools/redis_insight.yml"}; !slicesEqual(cfg.Services["redis_insight"].Compose, want) {
		t.Errorf("Services[redis_insight].Compose = %v, want %v", cfg.Services["redis_insight"].Compose, want)
	}
	if want := []string{"compose/tools/mailpit.yml"}; !slicesEqual(cfg.Services["mailpit"].Compose, want) {
		t.Errorf("Services[mailpit].Compose = %v, want %v", cfg.Services["mailpit"].Compose, want)
	}
	// Adminer is disabled and has no compose, so check that empty is OK.
	if len(cfg.Services["adminer"].Compose) != 0 {
		t.Errorf("Services[adminer].Compose = %v, want empty", cfg.Services["adminer"].Compose)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadConfig_composeAbsent(t *testing.T) {
	// When compose section is absent, fields are zero values.
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Compose.Base != "" {
		t.Errorf("Compose.Base = %q, want empty when section absent", cfg.Compose.Base)
	}
}

// --- Config Validation ---

func TestValidateConfigKeys_nilMapsAreSafe(t *testing.T) {
	cfg := &DweConfig{Services: nil}
	err := validateConfigKeys(cfg)
	if err != nil {
		t.Errorf("validateConfigKeys on nil maps = %v, want nil", err)
	}
}

func TestValidateConfigKeys_servicePortsHostsIdentifierSafety(t *testing.T) {
	tests := []struct {
		name      string
		ports     map[string]ServicePortSpec
		hosts     map[string]string
		wantError bool
	}{
		{"valid port key", map[string]ServicePortSpec{"http": {Port: 3000}}, nil, false},
		{"valid host key", nil, map[string]string{"main": "main.localhost"}, false},
		{"invalid port key dash", map[string]ServicePortSpec{"my-port": {Port: 3000}}, nil, true},
		{"invalid port key leading digit", map[string]ServicePortSpec{"1port": {Port: 3000}}, nil, true},
		{"invalid host key dash", nil, map[string]string{"my-host": "x.localhost"}, true},
		{"invalid host key dot", nil, map[string]string{"my.host": "x.localhost"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &DweConfig{
				Services: map[string]ServiceConfig{
					"svc": {Type: ServiceTypeTool, Container: "test", Ports: tt.ports, Hosts: tt.hosts},
				},
			}
			err := validateConfigKeys(cfg)
			if (err != nil) != tt.wantError {
				t.Errorf("validateConfigKeys = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestDetectLegacyComposeOverlays_rejectsOldFormat(t *testing.T) {
	raw := map[string]any{
		"compose": map[string]any{
			"overlays": map[string]any{
				"adminer": "compose/tools/adminer.yml",
			},
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err == nil {
		t.Error("detectLegacyComposeOverlays should reject legacy compose.overlays")
	}
}

func TestDetectLegacyComposeOverlays_rejectsEmptyOverlays(t *testing.T) {
	// An empty compose.overlays: {} is still a legacy key and must be rejected.
	raw := map[string]any{
		"compose": map[string]any{
			"overlays": map[string]any{},
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err == nil {
		t.Error("detectLegacyComposeOverlays should reject empty compose.overlays block")
	}
}

func TestDetectLegacyComposeOverlays_allowsNewFormat(t *testing.T) {
	raw := map[string]any{
		"compose": map[string]any{
			"base": "compose.yaml",
		},
	}
	err := detectLegacyComposeOverlays(raw)
	if err != nil {
		t.Errorf("detectLegacyComposeOverlays on new format = %v, want nil", err)
	}
}

func TestLoadConfig_nilSafety(t *testing.T) {
	// A minimal workspace.yml with no tools, runtime, or services should load without panic.
	minimalYML := `
schema_version: "1"
project:
  name: test
`
	path := writeFullFixture(t, minimalYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// With no services declared, cfg.Services should be empty / nil-safe.
	if len(cfg.Services) != 0 {
		t.Errorf("expected empty services, got %v", cfg.Services)
	}
}

func TestLoadConfig_arbitraryToolKey(t *testing.T) {
	// An arbitrary new tool (elasticvue) declared in services.yml is preserved.
	toolsWithNewTool := `
services:
  elasticvue:
    type: tool
    container: elasticvue
    compose:
      - compose/tools/elasticvue.yml
    ports:
      main: 9200
    hosts:
      main: elastic.localhost
`
	defaultsWithNewTool := `
schema_version: "1"
services:
  elasticvue:
    enabled: false
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithNewTool, "", "", toolsWithNewTool)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	tool, ok := cfg.Services["elasticvue"]
	if !ok {
		t.Error("elasticvue tool not found in merged config")
	} else if tool.Container != "elasticvue" || tool.Port("main") != 9200 || tool.Host("main") != "elastic.localhost" {
		t.Errorf("elasticvue tool definition incorrect: %+v", tool)
	}
}

// --- Services (loaded from services.yml) ---

const sampleServicesYML = `
services:
  main:
    type: app
    container: app-main
    required: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    configs:
      - .env
  main-debug:
    type: app
    container: app-main-debug
    required: false
    extends: main
    compose:
      - compose/services/main/debug.yml
`

func TestLoadServicesConfig_basic(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}
	main := services["main"]
	if main.Container != "app-main" {
		t.Errorf("main.Container = %q, want app-main", main.Container)
	}
	if !main.Required {
		t.Error("main.Required should be true")
	}
	if main.Dir != "./services/main" {
		t.Errorf("main.Dir = %q, want ./services/main", main.Dir)
	}
	if len(main.Configs) != 1 || main.Configs[0].File != ".env" {
		t.Errorf("main.Configs = %v, want [{File:.env}]", main.Configs)
	}
}

func TestLoadServicesConfig_extendsResolved(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	debug := services["main-debug"]
	if debug.Container != "app-main-debug" {
		t.Errorf("main-debug.Container = %q, want app-main-debug", debug.Container)
	}
	// Inherited from main via extends
	if debug.Dir != "./services/main" {
		t.Errorf("main-debug.Dir = %q, want ./services/main (inherited)", debug.Dir)
	}
	if debug.DirInternal != "/workspace" {
		t.Errorf("main-debug.DirInternal = %q, want /workspace (inherited)", debug.DirInternal)
	}
	if debug.WorkDirInternal != "/workspace/src" {
		t.Errorf("main-debug.WorkDirInternal = %q, want /workspace/src (inherited)", debug.WorkDirInternal)
	}
	if len(debug.Configs) != 1 || debug.Configs[0].File != ".env" {
		t.Errorf("main-debug.Configs = %v, want [{File:.env}] (inherited)", debug.Configs)
	}
	if len(debug.Compose) != 1 || debug.Compose[0] != "compose/services/main/debug.yml" {
		t.Errorf("main-debug.Compose = %v, want [compose/services/main/debug.yml]", debug.Compose)
	}
}

func TestLoadServicesConfig_extendsInheritsContainer(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent-ctr
    dir: ./services/parent
  child-no-container:
    type: app
    extends: parent
  child-own-container:
    type: app
    container: child-ctr
    extends: parent
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child with no container should default to folder name, not inherit parent's container
	if got := services["child-no-container"].Container; got != "child-no-container" {
		t.Errorf("child-no-container.Container = %q, want child-no-container (folder-name default, not inherited)", got)
	}
	// child with its own container should keep it
	if got := services["child-own-container"].Container; got != "child-ctr" {
		t.Errorf("child-own-container.Container = %q, want child-ctr (own value)", got)
	}
}

func TestLoadServicesConfig_extendsUnknownParent(t *testing.T) {
	yml := `
services:
  child:
    type: app
    container: child
    extends: nonexistent
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	_, err := LoadServices(dir)
	if err == nil {
		t.Fatal("expected error for unknown extends parent")
	}
}

func TestLoadConfig_servicesEnabledFromMerge(t *testing.T) {
	defaults := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
  main-debug:
    enabled: false
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaults, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	main := cfg.Services["main"]
	if !main.Enabled {
		t.Error("main should be enabled (mandatory)")
	}
	debug := cfg.Services["main-debug"]
	if debug.Enabled {
		t.Error("main-debug should be disabled (defaults say false)")
	}
}

func TestLoadConfig_servicesEnabledByLocal(t *testing.T) {
	defaults := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: true
  mailpit:
    enabled: true
  main-debug:
    enabled: false
`
	local := `
services:
  main-debug:
    enabled: true
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaults, local, sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	debug := cfg.Services["main-debug"]
	if !debug.Enabled {
		t.Error("main-debug should be enabled (local override)")
	}
}

func TestLoadConfig_servicesInjectedIntoRaw(t *testing.T) {
	path := writeFullFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Export rules resolve from raw map — verify service data is there.
	val, ok := ResolvePath(cfg.Raw, "services.main.container")
	if !ok {
		t.Fatal("services.main.container not found in raw map")
	}
	if val != "app-main" {
		t.Errorf("services.main.container = %v, want app-main", val)
	}
}

func TestLoadConfig_noServicesFile(t *testing.T) {
	// Without services.yml, cfg.Services should be empty/nil-safe (no error).
	path := writeFullFixture(t, sampleWorkspaceYML, "", "", "", noToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Services) != 0 {
		t.Errorf("Services should be empty when services.yml absent, got %v", cfg.Services)
	}
}

// --- DeployConfig loading ---

const sampleDeployYML = `
phases:
  - name: setup
    description: Prepare directories
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/{src,configs}
        description: Create service hub directories
      - name: copy-configs
        type: shell
        cmd: dwe deploy config main
        description: Copy template configs
        when:
          type: template
          expr: "{{.Runtime.UseHTTPS}}"
  - name: start
    description: Start containers
    steps:
      - name: up
        type: command
        cmd: up
        description: Start all containers
`

func writeDeployFixture(t *testing.T, deployYML string) string {
	t.Helper()
	dir := t.TempDir()
	workspacePath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(workspacePath, []byte(sampleWorkspaceYML), 0644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	workspaceDir := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("mkdir workspace/: %v", err)
	}
	if deployYML != "" {
		if err := os.WriteFile(filepath.Join(workspaceDir, "deploy.yml"), []byte(deployYML), 0644); err != nil {
			t.Fatalf("write deploy.yml: %v", err)
		}
	}
	return workspacePath
}

func TestLoadServiceDeployConfig_phasesPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Fatalf("Phases len = %d, want 2", len(cfg.Phases))
	}
	if cfg.Phases[0].Name != "setup" {
		t.Errorf("Phases[0].Name = %q, want setup", cfg.Phases[0].Name)
	}
	if cfg.Phases[1].Name != "start" {
		t.Errorf("Phases[1].Name = %q, want start", cfg.Phases[1].Name)
	}
}

func TestLoadServiceDeployConfig_stepWithCmd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.Name != "create-dirs" {
		t.Errorf("step.Name = %q, want create-dirs", step.Name)
	}
	if step.Cmd == "" {
		t.Error("step.Cmd should be set for shell: steps")
	}
	if step.Type != "shell" {
		t.Error("step.Type should be 'shell' for cmd: steps")
	}
}

func TestLoadServiceDeployConfig_stepWithCommand(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[1].Steps[0]
	if step.Name != "up" {
		t.Errorf("step.Name = %q, want up", step.Name)
	}
	if step.Cmd == "" {
		t.Error("step.Cmd should be set for command: steps")
	}
	if step.Type != "command" {
		t.Error("step.Type should be 'command' for command: steps")
	}
}

func TestLoadServiceDeployConfig_stepWithWhen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[1]
	if step.When == nil {
		t.Fatalf("step.When is nil, want a Condition")
	}
	if step.When.Type != "template" {
		t.Errorf("step.When.Type = %q, want template", step.When.Type)
	}
	if step.When.Expr != "{{.Runtime.UseHTTPS}}" {
		t.Errorf("step.When.Expr = %q, want {{.Runtime.UseHTTPS}}", step.When.Expr)
	}
}

func TestLoadServiceDeployConfig_strictDecodeStringWhen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        when: "{{.Runtime.UseHTTPS}}"
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for string-form when:, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when' field: %v", err)
	}
}

func TestLoadServiceDeployConfig_invalidWhenType(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        when:
          type: bogus
          cmd: something
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for invalid when type, got nil")
	} else if !strings.Contains(err.Error(), "when") {
		t.Errorf("error should mention 'when': %v", err)
	}
}

func TestLoadServiceDeployConfig_strictDecodeUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	invalidYAML := `
phases:
  - name: setup
    steps:
      - name: test
        type: shell
        cmd: echo hello
        notafield: value
`
	if err := os.WriteFile(path, []byte(invalidYAML), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Errorf("LoadServiceDeployConfig expected error for unknown field, got nil")
	} else if !strings.Contains(err.Error(), "notafield") && !strings.Contains(err.Error(), "unknown") {
		t.Errorf("error should mention 'notafield' or 'unknown': %v", err)
	}
}

func TestLoadServiceDeployConfig_notFound(t *testing.T) {
	_, err := LoadServiceDeployConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadServiceDeployConfig_invalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte("{ invalid yaml ]["), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}

func TestLoadConfig_deployLoaded(t *testing.T) {
	workspacePath := writeDeployFixture(t, sampleDeployYML)
	cfg, err := LoadConfig(workspacePath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Deploy.Phases) != 2 {
		t.Fatalf("Deploy.Phases len = %d, want 2", len(cfg.Deploy.Phases))
	}
	if cfg.Deploy.Phases[0].Name != "setup" {
		t.Errorf("Deploy.Phases[0].Name = %q, want setup", cfg.Deploy.Phases[0].Name)
	}
}

func TestLoadConfig_deployAbsent(t *testing.T) {
	// No deploy.yml — Deploy field should be nil (no error).
	workspacePath := writeDeployFixture(t, "")
	cfg, err := LoadConfig(workspacePath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Deploy == nil {
		t.Errorf("Deploy should not be nil (empty default when deploy.yml absent), got %v", cfg.Deploy)
	}
	if len(cfg.Deploy.Phases) != 0 {
		t.Errorf("Deploy.Phases should be empty when deploy.yml absent, got %d phases", len(cfg.Deploy.Phases))
	}
}

func TestLoadServiceDeployConfig_emptyPhases(t *testing.T) {
	yml := "phases: []\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 0 {
		t.Errorf("Phases len = %d, want 0", len(cfg.Phases))
	}
}

func TestLoadServiceDeployConfig_legacyRunAndCommandFieldsRejected(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: bad-step
        run: echo hi
        command: services.main.migrate
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected strict-decode error for removed legacy fields 'run' and 'command', got nil")
	}
}

func TestLoadServiceDeployConfig_checkBadTypeProducesWrappedError(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: my-step
        type: shell
        cmd: echo hi
        check:
          type: badtype
          cmd: some-check
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected validation error for bad check type, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "my-step") {
		t.Errorf("error %q does not contain step name 'my-step'", msg)
	}
	if !strings.Contains(msg, "setup") {
		t.Errorf("error %q does not contain phase name 'setup'", msg)
	}
	if !strings.Contains(msg, "check") {
		t.Errorf("error %q does not contain 'check'", msg)
	}
}

func TestLoadServiceDeployConfig_stepNeitherCmdNorCommand(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: empty-step
        description: no cmd or command
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected error for step with neither cmd nor command, got nil")
	}
}

func TestLoadServiceDeployConfig_stepWithServiceConfigsCopy(t *testing.T) {
	yml := `phases:
  - name: setup
    steps:
      - name: copy-configs
        type: builtin
        cmd: service_configs_copy
        with:
          service: main
          mode: replace
        description: Copy configs
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.Type != "builtin" {
		t.Errorf("Type = %q, want builtin", step.Type)
	}
	if step.Cmd != "service_configs_copy" {
		t.Errorf("Cmd = %q, want service_configs_copy", step.Cmd)
	}
	service, _ := step.With["service"].(string)
	if service != "main" {
		t.Errorf("With[service] = %q, want main", service)
	}
	mode, _ := step.With["mode"].(string)
	if mode != "replace" {
		t.Errorf("With[mode] = %q, want replace", mode)
	}
}

func TestLoadServiceDeployConfig_stepWithCommandAndWith(t *testing.T) {
	yml := `phases:
  - name: init
    steps:
      - name: migrate
        type: command
        cmd: services.main.migrate
        with:
          db: mydb
          env: testing
        description: Run migrations
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.Type != "command" {
		t.Errorf("Type = %q, want command", step.Type)
	}
	if step.Cmd != "services.main.migrate" {
		t.Errorf("Cmd = %q, want services.main.migrate", step.Cmd)
	}
	if step.With["db"] != "mydb" {
		t.Errorf("With[db] = %q, want mydb", step.With["db"])
	}
	if step.With["env"] != "testing" {
		t.Errorf("With[env] = %q, want testing", step.With["env"])
	}
}

// --- DeployServices marker validation ---

func TestLoadServiceDeployConfig_deployServicesPhase(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    description: Deploy all services
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error: deploy_services is not allowed in per-service deploy.yml")
	}
	if !strings.Contains(err.Error(), "deploy_services is not allowed") {
		t.Errorf("error should mention 'deploy_services is not allowed', got: %v", err)
	}
}

func TestLoadServiceDeployConfig_deployServicesWithStepsError(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    steps:
      - name: bad
        cmd: echo bad
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with steps")
	}
}

func TestLoadServiceDeployConfig_deployServicesWithWhenError(t *testing.T) {
	yml := `phases:
  - name: services
    deploy_services: true
    when:
      type: shell
      cmd: "test -f marker"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase with when, got nil")
	}
	if !strings.Contains(err.Error(), "deploy_services is not allowed") {
		t.Errorf("error should mention 'deploy_services is not allowed', got: %v", err)
	}
}

func TestLoadServiceDeployConfig_phaseUIFieldRejected(t *testing.T) {
	// The ui: field was removed from DeployPhase. Strict decode rejects unknown fields.
	yml := `phases:
  - name: setup
    description: Setup phase
    ui: plain
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
        description: Create directories
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatalf("LoadServiceDeployConfig expected error for unknown field ui, got nil")
	}
	if !strings.Contains(err.Error(), "ui") && !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("error should mention ui or unknown field: %v", err)
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedDefaultFalseSimple(t *testing.T) {
	// Phases without untracked: default to false.
	yml := `phases:
  - name: start
    description: Start phase
    steps:
      - name: up
        type: dwe
        cmd: "docker up"
        description: Start containers
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (default)")
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedField(t *testing.T) {
	yml := `phases:
  - name: setup
    description: Setup phase
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
  - name: post-deploy
    description: Post-deploy phase
    untracked: true
    steps:
      - name: info
        type: dwe
        cmd: "info"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Fatalf("Phases len = %d, want 2", len(cfg.Phases))
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (default)")
	}
	if !cfg.Phases[1].Untracked {
		t.Error("Phases[1].Untracked = false, want true")
	}
}

func TestLoadServiceDeployConfig_phaseUntrackedDefaultFalse(t *testing.T) {
	yml := `phases:
  - name: start
    description: Start phase
    steps:
      - name: up
        type: dwe
        cmd: "docker up"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write deploy.yml: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.Phases[0].Untracked {
		t.Error("Phases[0].Untracked = true, want false (zero value default)")
	}
}

// --- TopoSortServices ---

func TestTopoSortServices_noDeps(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {},
		"b": {},
	}
	sorted, err := TopoSortServices([]string{"b", "a"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	if len(sorted) != 2 {
		t.Fatalf("want 2, got %d", len(sorted))
	}
}

func TestTopoSortServices_withDeps(t *testing.T) {
	services := map[string]ServiceConfig{
		"main":   {},
		"worker": {DependsOn: []string{"main"}},
	}
	sorted, err := TopoSortServices([]string{"worker", "main"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	// main must come before worker
	mainIdx, workerIdx := -1, -1
	for i, name := range sorted {
		if name == "main" {
			mainIdx = i
		}
		if name == "worker" {
			workerIdx = i
		}
	}
	if mainIdx >= workerIdx {
		t.Errorf("main (idx %d) should come before worker (idx %d)", mainIdx, workerIdx)
	}
}

func TestTopoSortServices_circularError(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {DependsOn: []string{"b"}},
		"b": {DependsOn: []string{"a"}},
	}
	_, err := TopoSortServices([]string{"a", "b"}, services)
	if err == nil {
		t.Fatal("expected circular dependency error")
	}
}

func TestTopoSortServices_unknownDepError(t *testing.T) {
	services := map[string]ServiceConfig{
		"a": {DependsOn: []string{"nonexistent"}},
	}
	_, err := TopoSortServices([]string{"a"}, services)
	if err == nil {
		t.Fatal("expected unknown dependency error")
	}
}

func TestTopoSortServices_depNotInSetSkipped(t *testing.T) {
	// worker depends on main, but main is not in the set being sorted.
	// main exists in services though — should not error, just skip.
	services := map[string]ServiceConfig{
		"main":   {},
		"worker": {DependsOn: []string{"main"}},
	}
	sorted, err := TopoSortServices([]string{"worker"}, services)
	if err != nil {
		t.Fatalf("TopoSortServices: %v", err)
	}
	if len(sorted) != 1 || sorted[0] != "worker" {
		t.Errorf("got %v, want [worker]", sorted)
	}
}

// --- LoadServiceDeployConfigs ---

func TestLoadServiceDeployConfigs_loadsExisting(t *testing.T) {
	dir := t.TempDir()
	mainDeployDir := filepath.Join(dir, "workspace", "services", "main")
	if err := os.MkdirAll(mainDeployDir, 0755); err != nil {
		t.Fatal(err)
	}
	mainDeploy := `phases:
  - name: setup
    steps:
      - name: create-dirs
        type: shell
        cmd: mkdir -p services/main/src
`
	if err := os.WriteFile(filepath.Join(mainDeployDir, "deploy.yml"), []byte(mainDeploy), 0644); err != nil {
		t.Fatal(err)
	}
	services := map[string]ServiceConfig{
		"main":  {},
		"other": {},
	}
	result, err := LoadServiceDeployConfigs(dir, services)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfigs: %v", err)
	}
	if _, ok := result["main"]; !ok {
		t.Error("expected main deploy config to be loaded")
	}
	if _, ok := result["other"]; ok {
		t.Error("other should not be loaded (no deploy file)")
	}
}

// --- Loader split: LoadProjectDeployConfig / LoadServiceDeployConfig ---

func TestLoadProjectDeployConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	yml := `after:
  - somesvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadProjectDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for after: in project deploy.yml, got nil")
	}
	// The after field is structurally not allowed in ProjectDeployConfig,
	// so KnownFields(true) YAML decoding rejects it automatically.
	if !strings.Contains(err.Error(), "after") && !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("err = %v, want YAML decode error mentioning 'after' or 'unmarshal'", err)
	}
}

func TestLoadProjectDeployConfig_acceptsNoAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(sampleDeployYML), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadProjectDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Phases) != 2 {
		t.Errorf("Phases len = %d, want 2", len(cfg.Phases))
	}
}

func TestLoadServiceDeployConfig_permitsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	yml := `after:
  - othersvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 1 || cfg.After[0] != "othersvc" {
		t.Errorf("After = %v, want [othersvc]", cfg.After)
	}
}

func TestLoadResetConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reset.yml")
	yml := `after:
  - somesvc
phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
`
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadResetConfig(path)
	if err == nil {
		t.Fatal("expected error for after: in reset.yml, got nil")
	}
	// The after field is structurally not allowed in ProjectDeployConfig (used for reset),
	// so KnownFields(true) YAML decoding rejects it automatically.
	if !strings.Contains(err.Error(), "after") && !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("err = %v, want YAML decode error mentioning 'after' or 'unmarshal'", err)
	}
}

// --- LoadServiceResetConfig / LoadServiceResetConfigs ---

func TestLoadServiceResetConfig_presentFile(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "workspace", "services", "mydb")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `phases:
  - name: wipe
    steps:
      - name: drop
        type: shell
        cmd: 'echo drop'
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceResetConfig(dir, "mydb")
	if err != nil {
		t.Fatalf("LoadServiceResetConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config, got nil")
	}
	if len(cfg.Phases) != 1 || cfg.Phases[0].Name != "wipe" {
		t.Errorf("phases = %v, want [{wipe ...}]", cfg.Phases)
	}
}

func TestLoadServiceResetConfig_missingFile(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadServiceResetConfig(dir, "ghost")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got: %v", err)
	}
	if cfg != nil {
		t.Fatalf("expected nil config for missing file, got: %+v", cfg)
	}
}

func TestLoadServiceResetConfig_unknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "workspace", "services", "svc")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `phases: []
bogus_field: true
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfig(dir, "svc")
	if err == nil {
		t.Fatal("expected strict-decode error for unknown field, got nil")
	}
}

func TestLoadServiceResetConfig_rejectsAfterField(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "workspace", "services", "svc")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	yml := `after:
  - other
phases: []
`
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfig(dir, "svc")
	if err == nil {
		t.Fatal("expected error for after: in service reset.yml, got nil")
	}
	// The after field is structurally not allowed in ProjectDeployConfig (used for service reset),
	// so KnownFields(true) YAML decoding rejects it automatically.
	if !strings.Contains(err.Error(), "after") && !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("err = %v, want YAML decode error mentioning 'after' or 'unmarshal'", err)
	}
}

func TestLoadServiceResetConfigs_loadsPresent(t *testing.T) {
	dir := t.TempDir()
	for _, svc := range []string{"db", "cache"} {
		d := filepath.Join(dir, "workspace", "services", svc)
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
		yml := "phases:\n  - name: wipe\n    steps:\n      - name: s\n        type: shell\n        cmd: 'true'\n"
		if err := os.WriteFile(filepath.Join(d, "reset.yml"), []byte(yml), 0644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// third service without reset.yml — should be omitted silently
	noReset := filepath.Join(dir, "workspace", "services", "noreset")
	if err := os.MkdirAll(noReset, 0755); err != nil {
		t.Fatal(err)
	}

	result, err := LoadServiceResetConfigs(dir)
	if err != nil {
		t.Fatalf("LoadServiceResetConfigs: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("want 2 entries (db, cache), got %d: %v", len(result), result)
	}
	for _, key := range []string{"db", "cache"} {
		if result[key] == nil {
			t.Errorf("expected non-nil entry for %q", key)
		}
	}
	if result["noreset"] != nil {
		t.Errorf("expected no entry for noreset, got %+v", result["noreset"])
	}
}

func TestLoadServiceResetConfigs_missingServicesDir(t *testing.T) {
	dir := t.TempDir()
	result, err := LoadServiceResetConfigs(dir)
	if err != nil {
		t.Fatalf("expected nil error for absent services dir, got: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty map, got %v", result)
	}
}

func TestLoadServiceResetConfigs_collectsErrors(t *testing.T) {
	dir := t.TempDir()
	// service with invalid reset.yml (unknown field)
	svcDir := filepath.Join(dir, "workspace", "services", "bad")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(svcDir, "reset.yml"), []byte("phases: []\nbad_key: x\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := LoadServiceResetConfigs(dir)
	if err == nil {
		t.Fatal("expected error for invalid service reset.yml, got nil")
	}
}

func TestDeployConfigAfter_decodePresent(t *testing.T) {
	yml := `after:
  - svc-a
  - svc-b
phases: []
`
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte(yml), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 2 || cfg.After[0] != "svc-a" || cfg.After[1] != "svc-b" {
		t.Errorf("After = %v, want [svc-a svc-b]", cfg.After)
	}
}

func TestDeployConfigAfter_decodeAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.yml")
	if err := os.WriteFile(path, []byte("phases: []\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.After) != 0 {
		t.Errorf("After = %v, want empty", cfg.After)
	}
}

// --- LoadServiceDeployConfigs new path ---

func TestLoadServiceDeployConfigs_strictDecoderRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	svcDir := filepath.Join(dir, "workspace", "services", "main")
	if err := os.MkdirAll(svcDir, 0755); err != nil {
		t.Fatal(err)
	}
	badYML := `phases:
  - name: setup
    steps:
      - name: noop
        type: shell
        cmd: 'true'
        notafield: boom
`
	if err := os.WriteFile(filepath.Join(svcDir, "deploy.yml"), []byte(badYML), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadServiceDeployConfigs(dir, map[string]ServiceConfig{"main": {}})
	if err == nil {
		t.Fatal("expected strict-decode error for unknown field, got nil")
	}
}

func TestLoadServiceDeployConfigs_onlyServicesWithDeployFile(t *testing.T) {
	dir := t.TempDir()
	mainDir := filepath.Join(dir, "workspace", "services", "main")
	if err := os.MkdirAll(mainDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mainDir, "deploy.yml"), []byte("phases: []\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// "other" has no deploy.yml
	otherDir := filepath.Join(dir, "workspace", "services", "other")
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatal(err)
	}
	services := map[string]ServiceConfig{"main": {}, "other": {}}
	result, err := LoadServiceDeployConfigs(dir, services)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfigs: %v", err)
	}
	if _, ok := result["main"]; !ok {
		t.Error("main should be loaded")
	}
	if _, ok := result["other"]; ok {
		t.Error("other should not be loaded (no deploy.yml)")
	}
}

func TestLoadConfig_exportsLoaded(t *testing.T) {
	defaultsWithExports := sampleDefaultsYML + `
exports:
  env:
    - name: APP_PORT
      from: services.app.ports.http
      format: int
    - name: STATE
      from: state
`
	path := writeLayeredFixture(t, sampleWorkspaceYML, defaultsWithExports, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if len(cfg.Exports.Env) != 2 {
		t.Fatalf("Exports.Env len = %d, want 2", len(cfg.Exports.Env))
	}
	if cfg.Exports.Env[0].Name != "APP_PORT" {
		t.Errorf("rule[0].Name = %q", cfg.Exports.Env[0].Name)
	}
	if cfg.Exports.Env[0].From != "services.app.ports.http" {
		t.Errorf("rule[0].From = %q", cfg.Exports.Env[0].From)
	}
	if cfg.Exports.Env[0].Format != "int" {
		t.Errorf("rule[0].Format = %q", cfg.Exports.Env[0].Format)
	}
}

func TestLoadConfig_reservedExportNameRejected(t *testing.T) {
	for _, name := range ReservedExportNames {
		t.Run(name, func(t *testing.T) {
			defaultsWithReservedRule := sampleDefaultsYML + `
exports:
  env:
    - name: ` + name + `
      from: services.app.ports.http
`
			path := writeLayeredFixture(t, sampleWorkspaceYML, defaultsWithReservedRule, "")
			_, err := LoadConfig(path)
			if err == nil {
				t.Fatalf("LoadConfig: expected error for reserved name %q, got nil", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q should mention reserved name %q", err.Error(), name)
			}
			if !strings.Contains(err.Error(), "reserved") {
				t.Errorf("error %q should explain that the name is reserved", err.Error())
			}
		})
	}
}

func TestIsReservedExportName(t *testing.T) {
	for _, name := range ReservedExportNames {
		if !IsReservedExportName(name) {
			t.Errorf("IsReservedExportName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "APP_PORT", "project", "uid", "gid", "PROJECT_NAME"} {
		if IsReservedExportName(name) {
			t.Errorf("IsReservedExportName(%q) = true, want false", name)
		}
	}
}

// --- ServiceCLIConfig: Mode and Env fields ---

const sampleServicesWithCLIYML = `
services:
  main:
    type: app
    container: app-main
    required: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    configs:
      - .env
    cli:
      mode: auto
      shell: bash
      user: www-data
      workdir: /workspace/src
      env:
        APP_ENV: local
        DEBUG: "true"
  main-debug:
    type: app
    container: app-main-debug
    required: false
    extends: main
    compose:
      - compose/services/main/debug.yml
  main-run:
    type: app
    container: app-main-run
    required: false
    extends: main
    cli:
      mode: run
`

func TestLoadServicesConfig_modeField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if main.CLI.Mode != "auto" {
		t.Errorf("main.CLI.Mode = %q, want auto", main.CLI.Mode)
	}
}

func TestLoadServicesConfig_envField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	main := services["main"]
	if len(main.CLI.Env) != 2 {
		t.Fatalf("main.CLI.Env len = %d, want 2", len(main.CLI.Env))
	}
	if main.CLI.Env["APP_ENV"] != "local" {
		t.Errorf("main.CLI.Env[APP_ENV] = %q, want local", main.CLI.Env["APP_ENV"])
	}
	if main.CLI.Env["DEBUG"] != "true" {
		t.Errorf("main.CLI.Env[DEBUG] = %q, want true", main.CLI.Env["DEBUG"])
	}
}

func TestLoadServicesConfig_extendsInheritsMode(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-debug extends main and has no CLI block of its own — inherits mode from parent
	debug := services["main-debug"]
	if debug.CLI.Mode != "auto" {
		t.Errorf("main-debug.CLI.Mode = %q, want auto (inherited from main)", debug.CLI.Mode)
	}
}

func TestLoadServicesConfig_extendsInheritsEnv(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-debug extends main and has no CLI.Env of its own — inherits env from parent
	debug := services["main-debug"]
	if len(debug.CLI.Env) != 2 {
		t.Fatalf("main-debug.CLI.Env len = %d, want 2 (inherited from main)", len(debug.CLI.Env))
	}
	if debug.CLI.Env["APP_ENV"] != "local" {
		t.Errorf("main-debug.CLI.Env[APP_ENV] = %q, want local (inherited)", debug.CLI.Env["APP_ENV"])
	}
}

func TestLoadServicesConfig_extendsOverridesMode(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithCLIYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// main-run extends main but sets its own mode: run — should NOT inherit parent mode
	run := services["main-run"]
	if run.CLI.Mode != "run" {
		t.Errorf("main-run.CLI.Mode = %q, want run (own value, not inherited)", run.CLI.Mode)
	}
}

// --- ServiceConfig.Dirs field ---

const sampleServicesWithDirsYML = `
services:
  base:
    type: app
    container: app-base
    required: true
    dir: ./services/base
    dirs:
      - logs
      - home
      - runtime
  child:
    type: app
    container: app-child
    required: false
    extends: base
    dirs:
      - extra
  child-nodir:
    type: app
    container: app-child-nodir
    required: false
    extends: base
  child-overlap:
    type: app
    container: app-child-overlap
    required: false
    extends: base
    dirs:
      - logs
      - home
      - custom
`

func TestLoadServicesConfig_dirsField(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	base := services["base"]
	want := []string{"logs", "home", "runtime"}
	if len(base.Dirs) != len(want) {
		t.Fatalf("base.Dirs = %v, want %v", base.Dirs, want)
	}
	for i, d := range want {
		if base.Dirs[i] != d {
			t.Errorf("base.Dirs[%d] = %q, want %q", i, base.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsInheritedAndMerged(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child extends base (dirs: [logs, home, runtime]) and adds dirs: [extra]
	// expected: parent first, then child additions
	child := services["child"]
	want := []string{"logs", "home", "runtime", "extra"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child.Dirs = %v, want %v", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsInheritedWhenChildEmpty(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child-nodir extends base and has no dirs of its own — should inherit parent dirs
	child := services["child-nodir"]
	want := []string{"logs", "home", "runtime"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child-nodir.Dirs = %v, want %v", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child-nodir.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

func TestLoadServicesConfig_dirsDeduplicated(t *testing.T) {
	dir := t.TempDir()
	writeServicesDir(t, dir, sampleServicesWithDirsYML)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}
	// child-overlap extends base (logs, home, runtime) and adds (logs, home, custom)
	// duplicate logs and home must appear only once; parent order preserved
	child := services["child-overlap"]
	want := []string{"logs", "home", "runtime", "custom"}
	if len(child.Dirs) != len(want) {
		t.Fatalf("child-overlap.Dirs = %v, want %v (deduplicated)", child.Dirs, want)
	}
	for i, d := range want {
		if child.Dirs[i] != d {
			t.Errorf("child-overlap.Dirs[%d] = %q, want %q", i, child.Dirs[i], d)
		}
	}
}

// --- ServiceConfig display accessors ---

func TestServiceConfig_DisplayTitle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		title     string
		folderKey string
		want      string
	}{
		{name: "override", title: "Custom Title", folderKey: "redis_insight", want: "Custom Title"},
		{name: "title-case", title: "", folderKey: "redis_insight", want: "Redis Insight"},
		{name: "hyphen", title: "", folderKey: "app-main", want: "App Main"},
		{name: "simple", title: "", folderKey: "catalog", want: "Catalog"},
		{name: "preserve internal caps", title: "", folderKey: "myAPI", want: "MyAPI"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := ServiceConfig{Info: ServiceInfoBlock{Title: tt.title}}
			got := s.DisplayTitle(tt.folderKey)
			if got != tt.want {
				t.Errorf("DisplayTitle(%q) = %q, want %q", tt.folderKey, got, tt.want)
			}
		})
	}
}

func TestServiceInfoPath_DisplayIcon(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		icon string
		want string
	}{
		{name: "override", icon: "📖", want: "📖"},
		{name: "default", icon: "", want: "🔗"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := ServiceInfoPath{Icon: tt.icon}
			got := p.DisplayIcon()
			if got != tt.want {
				t.Errorf("DisplayIcon() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceConfig_DisplayHostKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		hostKey string
		want    string
	}{
		{name: "override", hostKey: "console", want: "console"},
		{name: "default", hostKey: "", want: "web"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := ServiceConfig{Info: ServiceInfoBlock{PrimaryHost: tt.hostKey}}
			got := s.DisplayHostKey()
			if got != tt.want {
				t.Errorf("DisplayHostKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestServiceConfig_DisplayPortKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		portKey string
		want    string
	}{
		{name: "override", portKey: "console", want: "console"},
		{name: "default", portKey: "", want: "http"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := ServiceConfig{Info: ServiceInfoBlock{PrimaryPort: tt.portKey}}
			got := s.DisplayPortKey()
			if got != tt.want {
				t.Errorf("DisplayPortKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLoadServiceFolder_withInfoBlock(t *testing.T) {
	dir := t.TempDir()
	serviceYAML := `
type: app
container: app-main
icon: "📦"
info:
  title: "Main App"
  primary_host: web
  primary_port: http
  paths:
    - name: "API Docs"
      path: /api/docs
      icon: "📖"
    - name: "Clockwork"
      path: /__clockwork
`
	writeServiceYAML(t, dir, "main", serviceYAML)
	svc, err := LoadServiceFolder(dir, "main")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if svc.Icon != "📦" {
		t.Errorf("Icon = %q, want 📦", svc.Icon)
	}
	if svc.Info.Title != "Main App" {
		t.Errorf("Info.Title = %q, want Main App", svc.Info.Title)
	}
	if svc.Info.PrimaryHost != "web" {
		t.Errorf("Info.PrimaryHost = %q, want web", svc.Info.PrimaryHost)
	}
	if svc.Info.PrimaryPort != "http" {
		t.Errorf("Info.PrimaryPort = %q, want http", svc.Info.PrimaryPort)
	}
	if len(svc.Info.Paths) != 2 {
		t.Errorf("len(Info.Paths) = %d, want 2", len(svc.Info.Paths))
	}
	if svc.Info.Paths[0].Name != "API Docs" {
		t.Errorf("Paths[0].Name = %q, want API Docs", svc.Info.Paths[0].Name)
	}
	if svc.Info.Paths[0].Path != "/api/docs" {
		t.Errorf("Paths[0].Path = %q, want /api/docs", svc.Info.Paths[0].Path)
	}
	if svc.Info.Paths[0].Icon != "📖" {
		t.Errorf("Paths[0].Icon = %q, want 📖", svc.Info.Paths[0].Icon)
	}
	// Paths[1] has no icon, verify it loaded
	if svc.Info.Paths[1].Name != "Clockwork" {
		t.Errorf("Paths[1].Name = %q, want Clockwork", svc.Info.Paths[1].Name)
	}
	if svc.Info.Paths[1].Icon != "" {
		t.Errorf("Paths[1].Icon = %q, want empty", svc.Info.Paths[1].Icon)
	}
}

func TestLoadServiceFolder_rejectOldInfoFieldNames(t *testing.T) {
	dir := t.TempDir()
	serviceYAML := `
type: app
container: app-main
info:
  title: "My App"
  host_key: web
  port_key: http
`
	writeServiceYAML(t, dir, "main", serviceYAML)
	_, err := LoadServiceFolder(dir, "main")
	if err == nil {
		t.Fatalf("LoadServiceFolder: expected error for old field names, got nil")
	}
	// KnownFields strict-decode rejects unknown fields
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("LoadServiceFolder error should mention unknown field, got: %v", err)
	}
}

func TestLoadServiceFolder_strictDecode_infotypo(t *testing.T) {
	dir := t.TempDir()
	serviceYAML := `
type: tool
container: redis
info:
  tilte: "Redis"
`
	writeServiceYAML(t, dir, "redis", serviceYAML)
	_, err := LoadServiceFolder(dir, "redis")
	if err == nil {
		t.Fatal("expected error for typo in info field")
	}
	// Verify it's a KnownFields error
	if !strings.Contains(err.Error(), "unknown field") && !strings.Contains(err.Error(), "tilte") {
		t.Errorf("error message should mention unknown field: %v", err)
	}
}

func TestLoadServiceFolder_pathsOrderPreserved(t *testing.T) {
	dir := t.TempDir()
	serviceYAML := `
type: app
container: app-main
info:
  paths:
    - name: "First"
      path: /first
    - name: "Second"
      path: /second
    - name: "Third"
      path: /third
`
	writeServiceYAML(t, dir, "main", serviceYAML)
	svc, err := LoadServiceFolder(dir, "main")
	if err != nil {
		t.Fatalf("LoadServiceFolder: %v", err)
	}
	if len(svc.Info.Paths) != 3 {
		t.Fatalf("len(Paths) = %d, want 3", len(svc.Info.Paths))
	}
	want := []string{"First", "Second", "Third"}
	for i, name := range want {
		if svc.Info.Paths[i].Name != name {
			t.Errorf("Paths[%d].Name = %q, want %q", i, svc.Info.Paths[i].Name, name)
		}
	}
}

// --- LoadLifecycleConfig ---

const sampleLifecycleYML = `
run:
  show_info: true
  final_message: "Project is ready for work!"
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
        - name: wait
          type: dwe
          cmd: "docker wait"
stop:
  final_message: "Project is stopped. Have a nice day!"
  phases:
    - name: stop
      steps:
        - name: down
          type: dwe
          cmd: "docker down"
`

func writeLifecycleFixture(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lifecycle.yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write lifecycle.yml: %v", err)
	}
	return path
}

func TestLoadLifecycleConfig_happyPath(t *testing.T) {
	path := writeLifecycleFixture(t, sampleLifecycleYML)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run == nil {
		t.Fatal("cfg.Run should not be nil")
	}
	if !cfg.Run.ShowInfo {
		t.Error("cfg.Run.ShowInfo should be true")
	}
	if cfg.Run.FinalMessage != "Project is ready for work!" {
		t.Errorf("cfg.Run.FinalMessage = %q", cfg.Run.FinalMessage)
	}
	if len(cfg.Run.Phases) != 1 || cfg.Run.Phases[0].Name != "start" {
		t.Errorf("cfg.Run.Phases = %v", cfg.Run.Phases)
	}
	if cfg.Stop == nil {
		t.Fatal("cfg.Stop should not be nil")
	}
	if cfg.Stop.FinalMessage != "Project is stopped. Have a nice day!" {
		t.Errorf("cfg.Stop.FinalMessage = %q", cfg.Stop.FinalMessage)
	}
	if len(cfg.Stop.Phases) != 1 || cfg.Stop.Phases[0].Name != "stop" {
		t.Errorf("cfg.Stop.Phases = %v", cfg.Stop.Phases)
	}
}

func TestLoadLifecycleConfig_notFound(t *testing.T) {
	_, err := LoadLifecycleConfig(filepath.Join(t.TempDir(), "nonexistent.yml"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

func TestLoadLifecycleConfig_invalidStep(t *testing.T) {
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: bad
          type: dwe
          cmd: "docker up"
          run: echo hello
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for step with two action fields, got nil")
	}
}

func TestLoadLifecycleConfig_rejectsDeployServicesPhase(t *testing.T) {
	yml := `
run:
  phases:
    - name: services
      deploy_services: true
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for deploy_services phase in lifecycle, got nil")
	}
}

func TestLoadLifecycleConfig_RejectsUpdateBlock(t *testing.T) {
	// The update: block was lifted out of lifecycle.yml into the top-level
	// update: config block. The strict (KnownFields) lifecycle loader must now
	// reject any lingering run.update as an unknown field.
	yml := `
run:
  update:
    mode: on
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
`
	path := writeLifecycleFixture(t, yml)
	_, err := LoadLifecycleConfig(path)
	if err == nil {
		t.Fatal("expected error for run.update (moved to top-level update: block), got nil")
	}
}

func TestLoadLifecycleConfig_defaultFinalMessages(t *testing.T) {
	// final_message omitted in both run and stop — defaults should be applied by loader.
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          type: dwe
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.FinalMessage != "Project is ready for work!" {
		t.Errorf("Run.FinalMessage = %q, want default", cfg.Run.FinalMessage)
	}
	if cfg.Stop.FinalMessage != "Project is stopped. Have a nice day!" {
		t.Errorf("Stop.FinalMessage = %q, want default", cfg.Stop.FinalMessage)
	}
}

func TestLoadLifecycleConfig_ContinueOnError_YAML(t *testing.T) {
	// Verify that continue_on_error: true in YAML is correctly parsed by the loader.
	yml := `
run:
  phases:
    - name: hooks
      steps:
        - name: optional-hook
          type: shell
          cmd: echo hello
          continue_on_error: true
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if len(cfg.Run.Phases) == 0 || len(cfg.Run.Phases[0].Steps) == 0 {
		t.Fatal("expected at least one phase with one step")
	}
	step := cfg.Run.Phases[0].Steps[0]
	if !step.ContinueOnError {
		t.Errorf("step.ContinueOnError = false, want true (continue_on_error: true in YAML)")
	}
}

func TestLoadLifecycleConfig_explicitFinalMessagesPreserved(t *testing.T) {
	yml := `
run:
  final_message: "Custom run message"
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
stop:
  final_message: "Custom stop message"
  phases:
    - name: stop
      steps:
        - name: down
          type: dwe
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.FinalMessage != "Custom run message" {
		t.Errorf("Run.FinalMessage = %q, want preserved value", cfg.Run.FinalMessage)
	}
	if cfg.Stop.FinalMessage != "Custom stop message" {
		t.Errorf("Stop.FinalMessage = %q, want preserved value", cfg.Stop.FinalMessage)
	}
}

// --- mergeDeduplicatedStrings ---

func TestMergeDeduplicatedStrings_basic(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b"}, []string{"c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d] got %q, want %q", i, got[i], v)
		}
	}
}

func TestMergeDeduplicatedStrings_deduplicates(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b", "c"}, []string{"b", "d"})
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, v := range want {
		if got[i] != v {
			t.Errorf("[%d] got %q, want %q", i, got[i], v)
		}
	}
}

func TestMergeDeduplicatedStrings_emptyA(t *testing.T) {
	got := mergeDeduplicatedStrings(nil, []string{"x", "y"})
	want := []string{"x", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeDeduplicatedStrings_emptyB(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"x", "y"}, nil)
	want := []string{"x", "y"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeDeduplicatedStrings_bothEmpty(t *testing.T) {
	got := mergeDeduplicatedStrings(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestMergeDeduplicatedStrings_allDuplicates(t *testing.T) {
	got := mergeDeduplicatedStrings([]string{"a", "b"}, []string{"a", "b"})
	want := []string{"a", "b"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// writePipelineFixture writes content to a temporary <name>.yml and returns the path.
func writePipelineFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, name+".yml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestLoadServiceDeployConfig_logDefaultEnabled verifies that omitting `log:` in
// deploy.yml defaults to logging enabled.
func TestLoadServiceDeployConfig_logDefaultEnabled(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "phases: []\n")
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if !cfg.LogEnabled() {
		t.Errorf("deploy log should default to enabled")
	}
}

// TestLoadServiceDeployConfig_logExplicitFalse verifies that `log: false` disables it.
func TestLoadServiceDeployConfig_logExplicitFalse(t *testing.T) {
	path := writePipelineFixture(t, "deploy", "log: false\nphases: []\n")
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	if cfg.LogEnabled() {
		t.Errorf("deploy log should be disabled when log: false")
	}
}

// TestLoadResetConfig_logDefaultDisabled verifies that omitting `log:` in
// reset.yml defaults to logging disabled.
func TestLoadResetConfig_logDefaultDisabled(t *testing.T) {
	path := writePipelineFixture(t, "reset", "phases: []\n")
	cfg, err := LoadResetConfig(path)
	if err != nil {
		t.Fatalf("LoadResetConfig: %v", err)
	}
	if cfg.LogEnabled() {
		t.Errorf("reset log should default to disabled")
	}
}

// TestLoadResetConfig_logExplicitTrue verifies that `log: true` enables it.
func TestLoadResetConfig_logExplicitTrue(t *testing.T) {
	path := writePipelineFixture(t, "reset", "log: true\nphases: []\n")
	cfg, err := LoadResetConfig(path)
	if err != nil {
		t.Fatalf("LoadResetConfig: %v", err)
	}
	if !cfg.LogEnabled() {
		t.Errorf("reset log should be enabled when log: true")
	}
}

// TestLoadLifecycleConfig_logDefaults verifies run/stop logging defaults to
// disabled when the `log:` field is omitted.
func TestLoadLifecycleConfig_logDefaults(t *testing.T) {
	yml := `
run:
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
stop:
  phases:
    - name: stop
      steps:
        - name: down
          type: dwe
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if cfg.Run.LogEnabled() {
		t.Errorf("run log should default to disabled")
	}
	if cfg.Stop.LogEnabled() {
		t.Errorf("stop log should default to disabled")
	}
}

// --- FilesGate ---

func TestLoadServiceDeployConfig_stepWithFilesGateShortForm(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate: readable
`
	path := writePipelineFixture(t, "deploy", yml)
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.FilesGate == nil {
		t.Fatal("FilesGate should be set")
	}
	if step.FilesGate.State != "readable" {
		t.Errorf("FilesGate.State = %q, want readable", step.FilesGate.State)
	}
	if step.FilesGate.Command != "" {
		t.Errorf("FilesGate.Command should be empty (default to step.cmd), got %q", step.FilesGate.Command)
	}
}

func TestLoadServiceDeployConfig_stepWithFilesGateLongForm(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate:
          state: missing
          require: [dump]
`
	path := writePipelineFixture(t, "deploy", yml)
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: %v", err)
	}
	step := cfg.Phases[0].Steps[0]
	if step.FilesGate == nil {
		t.Fatal("FilesGate should be set")
	}
	if step.FilesGate.State != "missing" {
		t.Errorf("FilesGate.State = %q, want missing", step.FilesGate.State)
	}
}

func TestLoadServiceDeployConfig_stepWithFilesGateUnknownField(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: dump-deploy
        type: command
        cmd: db-dump-deploy
        files_gate:
          state: readable
          unknown_field: value
`
	path := writePipelineFixture(t, "deploy", yml)
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("LoadServiceDeployConfig: expected error for unknown field in files_gate")
	}
	if !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("LoadServiceDeployConfig error should mention unknown field, got: %v", err)
	}
}

// --- Phase name validation ---

func TestLoadServiceDeployConfig_rejectsUnderscorePhaseName(t *testing.T) {
	// Phase names starting with "_" are reserved for engine-synthetic phases and
	// must be rejected at loader time so users cannot craft user-authored pipelines
	// that masquerade as engine phases.
	yml := `
phases:
  - name: _evil_phase
    steps:
      - name: do-thing
        type: shell
        cmd: echo hi
`
	path := writePipelineFixture(t, "deploy", yml)
	_, err := LoadServiceDeployConfig(path)
	if err == nil {
		t.Fatal("expected error for underscore-prefixed phase name")
	}
	if !strings.Contains(err.Error(), "_evil_phase") {
		t.Errorf("error should mention the phase name, got: %v", err)
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should mention reserved, got: %v", err)
	}
}

func TestLoadServiceDeployConfig_regularPhaseNameOK(t *testing.T) {
	yml := `
phases:
  - name: setup
    steps:
      - name: init
        type: shell
        cmd: echo ok
`
	path := writePipelineFixture(t, "deploy", yml)
	cfg, err := LoadServiceDeployConfig(path)
	if err != nil {
		t.Fatalf("LoadServiceDeployConfig: unexpected error: %v", err)
	}
	if len(cfg.Phases) != 1 || cfg.Phases[0].Name != "setup" {
		t.Errorf("unexpected phases: %+v", cfg.Phases)
	}
}

// --- Binary Accessors ---

func TestLoadConfig_rejectsBinariesBlock(t *testing.T) {
	// LoadConfig rejects workspace.yml with binaries: block and returns migration error
	cfgYAML := `
schema_version: "1"
project:
  name: test
binaries:
  docker: podman
`
	path := writeLayeredFixture(t, cfgYAML, sampleDefaultsYML, "")
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig: expected error for binaries: block, got nil")
	}
	if !strings.Contains(err.Error(), "binaries: moved to") {
		t.Errorf("LoadConfig error message = %q, want migration message about ~/.config/dwe/config", err.Error())
	}
}

func TestLoadConfig_rejectsToolsBlock(t *testing.T) {
	// LoadConfig rejects workspace.yml with tools: block and returns migration error
	cfgYAML := `
schema_version: "1"
project:
  name: test
tools:
  - name: mytool
`
	path := writeLayeredFixture(t, cfgYAML, sampleDefaultsYML, "")
	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("LoadConfig: expected error for tools: block, got nil")
	}
	if !strings.Contains(err.Error(), "tools: no longer supported") {
		t.Errorf("LoadConfig error message = %q, want migration message about services with type: tool", err.Error())
	}
}

func TestBinaryAccessorDefaults(t *testing.T) {
	// All accessors return defaults when cfg is nil
	if got := DweBin(nil); got != "dwe" {
		t.Errorf("DweBin(nil) = %q, want dwe", got)
	}
	if got := DockerBin(nil); got != "docker" {
		t.Errorf("DockerBin(nil) = %q, want docker", got)
	}
	if got := ShellBin(nil); got != "sh" {
		t.Errorf("ShellBin(nil) = %q, want sh", got)
	}
	if got := GitBin(nil); got != "git" {
		t.Errorf("GitBin(nil) = %q, want git", got)
	}
	if got := MmdcBin(nil); got != "mmdc" {
		t.Errorf("MmdcBin(nil) = %q, want mmdc", got)
	}

	// All accessors return defaults when cfg exists but userConfig is nil
	cfg := &DweConfig{}
	if got := DweBin(cfg); got != "dwe" {
		t.Errorf("DweBin(cfg) = %q, want dwe", got)
	}
	if got := DockerBin(cfg); got != "docker" {
		t.Errorf("DockerBin(cfg) = %q, want docker", got)
	}
	if got := ShellBin(cfg); got != "sh" {
		t.Errorf("ShellBin(cfg) = %q, want sh", got)
	}
	if got := GitBin(cfg); got != "git" {
		t.Errorf("GitBin(cfg) = %q, want git", got)
	}
	if got := MmdcBin(cfg); got != "mmdc" {
		t.Errorf("MmdcBin(cfg) = %q, want mmdc", got)
	}
}

func TestBinaryAccessorUserConfigOverrides(t *testing.T) {
	// Accessors use userConfig overrides when available
	cfg := &DweConfig{
		userConfig: &userpkg.Config{
			Binaries: map[string]string{
				"dwe":    "/custom/dwe",
				"docker": "podman",
				"shell":  "bash",
				"git":    "/opt/git",
				"mmdc":   "mermaid",
			},
		},
	}
	if got := DweBin(cfg); got != "/custom/dwe" {
		t.Errorf("DweBin(cfg) = %q, want /custom/dwe", got)
	}
	if got := DockerBin(cfg); got != "podman" {
		t.Errorf("DockerBin(cfg) = %q, want podman", got)
	}
	if got := ShellBin(cfg); got != "bash" {
		t.Errorf("ShellBin(cfg) = %q, want bash", got)
	}
	if got := GitBin(cfg); got != "/opt/git" {
		t.Errorf("GitBin(cfg) = %q, want /opt/git", got)
	}
	if got := MmdcBin(cfg); got != "mermaid" {
		t.Errorf("MmdcBin(cfg) = %q, want mermaid", got)
	}
}

func TestBinaryAccessorPartialUserConfigOverrides(t *testing.T) {
	// Accessors fall back to defaults for missing overrides
	cfg := &DweConfig{
		userConfig: &userpkg.Config{
			Binaries: map[string]string{
				"docker": "podman",
			},
		},
	}
	if got := DweBin(cfg); got != "dwe" {
		t.Errorf("DweBin(cfg) = %q, want dwe (default)", got)
	}
	if got := DockerBin(cfg); got != "podman" {
		t.Errorf("DockerBin(cfg) = %q, want podman (override)", got)
	}
	if got := ShellBin(cfg); got != "sh" {
		t.Errorf("ShellBin(cfg) = %q, want sh (default)", got)
	}
}

func TestBinOverride(t *testing.T) {
	tests := []struct {
		name string
		cfg  *DweConfig
		key  string
		def  string
		want string
	}{
		{
			name: "nil cfg returns default",
			cfg:  nil,
			key:  "docker",
			def:  "docker",
			want: "docker",
		},
		{
			name: "cfg with nil userConfig returns default",
			cfg:  &DweConfig{},
			key:  "docker",
			def:  "docker",
			want: "docker",
		},
		{
			name: "override present returns override",
			cfg: &DweConfig{
				userConfig: &userpkg.Config{
					Binaries: map[string]string{"docker": "podman"},
				},
			},
			key:  "docker",
			def:  "docker",
			want: "podman",
		},
		{
			name: "override key missing returns default",
			cfg: &DweConfig{
				userConfig: &userpkg.Config{
					Binaries: map[string]string{"git": "/opt/git"},
				},
			},
			key:  "docker",
			def:  "docker",
			want: "docker",
		},
		{
			name: "override present with empty-string value overrides default",
			cfg: &DweConfig{
				userConfig: &userpkg.Config{
					Binaries: map[string]string{"docker": ""},
				},
			},
			key: "docker",
			def: "docker",
			// BinaryOverride returns ("", true) for an empty-string entry,
			// so binOverride returns the empty string (override wins).
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := binOverride(tt.cfg, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("binOverride(%v, %q, %q) = %q, want %q", tt.cfg, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

func TestLoadConfig_docsDefaults(t *testing.T) {
	// Test that docs config defaults to empty string and 0, which resolve to "auto" and 100 MB
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Empty mermaid field defaults to "auto" via MermaidMode accessor
	if got := MermaidMode(cfg); got != "auto" {
		t.Errorf("MermaidMode(cfg) = %q, want auto", got)
	}

	// Zero cache size defaults to 100 MB via MermaidCacheSizeMB accessor
	if got := MermaidCacheSizeMB(cfg); got != 100 {
		t.Errorf("MermaidCacheSizeMB(cfg) = %d, want 100", got)
	}
}

func TestLoadConfig_docsConfigured(t *testing.T) {
	cfgYAML := sampleWorkspaceYML
	defaultsYML := sampleDefaultsYML + `
docs:
  mermaid: mmdc
  cache_size_mb: 50
`
	path := writeLayeredFixture(t, cfgYAML, defaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if got := cfg.Docs.Mermaid; got != "mmdc" {
		t.Errorf("Docs.Mermaid = %q, want mmdc", got)
	}
	if got := cfg.Docs.CacheSizeMB; got != 50 {
		t.Errorf("Docs.CacheSizeMB = %d, want 50", got)
	}

	// Accessors should return the configured values
	if got := MermaidMode(cfg); got != "mmdc" {
		t.Errorf("MermaidMode(cfg) = %q, want mmdc", got)
	}
	if got := MermaidCacheSizeMB(cfg); got != 50 {
		t.Errorf("MermaidCacheSizeMB(cfg) = %d, want 50", got)
	}
}

func TestLoadConfig_docsCacheSizeClamp(t *testing.T) {
	// Test that negative cache size is clamped to 100 by the accessor
	cfgYAML := sampleWorkspaceYML
	defaultsYML := sampleDefaultsYML + `
docs:
  cache_size_mb: -1
`
	path := writeLayeredFixture(t, cfgYAML, defaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Raw config contains the negative value
	if got := cfg.Docs.CacheSizeMB; got != -1 {
		t.Errorf("Docs.CacheSizeMB = %d, want -1 (raw)", got)
	}

	// But the accessor clamps it to 100
	if got := MermaidCacheSizeMB(cfg); got != 100 {
		t.Errorf("MermaidCacheSizeMB(cfg) = %d, want 100 (clamped)", got)
	}
}

// TestLoadConfig_noTopLevelIDEField verifies that the top-level IDE config
// has been removed from DweConfig. The IDE field is no longer part of the
// typed configuration and cfg.IDE does not exist.
func TestLoadConfig_noTopLevelIDEField(t *testing.T) {
	cfgStructType := reflect.TypeFor[DweConfig]()
	if _, ok := cfgStructType.FieldByName("IDE"); ok {
		t.Error("DweConfig should not have an IDE field")
	}
}

// TestLoadLifecycleConfig_logExplicit verifies that `log: true` is respected
// for both run and stop.
func TestLoadLifecycleConfig_logExplicit(t *testing.T) {
	yml := `
run:
  log: true
  phases:
    - name: start
      steps:
        - name: up
          type: dwe
          cmd: "docker up"
stop:
  log: true
  phases:
    - name: stop
      steps:
        - name: down
          type: dwe
          cmd: "docker down"
`
	path := writeLifecycleFixture(t, yml)
	cfg, err := LoadLifecycleConfig(path)
	if err != nil {
		t.Fatalf("LoadLifecycleConfig: %v", err)
	}
	if !cfg.Run.LogEnabled() {
		t.Errorf("run log should be enabled when log: true")
	}
	if !cfg.Stop.LogEnabled() {
		t.Errorf("stop log should be enabled when log: true")
	}
}

// --- ComposeFilesAll ---

func TestComposeFilesAll_baseOnly(t *testing.T) {
	// Only base file, no tool overlays or service composes.
	defaultsWithCompose := minimalDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithCompose, "", "", minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	got := cfg.ComposeFilesAll()
	want := []string{"compose.yaml"}
	if len(got) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(got), len(want), got)
	}
	if got[0] != want[0] {
		t.Errorf("ComposeFilesAll[0] = %q, want %q", got[0], want[0])
	}
}

func TestComposeFilesAll_baseAndDisabledToolOverlay(t *testing.T) {
	// Base + disabled tool overlay. ComposeFilesAll must include disabled overlays.
	// Create a tools.yml with all 3 tools having compose files; defaults disables adminer.
	customTools := `
services:
  adminer:
    type: tool
    container: adminer
    compose:
      - compose/tools/adminer.yml
  mailpit:
    type: tool
    container: mailpit
    compose:
      - compose/tools/mailpit.yml
  redis_insight:
    type: tool
    container: redis_insight
    compose:
      - compose/tools/redis_insight.yml
`
	defaultsWithCompose := `
schema_version: "1"
services:
  adminer:
    enabled: false
  mailpit:
    enabled: true
  redis_insight:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithCompose, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	// Note: adminer is disabled, redis_insight and mailpit enabled.
	// ComposeFilesAll must include all, ComposeFiles only includes enabled.
	allFiles := cfg.ComposeFilesAll()
	activeFiles := cfg.ComposeFiles()

	// Verify ComposeFilesAll includes all overlays in sorted key order.
	want := []string{
		"compose.yaml",
		"compose/tools/adminer.yml",       // sorted first, even though disabled
		"compose/tools/mailpit.yml",       // sorted
		"compose/tools/redis_insight.yml", // sorted
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// Verify ComposeFiles only includes enabled overlays (redis_insight, mailpit, but not adminer).
	wantActive := []string{
		"compose.yaml",
		"compose/tools/mailpit.yml",       // sorted
		"compose/tools/redis_insight.yml", // sorted
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_baseAndDisabledServiceOverlay(t *testing.T) {
	// Base + service composes, with mixed enabled/disabled services.
	// Use minimalDefaultsYML to avoid tool compose files.
	defaultsWithCompose := `
schema_version: "1"
services:
  adminer:
    enabled: false
  redis_insight:
    enabled: false
  mailpit:
    enabled: false
  worker:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
state: ""
compose:
  base: compose.yaml
`
	servicesYML := `
services:
  main:
    type: app
    container: app-main
    required: true
    dir: ./services/main
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/main/base.yml
  worker:
    type: app
    container: app-worker
    required: false
    dir: ./services/worker
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/worker/base.yml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithCompose, "", servicesYML, minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// ComposeFilesAll includes all services' composes, regardless of enabled state.
	allFiles := cfg.ComposeFilesAll()
	want := []string{
		"compose.yaml",
		"compose/services/main/base.yml",
		"compose/services/worker/base.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// ComposeFiles only includes enabled services.
	activeFiles := cfg.ComposeFiles()
	wantActive := []string{
		"compose.yaml",
		"compose/services/main/base.yml",
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_fullMixedScenario(t *testing.T) {
	// Base + tool overlays (some disabled) + service composes (some disabled).
	// Verify all are included in ComposeFilesAll, in correct order.
	// Use sampleDefaultsYML which already has tool compose files.
	defaultsWithBase := sampleDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithBase, "", sampleServicesYML, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	allFiles := cfg.ComposeFilesAll()

	// Expected: base, all tool overlays in sorted key order, all service composes in sorted name order.
	// From sampleDefaultsYML: adminer (disabled, no compose), mailpit (enabled), redis_insight (enabled)
	want := []string{
		"compose.yaml",
		// Tool overlays in sorted key order (mailpit, redis_insight)
		"compose/tools/mailpit.yml",
		"compose/tools/redis_insight.yml",
		// Service composes in sorted service name order (main, main-debug)
		"compose/services/main/debug.yml", // from main-debug service in sampleServicesYML
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_noBase(t *testing.T) {
	// Tool overlays without a base file. ComposeFilesAll must still work.
	// Use sampleDefaultsYML which has mailpit and redis_insight enabled with compose files.
	path := writeLayeredFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	// sampleDefaultsYML doesn't have compose.base, only tool compose files (sorted).
	want := []string{
		"compose/tools/mailpit.yml",
		"compose/tools/redis_insight.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_empty(t *testing.T) {
	// No compose section at all, and no tools with compose files.
	// Use minimalDefaultsYML/minimalToolsYML which have no compose section and no tool compose files.
	path := writeFullFixture(t, sampleWorkspaceYML, minimalDefaultsYML, "", "", minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	if len(allFiles) != 0 {
		t.Errorf("ComposeFilesAll should be empty when no compose section and no tool composes, got %v", allFiles)
	}
}

func TestComposeFilesAll_serviceMultipleComposeFiles(t *testing.T) {
	// Service with multiple compose files. They should all be included, in order.
	// Use minimalDefaultsYML to focus on service compose files, not tool ones.
	serviceYML := `
services:
  multi:
    type: app
    container: multi
    required: true
    dir: ./services/multi
    dir_internal: /workspace
    work_dir_internal: /workspace/src
    compose:
      - compose/services/multi/base.yml
      - compose/services/multi/debug.yml
      - compose/services/multi/test.yml
`
	defaultsWithCompose := minimalDefaultsYML + `
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithCompose, "", serviceYML, minimalToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	want := []string{
		"compose.yaml",
		"compose/services/multi/base.yml",
		"compose/services/multi/debug.yml",
		"compose/services/multi/test.yml",
	}
	if len(allFiles) != len(want) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(want), allFiles)
	}
	for i, w := range want {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}
}

func TestComposeFilesAll_unknownToolWithCompose(t *testing.T) {
	// An unknown/new tool (elasticvue) with compose file should be included
	// when enabled. This confirms the data-driven path works for any tool key.
	customTools := `
services:
  elasticvue:
    type: tool
    container: elasticvue
    compose:
      - compose/tools/elasticvue.yml
  adminer:
    type: tool
    container: adminer
`
	defaultsWithElasticvue := `
schema_version: "1"
services:
  elasticvue:
    enabled: true
  adminer:
    enabled: false
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsWithElasticvue, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	allFiles := cfg.ComposeFilesAll()
	activeFiles := cfg.ComposeFiles()

	// ComposeFilesAll should include all tools (adminer disabled, but still listed).
	// ComposeFilesAll includes all tools with compose files (enabled or disabled).
	// adminer has no compose field, so it's not included.
	wantAll := []string{
		"compose.yaml",
		"compose/tools/elasticvue.yml", // enabled, included
	}

	if len(allFiles) != len(wantAll) {
		t.Fatalf("ComposeFilesAll len = %d, want %d: %v", len(allFiles), len(wantAll), allFiles)
	}
	for i, w := range wantAll {
		if allFiles[i] != w {
			t.Errorf("ComposeFilesAll[%d] = %q, want %q", i, allFiles[i], w)
		}
	}

	// ComposeFiles only includes enabled tools with compose files.
	wantActive := []string{
		"compose.yaml",
		"compose/tools/elasticvue.yml", // enabled, included
	}
	if len(activeFiles) != len(wantActive) {
		t.Fatalf("ComposeFiles len = %d, want %d: %v", len(activeFiles), len(wantActive), activeFiles)
	}
	for i, w := range wantActive {
		if activeFiles[i] != w {
			t.Errorf("ComposeFiles[%d] = %q, want %q", i, activeFiles[i], w)
		}
	}
}

func TestComposeFilesAll_determinismSortedOrder(t *testing.T) {
	// Verify that ComposeFiles returns tools in sorted key order, not declaration order.
	// Build a config with 3+ tools whose keys sort differently from declaration order.
	// Run 100 times to catch any non-determinism due to Go's randomized map iteration.
	customTools := `
services:
  zebra:
    type: tool
    container: zebra
    compose:
      - compose/tools/zebra.yml
  apple:
    type: tool
    container: apple
    compose:
      - compose/tools/apple.yml
  mango:
    type: tool
    container: mango
    compose:
      - compose/tools/mango.yml
`
	defaultsUnsorted := `
schema_version: "1"
services:
  zebra:
    enabled: true
  apple:
    enabled: true
  mango:
    enabled: true
runtime:
  use_https: false
  spx:
    path: ""
compose:
  base: compose.yaml
`
	path := writeFullFixture(t, sampleWorkspaceYML, defaultsUnsorted, "", "", customTools)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// Expected order: sorted by key (apple, mango, zebra), not declaration order (zebra, apple, mango).
	wantSorted := []string{
		"compose.yaml",
		"compose/tools/apple.yml",
		"compose/tools/mango.yml",
		"compose/tools/zebra.yml",
	}

	// Run ComposeFiles 100 times and verify order is always the same.
	for i := range 100 {
		got := cfg.ComposeFiles()
		if len(got) != len(wantSorted) {
			t.Fatalf("iteration %d: ComposeFiles len = %d, want %d: %v", i, len(got), len(wantSorted), got)
		}
		for j, w := range wantSorted {
			if got[j] != w {
				t.Errorf("iteration %d: ComposeFiles[%d] = %q, want %q", i, j, got[j], w)
			}
		}
	}
}

// --- Local compose overlays (compose.extra / LocalComposeExtra) ---

func TestComposeFiles_LocalOverlays(t *testing.T) {
	t.Parallel()

	type want struct {
		active []string
		all    []string
	}

	cases := []struct {
		name string
		cfg  *DweConfig
		want want
	}{
		{
			name: "per-service overlay present when enabled",
			cfg: &DweConfig{
				Compose: ComposeConfig{Base: "compose.yaml"},
				Services: map[string]ServiceConfig{
					"api": {
						Type:              ServiceTypeApp,
						Enabled:           true,
						Compose:           []string{"compose/api.yml"},
						LocalComposeExtra: []string{"compose/api.local.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml", "compose/api.yml", "compose/api.local.yml"},
				all:    []string{"compose.yaml", "compose/api.yml", "compose/api.local.yml"},
			},
		},
		{
			name: "per-service overlay omitted when disabled (active) but included in all",
			cfg: &DweConfig{
				Compose: ComposeConfig{Base: "compose.yaml"},
				Services: map[string]ServiceConfig{
					"api": {
						Type:              ServiceTypeApp,
						Enabled:           false,
						Compose:           []string{"compose/api.yml"},
						LocalComposeExtra: []string{"compose/api.local.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml"},
				all:    []string{"compose.yaml", "compose/api.yml", "compose/api.local.yml"},
			},
		},
		{
			name: "project-wide overlay always last",
			cfg: &DweConfig{
				Compose: ComposeConfig{
					Base:  "compose.yaml",
					Extra: []string{"compose.local.yml"},
				},
				Services: map[string]ServiceConfig{
					"api": {
						Type:    ServiceTypeApp,
						Enabled: true,
						Compose: []string{"compose/api.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml", "compose/api.yml", "compose.local.yml"},
				all:    []string{"compose.yaml", "compose/api.yml", "compose.local.yml"},
			},
		},
		{
			name: "deterministic order across groups (tools, infra, apps)",
			cfg: &DweConfig{
				Compose: ComposeConfig{
					Base:  "compose.yaml",
					Extra: []string{"compose.local.yml"},
				},
				Services: map[string]ServiceConfig{
					"zebra-app": {
						Type:              ServiceTypeApp,
						Enabled:           true,
						Compose:           []string{"compose/zebra-app.yml"},
						LocalComposeExtra: []string{"compose/zebra-app.local.yml"},
					},
					"apple-app": {
						Type:              ServiceTypeApp,
						Enabled:           true,
						Compose:           []string{"compose/apple-app.yml"},
						LocalComposeExtra: []string{"compose/apple-app.local.yml"},
					},
					"mango-tool": {
						Type:              ServiceTypeTool,
						Enabled:           true,
						Compose:           []string{"compose/mango-tool.yml"},
						LocalComposeExtra: []string{"compose/mango-tool.local.yml"},
					},
					"apple-tool": {
						Type:              ServiceTypeTool,
						Enabled:           true,
						Compose:           []string{"compose/apple-tool.yml"},
						LocalComposeExtra: []string{"compose/apple-tool.local.yml"},
					},
					"db-infra": {
						Type:              ServiceTypeInfra,
						Enabled:           true,
						Compose:           []string{"compose/db-infra.yml"},
						LocalComposeExtra: []string{"compose/db-infra.local.yml"},
					},
				},
			},
			want: want{
				active: []string{
					"compose.yaml",
					"compose/apple-tool.yml", "compose/apple-tool.local.yml",
					"compose/mango-tool.yml", "compose/mango-tool.local.yml",
					"compose/db-infra.yml", "compose/db-infra.local.yml",
					"compose/apple-app.yml", "compose/apple-app.local.yml",
					"compose/zebra-app.yml", "compose/zebra-app.local.yml",
					"compose.local.yml",
				},
				all: []string{
					"compose.yaml",
					"compose/apple-tool.yml", "compose/apple-tool.local.yml",
					"compose/mango-tool.yml", "compose/mango-tool.local.yml",
					"compose/db-infra.yml", "compose/db-infra.local.yml",
					"compose/apple-app.yml", "compose/apple-app.local.yml",
					"compose/zebra-app.yml", "compose/zebra-app.local.yml",
					"compose.local.yml",
				},
			},
		},
		{
			name: "overlay-only service (no svc.Compose) still emits overlay when enabled",
			cfg: &DweConfig{
				Compose: ComposeConfig{Base: "compose.yaml"},
				Services: map[string]ServiceConfig{
					"api": {
						Type:              ServiceTypeApp,
						Enabled:           true,
						LocalComposeExtra: []string{"compose/api.local.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml", "compose/api.local.yml"},
				all:    []string{"compose.yaml", "compose/api.local.yml"},
			},
		},
		{
			name: "overlay-only service omitted when disabled (active)",
			cfg: &DweConfig{
				Compose: ComposeConfig{Base: "compose.yaml"},
				Services: map[string]ServiceConfig{
					"api": {
						Type:              ServiceTypeApp,
						Enabled:           false,
						LocalComposeExtra: []string{"compose/api.local.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml"},
				all:    []string{"compose.yaml", "compose/api.local.yml"},
			},
		},
		{
			name: "no local overlays anywhere → backward-compatible output",
			cfg: &DweConfig{
				Compose: ComposeConfig{Base: "compose.yaml"},
				Services: map[string]ServiceConfig{
					"api": {
						Type:    ServiceTypeApp,
						Enabled: true,
						Compose: []string{"compose/api.yml"},
					},
				},
			},
			want: want{
				active: []string{"compose.yaml", "compose/api.yml"},
				all:    []string{"compose.yaml", "compose/api.yml"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			active := tc.cfg.ComposeFiles()
			if !slicesEqual(active, tc.want.active) {
				t.Errorf("ComposeFiles() = %v, want %v", active, tc.want.active)
			}
			all := tc.cfg.ComposeFilesAll()
			if !slicesEqual(all, tc.want.all) {
				t.Errorf("ComposeFilesAll() = %v, want %v", all, tc.want.all)
			}
		})
	}
}

func TestComposeFiles_LocalOverlays_GoldenFullPipeline(t *testing.T) {
	t.Parallel()

	// Pins the FULL expected -f list for a mixed-group scenario. Any future
	// regression in iteration order or overlay placement will trip this test.
	cfg := &DweConfig{
		Compose: ComposeConfig{
			Base:  "compose.yaml",
			Extra: []string{"compose.local.yml", "compose.local.2.yml"},
		},
		Services: map[string]ServiceConfig{
			"adminer": {
				Type:              ServiceTypeTool,
				Enabled:           true,
				Compose:           []string{"compose/tools/adminer.yml"},
				LocalComposeExtra: []string{"compose/tools/adminer.local.yml"},
			},
			"mailhog": {
				Type:    ServiceTypeTool,
				Enabled: true,
				Compose: []string{"compose/tools/mailhog.yml"},
			},
			"postgres": {
				Type:              ServiceTypeInfra,
				Enabled:           true,
				Compose:           []string{"compose/infra/postgres.yml"},
				LocalComposeExtra: []string{"compose/infra/postgres.local.yml"},
			},
			"redis": {
				Type:    ServiceTypeInfra,
				Enabled: false, // disabled — excluded in active
				Compose: []string{"compose/infra/redis.yml"},
			},
			"api": {
				Type:              ServiceTypeApp,
				Enabled:           true,
				Compose:           []string{"compose/apps/api.yml"},
				LocalComposeExtra: []string{"compose/apps/api.local.yml"},
			},
			"web": {
				Type:    ServiceTypeApp,
				Enabled: true,
				Compose: []string{"compose/apps/web.yml"},
			},
		},
	}

	wantActive := []string{
		"compose.yaml",
		"compose/tools/adminer.yml", "compose/tools/adminer.local.yml",
		"compose/tools/mailhog.yml",
		"compose/infra/postgres.yml", "compose/infra/postgres.local.yml",
		"compose/apps/api.yml", "compose/apps/api.local.yml",
		"compose/apps/web.yml",
		"compose.local.yml", "compose.local.2.yml",
	}
	if got := cfg.ComposeFiles(); !slicesEqual(got, wantActive) {
		t.Errorf("ComposeFiles() mismatch:\n got: %v\nwant: %v", got, wantActive)
	}

	wantAll := []string{
		"compose.yaml",
		"compose/tools/adminer.yml", "compose/tools/adminer.local.yml",
		"compose/tools/mailhog.yml",
		"compose/infra/postgres.yml", "compose/infra/postgres.local.yml",
		"compose/infra/redis.yml",
		"compose/apps/api.yml", "compose/apps/api.local.yml",
		"compose/apps/web.yml",
		"compose.local.yml", "compose.local.2.yml",
	}
	if got := cfg.ComposeFilesAll(); !slicesEqual(got, wantAll) {
		t.Errorf("ComposeFilesAll() mismatch:\n got: %v\nwant: %v", got, wantAll)
	}
}

// --- ServiceConfig.IDE field ---

// TestServiceConfig_IDERenderEnabledExplicit tests the tristate logic for IDE rendering.
func TestServiceConfig_IDERenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool // wantExp indicates whether the value is explicit
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.IDERenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("IDERenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("IDERenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_IDERenderEnabled tests the simple bool wrapper.
func TestServiceConfig_IDERenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{IDE: ServiceIDEConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default false",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.IDERenderEnabled(); got != tt.wantBool {
				t.Errorf("IDERenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_IDEEnabled tests IDE block inheritance.
func TestLoadServicesConfig_IDEEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    required: true
    dir: ./services/parent
    render:
      ide:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    required: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    required: false
    extends: parent
    render:
      ide:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    required: false
    extends: parent
    render:
      ide:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    required: false
    extends: parent
    render:
      ide:
        enabled: true
        template: both-tmpl
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.Render.IDE.Enabled == nil || *parent.Render.IDE.Enabled != false {
		t.Errorf("parent Render.IDE.Enabled should be false, got %v", parent.Render.IDE.Enabled)
	}
	if parent.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("parent Render.IDE.Template = %q, want parent-tmpl", parent.Render.IDE.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.Render.IDE.Enabled == nil || *childInh.Render.IDE.Enabled != false {
		t.Errorf("child-inherit Render.IDE.Enabled should inherit false from parent, got %v", childInh.Render.IDE.Enabled)
	}
	if childInh.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.IDE.Template should inherit parent-tmpl, got %q", childInh.Render.IDE.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.IDE.Enabled == nil || *childOvrE.Render.IDE.Enabled != true {
		t.Errorf("child-override-enabled Render.IDE.Enabled should be true, got %v", childOvrE.Render.IDE.Enabled)
	}
	if childOvrE.Render.IDE.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.IDE.Template should inherit parent-tmpl, got %q", childOvrE.Render.IDE.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.Render.IDE.Enabled == nil || *childOvrT.Render.IDE.Enabled != false {
		t.Errorf("child-override-template Render.IDE.Enabled should inherit false from parent, got %v", childOvrT.Render.IDE.Enabled)
	}
	if childOvrT.Render.IDE.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.IDE.Template = %q, want child-tmpl", childOvrT.Render.IDE.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.Render.IDE.Enabled == nil || *childOvrB.Render.IDE.Enabled != true {
		t.Errorf("child-override-both Render.IDE.Enabled should be true, got %v", childOvrB.Render.IDE.Enabled)
	}
	if childOvrB.Render.IDE.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.IDE.Template = %q, want both-tmpl", childOvrB.Render.IDE.Template)
	}
}

// TestServiceConfig_AIRenderEnabledExplicit tests the tristate logic for AI docs rendering.
func TestServiceConfig_AIRenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool // wantExp indicates whether the value is explicit
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.AIRenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("AIRenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("AIRenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_AIRenderEnabled tests the simple bool wrapper.
func TestServiceConfig_AIRenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{AI: ServiceAIConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default false",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.AIRenderEnabled(); got != tt.wantBool {
				t.Errorf("AIRenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_AIEnabled tests AI docs block inheritance.
func TestLoadServicesConfig_AIEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    required: true
    dir: ./services/parent
    render:
      ai:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    required: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    required: false
    extends: parent
    render:
      ai:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    required: false
    extends: parent
    render:
      ai:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    required: false
    extends: parent
    render:
      ai:
        enabled: true
        template: both-tmpl
  grandchild-multi-hop:
    type: app
    container: grandchild
    required: false
    extends: child-inherit
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	// Parent has explicit false and template
	parent := services["parent"]
	if parent.Render.AI.Enabled == nil || *parent.Render.AI.Enabled != false {
		t.Errorf("parent Render.AI.Enabled should be false, got %v", parent.Render.AI.Enabled)
	}
	if parent.Render.AI.Template != "parent-tmpl" {
		t.Errorf("parent Render.AI.Template = %q, want parent-tmpl", parent.Render.AI.Template)
	}

	// Child inherits both parent's enabled and template
	childInh := services["child-inherit"]
	if childInh.Render.AI.Enabled == nil || *childInh.Render.AI.Enabled != false {
		t.Errorf("child-inherit Render.AI.Enabled should inherit false from parent, got %v", childInh.Render.AI.Enabled)
	}
	if childInh.Render.AI.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.AI.Template should inherit parent-tmpl, got %q", childInh.Render.AI.Template)
	}

	// Child overrides enabled but inherits template
	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.AI.Enabled == nil || *childOvrE.Render.AI.Enabled != true {
		t.Errorf("child-override-enabled Render.AI.Enabled should be true, got %v", childOvrE.Render.AI.Enabled)
	}
	if childOvrE.Render.AI.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.AI.Template should inherit parent-tmpl, got %q", childOvrE.Render.AI.Template)
	}

	// Child overrides template but inherits enabled
	childOvrT := services["child-override-template"]
	if childOvrT.Render.AI.Enabled == nil || *childOvrT.Render.AI.Enabled != false {
		t.Errorf("child-override-template Render.AI.Enabled should inherit false from parent, got %v", childOvrT.Render.AI.Enabled)
	}
	if childOvrT.Render.AI.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.AI.Template = %q, want child-tmpl", childOvrT.Render.AI.Template)
	}

	// Child overrides both
	childOvrB := services["child-override-both"]
	if childOvrB.Render.AI.Enabled == nil || *childOvrB.Render.AI.Enabled != true {
		t.Errorf("child-override-both Render.AI.Enabled should be true, got %v", childOvrB.Render.AI.Enabled)
	}
	if childOvrB.Render.AI.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.AI.Template = %q, want both-tmpl", childOvrB.Render.AI.Template)
	}

	// Grandchild (multi-hop): inherits from child-inherit
	grandchild := services["grandchild-multi-hop"]
	if grandchild.Render.AI.Enabled == nil || *grandchild.Render.AI.Enabled != false {
		t.Errorf("grandchild-multi-hop Render.AI.Enabled should inherit false from parent chain, got %v", grandchild.Render.AI.Enabled)
	}
	if grandchild.Render.AI.Template != "parent-tmpl" {
		t.Errorf("grandchild-multi-hop Render.AI.Template should inherit parent-tmpl, got %q", grandchild.Render.AI.Template)
	}
}

// TestServiceConfig_GitRenderEnabledExplicit tests the tristate logic for git-hooks rendering.
func TestServiceConfig_GitRenderEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "omitted on app type",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
			wantExp:  false,
		},
		{
			name:     "omitted on db type",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.GitRenderEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("GitRenderEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("GitRenderEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_GitRenderEnabled tests the simple bool wrapper.
func TestServiceConfig_GitRenderEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(true)}}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Render: ServiceRenderConfig{Git: ServiceGitHooksConfig{Enabled: ptr(false)}}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default true",
			svc:      ServiceConfig{Type: "app"},
			wantBool: true,
		},
		{
			name:     "db default false",
			svc:      ServiceConfig{Type: "db"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.GitRenderEnabled(); got != tt.wantBool {
				t.Errorf("GitRenderEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestLoadServicesConfig_GitEnabled tests git-hooks block inheritance through extends.
func TestLoadServicesConfig_GitEnabled(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    required: true
    dir: ./services/parent
    render:
      git:
        enabled: false
        template: parent-tmpl
  child-inherit:
    type: app
    container: child-inherit
    required: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    required: false
    extends: parent
    render:
      git:
        enabled: true
  child-override-template:
    type: app
    container: child-override-template
    required: false
    extends: parent
    render:
      git:
        template: child-tmpl
  child-override-both:
    type: app
    container: child-override-both
    required: false
    extends: parent
    render:
      git:
        enabled: true
        template: both-tmpl
  grandchild-multi-hop:
    type: app
    container: grandchild
    required: false
    extends: child-inherit
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	parent := services["parent"]
	if parent.Render.Git.Enabled == nil || *parent.Render.Git.Enabled != false {
		t.Errorf("parent Render.Git.Enabled should be false, got %v", parent.Render.Git.Enabled)
	}
	if parent.Render.Git.Template != "parent-tmpl" {
		t.Errorf("parent Render.Git.Template = %q, want parent-tmpl", parent.Render.Git.Template)
	}

	childInh := services["child-inherit"]
	if childInh.Render.Git.Enabled == nil || *childInh.Render.Git.Enabled != false {
		t.Errorf("child-inherit Render.Git.Enabled should inherit false from parent, got %v", childInh.Render.Git.Enabled)
	}
	if childInh.Render.Git.Template != "parent-tmpl" {
		t.Errorf("child-inherit Render.Git.Template should inherit parent-tmpl, got %q", childInh.Render.Git.Template)
	}

	childOvrE := services["child-override-enabled"]
	if childOvrE.Render.Git.Enabled == nil || *childOvrE.Render.Git.Enabled != true {
		t.Errorf("child-override-enabled Render.Git.Enabled should be true, got %v", childOvrE.Render.Git.Enabled)
	}
	if childOvrE.Render.Git.Template != "parent-tmpl" {
		t.Errorf("child-override-enabled Render.Git.Template should inherit parent-tmpl, got %q", childOvrE.Render.Git.Template)
	}

	childOvrT := services["child-override-template"]
	if childOvrT.Render.Git.Enabled == nil || *childOvrT.Render.Git.Enabled != false {
		t.Errorf("child-override-template Render.Git.Enabled should inherit false from parent, got %v", childOvrT.Render.Git.Enabled)
	}
	if childOvrT.Render.Git.Template != "child-tmpl" {
		t.Errorf("child-override-template Render.Git.Template = %q, want child-tmpl", childOvrT.Render.Git.Template)
	}

	childOvrB := services["child-override-both"]
	if childOvrB.Render.Git.Enabled == nil || *childOvrB.Render.Git.Enabled != true {
		t.Errorf("child-override-both Render.Git.Enabled should be true, got %v", childOvrB.Render.Git.Enabled)
	}
	if childOvrB.Render.Git.Template != "both-tmpl" {
		t.Errorf("child-override-both Render.Git.Template = %q, want both-tmpl", childOvrB.Render.Git.Template)
	}

	grandchild := services["grandchild-multi-hop"]
	if grandchild.Render.Git.Enabled == nil || *grandchild.Render.Git.Enabled != false {
		t.Errorf("grandchild-multi-hop Render.Git.Enabled should inherit false from parent chain, got %v", grandchild.Render.Git.Enabled)
	}
	if grandchild.Render.Git.Template != "parent-tmpl" {
		t.Errorf("grandchild-multi-hop Render.Git.Template should inherit parent-tmpl, got %q", grandchild.Render.Git.Template)
	}
}

// TestLoadConfig_GitNotInjectedIntoRaw confirms git.* is not injected into raw["services"]
// for dot-path expressions (parity with ide/ai).
func TestLoadConfig_GitNotInjectedIntoRaw(t *testing.T) {
	svcYml := `services:
  main:
    type: app
    container: main
    required: true
    dir: ./services/main
    render:
      git:
        enabled: true
        template: custom
`
	path := writeFullFixture(t, sampleWorkspaceYML, sampleDefaultsYML, "", svcYml, sampleToolsYML)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Services["main"].Render.Git.Template != "custom" {
		t.Fatalf("expected Render.Git.Template to be loaded, got %q", cfg.Services["main"].Render.Git.Template)
	}
	svcMap, ok := cfg.Raw["services"].(map[string]any)
	if !ok {
		t.Fatalf("raw services not a map: %T", cfg.Raw["services"])
	}
	main, ok := svcMap["main"].(map[string]any)
	if !ok {
		t.Fatalf("raw services.main not a map: %T", svcMap["main"])
	}
	if _, present := main["git"]; present {
		t.Errorf("git.* should NOT be injected into raw[services][main]; got: %v", main["git"])
	}
}

// TestServiceConfig_BridgeEnabledExplicit tests the tristate logic for the host bridge.
func TestServiceConfig_BridgeEnabledExplicit(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
		wantExp  bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Bridge: ServiceBridgeConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "explicit false on app type",
			svc:      ServiceConfig{Type: "app", Bridge: ServiceBridgeConfig{Enabled: ptr(false)}}, //nolint:modernize
			wantBool: false,
			wantExp:  true,
		},
		{
			name:     "explicit true on infra type",
			svc:      ServiceConfig{Type: "infra", Bridge: ServiceBridgeConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
			wantExp:  true,
		},
		{
			name:     "omitted on app type defaults off (strict opt-in)",
			svc:      ServiceConfig{Type: "app"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on infra type",
			svc:      ServiceConfig{Type: "infra"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on tool type",
			svc:      ServiceConfig{Type: "tool"},
			wantBool: false,
			wantExp:  false,
		},
		{
			name:     "omitted on empty type",
			svc:      ServiceConfig{Type: ""},
			wantBool: false,
			wantExp:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotExp := tt.svc.BridgeEnabledExplicit()
			if got != tt.wantBool {
				t.Errorf("BridgeEnabledExplicit() bool = %v, want %v", got, tt.wantBool)
			}
			if gotExp != tt.wantExp {
				t.Errorf("BridgeEnabledExplicit() explicit = %v, want %v", gotExp, tt.wantExp)
			}
		})
	}
}

// TestServiceConfig_BridgeEnabled tests the simple bool wrapper.
func TestServiceConfig_BridgeEnabled(t *testing.T) {
	tests := []struct {
		name     string
		svc      ServiceConfig
		wantBool bool
	}{
		{
			name:     "explicit true",
			svc:      ServiceConfig{Bridge: ServiceBridgeConfig{Enabled: ptr(true)}}, //nolint:modernize
			wantBool: true,
		},
		{
			name:     "explicit false",
			svc:      ServiceConfig{Type: "app", Bridge: ServiceBridgeConfig{Enabled: ptr(false)}}, //nolint:modernize
			wantBool: false,
		},
		{
			name:     "app default false (strict opt-in)",
			svc:      ServiceConfig{Type: "app"},
			wantBool: false,
		},
		{
			name:     "infra default false",
			svc:      ServiceConfig{Type: "infra"},
			wantBool: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.BridgeEnabled(); got != tt.wantBool {
				t.Errorf("BridgeEnabled() = %v, want %v", got, tt.wantBool)
			}
		})
	}
}

// TestServiceConfig_BridgeShimPath tests the shim mount path default and override.
func TestServiceConfig_BridgeShimPath(t *testing.T) {
	tests := []struct {
		name string
		svc  ServiceConfig
		want string
	}{
		{
			name: "default when unset",
			svc:  ServiceConfig{Type: "app"},
			want: DefaultBridgeShimPath,
		},
		{
			name: "explicit override",
			svc:  ServiceConfig{Type: "app", Bridge: ServiceBridgeConfig{ShimPath: "/opt/bin/dwe"}},
			want: "/opt/bin/dwe",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.BridgeShimPath(); got != tt.want {
				t.Errorf("BridgeShimPath() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestServiceConfig_BridgeOnUnreachable tests the unreachable-daemon policy default and override.
func TestServiceConfig_BridgeOnUnreachable(t *testing.T) {
	tests := []struct {
		name string
		svc  ServiceConfig
		want string
	}{
		{
			name: "default fail when unset",
			svc:  ServiceConfig{Type: "app"},
			want: BridgeOnUnreachableFail,
		},
		{
			name: "explicit warn",
			svc:  ServiceConfig{Type: "app", Bridge: ServiceBridgeConfig{OnUnreachable: BridgeOnUnreachableWarn}},
			want: BridgeOnUnreachableWarn,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.svc.BridgeOnUnreachable(); got != tt.want {
				t.Errorf("BridgeOnUnreachable() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestLoadServicesConfig_BridgeExtends tests bridge block inheritance through extends.
func TestLoadServicesConfig_BridgeExtends(t *testing.T) {
	yml := `
services:
  parent:
    type: app
    container: parent
    required: true
    dir: ./services/parent
    bridge:
      enabled: false
      shim_path: /opt/parent/dwe
      on_unreachable: warn
  child-inherit:
    type: app
    container: child-inherit
    required: false
    extends: parent
  child-override-enabled:
    type: app
    container: child-override-enabled
    required: false
    extends: parent
    bridge:
      enabled: true
  child-override-path:
    type: app
    container: child-override-path
    required: false
    extends: parent
    bridge:
      shim_path: /opt/child/dwe
  child-override-unreachable:
    type: app
    container: child-override-unreachable
    required: false
    extends: parent
    bridge:
      on_unreachable: fail
  grandchild-multi-hop:
    type: app
    container: grandchild
    required: false
    extends: child-inherit
  unset-parent:
    type: app
    container: unset-parent
    required: true
    dir: ./services/unset-parent
  unset-child:
    type: app
    container: unset-child
    required: false
    extends: unset-parent
`
	dir := t.TempDir()
	writeServicesDir(t, dir, yml)
	services, err := LoadServices(dir)
	if err != nil {
		t.Fatalf("LoadServices: %v", err)
	}

	parent := services["parent"]
	if parent.Bridge.Enabled == nil || *parent.Bridge.Enabled != false {
		t.Errorf("parent Bridge.Enabled should be false, got %v", parent.Bridge.Enabled)
	}

	childInh := services["child-inherit"]
	if childInh.Bridge.Enabled == nil || *childInh.Bridge.Enabled != false {
		t.Errorf("child-inherit Bridge.Enabled should inherit false from parent, got %v", childInh.Bridge.Enabled)
	}
	if childInh.Bridge.ShimPath != "/opt/parent/dwe" {
		t.Errorf("child-inherit Bridge.ShimPath should inherit /opt/parent/dwe, got %q", childInh.Bridge.ShimPath)
	}
	if childInh.Bridge.OnUnreachable != "warn" {
		t.Errorf("child-inherit Bridge.OnUnreachable should inherit warn, got %q", childInh.Bridge.OnUnreachable)
	}

	childOvrE := services["child-override-enabled"]
	if childOvrE.Bridge.Enabled == nil || *childOvrE.Bridge.Enabled != true {
		t.Errorf("child-override-enabled Bridge.Enabled should be true, got %v", childOvrE.Bridge.Enabled)
	}
	if childOvrE.Bridge.ShimPath != "/opt/parent/dwe" {
		t.Errorf("child-override-enabled Bridge.ShimPath should inherit /opt/parent/dwe, got %q", childOvrE.Bridge.ShimPath)
	}

	childOvrP := services["child-override-path"]
	if childOvrP.Bridge.Enabled == nil || *childOvrP.Bridge.Enabled != false {
		t.Errorf("child-override-path Bridge.Enabled should inherit false from parent, got %v", childOvrP.Bridge.Enabled)
	}
	if childOvrP.Bridge.ShimPath != "/opt/child/dwe" {
		t.Errorf("child-override-path Bridge.ShimPath = %q, want /opt/child/dwe", childOvrP.Bridge.ShimPath)
	}

	childOvrU := services["child-override-unreachable"]
	if childOvrU.Bridge.OnUnreachable != "fail" {
		t.Errorf("child-override-unreachable Bridge.OnUnreachable = %q, want explicit fail over parent's warn", childOvrU.Bridge.OnUnreachable)
	}
	if childOvrU.Bridge.ShimPath != "/opt/parent/dwe" {
		t.Errorf("child-override-unreachable Bridge.ShimPath should inherit /opt/parent/dwe, got %q", childOvrU.Bridge.ShimPath)
	}

	grandchild := services["grandchild-multi-hop"]
	if grandchild.Bridge.Enabled == nil || *grandchild.Bridge.Enabled != false {
		t.Errorf("grandchild-multi-hop Bridge.Enabled should inherit false from parent chain, got %v", grandchild.Bridge.Enabled)
	}
	if grandchild.Bridge.OnUnreachable != "warn" {
		t.Errorf("grandchild-multi-hop Bridge.OnUnreachable should inherit warn, got %q", grandchild.Bridge.OnUnreachable)
	}

	// Neither side set bridge: tristate stays nil (the strict opt-in default
	// — off for every type — applies via the accessor).
	unsetChild := services["unset-child"]
	if unsetChild.Bridge.Enabled != nil {
		t.Errorf("unset-child Bridge.Enabled should stay nil, got %v", *unsetChild.Bridge.Enabled)
	}
	if unsetChild.BridgeEnabled() {
		t.Error("unset-child BridgeEnabled() should default false (bridge is strictly opt-in)")
	}
	if unsetChild.Bridge.ShimPath != "" || unsetChild.BridgeShimPath() != DefaultBridgeShimPath {
		t.Errorf("unset-child shim path: raw %q, resolved %q", unsetChild.Bridge.ShimPath, unsetChild.BridgeShimPath())
	}
}

// ptr is a helper to create a pointer to a value.
// nolint: unused,modernize // used in test table initialization
func ptr[T any](v T) *T {
	return &v
}

// TestDeployStep_UnmarshalYAML covers the custom unmarshaller's allow-list
// and the parallel-vs-leaf mutual exclusion rules.
func TestDeployStep_UnmarshalYAML(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantErr   bool
		errSubstr string
	}{
		{
			name: "leaf step parses",
			yaml: `
name: hello
type: shell
cmd: echo hi
`,
		},
		{
			name: "leaf step with timeout parses",
			yaml: `
name: hello
type: shell
cmd: echo hi
timeout: 90s
`,
		},
		{
			name: "pure parallel parses",
			yaml: `
name: group
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
    - {name: b, type: shell, cmd: echo b}
`,
		},
		{
			name: "parallel + type rejected",
			yaml: `
name: g
type: shell
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "type",
		},
		{
			name: "parallel + cmd rejected",
			yaml: `
name: g
cmd: x
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "cmd",
		},
		{
			name: "parallel + with rejected",
			yaml: `
name: g
with: {x: 1}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "with",
		},
		{
			name: "parallel + check rejected",
			yaml: `
name: g
check: {type: shell, cmd: echo ok}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "check",
		},
		{
			name: "parallel + files_gate rejected",
			yaml: `
name: g
files_gate:
  files: [foo]
  required:
    state: readable
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "files_gate",
		},
		{
			name: "parallel + continue_on_error rejected",
			yaml: `
name: g
continue_on_error: true
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "continue_on_error",
		},
		{
			name: "parallel + timeout rejected",
			yaml: `
name: g
timeout: 90s
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "timeout",
		},
		{
			name: "parallel + when accepted",
			yaml: `
name: g
when: {type: shell, cmd: "true"}
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "parallel + skip_confirm accepted",
			yaml: `
name: g
skip_confirm: true
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "parallel + description + name accepted",
			yaml: `
name: g
description: hello
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
		},
		{
			name: "empty parallel.steps parses at loader (length checked later)",
			yaml: `
name: g
parallel:
  steps: []
`,
		},
		{
			name: "single-element parallel.steps parses at loader",
			yaml: `
name: g
parallel:
  steps:
    - {name: only, type: shell, cmd: echo a}
`,
		},
		{
			name: "unknown field on DeployStep rejected",
			yaml: `
name: g
typo_field: hello
type: shell
cmd: echo a
`,
			wantErr:   true,
			errSubstr: "typo_field",
		},
		{
			name: "unknown field on ParallelGroup rejected",
			yaml: `
name: g
parallel:
  max_concurent: 2
  steps:
    - {name: a, type: shell, cmd: echo a}
`,
			wantErr:   true,
			errSubstr: "max_concurent",
		},
		{
			name: "nested parallel parses (validated at plan time)",
			yaml: `
name: outer
parallel:
  steps:
    - name: inner
      parallel:
        steps:
          - {name: a, type: shell, cmd: echo a}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var step DeployStep
			err := yamlUnmarshalForTest(tt.yaml, &step)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errSubstr)
				}
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestDeployStep_TimeoutRoundTrip verifies the raw timeout string decodes
// unchanged and defaults to empty when absent.
func TestDeployStep_TimeoutRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{name: "absent", yaml: `
name: hello
type: shell
cmd: echo hi
`, want: ""},
		{name: "present", yaml: `
name: hello
type: shell
cmd: echo hi
timeout: 90s
`, want: "90s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var step DeployStep
			if err := yamlUnmarshalForTest(tt.yaml, &step); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if step.Timeout != tt.want {
				t.Fatalf("step.Timeout = %q, want %q", step.Timeout, tt.want)
			}
		})
	}
}

// TestParallelGroup_FailFastTristate verifies the *bool round-trips nil/true/false.
func TestParallelGroup_FailFastTristate(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want *bool
	}{
		{name: "absent", yaml: `
name: g
parallel:
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: nil},
		{name: "true", yaml: `
name: g
parallel:
  fail_fast: true
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: new(true)},
		{name: "false", yaml: `
name: g
parallel:
  fail_fast: false
  steps:
    - {name: a, type: shell, cmd: echo a}
`, want: new(false)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var step DeployStep
			if err := yamlUnmarshalForTest(tt.yaml, &step); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if step.Parallel == nil {
				t.Fatalf("expected non-nil Parallel")
			}
			got := step.Parallel.FailFast
			switch {
			case tt.want == nil && got != nil:
				t.Fatalf("expected nil, got %v", *got)
			case tt.want != nil && got == nil:
				t.Fatalf("expected %v, got nil", *tt.want)
			case tt.want != nil && got != nil && *tt.want != *got:
				t.Fatalf("expected %v, got %v", *tt.want, *got)
			}
		})
	}
}

func yamlUnmarshalForTest(src string, out any) error {
	return yaml.Unmarshal([]byte(src), out)
}

// TestAllowedRootKeysSubsetOfKnownVarHeads pins the cross-package contract
// between the strict-root allowlist here and tpl.KnownVarHeads: every key a
// project config layer may legally declare at its root must also be
// resolvable through ${...} template syntax. tpl is a leaf and must not
// import this package, so the two lists are independently maintained; this
// test is the only thing standing between a new root key and templates that
// silently stop rendering it.
func TestAllowedRootKeysSubsetOfKnownVarHeads(t *testing.T) {
	known := make(map[string]struct{}, len(tpl.KnownVarHeads))
	for _, h := range tpl.KnownVarHeads {
		known[h] = struct{}{}
	}
	for _, k := range allowedRootKeys {
		if _, ok := known[k]; !ok {
			t.Errorf("allowedRootKeys contains %q, which is missing from tpl.KnownVarHeads", k)
		}
	}
}

// Tests below this point were heavily tied to the legacy tools.yml /
// runtime.ports / runtime.hosts shape. They are kept as no-op stubs here so
// the file's structure remains unchanged; the new behaviour is exercised by
// the dedicated services-overlay/injection tests added in services_overlay_test.go.
var _ = sampleToolsServicesYML

// Legacy tools.yml shape removed in the unified-services-schema refactor.
// New behaviour is exercised by services_overlay_test.go.
