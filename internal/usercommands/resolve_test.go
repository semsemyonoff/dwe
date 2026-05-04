package usercommands

import (
	"testing"

	"devbox-cli/internal/config"
	"devbox-cli/internal/tpl"
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

func TestResolveParams_DefaultFromBeatsLiteralDefault(t *testing.T) {
	// When both default and default_from are set and the config path resolves
	// to a non-empty value, default_from wins. The literal default acts as a
	// safety net only when the config path is missing or empty.
	cfg := makeConfig(map[string]any{
		"db": map[string]any{"database": "from_config"},
	})
	defs := map[string]ParamDef{
		"database": {
			Type:        ParamTypeString,
			Default:     "literal_fallback",
			DefaultFrom: "db.database",
		},
	}
	got, err := ResolveParams(defs, nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["database"] != "from_config" {
		t.Errorf("expected default_from to win, got %v", got["database"])
	}
}

func TestResolveParams_DefaultFromEmptyFallsBackToLiteralDefault(t *testing.T) {
	// When default_from resolves but to an empty string, fall back to the
	// literal default rather than treating empty as a deliberate value.
	cfg := makeConfig(map[string]any{
		"db": map[string]any{"database": ""},
	})
	defs := map[string]ParamDef{
		"database": {
			Type:        ParamTypeString,
			Default:     "literal_fallback",
			DefaultFrom: "db.database",
		},
	}
	got, err := ResolveParams(defs, nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["database"] != "literal_fallback" {
		t.Errorf("expected literal default fallback, got %v", got["database"])
	}
}

func TestResolveParams_DefaultFromMissingFallsBackToLiteralDefault(t *testing.T) {
	cfg := makeConfig(map[string]any{})
	defs := map[string]ParamDef{
		"database": {
			Type:        ParamTypeString,
			Default:     "literal_fallback",
			DefaultFrom: "db.database",
		},
	}
	got, err := ResolveParams(defs, nil, cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["database"] != "literal_fallback" {
		t.Errorf("expected literal default fallback, got %v", got["database"])
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
	env, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	env, err := BuildEnv(cmd, nil, ctx, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	env, err := BuildEnv(cmd, nil, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DEVBOX_ROOT"] != "${project.name}" {
		t.Errorf("expected raw template string, got %v", env["DEVBOX_ROOT"])
	}
}

func TestBuildEnv_CommandLevelEnvConflictWithParamEnv(t *testing.T) {
	// With the conflict guard, command-level env declaring the same name as param env is rejected.
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"val": {Env: "MY_VAR"},
		},
		Env: map[string]string{"MY_VAR": "override"},
	}
	params := map[string]any{"val": "from-param"}
	_, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err == nil {
		t.Fatalf("expected error for env conflict")
	}
	if !containsString(err.Error(), "MY_VAR") || !containsString(err.Error(), "conflict") {
		t.Errorf("error message should mention conflict and MY_VAR, got: %v", err)
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
	env, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := env["hidden"]; ok {
		t.Errorf("param without Env should not appear in env map")
	}
}

func TestBuildEnv_EmptyCommandNoError(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
	}
	env, err := BuildEnv(cmd, nil, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	env, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
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
	env, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["WORKERS"] != "4" {
		t.Errorf("expected WORKERS=4, got %v", env["WORKERS"])
	}
}

func TestBuildEnv_FileEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Files: map[string]FileSpec{
			"dump": {Env: "DUMP_FILE"},
		},
	}
	files := map[string]tpl.ResolvedFile{
		"dump": {Path: "/tmp/dump.sql.gz"},
	}
	env, err := BuildEnv(cmd, nil, nil, files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DUMP_FILE"] != "/tmp/dump.sql.gz" {
		t.Errorf("expected DUMP_FILE=/tmp/dump.sql.gz, got %v", env["DUMP_FILE"])
	}
}

func TestBuildEnv_EmptyFilesMap_Regression(t *testing.T) {
	// When no files are declared, BuildEnv should behave identically to before the change.
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"db_name": {Env: "DB_DATABASE"},
		},
		Env: map[string]string{"MY_VAR": "value"},
	}
	params := map[string]any{"db_name": "mydb"}
	env, err := BuildEnv(cmd, params, nil, map[string]tpl.ResolvedFile{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DB_DATABASE"] != "mydb" {
		t.Errorf("expected DB_DATABASE=mydb, got %v", env["DB_DATABASE"])
	}
	if env["MY_VAR"] != "value" {
		t.Errorf("expected MY_VAR=value, got %v", env["MY_VAR"])
	}
}

func TestBuildEnv_ConflictBetweenFileAndParam(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Params: map[string]ParamDef{
			"val": {Env: "MY_VAR"},
		},
		Files: map[string]FileSpec{
			"file1": {Env: "MY_VAR"},
		},
	}
	params := map[string]any{"val": "from-param"}
	files := map[string]tpl.ResolvedFile{
		"file1": {Path: "/tmp/file"},
	}
	_, err := BuildEnv(cmd, params, nil, files)
	if err == nil {
		t.Fatalf("expected error for env conflict")
	}
	if !containsString(err.Error(), "MY_VAR") || !containsString(err.Error(), "conflict") {
		t.Errorf("error message should mention conflict and MY_VAR, got: %v", err)
	}
}

func TestBuildEnv_ConflictBetweenFileAndCommandEnv(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Files: map[string]FileSpec{
			"file1": {Env: "MY_VAR"},
		},
		Env: map[string]string{"MY_VAR": "value"},
	}
	files := map[string]tpl.ResolvedFile{
		"file1": {Path: "/tmp/file"},
	}
	_, err := BuildEnv(cmd, nil, nil, files)
	if err == nil {
		t.Fatalf("expected error for env conflict")
	}
	if !containsString(err.Error(), "MY_VAR") || !containsString(err.Error(), "conflict") {
		t.Errorf("error message should mention conflict and MY_VAR, got: %v", err)
	}
}

