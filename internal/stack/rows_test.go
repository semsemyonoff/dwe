package stack

import (
	"slices"
	"testing"

	"devbox-cli/internal/config"
)

// mkTool returns a ServiceConfig with Type=tool and the single-port shape
// used by stack tests pre-unification.
func mkTool(enabled bool, container, host string, port int) config.ServiceConfig {
	svc := config.ServiceConfig{
		Type:      config.ServiceTypeTool,
		Enabled:   enabled,
		Container: container,
	}
	if port != 0 {
		svc.Ports = map[string]int{"main": port}
	}
	if host != "" {
		svc.Hosts = map[string]string{"main": host}
	}
	return svc
}

func TestBuildToolRows_Empty(t *testing.T) {
	cfg := &config.DevboxConfig{}
	rows := BuildToolRows(cfg)
	if rows == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(rows) != 0 {
		t.Errorf("expected empty rows, got %d rows", len(rows))
	}
}

func TestBuildToolRows_NilTools(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: nil,
	}
	rows := BuildToolRows(cfg)
	if rows == nil {
		t.Fatal("expected non-nil slice, got nil")
	}
	if len(rows) != 0 {
		t.Errorf("expected empty rows, got %d rows", len(rows))
	}
}

func TestBuildToolRows_SortedOrder(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"zebra":  mkTool(true, "zebra", "z.local", 9000),
			"apple":  mkTool(false, "apple", "a.local", 9001),
			"middle": mkTool(true, "middle", "m.local", 9002),
		},
	}

	rows := BuildToolRows(cfg)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	names := []string{rows[0].Name, rows[1].Name, rows[2].Name}
	expectedNames := []string{"apple", "middle", "zebra"}
	if !slices.Equal(names, expectedNames) {
		t.Errorf("expected order %v, got %v", expectedNames, names)
	}
}

func TestBuildToolRows_PopulatesFields(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"adminer": mkTool(true, "adminer", "adminer.localhost", 8080),
		},
	}

	rows := BuildToolRows(cfg)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}

	row := rows[0]
	if row.Name != "adminer" {
		t.Errorf("expected Name=adminer, got %q", row.Name)
	}
	if !row.Enabled {
		t.Errorf("expected Enabled=true, got %v", row.Enabled)
	}
	if row.Container != "adminer" {
		t.Errorf("expected Container=adminer, got %q", row.Container)
	}
	if row.Host != "adminer.localhost" {
		t.Errorf("expected Host=adminer.localhost, got %q", row.Host)
	}
	if row.Port != 8080 {
		t.Errorf("expected Port=8080, got %d", row.Port)
	}
}

func TestBuildToolRows_ArbitraryToolNames(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"elasticvue":           mkTool(true, "elasticvue", "elasticvue.localhost", 8050),
			"opensearch_dashboard": mkTool(false, "opensearch-dashboard", "dashboard.localhost", 5601),
		},
	}

	rows := BuildToolRows(cfg)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}

	names := []string{rows[0].Name, rows[1].Name}
	expectedNames := []string{"elasticvue", "opensearch_dashboard"}
	if !slices.Equal(names, expectedNames) {
		t.Errorf("expected names %v, got %v", expectedNames, names)
	}

	elasticvue := rows[0]
	if elasticvue.Enabled != true {
		t.Errorf("expected elasticvue enabled, got %v", elasticvue.Enabled)
	}

	opensearch := rows[1]
	if opensearch.Enabled != false {
		t.Errorf("expected opensearch disabled, got %v", opensearch.Enabled)
	}
}

func TestBuildToolRows_FiltersOutNonTools(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"adminer": mkTool(true, "adminer", "adminer.localhost", 8080),
			"main":    {Type: config.ServiceTypeApp, Container: "app-main"},
			"db":      {Type: config.ServiceTypeInfra, Container: "db"},
		},
	}
	rows := BuildToolRows(cfg)
	if len(rows) != 1 || rows[0].Name != "adminer" {
		t.Errorf("expected only adminer in tool rows, got %v", rows)
	}
}

func TestBuildToolRows_DeterministicOrdering_MultipleInvocations(t *testing.T) {
	cfg := &config.DevboxConfig{
		Services: map[string]config.ServiceConfig{
			"zebra":  mkTool(true, "zebra", "z.local", 9000),
			"apple":  mkTool(false, "apple", "a.local", 9001),
			"banana": mkTool(true, "banana", "b.local", 9002),
			"middle": mkTool(true, "middle", "m.local", 9003),
		},
	}

	expectedNames := []string{"apple", "banana", "middle", "zebra"}

	for range 100 {
		rows := BuildToolRows(cfg)
		if len(rows) != len(expectedNames) {
			t.Fatalf("invocation failed: expected %d rows, got %d", len(expectedNames), len(rows))
		}

		for j, expectedName := range expectedNames {
			if rows[j].Name != expectedName {
				t.Errorf("row %d expected name %q, got %q", j, expectedName, rows[j].Name)
			}
		}
	}
}
