package render_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/ui/render"
)

// marker is a syntactically valid ENC[age:…] scalar. The renderer only ever
// pattern-matches markers, so no real ciphertext is needed here.
const marker = "ENC[age:YWJjZA==]"

func TestMaskSecretValue(t *testing.T) {
	tests := []struct {
		name        string
		value       any
		want        any
		wantMasked  bool
		wantSameRef bool
	}{
		{name: "plain scalar", value: "localhost", want: "localhost", wantSameRef: true},
		{name: "non-string scalar", value: 42, want: 42, wantSameRef: true},
		{name: "nil", value: nil, want: nil, wantSameRef: true},
		{name: "marker", value: marker, want: render.EncryptedPlaceholder, wantMasked: true},
		{
			name:  "marker inside text is data",
			value: "prefix " + marker + " suffix",
			want:  "prefix " + marker + " suffix",
		},
		{
			name:       "mapping leaf",
			value:      map[string]any{"host": "db", "pass": marker},
			want:       map[string]any{"host": "db", "pass": render.EncryptedPlaceholder},
			wantMasked: true,
		},
		{
			name:       "sequence element",
			value:      []any{"a", marker},
			want:       []any{"a", render.EncryptedPlaceholder},
			wantMasked: true,
		},
		{
			name:       "nested",
			value:      map[string]any{"db": map[string]any{"tokens": []any{marker}}},
			want:       map[string]any{"db": map[string]any{"tokens": []any{render.EncryptedPlaceholder}}},
			wantMasked: true,
		},
		{
			// yaml.v3 demotes a mapping with one non-string key to map[any]any,
			// which is legal inside vars: — the marker must not reach the
			// terminal just because the sibling key is an integer.
			name:       "non-string-keyed mapping",
			value:      map[any]any{8080: marker, "host": "db"},
			want:       map[any]any{8080: render.EncryptedPlaceholder, "host": "db"},
			wantMasked: true,
		},
		{
			name:       "marker under a non-string-keyed mapping",
			value:      map[string]any{"ports": map[any]any{8080: marker}},
			want:       map[string]any{"ports": map[any]any{8080: render.EncryptedPlaceholder}},
			wantMasked: true,
		},
		{
			name:        "clean non-string-keyed mapping is untouched",
			value:       map[any]any{8080: "open"},
			want:        map[any]any{8080: "open"},
			wantSameRef: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, masked := render.MaskSecretValue(tc.value)
			if masked != tc.wantMasked {
				t.Errorf("masked = %v, want %v", masked, tc.wantMasked)
			}
			if !equalValue(got, tc.want) {
				t.Errorf("value = %#v, want %#v", got, tc.want)
			}
			// A marker-free composite must come back as the caller's own
			// value, not a copy: MaskSecretValue runs on every `dwe vars`
			// render, and copying the whole merged tree each time is the cost
			// the "copied only when it contains a marker" contract avoids.
			if tc.wantSameRef && !sameRef(got, tc.value) {
				t.Errorf("value was copied although it holds no marker: %#v", got)
			}
		})
	}
}

// TestMaskSecretValueDoesNotMutate pins that masking copies rather than
// rewriting the caller's map — the merged config is shared with every other
// consumer, so an in-place mask would blank the value everywhere.
func TestMaskSecretValueDoesNotMutate(t *testing.T) {
	src := map[string]any{"pass": marker}
	got, masked := render.MaskSecretValue(src)
	if !masked {
		t.Fatal("expected masked")
	}
	if src["pass"] != marker {
		t.Errorf("source mutated: %v", src["pass"])
	}
	if m, _ := got.(map[string]any); m["pass"] != render.EncryptedPlaceholder {
		t.Errorf("copy not masked: %#v", got)
	}
}

// sameRef reports whether two values are the same composite (or the same
// scalar). reflect.ValueOf().Pointer() is defined for maps and slices, which is
// what the no-copy contract is about; everything else falls back to equality.
func sameRef(a, b any) bool {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	if !va.IsValid() || !vb.IsValid() {
		return a == b
	}
	if va.Kind() != vb.Kind() {
		return false
	}
	switch va.Kind() {
	case reflect.Map, reflect.Slice:
		return va.Pointer() == vb.Pointer()
	default:
		return a == b
	}
}

func equalValue(a, b any) bool {
	switch ta := a.(type) {
	case map[string]any:
		tb, ok := b.(map[string]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for k, v := range ta {
			if !equalValue(v, tb[k]) {
				return false
			}
		}
		return true
	case map[any]any:
		tb, ok := b.(map[any]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for k, v := range ta {
			if !equalValue(v, tb[k]) {
				return false
			}
		}
		return true
	case []any:
		tb, ok := b.([]any)
		if !ok || len(ta) != len(tb) {
			return false
		}
		for i, v := range ta {
			if !equalValue(v, tb[i]) {
				return false
			}
		}
		return true
	default:
		return a == b
	}
}

func TestVarLayerBadge(t *testing.T) {
	tests := []struct {
		layer     string
		encrypted bool
		want      string
	}{
		{layer: "local", want: "local"},
		{layer: "default", want: "default"},
		{layer: "", want: ""},
		{layer: "local", encrypted: true, want: "local (encrypted)"},
		{layer: "default", encrypted: true, want: "default (encrypted)"},
		{layer: "", encrypted: true, want: "encrypted"},
	}
	for _, tc := range tests {
		if got := render.VarLayerBadge(tc.layer, tc.encrypted); got != tc.want {
			t.Errorf("VarLayerBadge(%q, %v) = %q, want %q", tc.layer, tc.encrypted, got, tc.want)
		}
	}
}

func TestRenderVarsListMasksEncrypted(t *testing.T) {
	out := render.VarsList([]render.VarListItem{
		{Path: "vars.app.name", Value: "myapp", Layer: "default"},
		{Path: "vars.api.token", Value: marker, Layer: "default", Encrypted: true},
	}, "")

	if strings.Contains(out, "ENC[age:") {
		t.Errorf("list leaked ciphertext:\n%s", out)
	}
	for _, want := range []string{render.EncryptedPlaceholder, "[default (encrypted)]", "myapp"} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing %q:\n%s", want, out)
		}
	}
}

func TestRenderVarInspectSecret(t *testing.T) {
	out := render.VarInspectView(render.VarInspect{
		Path:      "vars.api.token",
		Default:   marker,
		DefaultOK: true,
		Current:   marker,
		CurrentOK: true,
		Origin:    "workspace/defaults.yml",
		Secret:    "unresolved — no identity for age1test",
	}, 80)

	if strings.Contains(out, "ENC[age:") {
		t.Errorf("inspect leaked ciphertext:\n%s", out)
	}
	for _, want := range []string{render.EncryptedPlaceholder, "Secret", "no identity for age1test"} {
		if !strings.Contains(out, want) {
			t.Errorf("inspect missing %q:\n%s", want, out)
		}
	}
	// A var that is not a secret keeps the historical block (no Secret row).
	plain := render.VarInspectView(render.VarInspect{
		Path: "vars.db.host", Default: "localhost", DefaultOK: true,
		Current: "localhost", CurrentOK: true, Origin: "workspace.yml",
	}, 80)
	if strings.Contains(plain, "Secret") {
		t.Errorf("non-secret inspect grew a Secret row:\n%s", plain)
	}
}
