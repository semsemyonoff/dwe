package render

// Byte-exact goldens for every table renderer. The width budget is 0
// (disabled) whenever the sink is not a TTY and the tests pin the width
// seams to non-TTY, so these files must stay byte-stable: a golden that
// needs regenerating means rendering behavior changed.
//
// Regenerate with:
//
//	make embedded-docs && UPDATE_GOLDEN=1 go test ./internal/core/ui/render/...
//
// These tests must NOT call t.Parallel(): pinGoldenPalette (test_helpers_test.go)
// mutates package-level lipgloss/styles state for the duration of each test,
// and concurrent goldens would race on and corrupt each other's rendered
// output.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// assertGolden compares got against testdata/<name>, writing the file when
// UPDATE_GOLDEN is set. Mirrors the statustui/docstui/cmdbrowser golden
// helper convention.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("creating testdata: %v", err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("writing golden %s: %v", path, err)
		}
		t.Logf("updated golden: %s", path)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden %s (run UPDATE_GOLDEN=1 to create): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("golden %s mismatch:\ngot:\n%s\n\nwant:\n%s", name, got, want)
	}
}

// goldenTableRows returns representative Table() input, including an empty
// cell and an em-dash placeholder.
func goldenTableRows() ([]string, [][]string) {
	headers := []string{"NAME", "STATE", "NOTE"}
	rows := [][]string{
		{"app-main", "enabled", "primary service"},
		{"app-second", "disabled", "—"},
		{"app-third", "enabled", ""},
	}
	return headers, rows
}

func TestGolden_Table(t *testing.T) {
	pinGoldenPalette(t)
	headers, rows := goldenTableRows()
	assertGolden(t, "table.golden", Table(headers, rows))
}

// goldenDaemonRows returns representative DaemonTable() input, including an
// empty PARAMS cell that renders as an em-dash placeholder.
func goldenDaemonRows() []DaemonTableRow {
	return []DaemonTableRow{
		{ID: "services.main.queue", Params: "name=default", Container: "proj-php_queue_default", Uptime: "5m12s"},
		{ID: "services.main.scheduler", Params: "", Container: "proj-php_scheduler", Uptime: "1h3m0s"},
	}
}

func TestGolden_DaemonTable(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "daemon_table.golden", DaemonTable(goldenDaemonRows()))
}

// goldenDeployStatusRows returns representative DeployStatus() input,
// including a row with no LAST FAILED entry (em-dash placeholder).
func goldenDeployStatusRows() []DeployStatusRow {
	return []DeployStatusRow{
		{Service: "main", Status: "deployed", ConfigDelta: "ok", PrevHashShort: "abc12345", CurrHashShort: "abc12345"},
		{
			Service: "db", Status: "failed", ConfigDelta: "changed",
			PrevHashShort: "old12345", CurrHashShort: "new12345",
			LastFailedPhase: "setup", LastFailedStep: "init-db",
		},
		{Service: "cache", Status: "not_deployed", ConfigDelta: "missing", PrevHashShort: "—", CurrHashShort: "—"},
	}
}

func TestGolden_DeployStatus(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "deploy_status.golden", DeployStatus(goldenDeployStatusRows()))
}

// goldenGitWorkspaceRows returns representative GitWorkspace() input,
// including a blank row (service with no own .git — all em-dash cells).
func goldenGitWorkspaceRows() []statusview.GitWorkspaceRow {
	return []statusview.GitWorkspaceRow{
		{Service: "app", Dir: "./services/app", Branch: "main", SHA: "abcdef12", Dirty: true, AheadBehind: "+1/-2"},
		{Service: "worker", Dir: "./services/worker", Branch: "feature/x", SHA: "deadbeef", Dirty: false, AheadBehind: "+0/-0"},
		{Service: "static", Dir: "./services/static"},
	}
}

func TestGolden_GitWorkspace(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "git_workspace.golden", GitWorkspace(goldenGitWorkspaceRows()))
}

