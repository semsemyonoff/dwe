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
