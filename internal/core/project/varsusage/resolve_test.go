package varsusage

import (
	"reflect"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
)

func TestEnumerateVars(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]any
		want []string
	}{
		{
			name: "nil vars",
			vars: nil,
			want: nil,
		},
		{
			name: "empty vars",
			vars: map[string]any{},
			want: nil,
		},
		{
			name: "flat leaves sorted",
			vars: map[string]any{"b": 1, "a": "x", "c": true},
			want: []string{"vars.a", "vars.b", "vars.c"},
		},
		{
			name: "nested namespaces",
			vars: map[string]any{
				"db": map[string]any{
					"host": "localhost",
					"port": 5432,
				},
				"name": "demo",
			},
			want: []string{"vars.db.host", "vars.db.port", "vars.name"},
		},
		{
			name: "sequence and empty map are leaves",
			vars: map[string]any{
				"list":  []any{"a", "b"},
				"empty": map[string]any{},
			},
			want: []string{"vars.empty", "vars.list"},
		},
		{
			name: "deep nesting",
			vars: map[string]any{
				"a": map[string]any{"b": map[string]any{"c": 1}},
			},
			want: []string{"vars.a.b.c"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EnumerateVars(&config.DweConfig{Vars: tt.vars})
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("EnumerateVars() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnumerateVars_nilConfig(t *testing.T) {
	if got := EnumerateVars(nil); got != nil {
		t.Errorf("EnumerateVars(nil) = %v, want nil", got)
	}
}

func TestResolveVar(t *testing.T) {
	raw := map[string]any{
		"vars": map[string]any{
			"db": map[string]any{
				"host": "localhost",
				"port": 5432,
			},
		},
	}
	cfg := &config.DweConfig{Raw: raw}

	t.Run("leaf scalar", func(t *testing.T) {
		v, ok := ResolveVar(cfg, "vars.db.host")
		if !ok || v != "localhost" {
			t.Errorf("got %v,%v", v, ok)
		}
	})
	t.Run("namespace subtree", func(t *testing.T) {
		v, ok := ResolveVar(cfg, "vars.db")
		if !ok {
			t.Fatal("namespace not found")
		}
		m, isMap := v.(map[string]any)
		if !isMap || m["port"] != 5432 {
			t.Errorf("subtree = %v", v)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, ok := ResolveVar(cfg, "vars.nope"); ok {
			t.Error("want not found")
		}
	})
	t.Run("nil config", func(t *testing.T) {
		if _, ok := ResolveVar(nil, "vars.x"); ok {
			t.Error("want not found for nil config")
		}
	})
}

func TestCoerceScalar(t *testing.T) {
	tests := []struct {
		raw     string
		want    any
		wantErr bool
	}{
		{raw: "true", want: true},
		{raw: "false", want: false},
		{raw: "42", want: 42},
		{raw: "-5", want: -5},
		{raw: "0", want: 0},
		{raw: "1.5", want: 1.5},
		{raw: `"quoted"`, want: "quoted"},
		{raw: `'single'`, want: "single"},
		{raw: `"42"`, want: "42"},     // quoted number stays a string
		{raw: `"true"`, want: "true"}, // quoted bool stays a string
		{raw: "hello", want: "hello"},
		{raw: "yes", want: "yes"}, // YAML 1.2 core schema: string, not bool
		{raw: "no", want: "no"},
		{raw: "on", want: "on"},
		{raw: "off", want: "off"},
		{raw: "0755", want: "0755"}, // leading-zero/octal kept verbatim
		{raw: "01", want: "01"},
		{raw: "00", want: "00"},
		{raw: "0x1F", want: "0x1F"}, // non-canonical int form kept verbatim
		{raw: "+3", want: "+3"},
		{raw: "1.2.3", want: "1.2.3"},
		{raw: ".inf", want: ".inf"},   // non-finite float kept verbatim (not JSON-representable)
		{raw: "-.inf", want: "-.inf"}, // (else `--output json` reads crash)
		{raw: ".nan", want: ".nan"},
		{raw: ".NaN", want: ".NaN"},
		{raw: "2024-01-02", want: "2024-01-02"}, // timestamp kept as string
		{raw: "", want: nil},                    // empty arg → null
		{raw: "null", want: nil},
		{raw: "~", want: nil},
		{raw: `{a: b}`, wantErr: true}, // map rejected
		{raw: `[a, b]`, wantErr: true}, // sequence rejected
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := CoerceScalar(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("CoerceScalar(%q) = %v, want error", tt.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("CoerceScalar(%q): unexpected error %v", tt.raw, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CoerceScalar(%q) = %v (%T), want %v (%T)", tt.raw, got, got, tt.want, tt.want)
			}
		})
	}
}
