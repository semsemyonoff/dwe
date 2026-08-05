package command

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// buildTestRegistry builds a small registry with deterministic content for JSON tests.
func buildTestRegistry(t *testing.T) *usercommands.Registry {
	t.Helper()
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "app.install",
		Group:       "app",
		LocalName:   "install",
		Type:        usercommands.CommandTypeServiceExec,
		Description: "Install dependencies",
		Service:     "app-main",
		Cmd:         "composer install",
		Private:     false,
		Params: map[string]usercommands.ParamDef{
			"env": {Type: model.ParamTypeString, Description: "target environment", Required: true},
		},
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:        "db.down",
		Group:     "db",
		LocalName: "down",
		Type:      usercommands.CommandTypeShell,
		Cmd:       "docker compose down",
		Private:   false,
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:        "db.secret",
		Group:     "db",
		LocalName: "secret",
		Type:      usercommands.CommandTypeShell,
		Cmd:       "echo secret",
		Private:   true,
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:        "db.up",
		Group:     "db",
		LocalName: "up",
		Type:      usercommands.CommandTypeShell,
		Cmd:       "docker compose up -d",
		Private:   false,
	})
	return reg
}

// --- buildCommandsListJSON tests ---

func TestBuildCommandsListJSON_publicOnly(t *testing.T) {
	reg := buildTestRegistry(t)
	data := buildCommandsListJSON(reg, "", false, i18n.NopTranslator{}, "")
	// Should exclude db.secret (private)
	if len(data.Commands) != 3 {
		t.Errorf("expected 3 public commands, got %d", len(data.Commands))
	}
	for _, c := range data.Commands {
		if c.ID == "db.secret" {
			t.Error("private command db.secret should not appear in public list")
		}
	}
}

func TestBuildCommandsListJSON_includeAll(t *testing.T) {
	reg := buildTestRegistry(t)
	data := buildCommandsListJSON(reg, "", true, i18n.NopTranslator{}, "")
	if len(data.Commands) != 4 {
		t.Errorf("expected 4 commands (including private), got %d", len(data.Commands))
	}
	var foundSecret bool
	for _, c := range data.Commands {
		if c.ID == "db.secret" {
			foundSecret = true
			if !c.Private {
				t.Error("db.secret should have private=true")
			}
		}
	}
	if !foundSecret {
		t.Error("db.secret should appear in --all list")
	}
}

func TestBuildCommandsListJSON_groupFilter(t *testing.T) {
	reg := buildTestRegistry(t)
	data := buildCommandsListJSON(reg, "db", false, i18n.NopTranslator{}, "")
	for _, c := range data.Commands {
		if c.Group != "db" {
			t.Errorf("group filter should restrict to db, got group=%q for %q", c.Group, c.ID)
		}
	}
	if len(data.Commands) != 2 {
		t.Errorf("expected 2 public db commands, got %d", len(data.Commands))
	}
}

func TestBuildCommandsListJSON_paramsIncluded(t *testing.T) {
	reg := buildTestRegistry(t)
	data := buildCommandsListJSON(reg, "", false, i18n.NopTranslator{}, "")
	var install *commandEntryJSON
	for i := range data.Commands {
		if data.Commands[i].ID == "app.install" {
			install = &data.Commands[i]
			break
		}
	}
	if install == nil {
		t.Fatal("app.install not found in list")
	}
	if len(install.Params) != 1 {
		t.Fatalf("expected 1 param, got %d", len(install.Params))
	}
	p := install.Params[0]
	if p.Name != "env" {
		t.Errorf("param name: want %q, got %q", "env", p.Name)
	}
	if !p.Required {
		t.Error("param env should be required")
	}
	if p.Type != "string" {
		t.Errorf("param type: want %q, got %q", "string", p.Type)
	}
}

func TestBuildCommandsListJSON_sortedByID(t *testing.T) {
	reg := buildTestRegistry(t)
	data := buildCommandsListJSON(reg, "", false, i18n.NopTranslator{}, "")
	for i := 1; i < len(data.Commands); i++ {
		if data.Commands[i].ID < data.Commands[i-1].ID {
			t.Errorf("commands not sorted: %q before %q", data.Commands[i-1].ID, data.Commands[i].ID)
		}
	}
}

// --- buildCommandInspectJSON tests ---

func TestBuildCommandInspectJSON_shell(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "db.up",
		Group:       "db",
		LocalName:   "up",
		Type:        usercommands.CommandTypeShell,
		Description: "Start database",
		Cmd:         "docker compose up -d",
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if data.ID != "db.up" {
		t.Errorf("id: want %q, got %q", "db.up", data.ID)
	}
	if data.Type != "shell" {
		t.Errorf("type: want %q, got %q", "shell", data.Type)
	}
	if data.Description != "Start database" {
		t.Errorf("description: want %q, got %q", "Start database", data.Description)
	}
	if data.Cmd != "docker compose up -d" {
		t.Errorf("cmd: want %q, got %q", "docker compose up -d", data.Cmd)
	}
}

