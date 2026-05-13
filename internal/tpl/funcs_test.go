package tpl

import (
	"reflect"
	"testing"
)

// ---- Sprout Function Coverage ----

func TestSproutFunctions(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "hasSuffix",
			template: `{{ hasSuffix ".txt" "hello.txt" }}`,
			want:     "true",
		},
		{
			name:     "default with non-empty",
			template: `{{ default "fallback" "value" }}`,
			want:     "value",
		},
		{
			name:     "default with empty",
			template: `{{ default "fallback" "" }}`,
			want:     "fallback",
		},
		{
			name:     "ternary true",
			template: `{{ ternary "yes" "no" true }}`,
			want:     "yes",
		},
		{
			name:     "ternary false",
			template: `{{ ternary "yes" "no" false }}`,
			want:     "no",
		},
		{
			name:     "regexMatch",
			template: `{{ regexMatch "[a-z]+" "hello" }}`,
			want:     "true",
		},
		{
			name:     "add",
			template: `{{ add 5 3 }}`,
			want:     "8",
		},
		{
			name:     "max",
			template: `{{ max 1 5 3 }}`,
			want:     "5",
		},
		{
			name:     "list and first",
			template: `{{ first (list "a" "b" "c") }}`,
			want:     "a",
		},
		{
			name:     "pathBase",
			template: `{{ pathBase "/a/b/c.txt" }}`,
			want:     "c.txt",
		},
		{
			name:     "pathDir",
			template: `{{ pathDir "/a/b/c.txt" }}`,
			want:     "/a/b",
		},
		{
			name:     "pathBase chained with pathDir",
			template: `{{ pathDir (pathBase "/a/b/c.txt") }}`,
			want:     ".",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tt.template, nil)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- Time Rendering Smoke Test ----

func TestTimeRenderingSmoke(t *testing.T) {
	t.Parallel()
	// Smoke test that sprout's 'now' and 'date' work together.
	// We don't pin exact values — sprout's now/date are sprout's responsibility.
	// Just verify the format matches date pattern YYYY-MM-DD.
	got, err := Render(`{{ now | date "2006-01-02" }}`, nil)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	// Should match YYYY-MM-DD pattern: 4 digits - 2 digits - 2 digits
	if len(got) != 10 || got[4] != '-' || got[7] != '-' {
		t.Errorf("date output %q does not match YYYY-MM-DD pattern", got)
	}
	// Verify all characters are digits or dash
	for i, ch := range got {
		if i == 4 || i == 7 {
			if ch != '-' {
				t.Errorf("char at position %d should be dash, got %q", i, ch)
			}
		} else if ch < '0' || ch > '9' {
			t.Errorf("char at position %d should be digit, got %q", i, ch)
		}
	}
}

// ---- AppURL Regression Tests ----

func TestAppURLRegression(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
	}{
		{
			name:     "http default port",
			template: `{{ appURL "localhost" 80 false }}`,
			want:     "http://localhost",
		},
		{
			name:     "https default port",
			template: `{{ appURL "localhost" 443 true }}`,
			want:     "https://localhost",
		},
		{
			name:     "http custom port",
			template: `{{ appURL "localhost" 3000 false }}`,
			want:     "http://localhost:3000",
		},
		{
			name:     "https custom port",
			template: `{{ appURL "localhost" 8443 true }}`,
			want:     "https://localhost:8443",
		},
		{
			name:     "empty host defaults to localhost",
			template: `{{ appURL "" 3000 false }}`,
			want:     "http://localhost:3000",
		},
		{
			name:     "with path",
			template: `{{ appURL "app.local" 80 false "?SPX_KEY=dev" }}`,
			want:     "http://app.local/?SPX_KEY=dev",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tt.template, nil)
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- Hermetic Boundary Tests ----

func TestHermeticBoundary(t *testing.T) {
	cases := []struct {
		name     string
		template string
		shouldErr bool
	}{
		{
			name:      "env registry not available",
			template:  `{{ env "PATH" }}`,
			shouldErr: true,
		},
		{
			name:      "network registry not available",
			template:  `{{ getHostByName "example.com" }}`,
			shouldErr: true,
		},
		{
			name:      "random registry not available",
			template:  `{{ randAlpha 8 }}`,
			shouldErr: true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Render(tt.template, nil)
			if (err != nil) != tt.shouldErr {
				if tt.shouldErr {
					t.Errorf("expected error, got nil")
				} else {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// ---- Legacy-Removal Negative Tests ----

func TestLegacyRemoval(t *testing.T) {
	cases := []struct {
		name      string
		template  string
		shouldErr bool
		note      string
	}{
		{
			name:      "zero-arg date errors",
			template:  `{{ date }}`,
			shouldErr: true,
			note:      "sprout's date is variadic, errors on zero args",
		},
		{
			name:      "datetime helper removed entirely",
			template:  `{{ datetime }}`,
			shouldErr: true,
			note:      "no sprout equivalent; must use now | date",
		},
		{
			name:      "generic base function removed",
			template:  `{{ base "/x" }}`,
			shouldErr: true,
			note:      "must use pathBase or osBase",
		},
		{
			name:      "generic dir function removed",
			template:  `{{ dir "/x" }}`,
			shouldErr: true,
			note:      "must use pathDir or osDir",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := Render(tt.template, nil)
			if (err != nil) != tt.shouldErr {
				if tt.shouldErr {
					t.Errorf("expected error for %s, got nil", tt.note)
				} else {
					t.Errorf("unexpected error for %s: %v", tt.note, err)
				}
			}
		})
	}
}

// ---- OnceValue Caching Test ----

func TestOnceValueCaching(t *testing.T) {
	t.Parallel()
	// Call FuncMap twice and verify the cached appURL function is the same object.
	fm1 := FuncMap()
	fm2 := FuncMap()

	appURLFn1, ok1 := fm1["appURL"]
	appURLFn2, ok2 := fm2["appURL"]

	if !ok1 || !ok2 {
		t.Fatal("appURL not in FuncMap")
	}

	// Get the function pointers to verify they point to the same underlying function.
	ptr1 := reflect.ValueOf(appURLFn1).Pointer()
	ptr2 := reflect.ValueOf(appURLFn2).Pointer()

	if ptr1 != ptr2 {
		t.Error("appURL function pointer differs between calls; cache did not hit")
	}
}

// ---- Isolation Test (Shallow Clone Defense) ----

func TestCommandFuncMapIsolation(t *testing.T) {
	t.Parallel()
	// Call commandFuncMap (which extends with resolve/resolveMap/resolveFile),
	// then call FuncMap, and verify resolve* entries do not leak into the base.
	cmdFM := commandFuncMap()
	_, hasResolve := cmdFM["resolve"]
	if !hasResolve {
		t.Fatal("commandFuncMap should have 'resolve' entries")
	}

	baseFM := FuncMap()
	_, hasResolveInBase := baseFM["resolve"]
	if hasResolveInBase {
		t.Error("resolve leaked into base FuncMap; shallow clone defense failed")
	}
}
