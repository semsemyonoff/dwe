package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeLayerFixture writes a minimal 3-layer project tree (workspace.yml plus
// optional workspace/defaults.yml and workspace/local.yml) and returns the
// workspace.yml path. Empty content for an optional layer skips that file.
func writeLayerFixture(t *testing.T, ws, defaults, local string) string {
	t.Helper()
	dir := t.TempDir()
	wsPath := filepath.Join(dir, "workspace.yml")
	if err := os.WriteFile(wsPath, []byte(ws), 0o644); err != nil {
		t.Fatalf("write workspace.yml: %v", err)
	}
	if defaults != "" || local != "" {
		if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0o755); err != nil {
			t.Fatalf("mkdir workspace/: %v", err)
		}
	}
	if defaults != "" {
		if err := os.WriteFile(filepath.Join(dir, "workspace", "defaults.yml"), []byte(defaults), 0o644); err != nil {
			t.Fatalf("write defaults.yml: %v", err)
		}
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(dir, "workspace", "local.yml"), []byte(local), 0o644); err != nil {
			t.Fatalf("write local.yml: %v", err)
		}
	}
	return wsPath
}

func TestLoadLayers_optionalLayersAbsent(t *testing.T) {
	ws := writeLayerFixture(t, "vars:\n  a: 1\n", "", "")
	layers, err := LoadLayers(ws)
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if len(layers) != 1 {
		t.Fatalf("want 1 layer (workspace.yml only), got %d", len(layers))
	}
	if layers[0].Path != ws {
		t.Fatalf("layer[0].Path = %q, want %q", layers[0].Path, ws)
	}
}

func TestLoadLayers_allThreePresent(t *testing.T) {
	ws := writeLayerFixture(t, "vars:\n  a: 1\n", "vars:\n  b: 2\n", "vars:\n  c: 3\n")
	layers, err := LoadLayers(ws)
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("want 3 layers, got %d", len(layers))
	}
	wantOrder := []string{
		ws,
		LocalLayerPath(ws)[:len(LocalLayerPath(ws))-len("local.yml")] + "defaults.yml",
		LocalLayerPath(ws),
	}
	for i, want := range wantOrder {
		if layers[i].Path != want {
			t.Errorf("layer[%d].Path = %q, want %q", i, layers[i].Path, want)
		}
	}
}

func TestLoadLayers_missingWorkspace(t *testing.T) {
	_, err := LoadLayers(filepath.Join(t.TempDir(), "nope.yml"))
	if err == nil {
		t.Fatal("want error for missing workspace.yml, got nil")
	}
}

