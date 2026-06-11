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
			got, err := render.RenderVarValue(tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderVarsList(t *testing.T) {
	items := []render.VarListItem{
		{Path: "vars.db.host", Value: "localhost", Layer: "default"},
		{Path: "vars.db.port", Value: 5432, Layer: "local"},
		{Path: "vars.app.name", Value: "demo", Layer: "default"},
	}

	t.Run("unfiltered shows all leaves with values and badges", func(t *testing.T) {
		out := render.RenderVarsList(items, "")
		for _, want := range []string{
			"vars.db.host", "localhost", "[default]",
			"vars.db.port", "5432", "[local]",
			"vars.app.name", "demo",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("namespace filter keeps only matching leaves", func(t *testing.T) {
		out := render.RenderVarsList(items, "vars.db")
		if !strings.Contains(out, "vars.db.host") || !strings.Contains(out, "vars.db.port") {
			t.Errorf("filtered output missing db leaves:\n%s", out)
		}
		if strings.Contains(out, "vars.app.name") {
			t.Errorf("filtered output leaked app leaf:\n%s", out)
		}
	})

	t.Run("namespace dot-boundary does not match sibling prefix", func(t *testing.T) {
		extra := append([]render.VarListItem{}, items...)
		extra = append(extra, render.VarListItem{Path: "vars.dbx.host", Value: "x"})
		out := render.RenderVarsList(extra, "vars.db")
		if strings.Contains(out, "vars.dbx.host") {
			t.Errorf("dot-boundary filter leaked vars.dbx.host:\n%s", out)
		}
	})

	t.Run("no match returns empty", func(t *testing.T) {
		if out := render.RenderVarsList(items, "vars.nope"); out != "" {
			t.Errorf("expected empty, got %q", out)
		}
	})

	t.Run("deterministic ordering preserves input order", func(t *testing.T) {
		out := render.RenderVarsList(items, "")
		hostIdx := strings.Index(out, "vars.db.host")
		portIdx := strings.Index(out, "vars.db.port")
		appIdx := strings.Index(out, "vars.app.name")
		if !(hostIdx < portIdx && portIdx < appIdx) {
			t.Errorf("ordering not preserved: host=%d port=%d app=%d", hostIdx, portIdx, appIdx)
		}
	})
}

func TestRenderVarInspect(t *testing.T) {
	t.Run("author only, no local override, no usages", func(t *testing.T) {
		out := render.RenderVarInspect(render.VarInspect{
			Path:        "vars.db.host",
			Author:      "localhost",
			AuthorOK:    true,
			Effective:   "localhost",
			EffectiveOK: true,
			Origin:      "workspace/workspace.yml",
		}, 80)
		for _, want := range []string{
			"vars.db.host", "Author", "localhost",
			"Local", "(not set)", "Effective",
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
		out := render.RenderVarInspect(render.VarInspect{
			Path:        "vars.db.host",
			Author:      "localhost",
			AuthorOK:    true,
			Local:       "db.internal",
			LocalOK:     true,
			Effective:   "db.internal",
			EffectiveOK: true,
			Origin:      "workspace/local.yml",
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
		out := render.RenderVarInspect(render.VarInspect{
			Path:        "vars.db",
			Author:      map[string]any{"host": "localhost"},
			AuthorOK:    true,
			Effective:   map[string]any{"host": "localhost"},
			EffectiveOK: true,
			Origin:      "workspace/workspace.yml",
		}, 80)
		if !strings.Contains(out, "host: localhost") {
			t.Errorf("expected inline subtree, got:\n%s", out)
		}
	})
}
