package stack

import (
	"slices"
	"testing"

	"devbox-cli/internal/config"
)

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
		Tools: config.ToolsConfig(nil),
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
		Tools: config.ToolsConfig{
			"zebra":  {Enabled: true, Container: "zebra", Host: "z.local", Port: 9000},
			"apple":  {Enabled: false, Container: "apple", Host: "a.local", Port: 9001},
			"middle": {Enabled: true, Container: "middle", Host: "m.local", Port: 9002},
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
		Tools: config.ToolsConfig{
			"adminer": {
				Enabled:   true,
				Container: "adminer",
				Host:      "adminer.localhost",
				Port:      8080,
			},
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
		Tools: config.ToolsConfig{
			"elasticvue": {
				Enabled:   true,
				Container: "elasticvue",
				Host:      "elasticvue.localhost",
				Port:      8050,
			},
			"opensearch_dashboard": {
				Enabled:   false,
				Container: "opensearch-dashboard",
				Host:      "dashboard.localhost",
				Port:      5601,
			},
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

func TestBuildToolRows_DeterministicOrdering_MultipleInvocations(t *testing.T) {
	cfg := &config.DevboxConfig{
		Tools: config.ToolsConfig{
			"zebra":  {Enabled: true, Container: "zebra", Host: "z.local", Port: 9000},
			"apple":  {Enabled: false, Container: "apple", Host: "a.local", Port: 9001},
			"banana": {Enabled: true, Container: "banana", Host: "b.local", Port: 9002},
			"middle": {Enabled: true, Container: "middle", Host: "m.local", Port: 9003},
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
