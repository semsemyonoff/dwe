package render

import (
	"fmt"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
)

func TestRenderTable_Basic(t *testing.T) {
	resetStyles()
	headers := []string{"NAME", "STATE"}
	rows := [][]string{
		{"app-main", "enabled"},
		{"app-second", "disabled"},
	}
	out := Table(headers, rows)
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
	if got := DaemonTable(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestRenderDaemonTable_RendersRows(t *testing.T) {
	resetStyles()
	out := DaemonTable([]DaemonTableRow{
		{ID: "services.main.queue", Params: "name=default", Container: "proj-php_queue_default", Uptime: "5m0s"},
	})
	for _, want := range []string{"ID", "PARAMS", "CONTAINER", "UPTIME", "services.main.queue", "proj-php_queue_default", "5m0s"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got %q", want, out)
		}
	}
}

func TestRenderDaemonTable_EmptyNameFallback(t *testing.T) {
	resetStyles()
	out := DaemonTable([]DaemonTableRow{
		{ID: "single", Params: "", Container: "c1", Uptime: "1s"},
	})
	if !strings.Contains(out, "—") {
		t.Errorf("expected em-dash fallback for empty name, got %q", out)
	}
}

func TestRenderTable_Empty(t *testing.T) {
	resetStyles()
	out := Table([]string{"COL1", "COL2"}, nil)
	if !strings.Contains(out, "COL1") {
		t.Error("expected empty-rows table to still render headers")
	}
}

func TestRenderTable_SingleRow(t *testing.T) {
	resetStyles()
	out := Table([]string{"TOOL", "PORT"}, [][]string{{"adminer", "8080"}})
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
	styles.ApplyStyles(&config.StylesConfig{
		Colors: config.StylesColors{
			Border: "#CB0000",
			Accent: "#D10000",
		},
	})
	out := Table([]string{"NAME"}, [][]string{{"foo"}})
	if !strings.Contains(out, "NAME") {
		t.Error("expected header NAME in output after ApplyStyles")
	}
	if !strings.Contains(out, "foo") {
		t.Error("expected row value foo in output after ApplyStyles")
	}
	resetStyles()
}

func TestRenderServicesTable_Basic(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "main", Container: "app-main", Mandatory: true, Running: true},
		{Name: "second", Container: "app-second", Enabled: true, Running: false},
		{Name: "worker", Container: "app-worker", Mandatory: false, Enabled: false},
	}
	out := ServicesTable(rows, nil, false)

	for _, want := range []string{
		"NAME", "CONTAINER", "HOSTS", "PORTS", "STATE", "RUNNING",
		"main", "app-main", "mandatory", "running",
		"second", "app-second", "enabled", "stopped",
		"worker", "app-worker", "disabled",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "DIR") {
		t.Errorf("withDirCol=false should NOT include DIR column:\n%s", out)
	}
}

