package render_test

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/varsusage"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
)

func TestRenderVarValue(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    string
		wantErr bool
	}{
		{name: "string scalar", value: "localhost", want: "localhost"},
		{name: "int scalar", value: 42, want: "42"},
		{name: "bool scalar", value: true, want: "true"},
		{name: "nil scalar", value: nil, want: "null"},
		{name: "empty string", value: "", want: `""`},
		{
			name:  "map subtree",
			value: map[string]any{"host": "localhost", "port": 5432},
			want:  "host: localhost\nport: 5432",
		},
		{
			name:  "sequence subtree",
			value: []any{"a", "b"},
			want:  "- a\n- b",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := render.VarValue(tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVarValueStyled(t *testing.T) {
	t.Run("scalar value is preserved (styling is content-transparent)", func(t *testing.T) {
		got, err := render.VarValueStyled("localhost")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, "localhost") {
			t.Errorf("styled scalar missing value: %q", got)
		}
	})
	t.Run("subtree preserves YAML content (styling is content-transparent)", func(t *testing.T) {
		got, err := render.VarValueStyled(map[string]any{"host": "localhost", "port": 5432})
		if err != nil {
			t.Fatal(err)
		}
		// Keys, values, and structure must all survive the colorizer so the
		// subtree stays readable and pipe-equivalent.
		for _, want := range []string{"host", "localhost", "port", "5432"} {
			if !strings.Contains(got, want) {
				t.Errorf("styled subtree missing %q:\n%s", want, got)
			}
		}
		// Line count is unchanged (no lines dropped/merged by the line walker).
		raw, _ := render.VarValue(map[string]any{"host": "localhost", "port": 5432})
		if gotN, rawN := strings.Count(got, "\n"), strings.Count(raw, "\n"); gotN != rawN {
			t.Errorf("line count changed: styled=%d raw=%d", gotN, rawN)
		}
	})
}

func TestVarSetConfirmation(t *testing.T) {
	got := render.VarSetConfirmation("vars.db.port", "5432")
	// The vars. prefix is stripped for display.
	if strings.Contains(got, "vars.db.port") {
		t.Errorf("confirmation should strip the vars. prefix:\n%s", got)
	}
	for _, want := range []string{"✓", "set", "db.port", "=", "5432"} {
		if !strings.Contains(got, want) {
			t.Errorf("confirmation missing %q:\n%s", want, got)
		}
	}
}

func TestRenderVarsList(t *testing.T) {
	items := []render.VarListItem{
		{Path: "vars.db.host", Value: "localhost", Layer: "default"},
		{Path: "vars.db.port", Value: 5432, Layer: "local"},
		{Path: "vars.app.name", Value: "demo", Layer: "default"},
	}

	// Rows display paths with the vars. prefix stripped; the namespace filter
	// argument is still the canonical full path.
	t.Run("unfiltered shows all leaves with values and badges", func(t *testing.T) {
		out := render.VarsList(items, "")
		for _, want := range []string{
			"db.host", "localhost", "[default]",
			"db.port", "5432", "[local]",
			"app.name", "demo",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "vars.db.host") {
			t.Errorf("rows should strip the vars. prefix:\n%s", out)
		}
	})

	t.Run("namespace filter keeps only matching leaves", func(t *testing.T) {
		out := render.VarsList(items, "vars.db")
		if !strings.Contains(out, "db.host") || !strings.Contains(out, "db.port") {
			t.Errorf("filtered output missing db leaves:\n%s", out)
		}
		if strings.Contains(out, "app.name") {
			t.Errorf("filtered output leaked app leaf:\n%s", out)
		}
	})

	t.Run("namespace dot-boundary does not match sibling prefix", func(t *testing.T) {
		extra := append([]render.VarListItem{}, items...)
		extra = append(extra, render.VarListItem{Path: "vars.dbx.host", Value: "x"})
		out := render.VarsList(extra, "vars.db")
		if strings.Contains(out, "dbx.host") {
			t.Errorf("dot-boundary filter leaked vars.dbx.host:\n%s", out)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		if out := render.VarsList(items, "vars.nope"); out != "" {
			t.Errorf("expected empty, got %q", out)
		}
	})

	t.Run("deterministic ordering preserves input order", func(t *testing.T) {
		out := render.VarsList(items, "")
		hostIdx := strings.Index(out, "db.host")
		portIdx := strings.Index(out, "db.port")
		appIdx := strings.Index(out, "app.name")
		if hostIdx >= portIdx || portIdx >= appIdx {
			t.Errorf("ordering not preserved: host=%d port=%d app=%d", hostIdx, portIdx, appIdx)
		}
	})
}

func TestRenderVarInspect(t *testing.T) {
	t.Run("default only, no local override, no usages", func(t *testing.T) {
		out := render.VarInspectView(render.VarInspect{
			Path:      "vars.db.host",
			Default:   "localhost",
			DefaultOK: true,
			Current:   "localhost",
			CurrentOK: true,
			Origin:    "workspace/workspace.yml",
		}, 80)
		for _, want := range []string{
			"db.host", "Default", "localhost",
			"Local", "(not set)", "Current",
			"workspace/workspace.yml",
			"Usages: none found",
			"dynamically-built var paths are not tracked",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("with local override and usages", func(t *testing.T) {
		out := render.VarInspectView(render.VarInspect{
			Path:      "vars.db.host",
			Default:   "localhost",
			DefaultOK: true,
			Local:     "db.internal",
			LocalOK:   true,
			Current:   "db.internal",
			CurrentOK: true,
			Origin:    "workspace/local.yml",
			Usages: []varsusage.Usage{
				{File: "workspace/services/app/deploy.yml", Line: 12, Kind: "template", Text: "cmd: connect ${vars.db.host}"},
				{File: "workspace/info.yml", Line: 3, Kind: "from", Text: "from: vars.db.host"},
			},
		}, 80)
		for _, want := range []string{
			"db.internal",
			"Usages (2):",
			"workspace/services/app/deploy.yml:12",
			"template",
			"workspace/info.yml:3",
			"from",
			"vars.db.host",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("subtree values render inline", func(t *testing.T) {
		out := render.VarInspectView(render.VarInspect{
			Path:      "vars.db",
			Default:   map[string]any{"host": "localhost"},
			DefaultOK: true,
			Current:   map[string]any{"host": "localhost"},
			CurrentOK: true,
			Origin:    "workspace/workspace.yml",
		}, 80)
		if !strings.Contains(out, "host: localhost") {
			t.Errorf("expected inline subtree, got:\n%s", out)
		}
	})
}