func TestResolveLayeredPath(t *testing.T) {
	tests := []struct {
		name      string
		ws        string
		defaults  string
		local     string
		path      string
		wantEff   any
		wantEffOK bool
		wantOrig  string // "ws" | "defaults" | "local" | ""
		check     func(t *testing.T, lv LayeredValue)
	}{
		{
			name:      "default only",
			ws:        "vars:\n  db:\n    host: localhost\n",
			path:      "vars.db.host",
			wantEff:   "localhost",
			wantEffOK: true,
			wantOrig:  "ws",
			check: func(t *testing.T, lv LayeredValue) {
				if lv.Default != "localhost" || !lv.DefaultOK {
					t.Errorf("default = %v,%v", lv.Default, lv.DefaultOK)
				}
				if lv.LocalOK {
					t.Errorf("local should be absent, got %v", lv.Local)
				}
			},
		},
		{
			name:      "local overrides default",
			ws:        "vars:\n  db:\n    host: localhost\n",
			local:     "vars:\n  db:\n    host: 10.0.0.1\n",
			path:      "vars.db.host",
			wantEff:   "10.0.0.1",
			wantEffOK: true,
			wantOrig:  "local",
			check: func(t *testing.T, lv LayeredValue) {
				if lv.Default != "localhost" || !lv.DefaultOK {
					t.Errorf("default = %v,%v", lv.Default, lv.DefaultOK)
				}
				if lv.Local != "10.0.0.1" || !lv.LocalOK {
					t.Errorf("local = %v,%v", lv.Local, lv.LocalOK)
				}
			},
		},
		{
			name:      "defaults layer feeds default",
			ws:        "vars:\n  a: 1\n",
			defaults:  "vars:\n  b: 2\n",
			path:      "vars.b",
			wantEff:   2,
			wantEffOK: true,
			wantOrig:  "defaults",
		},
		{
			name:      "local explicit-null does not override (deepMerge nil-skip)",
			ws:        "vars:\n  db:\n    host: localhost\n",
			local:     "vars:\n  db:\n    host: ~\n",
			path:      "vars.db.host",
			wantEff:   "localhost",
			wantEffOK: true,
			wantOrig:  "ws", // local's null was skipped, so origin stays on ws
			check: func(t *testing.T, lv LayeredValue) {
				if !lv.LocalOK || lv.Local != nil {
					t.Errorf("local should be present-but-nil, got %v,%v", lv.Local, lv.LocalOK)
				}
			},
		},
		{
			name:      "missing path",
			ws:        "vars:\n  a: 1\n",
			path:      "vars.nope",
			wantEff:   nil,
			wantEffOK: false,
			wantOrig:  "",
		},
		{
			name:      "namespace subtree merges layers",
			ws:        "vars:\n  db:\n    host: localhost\n",
			local:     "vars:\n  db:\n    port: 5432\n",
			path:      "vars.db",
			wantEffOK: true,
			wantOrig:  "local",
			check: func(t *testing.T, lv LayeredValue) {
				m, ok := lv.Current.(map[string]any)
				if !ok {
					t.Fatalf("effective subtree = %T, want map", lv.Current)
				}
				if m["host"] != "localhost" || m["port"] != 5432 {
					t.Errorf("merged subtree = %v", m)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ws := writeLayerFixture(t, tt.ws, tt.defaults, tt.local)
			lv, err := ResolveLayeredPath(ws, tt.path)
			if err != nil {
				t.Fatalf("ResolveLayeredPath: %v", err)
			}
			if lv.CurrentOK != tt.wantEffOK {
				t.Errorf("EffectiveOK = %v, want %v", lv.CurrentOK, tt.wantEffOK)
			}
			if tt.wantEff != nil && lv.Current != tt.wantEff {
				t.Errorf("Effective = %v (%T), want %v", lv.Current, lv.Current, tt.wantEff)
			}
			wantOrigin := map[string]string{
				"ws":       ws,
				"defaults": filepath.Join(filepath.Dir(ws), "workspace", "defaults.yml"),
				"local":    LocalLayerPath(ws),
				"":         "",
			}[tt.wantOrig]
			if lv.Origin != wantOrigin {
				t.Errorf("Origin = %q, want %q", lv.Origin, wantOrigin)
			}
			if tt.check != nil {
				tt.check(t, lv)
			}
		})
	}
}

// TestResolveLayeredPath_noLayerMutation guards the deep-copy: resolving must
// not mutate the layer Data maps (the Origin scan reads them after merging).
func TestResolveLayeredPath_noLayerMutation(t *testing.T) {
	ws := writeLayerFixture(t, "vars:\n  db:\n    host: localhost\n", "", "vars:\n  db:\n    port: 5432\n")
	layers, err := LoadLayers(ws)
	if err != nil {
		t.Fatalf("LoadLayers: %v", err)
	}
	resolveLayeredPath(layers, LocalLayerPath(ws), "vars.db")
	// The workspace layer's vars.db must still hold ONLY host (port came from
	// local and must not have leaked into the workspace layer's map).
	wsDB := layers[0].Data["vars"].(map[string]any)["db"].(map[string]any)
	if _, leaked := wsDB["port"]; leaked {
		t.Errorf("workspace layer vars.db was mutated: %v", wsDB)
	}
}
