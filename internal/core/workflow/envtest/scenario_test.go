package envtest

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLoadScenario_Valid(t *testing.T) {
	scn, err := LoadScenario(filepath.Join("testdata", "valid.yml"))
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if scn.Description != "Deploy with redis disabled" {
		t.Errorf("Description = %q", scn.Description)
	}
	if scn.Timeout != "15m" {
		t.Errorf("Timeout = %q, want 15m", scn.Timeout)
	}
	if got := scn.Env.Services.Disable; !reflect.DeepEqual(got, []string{"redis"}) {
		t.Errorf("Disable = %v", got)
	}
	if got := scn.Env.Services.Enable; !reflect.DeepEqual(got, []string{"postgres"}) {
		t.Errorf("Enable = %v", got)
	}
	if len(scn.Steps) != 2 {
		t.Fatalf("Steps len = %d, want 2", len(scn.Steps))
	}
	if scn.Steps[0].Cmd != "http_check" || scn.Steps[0].Type != "builtin" {
		t.Errorf("step 0 = %+v", scn.Steps[0])
	}
}

func TestLoadScenario_AutoVarKeptRaw(t *testing.T) {
	scn, err := LoadScenario(filepath.Join("testdata", "auto_var.yml"))
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if got, ok := scn.Env.Vars["app.http_port"].(string); !ok || got != AutoPortSentinel {
		t.Errorf("app.http_port = %v (%T), want raw string %q", scn.Env.Vars["app.http_port"], scn.Env.Vars["app.http_port"], AutoPortSentinel)
	}
	// A concrete numeric value keeps its YAML type.
	if got, ok := scn.Env.Vars["db.port"].(int); !ok || got != 5432 {
		t.Errorf("db.port = %v (%T), want int 5432", scn.Env.Vars["db.port"], scn.Env.Vars["db.port"])
	}
}

func TestLoadScenario_EnableDisableLists(t *testing.T) {
	scn, err := LoadScenario(filepath.Join("testdata", "enable_disable.yml"))
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if !reflect.DeepEqual(scn.Env.Services.Enable, []string{"app", "worker"}) {
		t.Errorf("Enable = %v", scn.Env.Services.Enable)
	}
	if !reflect.DeepEqual(scn.Env.Services.Disable, []string{"redis"}) {
		t.Errorf("Disable = %v", scn.Env.Services.Disable)
	}
}

func TestLoadScenario_Errors(t *testing.T) {
	cases := []struct {
		name    string
		file    string
		wantSub string
	}{
		{"unknown top field", "unknown_top_field.yml", "bogus"},
		{"unknown step field", "unknown_step_field.yml", "bogus"},
		{"missing type", "missing_type.yml", "type is required"},
		{"missing cmd", "missing_cmd.yml", "cmd is required"},
		{"unknown type", "unknown_type.yml", "unknown type"},
		{"invalid when", "invalid_when.yml", "unknown condition type"},
		{"empty file", "empty.yml", "empty or invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadScenario(filepath.Join("testdata", tc.file))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSub)
			}
		})
	}
}

func TestLoadScenario_InvalidFilename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Bad_Name.yml")
	if err := os.WriteFile(path, []byte("description: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadScenario(path)
	if err == nil {
		t.Fatal("expected invalid-name error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid scenario name") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestValidateScenarioName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"redis", true},
		{"my-scenario", true},
		{"my_scenario", true},
		{"web2", true},
		{"a", true},
		{"Redis", false},
		{"-leading", false},
		{"_leading", false},
		{"has space", false},
		{"has.dot", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateScenarioName(tc.name)
			if tc.ok && err != nil {
				t.Errorf("ValidateScenarioName(%q) = %v, want nil", tc.name, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("ValidateScenarioName(%q) = nil, want error", tc.name)
			}
		})
	}
}

func TestScenarioNameFromPath(t *testing.T) {
	cases := map[string]string{
		"testdata/valid.yml":            "valid",
		"/a/b/my-scenario.yaml":         "my-scenario",
		"redis.yml":                     "redis",
		"/root/workspace/tests/web.yml": "web",
	}
	for in, want := range cases {
		if got := ScenarioNameFromPath(in); got != want {
			t.Errorf("ScenarioNameFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestListScenarios(t *testing.T) {
	base := t.TempDir()
	testsDir := TestsDir(base)
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"beta.yml", "alpha.yml", "gamma.yaml", "README.md"} {
		if err := os.WriteFile(filepath.Join(testsDir, f), []byte("description: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListScenarios(base)
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListScenarios = %v, want %v", got, want)
	}
}

func TestListScenarios_AbsentDir(t *testing.T) {
	got, err := ListScenarios(t.TempDir())
	if err != nil {
		t.Fatalf("ListScenarios: %v", err)
	}
	if got != nil {
		t.Errorf("ListScenarios = %v, want nil", got)
	}
}

func TestListScenarios_InvalidFilename(t *testing.T) {
	base := t.TempDir()
	testsDir := TestsDir(base)
	if err := os.MkdirAll(testsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(testsDir, "Bad-Name.yml"), []byte("description: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ListScenarios(base)
	if err == nil {
		t.Fatal("expected invalid-name error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid scenario name") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestScenarioPath_ResolvesYmlAndYaml(t *testing.T) {
	base := t.TempDir()
	dir := TestsDir(base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A .yaml scenario: ListScenarios strips the extension to "alpha", so a
	// name+".yml" reconstruction (the old bug) would fail to open it. ScenarioPath
	// must resolve it back to the real .yaml file.
	if err := os.WriteFile(filepath.Join(dir, "alpha.yaml"), []byte("description: a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "beta.yml"), []byte("description: b\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got, err := ScenarioPath(base, "alpha"); err != nil || got != filepath.Join(dir, "alpha.yaml") {
		t.Fatalf("ScenarioPath(alpha) = %q, %v; want %q", got, err, filepath.Join(dir, "alpha.yaml"))
	}
	if got, err := ScenarioPath(base, "beta"); err != nil || got != filepath.Join(dir, "beta.yml") {
		t.Fatalf("ScenarioPath(beta) = %q, %v; want %q", got, err, filepath.Join(dir, "beta.yml"))
	}
	if _, err := ScenarioPath(base, "missing"); err == nil {
		t.Fatal("ScenarioPath(missing) = nil error, want a not-found error")
	}
}
