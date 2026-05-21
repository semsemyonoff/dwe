package ui

import (
	"strings"
	"testing"

	"devbox-cli/internal/config"
)

func TestRenderTable_Basic(t *testing.T) {
	resetStyles()
	headers := []string{"NAME", "STATE"}
	rows := [][]string{
		{"app-main", "enabled"},
		{"app-second", "disabled"},
	}
	out := RenderTable(headers, rows)
	if !strings.Contains(out, "NAME") {
		t.Error("expected table to contain header NAME")
	}
	if !strings.Contains(out, "STATE") {
		t.Error("expected table to contain header STATE")
	}
	if !strings.Contains(out, "app-main") {
		t.Error("expected table to contain row value app-main")
	}
	if !strings.Contains(out, "app-second") {
		t.Error("expected table to contain row value app-second")
	}
}

func TestRenderDaemonTable_Empty(t *testing.T) {
	resetStyles()
	if got := RenderDaemonTable(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRenderDaemonTable_RendersRows(t *testing.T) {
	resetStyles()
	out := RenderDaemonTable([]DaemonTableRow{
		{ID: "services.main.queue", Name: "name=default", Container: "proj-php_queue_default", Uptime: "5m0s"},
	})
	for _, want := range []string{"ID", "NAME", "CONTAINER", "UPTIME", "services.main.queue", "proj-php_queue_default", "5m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got %q", want, out)
		}
	}
}

func TestRenderDaemonTable_EmptyNameFallback(t *testing.T) {
	resetStyles()
	out := RenderDaemonTable([]DaemonTableRow{
		{ID: "single", Name: "", Container: "c1", Uptime: "1s"},
	})
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash fallback for empty name, got %q", out)
	}
}

func TestRenderTable_Empty(t *testing.T) {
	resetStyles()
	out := RenderTable([]string{"COL1", "COL2"}, nil)
	if !strings.Contains(out, "COL1") {
		t.Error("expected empty-rows table to still render headers")
	}
}

func TestRenderTable_SingleRow(t *testing.T) {
	resetStyles()
	out := RenderTable([]string{"TOOL", "PORT"}, [][]string{{"adminer", "8080"}})
	if !strings.Contains(out, "adminer") {
		t.Error("expected table to contain adminer")
	}
	if !strings.Contains(out, "8080") {
		t.Error("expected table to contain 8080")
	}
}

func TestRenderTable_UsesTableStyles(t *testing.T) {
	resetStyles()
	// Apply a custom table header color and verify the table still renders without panic.
	ApplyStyles(&config.StylesConfig{
		Colors: config.StylesColors{
			TableBorder: "203",
			TableHeader: "209",
		},
	})
	out := RenderTable([]string{"NAME"}, [][]string{{"foo"}})
	if !strings.Contains(out, "NAME") {
		t.Error("expected header NAME in output after ApplyStyles")
	}
	if !strings.Contains(out, "foo") {
		t.Error("expected row value foo in output after ApplyStyles")
	}
	resetStyles()
}

func TestRenderServiceTable_Basic(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "main", Container: "app-main", Mandatory: true, Running: true},
		{Name: "second", Container: "app-second", Enabled: true, Running: false},
		{Name: "worker", Container: "app-worker", Mandatory: false, Enabled: false},
	}
	out := RenderServiceTable(rows, nil)

	for _, want := range []string{
		"NAME", "CONTAINER", "STATE", "RUNNING",
		"main", "app-main", "mandatory", "running",
		"second", "app-second", "enabled", "stopped",
		"worker", "app-worker", "disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderServiceTable_Empty(t *testing.T) {
	resetStyles()
	out := RenderServiceTable(nil, nil)
	if !strings.Contains(out, "NAME") {
		t.Error("expected header NAME in empty service table")
	}
	if !strings.Contains(out, "STATE") {
		t.Error("expected header STATE in empty service table")
	}
}

func TestRenderServiceTable_DisabledRunStr(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "tool", Container: "c-tool", Mandatory: false, Enabled: false},
	}
	out := RenderServiceTable(rows, nil)
	if !strings.Contains(out, "—") {
		t.Errorf("disabled service should show em-dash run status\nfull output:\n%s", out)
	}
}