func TestBuildEnv_ConflictBetweenFileAndContext(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Context: map[string]ContextDef{
			"ctx": {From: "some.path", Env: "MY_VAR"},
		},
		Files: map[string]FileSpec{
			"file1": {Env: "MY_VAR"},
		},
	}
	ctx := map[string]any{"ctx": "value"}
	files := map[string]tpl.ResolvedFile{
		"file1": {Path: "/tmp/file"},
	}
	_, err := BuildEnv(cmd, nil, ctx, files)
	if err == nil {
		t.Fatalf("expected error for env conflict")
	}
	if !containsString(err.Error(), "MY_VAR") || !containsString(err.Error(), "conflict") {
		t.Errorf("error message should mention conflict and MY_VAR, got: %v", err)
	}
}

func TestBuildEnv_MultipleFileEnvs(t *testing.T) {
	cmd := &CommandDef{
		Type: CommandTypeCommand,
		Run:  "echo",
		Files: map[string]FileSpec{
			"dump": {Env: "DUMP_FILE"},
			"log":  {Env: "LOG_FILE"},
		},
	}
	files := map[string]tpl.ResolvedFile{
		"dump": {Path: "/tmp/dump.sql.gz"},
		"log":  {Path: "/tmp/app.log"},
	}
	env, err := BuildEnv(cmd, nil, nil, files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if env["DUMP_FILE"] != "/tmp/dump.sql.gz" {
		t.Errorf("expected DUMP_FILE=/tmp/dump.sql.gz, got %v", env["DUMP_FILE"])
	}
	if env["LOG_FILE"] != "/tmp/app.log" {
		t.Errorf("expected LOG_FILE=/tmp/app.log, got %v", env["LOG_FILE"])
	}
}

func TestBuildEnv_DumpCreateFixture(t *testing.T) {
	// Test the dump-create fixture: file env + param env + context env + command-level env
	// BuildEnv merges these sources without rendering; rendering happens later in the pipeline
	cmd := &CommandDef{
		Type: CommandTypeScript,
		Params: map[string]ParamDef{
			"database": {Type: ParamTypeString, Default: "mydb", Env: "DB_NAME"},
			"dump_dir": {Type: ParamTypeString, Default: "/tmp/dumps"},
		},
		Context: map[string]ContextDef{
			"db_user": {From: "db.user", Env: "DB_USER"},
		},
		Env: map[string]string{
			"DB_PASSWORD": "${db.password}",
		},
		Files: map[string]FileSpec{
			"dump": {Env: "DUMP_FILE"},
		},
	}

	params := map[string]any{
		"database": "mydb",
		"dump_dir": "/tmp/dumps",
	}

	context := map[string]any{
		"db_user": "root",
	}

	files := map[string]tpl.ResolvedFile{
		"dump": {Path: "/tmp/dumps/mydb_2026-04-29.sql.gz"},
	}

	env, err := BuildEnv(cmd, params, context, files)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify file env is set correctly (file path)
	if env["DUMP_FILE"] != "/tmp/dumps/mydb_2026-04-29.sql.gz" {
		t.Errorf("expected DUMP_FILE=/tmp/dumps/mydb_2026-04-29.sql.gz, got %v", env["DUMP_FILE"])
	}

	// Verify param env is injected (resolved param value)
	if env["DB_NAME"] != "mydb" {
		t.Errorf("expected DB_NAME=mydb, got %v", env["DB_NAME"])
	}

	// Verify context env is injected (resolved context value)
	if env["DB_USER"] != "root" {
		t.Errorf("expected DB_USER=root, got %v", env["DB_USER"])
	}

	// Verify command-level env contains raw template string (rendered later)
	if env["DB_PASSWORD"] != "${db.password}" {
		t.Errorf("expected DB_PASSWORD=${db.password} (raw template), got %v", env["DB_PASSWORD"])
	}
}

func TestBuildEnv_FileEnvConflictWithParamEnv(t *testing.T) {
	// Verify env-conflict guard rejects duplicate names
	cmd := &CommandDef{
		Type: CommandTypeScript,
		Params: map[string]ParamDef{
			"database": {Type: ParamTypeString, Env: "DUMP_FILE"},
		},
		Files: map[string]FileSpec{
			"dump": {Env: "DUMP_FILE"},
		},
	}

	params := map[string]any{"database": "mydb"}
	files := map[string]tpl.ResolvedFile{
		"dump": {Path: "/tmp/dump.sql.gz"},
	}

	_, err := BuildEnv(cmd, params, nil, files)
	if err == nil {
		t.Error("expected env conflict error, got nil")
	}
	if !containsString(err.Error(), "DUMP_FILE") {
		t.Errorf("expected error to mention DUMP_FILE, got: %v", err)
	}
}

// containsString is a helper to check if a string contains a substring.
func containsString(haystack, needle string) bool {
	for i := 0; i <= len(haystack)-len(needle); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestResolveParams_PatternValid(t *testing.T) {
	defs := map[string]ParamDef{
		"db": {Type: ParamTypeString, Pattern: `^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`},
	}
	for _, v := range []string{"mydb", "my_db", "my-db", "MyDB123", "a"} {
		_, err := ResolveParams(defs, map[string]string{"db": v}, nil)
		if err != nil {
			t.Errorf("value %q should be valid: %v", v, err)
		}
	}
}

func TestResolveParams_PatternInvalid(t *testing.T) {
	defs := map[string]ParamDef{
		"db": {Type: ParamTypeString, Pattern: `^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`},
	}
	for _, v := range []string{"`mydb`", "my db", "db;drop", "foo`bar", "-startswith"} {
		_, err := ResolveParams(defs, map[string]string{"db": v}, nil)
		if err == nil {
			t.Errorf("value %q should fail pattern validation", v)
		}
	}
}

func TestResolveParams_PatternEmptyValueSkipped(t *testing.T) {
	// An empty string bypasses pattern validation (it's the zero-value for optional params).
	defs := map[string]ParamDef{
		"db": {Type: ParamTypeString, Pattern: `^[a-zA-Z0-9_]+$`},
	}
	_, err := ResolveParams(defs, map[string]string{"db": ""}, nil)
	if err != nil {
		t.Errorf("empty value should skip pattern validation: %v", err)
	}
}

func TestResolveParams_PatternIgnoredForBool(t *testing.T) {
	// Pattern is silently ignored for bool params.
	defs := map[string]ParamDef{
		"flag": {Type: ParamTypeBool, Pattern: `^[a-z]+$`},
	}
	_, err := ResolveParams(defs, map[string]string{"flag": "true"}, nil)
	if err != nil {
		t.Errorf("pattern should be ignored for bool params: %v", err)
	}
}

func TestResolveParams_PatternIgnoredForInt(t *testing.T) {
	// Pattern is silently ignored for int params.
	defs := map[string]ParamDef{
		"count": {Type: ParamTypeInt, Pattern: `^[a-z]+$`},
	}
	_, err := ResolveParams(defs, map[string]string{"count": "42"}, nil)
	if err != nil {
		t.Errorf("pattern should be ignored for int params: %v", err)
	}
}

func TestResolveParams_PatternInvalidRegex(t *testing.T) {
	defs := map[string]ParamDef{
		"db": {Type: ParamTypeString, Pattern: `[invalid`},
	}
	_, err := ResolveParams(defs, map[string]string{"db": "mydb"}, nil)
	if err == nil {
		t.Error("invalid regex pattern should return an error")
	}
}

func TestResolveParams_PatternPathType(t *testing.T) {
	defs := map[string]ParamDef{
		"dir": {Type: ParamTypePath, Pattern: `^/[a-z/]+$`},
	}
	_, err := ResolveParams(defs, map[string]string{"dir": "/tmp/foo"}, nil)
	if err != nil {
		t.Errorf("valid path should pass pattern: %v", err)
	}
	_, err = ResolveParams(defs, map[string]string{"dir": "/TMP/FOO"}, nil)
	if err == nil {
		t.Error("invalid path should fail pattern")
	}
}