func TestBuildCommandInspectJSON_workflow(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "bootstrap",
		Type: usercommands.CommandTypeWorkflow,
		Steps: []usercommands.WorkflowStep{
			{Command: "db.up"},
			{Command: "app.install", With: map[string]string{"env": "dev"}},
			{Confirm: "Continue?"},
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if len(data.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(data.Steps))
	}
	if data.Steps[0].Kind != "command" || data.Steps[0].Command != "db.up" {
		t.Errorf("step[0]: want command=db.up, got kind=%q cmd=%q", data.Steps[0].Kind, data.Steps[0].Command)
	}
	if data.Steps[1].Kind != "command" || data.Steps[1].Command != "app.install" {
		t.Errorf("step[1]: want command=app.install, got %+v", data.Steps[1])
	}
	if data.Steps[1].With["env"] != "dev" {
		t.Errorf("step[1].with.env: want %q, got %q", "dev", data.Steps[1].With["env"])
	}
	if data.Steps[2].Kind != "confirm" || data.Steps[2].Confirm != "Continue?" {
		t.Errorf("step[2]: want confirm=Continue?, got %+v", data.Steps[2])
	}
}

func TestBuildCommandInspectJSON_params(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "app.install",
		Type: usercommands.CommandTypeServiceExec,
		Params: map[string]usercommands.ParamDef{
			"env":  {Type: model.ParamTypeString, Required: true, Description: "target env"},
			"mode": {Type: model.ParamTypeString, Default: "fast"},
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if len(data.Params) != 2 {
		t.Fatalf("expected 2 params, got %d", len(data.Params))
	}
	// Params must be sorted by name
	if data.Params[0].Name != "env" {
		t.Errorf("first param should be 'env' (sorted), got %q", data.Params[0].Name)
	}
	if data.Params[1].Name != "mode" {
		t.Errorf("second param should be 'mode' (sorted), got %q", data.Params[1].Name)
	}
	if !data.Params[0].Required {
		t.Error("param env should be required")
	}
	if data.Params[1].Default != "fast" {
		t.Errorf("param mode default: want %q, got %q", "fast", data.Params[1].Default)
	}
}

func TestBuildCommandInspectJSON_derivedFromDaemon(t *testing.T) {
	autoRemove := true
	def := &usercommands.CommandDef{
		ID:                "queue.start",
		Type:              usercommands.CommandTypeBuiltin,
		Cmd:               "docker_daemon_start",
		DerivedFromDaemon: "queue",
		SourceDaemon: &model.DaemonSpec{
			ContainerTemplate: "queue_${param.name}",
			OnAlreadyRunning:  "noop",
			AutoRemove:        &autoRemove,
			StopTimeout:       "15s",
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if data.DerivedFrom != "queue" {
		t.Errorf("derived_from: want %q, got %q", "queue", data.DerivedFrom)
	}
	if data.DaemonSpec == nil {
		t.Fatal("daemon_spec should be non-nil for derived commands")
	}
	if data.DaemonSpec.ContainerTemplate != "queue_${param.name}" {
		t.Errorf("container_template: want %q, got %q", "queue_${param.name}", data.DaemonSpec.ContainerTemplate)
	}
	if data.DaemonSpec.OnAlreadyRunning != "noop" {
		t.Errorf("on_already_running: want %q, got %q", "noop", data.DaemonSpec.OnAlreadyRunning)
	}
	if data.DaemonSpec.AutoRemove == nil || !*data.DaemonSpec.AutoRemove {
		t.Error("auto_remove should be true")
	}
	if data.DaemonSpec.StopTimeout != "15s" {
		t.Errorf("stop_timeout: want %q, got %q", "15s", data.DaemonSpec.StopTimeout)
	}
}

func TestBuildCommandInspectJSON_confirmation(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:               "db.reset",
		Type:             usercommands.CommandTypeShell,
		Cmd:              "echo reset",
		Confirmation:     true,
		ConfirmationText: "Drop and recreate database?",
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if !data.Confirmation {
		t.Error("confirmation should be true")
	}
	if data.ConfirmationText != "Drop and recreate database?" {
		t.Errorf("confirmation_text: want %q, got %q", "Drop and recreate database?", data.ConfirmationText)
	}
}

func TestBuildCommandInspectJSON_messages(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:   "db.up",
		Type: usercommands.CommandTypeShell,
		Messages: usercommands.CommandMessages{
			Success: "Database started.",
			Error:   "Database failed.",
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if data.Messages == nil {
		t.Fatal("messages should be non-nil")
	}
	if data.Messages.Success != "Database started." {
		t.Errorf("messages.success: want %q, got %q", "Database started.", data.Messages.Success)
	}
	if data.Messages.Error != "Database failed." {
		t.Errorf("messages.error: want %q, got %q", "Database failed.", data.Messages.Error)
	}
}

func TestBuildCommandInspectJSON_noMessages(t *testing.T) {
	def := &usercommands.CommandDef{ID: "db.up", Type: usercommands.CommandTypeShell}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	if data.Messages != nil {
		t.Error("messages should be nil when not set")
	}
}

func TestBuildCommandInspectJSON_validJSON(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "app.install",
		Group:       "app",
		Type:        usercommands.CommandTypeServiceExec,
		Description: "Install deps",
		Service:     "app-main",
		Cmd:         "composer install",
		Params: map[string]usercommands.ParamDef{
			"env": {Type: model.ParamTypeString, Required: true},
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var roundtrip commandInspectJSON
	if err := json.Unmarshal(b, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if roundtrip.ID != "app.install" {
		t.Errorf("roundtrip id: want %q, got %q", "app.install", roundtrip.ID)
	}
}

// --- golden tests ---

// commandListGoldenRegistry builds a deterministic registry for golden tests.
// Uses AddCommandForTest to avoid needing a project on disk.
func commandListGoldenRegistry() *usercommands.Registry {
	reg := usercommands.NewEmptyRegistry()
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "app.install",
		Group:       "app",
		LocalName:   "install",
		Type:        usercommands.CommandTypeServiceExec,
		Description: "Install application dependencies",
		Service:     "app-main",
		Cmd:         "composer install",
		Params: map[string]usercommands.ParamDef{
			"env": {Type: model.ParamTypeString, Description: "target environment", Required: true},
		},
	})
	reg.AddCommandForTest(&usercommands.CommandDef{
		ID:          "db.migrate",
		Group:       "db",
		LocalName:   "migrate",
		Type:        usercommands.CommandTypeShell,
		Description: "Run database migrations",
		Cmd:         "php artisan migrate",
	})
	return reg
}

func TestCommandList_JSONGolden(t *testing.T) {
	reg := commandListGoldenRegistry()
	data := buildCommandsListJSON(reg, "", false, i18n.NopTranslator{}, "")

	// Encode to JSON (compact).
	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(b)

	goldenPath := "testdata/list.json.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(raw), "\n")
	if got != want {
		t.Errorf("JSON list output mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestCommandInspect_JSONGolden(t *testing.T) {
	def := &usercommands.CommandDef{
		ID:          "db.migrate",
		Group:       "db",
		LocalName:   "migrate",
		Type:        usercommands.CommandTypeShell,
		Description: "Run database migrations",
		Cmd:         "php artisan migrate",
		Params: map[string]usercommands.ParamDef{
			"env": {Type: model.ParamTypeString, Description: "target environment", Required: true, Default: "local"},
		},
	}
	data := buildCommandInspectJSON(def, i18n.NopTranslator{}, "")

	b, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	got := string(b)

	goldenPath := "testdata/inspect.json.golden"
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(goldenPath, []byte(got+"\n"), 0644); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
		t.Logf("updated golden: %s", goldenPath)
		return
	}

	raw, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("reading golden %s: %v", goldenPath, err)
	}
	want := strings.TrimRight(string(raw), "\n")
	if got != want {
		t.Errorf("JSON inspect output mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

// TestBuildCommandInspectJSON_argvAppendFrom: inspect is the documented way to
// learn what a command does before running it, so an executable field must be
// visible there for both argv-building command families.
func TestBuildCommandInspectJSON_argvAppendFrom(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  *usercommands.CommandDef
	}{
		{
			name: "shell",
			def: &usercommands.CommandDef{
				ID:             "quality.staged",
				Type:           usercommands.CommandTypeShell,
				Argv:           []string{"ruff", "check"},
				ArgvAppendFrom: "git diff --name-only --cached",
			},
		},
		{
			name: "service_exec",
			def: &usercommands.CommandDef{
				ID:             "app.staged",
				Type:           usercommands.CommandTypeServiceExec,
				Service:        "app",
				Argv:           []string{"ruff", "check"},
				ArgvAppendFrom: "git diff --name-only --cached",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := buildCommandInspectJSON(tc.def, i18n.NopTranslator{}, "")
			if data.ArgvAppendFrom != "git diff --name-only --cached" {
				t.Errorf("argv_append_from: got %q", data.ArgvAppendFrom)
			}
		})
	}
}