// goldenServicesRows returns representative ServicesTable() input covering
// all three states (mandatory, enabled, disabled), running and stopped rows,
// a custom extra column, an empty Dir (em-dash placeholder), and an empty
// Hosts/Ports map (em-dash placeholder).
func goldenServicesRows() []ServiceTableRow {
	return []ServiceTableRow{
		{
			Name: "db", Icon: "🐘", Dir: "./services/db", Container: "proj-db",
			Hosts:     map[string]string{"web": "db.local"},
			Ports:     map[string]int{"pg": 5432},
			Mandatory: true, Running: true,
			Extras: map[string]string{"TAG": "16"},
		},
		{
			Name: "queue", Dir: "./services/queue", Container: "proj-queue",
			Mandatory: true, Running: false,
			Extras: map[string]string{"TAG": "3.12"},
		},
		{
			Name: "api", Dir: "./services/api", Container: "proj-api",
			Hosts:   map[string]string{"web": "api.local", "admin": "api-admin.local"},
			Ports:   map[string]int{"http": 8080, "grpc": 9090},
			Enabled: true, Running: true,
			Extras: map[string]string{"TAG": "v1.2"},
		},
		{
			Name: "worker", Dir: "", Container: "proj-worker",
			Enabled: true, Running: false,
			Extras: map[string]string{"TAG": "v1.0"},
		},
		{
			Name: "debug-tool", Dir: "", Container: "",
			Extras: map[string]string{},
		},
	}
}

func TestGolden_ServicesTable_WithDirCol(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "services_table_dircol.golden", ServicesTable(goldenServicesRows(), []string{"TAG"}, true))
}

func TestGolden_ServicesTable_NoDirCol(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "services_table_nodircol.golden", ServicesTable(goldenServicesRows(), []string{"TAG"}, false))
}

// goldenDiagnosticsRows returns representative DiagnosticRow input spanning
// multiple domains, including a long hadolint-style URL hint and a deep path
// that triggers wrapPath.
func goldenDiagnosticsRows() []DiagnosticRow {
	return []DiagnosticRow{
		{Severity: validate.SeverityOK, Domain: "config", Target: "config.dwe", File: "workspace.yml"},
		{
			Severity: validate.SeverityError,
			Domain:   "linters",
			Target:   "hadolint",
			File:     "services/admin/docker/images/base/vendor/Dockerfile",
			Message:  "Non-numeric user-id may not be resolvable by host system (DL3066)",
			Hint:     "https://github.com/hadolint/hadolint/wiki/DL3066",
		},
		{
			Severity: validate.SeverityWarning,
			Domain:   "env",
			Target:   "env.vars",
			File:     "",
			Message:  "missing default for optional variable",
		},
	}
}

func TestGolden_DiagnosticsTable(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "diagnostics_table.golden", DiagnosticsTable(goldenDiagnosticsRows()))
}

func TestGolden_DiagnosticsByDomain(t *testing.T) {
	pinGoldenPalette(t)
	assertGolden(t, "diagnostics_by_domain.golden", DiagnosticsByDomain(goldenDiagnosticsRows()))
}

// TestGolden_StableAcrossRuns re-renders every golden input a second time
// under the same pinned palette and asserts byte-for-byte stability, and
// that the pinned TrueColor profile actually produced ANSI escapes (a golden
// captured under an accidentally-downgraded profile would silently hide
// every color regression).
func TestGolden_StableAcrossRuns(t *testing.T) {
	pinGoldenPalette(t)
	headers, rows := goldenTableRows()

	renderers := map[string]func() string{
		"table":                 func() string { return Table(headers, rows) },
		"daemon_table":          func() string { return DaemonTable(goldenDaemonRows()) },
		"deploy_status":         func() string { return DeployStatus(goldenDeployStatusRows()) },
		"git_workspace":         func() string { return GitWorkspace(goldenGitWorkspaceRows()) },
		"services_table":        func() string { return ServicesTable(goldenServicesRows(), []string{"TAG"}, true) },
		"diagnostics_table":     func() string { return DiagnosticsTable(goldenDiagnosticsRows()) },
		"diagnostics_by_domain": func() string { return DiagnosticsByDomain(goldenDiagnosticsRows()) },
	}

	for name, render := range renderers {
		first := render()
		second := render()
		if first != second {
			t.Errorf("%s: output not stable across two consecutive runs", name)
		}
		if !strings.ContainsRune(first, 0x1b) {
			t.Errorf("%s: expected ANSI escape sequences under a pinned TrueColor profile, got none:\n%s", name, first)
		}
	}
}
