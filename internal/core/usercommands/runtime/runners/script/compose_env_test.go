package script

import (
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// unsetComposeEnv clears any ambient compose variables for one test so an
// omitted contract entry is observable.
func unsetComposeEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"COMPOSE_PROJECT_NAME", "COMPOSE_FILE"} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

const printComposeEnv = `printf 'NAME=%s\nFILE=%s\n' "${COMPOSE_PROJECT_NAME-unset}" "${COMPOSE_FILE-unset}"`

func TestRunner_ContractEnvVars_Compose(t *testing.T) {
	unsetComposeEnv(t)
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "compose.sh", printComposeEnv)

	ctx := RunContext{
		Cmd:    &CommandDef{Type: CommandTypeScript, ID: "test.compose", Script: &ScriptDef{Path: scriptPath}},
		Render: &tpl.RenderContext{},
		Config: &config.DweConfig{
			Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"},
			Compose: config.ComposeConfig{Base: "compose.yaml"},
			Services: map[string]config.ServiceConfig{
				"catalog": {Enabled: true, Compose: []string{"compose/services/catalog/app.yml"}},
			},
		},
		ProjectRoot: dir,
	}
	out, stderr, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr)
	}
	want := "NAME=dwe-laravel\nFILE=" + dir + "/compose.yaml:" + dir + "/compose/services/catalog/app.yml\n"
	if out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestRunner_ContractEnvVars_ComposeOmittedWhenEmpty(t *testing.T) {
	unsetComposeEnv(t)
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "compose.sh", printComposeEnv)

	ctx := RunContext{
		Cmd:         &CommandDef{Type: CommandTypeScript, ID: "test.compose", Script: &ScriptDef{Path: scriptPath}},
		Render:      &tpl.RenderContext{},
		ProjectRoot: dir,
	}
	out, stderr, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr)
	}
	if out != "NAME=unset\nFILE=unset\n" {
		t.Errorf("output = %q, want both variables unset", out)
	}
}

func TestRunner_ContractEnvVars_ComposeWinsOverUserEnv(t *testing.T) {
	dir := t.TempDir()
	scriptPath := writeScript(t, dir, "compose.sh", `printf '%s' "$COMPOSE_PROJECT_NAME"`)

	ctx := RunContext{
		Cmd: &CommandDef{
			Type:   CommandTypeScript,
			ID:     "test.compose",
			Script: &ScriptDef{Path: scriptPath},
			Env:    map[string]string{"COMPOSE_PROJECT_NAME": "user-supplied"},
		},
		Params:      map[string]any{},
		Context:     map[string]any{},
		Render:      &tpl.RenderContext{},
		Config:      &config.DweConfig{Project: config.ProjectConfig{Prefix: "dwe", Name: "laravel"}},
		ProjectRoot: dir,
	}
	out, stderr, err := captureOutput(t, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v (stderr=%s)", err, stderr)
	}
	if strings.TrimSpace(out) != "dwe-laravel" {
		t.Errorf("COMPOSE_PROJECT_NAME = %q, want the contract value dwe-laravel over env:", out)
	}
}