func TestRenderToolTable_Basic(t *testing.T) {
	resetStyles()
	rows := []ToolTableRow{
		{Name: "adminer", Host: "localhost", Port: 8080, Enabled: true, Running: true},
		{Name: "mailpit", Host: "localhost", Port: 8025, Enabled: true, Running: false},
		{Name: "redis-insight", Host: "localhost", Port: 8001, Enabled: false},
	}
	out := RenderToolTable(rows, nil)
	for _, want := range []string{
		"NAME", "HOST", "PORT", "STATE", "RUNNING",
		"adminer", "mailpit", "redis-insight",
		"running", "stopped", "disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderToolTable_ZeroPort(t *testing.T) {
	resetStyles()
	rows := []ToolTableRow{
		{Name: "tool", Host: "localhost", Port: 0, Enabled: true, Running: false},
	}
	out := RenderToolTable(rows, nil)
	if !strings.Contains(out, "—") {
		t.Errorf("zero port should render as em-dash:\n%s", out)
	}
}

func TestRenderToolTable_Empty(t *testing.T) {
	resetStyles()
	out := RenderToolTable(nil, nil)
	if !strings.Contains(out, "NAME") {
		t.Error("expected header NAME in empty tool table")
	}
}

func TestRenderServiceTable_ExtraCols_Populated(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{
			Name: "main", Container: "app-main", Enabled: true, Running: true,
			Extras: map[string]string{"TAG": "v1.2", "ENDPOINT": "http://main"},
		},
	}
	out := RenderServiceTable(rows, []string{"TAG", "ENDPOINT"})
	for _, want := range []string{"TAG", "ENDPOINT", "v1.2", "http://main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderServiceTable_ExtraCols_MissingKey(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{
			Name: "main", Container: "app-main", Enabled: true, Running: true,
			Extras: map[string]string{"TAG": "v1"},
		},
	}
	out := RenderServiceTable(rows, []string{"TAG", "MISSING"})
	if !strings.Contains(out, "—") {
		t.Errorf("missing key should render as em-dash:\n%s", out)
	}
	if !strings.Contains(out, "MISSING") {
		t.Error("expected header MISSING")
	}
}

func TestRenderToolTable_ExtraCols_Populated(t *testing.T) {
	resetStyles()
	rows := []ToolTableRow{
		{
			Name: "mailpit", Host: "mailpit.local", Port: 1080, Enabled: true, Running: true,
			Extras: map[string]string{"ENDPOINT": "http://mailpit.local:1080"},
		},
	}
	out := RenderToolTable(rows, []string{"ENDPOINT"})
	if !strings.Contains(out, "ENDPOINT") {
		t.Error("expected ENDPOINT header")
	}
	if !strings.Contains(out, "http://mailpit.local:1080") {
		t.Errorf("expected endpoint cell value:\n%s", out)
	}
}

func TestRenderToolTable_ExtraCols_MissingKey(t *testing.T) {
	resetStyles()
	rows := []ToolTableRow{
		{Name: "mailpit", Host: "h", Port: 1, Enabled: true, Running: true},
	}
	out := RenderToolTable(rows, []string{"ENDPOINT"})
	if !strings.Contains(out, "—") {
		t.Errorf("missing extras key should render as em-dash:\n%s", out)
	}
}

func TestRenderDeployStatus_Basic(t *testing.T) {
	resetStyles()
	rows := []DeployStatusRow{
		{
			Service:         "main",
			Status:          "deployed",
			ConfigDelta:     "ok",
			PrevHashShort:   "abc12345",
			CurrHashShort:   "abc12345",
			LastFailedPhase: "",
			LastFailedStep:  "",
		},
		{
			Service:         "db",
			Status:          "failed",
			ConfigDelta:     "changed",
			PrevHashShort:   "old12345",
			CurrHashShort:   "new12345",
			LastFailedPhase: "setup",
			LastFailedStep:  "init-db",
		},
	}
	out := RenderDeployStatus(rows)

	for _, want := range []string{
		"SERVICE", "STATUS", "CONFIG", "PREV HASH", "CURR HASH", "LAST FAILED",
		"main", "deployed", "ok",
		"db", "failed", "changed",
		"init-db", "setup / init-db",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderDeployStatus_Empty(t *testing.T) {
	resetStyles()
	out := RenderDeployStatus(nil)
	if !strings.Contains(out, "SERVICE") {
		t.Error("expected header SERVICE in empty deploy status table")
	}
}

func TestRenderDeployStatus_NoFailure(t *testing.T) {
	resetStyles()
	rows := []DeployStatusRow{
		{
			Service:         "web",
			Status:          "deployed",
			ConfigDelta:     "ok",
			PrevHashShort:   "hash1234",
			CurrHashShort:   "hash1234",
			LastFailedPhase: "",
			LastFailedStep:  "",
		},
	}
	out := RenderDeployStatus(rows)
	if !strings.Contains(out, "—") {
		t.Error("expected em-dash for missing last-failed when no failures")
	}
}
