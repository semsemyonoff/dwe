package commands

import (
	"testing"

	"devbox-cli/internal/config"
)

// --- helpers -----------------------------------------------------------------

func makeConfig(raw map[string]any) *config.DevboxConfig {
	return &config.DevboxConfig{Raw: raw}
}

// --- ResolveParams -----------------------------------------------------------

func TestResolveParams_ProvidedValue(t *testing.T) {
	defs := map[string]ParamDef{
		"name": {Type: ParamTypeString},
	}
	got, err := ResolveParams(defs, map[string]string{"name": "world"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["name"] != "world" {
		t.Errorf("expected %q, got %q", "world", got["name"])
	}
}

func TestResolveParams_LiteralDefault(t *testing.T) {
	defs := map[string]ParamDef{
		"env": {Type: ParamTypeString, Default: "production"},
	}
	got, err := ResolveParams(defs, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["env"] != "production" {
		t.Errorf("expected %q, got %q", "production", got["env"])
	}
}

func TestResolveParams_DefaultFrom(t *testing.T) {
	cfg := makeConfig(map[string]any{
		"runtime": map[string]any{
			"ports": map[string]any{
				"app": 8080,
			},
		},
	})
	defs := map[string]ParamDef{
		"port": {Type: ParamTypeString, DefaultFrom: "runtime.ports.app"},
	}
	got, err := ResolveParams(defs, nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["port"] != "8080" {
		t.Errorf("expected %q, got %v", "8080", got["port"])
	}
}

func TestResolveParams_ProvidedOverridesDefault(t *testing.T) {
	defs := map[string]ParamDef{
		"env": {Type: ParamTypeString, Default: "production"},
	}
	got, err := ResolveParams(defs, map[string]string{"env": "staging"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["env"] != "staging" {
		t.Errorf("expected %q, got %q", "staging", got["env"])
	}
}

func TestResolveParams_RequiredMissing(t *testing.T) {
	defs := map[string]ParamDef{
		"token": {Type: ParamTypeString, Required: true},
	}
	_, err := ResolveParams(defs, nil, nil)
	if err == nil {
		t.Fatal("expected error for missing required param")
	}
}

func TestResolveParams_RequiredProvidedIsOK(t *testing.T) {
	defs := map[string]ParamDef{
		"token": {Type: ParamTypeString, Required: true},
	}
	got, err := ResolveParams(defs, map[string]string{"token": "abc"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["token"] != "abc" {
		t.Errorf("expected %q, got %v", "abc", got["token"])
	}
}

func TestResolveParams_TypeCoercionBool(t *testing.T) {
	defs := map[string]ParamDef{
		"flag": {Type: ParamTypeBool},
	}

	for _, tc := range []struct {
		raw  string
		want bool
	}{
		{"true", true},
		{"false", false},
		{"1", true},
		{"0", false},
	} {
		got, err := ResolveParams(defs, map[string]string{"flag": tc.raw}, nil)
		if err != nil {
			t.Fatalf("raw=%q unexpected error: %v", tc.raw, err)
		}
		if got["flag"] != tc.want {
			t.Errorf("raw=%q: expected %v, got %v", tc.raw, tc.want, got["flag"])
		}
	}
}

func TestResolveParams_TypeCoercionBoolInvalid(t *testing.T) {
	defs := map[string]ParamDef{
		"flag": {Type: ParamTypeBool},
	}
	_, err := ResolveParams(defs, map[string]string{"flag": "notabool"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid bool value")
	}
}

func TestResolveParams_TypeCoercionInt(t *testing.T) {
	defs := map[string]ParamDef{
		"count": {Type: ParamTypeInt},
	}
	got, err := ResolveParams(defs, map[string]string{"count": "42"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["count"] != 42 {
		t.Errorf("expected 42, got %v", got["count"])
	}
}

func TestResolveParams_TypeCoercionIntInvalid(t *testing.T) {
	defs := map[string]ParamDef{
		"count": {Type: ParamTypeInt},
	}
	_, err := ResolveParams(defs, map[string]string{"count": "nan"}, nil)
	if err == nil {
		t.Fatal("expected error for invalid int value")
	}
}

func TestResolveParams_TypeCoercionPath(t *testing.T) {
	defs := map[string]ParamDef{
		"dir": {Type: ParamTypePath},
	}
	got, err := ResolveParams(defs, map[string]string{"dir": "/tmp/foo"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["dir"] != "/tmp/foo" {
		t.Errorf("expected %q, got %v", "/tmp/foo", got["dir"])
	}
}

func TestResolveParams_EmptyDefsNoError(t *testing.T) {
	got, err := ResolveParams(nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestResolveParams_BoolZeroDefault(t *testing.T) {
	defs := map[string]ParamDef{
		"flag": {Type: ParamTypeBool},
	}
	got, err := ResolveParams(defs, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["flag"] != false {
		t.Errorf("expected false, got %v", got["flag"])
	}
}

func TestResolveParams_IntZeroDefault(t *testing.T) {
	defs := map[string]ParamDef{
		"count": {Type: ParamTypeInt},
	}
	got, err := ResolveParams(defs, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["count"] != 0 {
		t.Errorf("expected 0, got %v", got["count"])
	}
}

// --- ResolveContext ----------------------------------------------------------

func TestResolveContext_FromPath(t *testing.T) {
	cfg := makeConfig(map[string]any{
		"project": map[string]any{"name": "laravel"},
	})
	defs := map[string]ContextDef{
		"project_name": {From: "project.name"},
	}
	got, err := ResolveContext(defs, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["project_name"] != "laravel" {
		t.Errorf("expected %q, got %v", "laravel", got["project_name"])
	}
}

func TestResolveContext_MissingPathNotRequired(t *testing.T) {
	cfg := makeConfig(map[string]any{})
	defs := map[string]ContextDef{
		"thing": {From: "does.not.exist"},
	}
	got, err := ResolveContext(defs, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["thing"] != nil {
		t.Errorf("expected nil, got %v", got["thing"])
	}
}

func TestResolveContext_RequiredMissing(t *testing.T) {
	cfg := makeConfig(map[string]any{})
	defs := map[string]ContextDef{
		"container": {From: "services.main.container", Required: true},
	}
	_, err := ResolveContext(defs, cfg)
	if err == nil {
		t.Fatal("expected error for missing required context")
	}
}

func TestResolveContext_RequiredPresentIsOK(t *testing.T) {
	cfg := makeConfig(map[string]any{
		"services": map[string]any{
			"main": map[string]any{"container": "devbox-laravel-main"},
		},
	})
	defs := map[string]ContextDef{
		"container": {From: "services.main.container", Required: true},
	}
	got, err := ResolveContext(defs, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["container"] != "devbox-laravel-main" {
		t.Errorf("expected %q, got %v", "devbox-laravel-main", got["container"])
	}
}

func TestResolveContext_NilConfig(t *testing.T) {
	defs := map[string]ContextDef{
		"thing": {From: "some.path"},
	}
	got, err := ResolveContext(defs, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["thing"] != nil {
		t.Errorf("expected nil, got %v", got["thing"])
	}
}

func TestResolveContext_EmptyDefsNoError(t *testing.T) {
	got, err := ResolveContext(nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

// --- BuildEnv ----------------------------------------------------------------

func TestBuildEnv_ParamEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"db_name": {Env: "DB_DATABASE"},
		},
	}
	params := map[string]any{"db_name": "mydb"}
	env := BuildEnv(cmd, params, nil)
	if env["DB_DATABASE"] != "mydb" {
		t.Errorf("expected DB_DATABASE=mydb, got %v", env["DB_DATABASE"])
	}
}

func TestBuildEnv_ContextEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Context: map[string]ContextDef{
			"container": {From: "services.main.container", Env: "APP_CONTAINER"},
		},
	}
	ctx := map[string]any{"container": "devbox-laravel-main"}
	env := BuildEnv(cmd, nil, ctx)
	if env["APP_CONTAINER"] != "devbox-laravel-main" {
		t.Errorf("expected APP_CONTAINER=devbox-laravel-main, got %v", env["APP_CONTAINER"])
	}
}

func TestBuildEnv_CommandLevelEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Env:  map[string]string{"DEVBOX_ROOT": "${project.name}"},
	}
	env := BuildEnv(cmd, nil, nil)
	if env["DEVBOX_ROOT"] != "${project.name}" {
		t.Errorf("expected raw template string, got %v", env["DEVBOX_ROOT"])
	}
}

func TestBuildEnv_CommandLevelEnvOverridesParamEnv(t *testing.T) {
	// Command-level env wins over param env for the same key.
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"val": {Env: "MY_VAR"},
		},
		Env: map[string]string{"MY_VAR": "override"},
	}
	params := map[string]any{"val": "from-param"}
	env := BuildEnv(cmd, params, nil)
	if env["MY_VAR"] != "override" {
		t.Errorf("expected override, got %v", env["MY_VAR"])
	}
}

func TestBuildEnv_ParamWithoutEnvSkipped(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"hidden": {Type: ParamTypeString}, // no Env set
		},
	}
	params := map[string]any{"hidden": "value"}
	env := BuildEnv(cmd, params, nil)
	if _, ok := env["hidden"]; ok {
		t.Errorf("param without Env should not appear in env map")
	}
}

func TestBuildEnv_EmptyCommandNoError(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
	}
	env := BuildEnv(cmd, nil, nil)
	if len(env) != 0 {
		t.Errorf("expected empty env map, got %v", env)
	}
}

func TestBuildEnv_BoolParamInEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"debug": {Type: ParamTypeBool, Env: "DEBUG"},
		},
	}
	params := map[string]any{"debug": true}
	env := BuildEnv(cmd, params, nil)
	if env["DEBUG"] != "true" {
		t.Errorf("expected DEBUG=true, got %v", env["DEBUG"])
	}
}

func TestBuildEnv_IntParamInEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"workers": {Type: ParamTypeInt, Env: "WORKERS"},
		},
	}
	params := map[string]any{"workers": 4}
	env := BuildEnv(cmd, params, nil)
	if env["WORKERS"] != "4" {
		t.Errorf("expected WORKERS=4, got %v", env["WORKERS"])
	}
}