func TestRenderServicesTable_HostsPortsRendered(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{
			Name: "adminer", Container: "adminer", Enabled: true, Running: true,
			Hosts: map[string]string{"web": "admin.local"},
			Ports: map[string]int{"http": 8027},
		},
		{
			Name: "rabbitmq", Container: "rabbitmq", Mandatory: true, Running: true,
			Ports: map[string]int{"amqp": 5672, "admin": 15672},
		},
		{
			Name: "bare", Container: "bare", Enabled: true, Running: false,
		},
	}
	out := ServicesTable(rows, nil, false)
	for _, want := range []string{
		"HOSTS", "PORTS",
		"admin.local", "8027", // single-entry: bare value
		"admin=15672", "amqp=5672", // multi-entry: name=value pairs, sorted
		"—", // empty maps → em-dash
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestFormatHostsCell(t *testing.T) {
	if got := formatHostsCell(nil); got != "—" {
		t.Errorf("nil hosts: got %q, want em-dash", got)
	}
	if got := formatHostsCell(map[string]string{"web": "app.local"}); got != "app.local" {
		t.Errorf("single host: got %q, want bare value", got)
	}
	got := formatHostsCell(map[string]string{"api": "api.local", "web": "web.local"})
	if got != "api=api.local, web=web.local" {
		t.Errorf("multi host: got %q, want sorted name=value pairs", got)
	}
}

func TestFormatPortsCell(t *testing.T) {
	if got := formatPortsCell(nil); got != "—" {
		t.Errorf("nil ports: got %q, want em-dash", got)
	}
	if got := formatPortsCell(map[string]int{"http": 80}); got != "80" {
		t.Errorf("single port: got %q, want bare value", got)
	}
	got := formatPortsCell(map[string]int{"amqp": 5672, "admin": 15672})
	if got != "admin=15672, amqp=5672" {
		t.Errorf("multi port: got %q, want sorted name=value pairs", got)
	}
}

func TestSortedKVPairs(t *testing.T) {
	if got := SortedKVPairs(map[string]string{}, func(v string) string { return v }); got != "" {
		t.Errorf("empty map: got %q, want empty string", got)
	}
	if got := SortedKVPairs(map[string]string{"web": "app.local"}, func(v string) string { return v }); got != "web=app.local" {
		t.Errorf("single entry: got %q, want web=app.local", got)
	}
	got := SortedKVPairs(map[string]int{"http": 80, "amqp": 5672}, func(v int) string { return fmt.Sprintf("%d", v) })
	if got != "amqp=5672, http=80" {
		t.Errorf("multi entry: got %q, want sorted name=value pairs", got)
	}
}

func TestRenderServicesTable_WithDirCol(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "main", Dir: "./services/main", Container: "app-main", Mandatory: true, Running: true},
		{Name: "worker", Dir: "", Container: "app-worker", Enabled: true},
	}
	out := ServicesTable(rows, nil, true)
	for _, want := range []string{"DIR", "./services/main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "—") {
		t.Errorf("empty Dir should render as em-dash:\n%s", out)
	}
}

func TestRenderServicesTable_Empty(t *testing.T) {
	resetStyles()
	out := ServicesTable(nil, nil, false)
	if !strings.Contains(out, "NAME") {
		t.Error("expected header NAME in empty service table")
	}
	if !strings.Contains(out, "STATE") {
		t.Error("expected header STATE in empty service table")
	}
}

func TestRenderServicesTable_DisabledRunStr(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{Name: "tool", Container: "c-tool", Mandatory: false, Enabled: false},
	}
	out := ServicesTable(rows, nil, false)
	if !strings.Contains(out, "—") {
		t.Errorf("disabled service should show em-dash run status\nfull output:\n%s", out)
	}
}

func TestRenderServicesTable_ExtraCols_Populated(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{
			Name: "main", Container: "app-main", Enabled: true, Running: true,
			Extras: map[string]string{"TAG": "v1.2", "ENDPOINT": "http://main"},
		},
	}
	out := ServicesTable(rows, []string{"TAG", "ENDPOINT"}, false)
	for _, want := range []string{"TAG", "ENDPOINT", "v1.2", "http://main"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}

func TestRenderServicesTable_ExtraCols_MissingKey(t *testing.T) {
	resetStyles()
	rows := []ServiceTableRow{
		{
			Name: "main", Container: "app-main", Enabled: true, Running: true,
			Extras: map[string]string{"TAG": "v1"},
		},
	}
	out := ServicesTable(rows, []string{"TAG", "MISSING"}, false)
	if !strings.Contains(out, "—") {
		t.Errorf("missing key should render as em-dash:\n%s", out)
	}
	if !strings.Contains(out, "MISSING") {
		t.Error("expected header MISSING")
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
	out := DeployStatus(rows)

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
	out := DeployStatus(nil)
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
	out := DeployStatus(rows)
	if !strings.Contains(out, "—") {
		t.Error("expected em-dash for missing last-failed when no failures")
	}
}
