package tpl

import (
	"bytes"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"text/template"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

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
		// maps registry
		{
			name:     "dict and hasKey",
			template: `{{ dict "a" 1 | hasKey "a" }}`,
			want:     "true",
		},
		// conversion registry
		{
			name:     "toInt",
			template: `{{ toInt "42" }}`,
			want:     "42",
		},
		// semver registry
		{
			name:     "semverCompare",
			template: `{{ semverCompare ">=1.0.0" "1.2.3" }}`,
			want:     "true",
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

// TestSproutSignatures pins the argument order sprout v1.1 enforces: the value
// a function operates on goes last. The legacy cases catch a sprout release
// quietly reintroducing Sprig-order tolerance; the regex cases are written so
// the pre-1.1 `regexp` order would render a different string.
func TestSproutSignatures(t *testing.T) {
	cases := []struct {
		name     string
		template string
		want     string
		wantErr  bool
	}{
		// maps registry
		{
			name:     "get",
			template: `{{ dict "a" 1 | get "a" }}`,
			want:     "1",
		},
		{
			name:     "get legacy order",
			template: `{{ get (dict "a" 1) "a" }}`,
			wantErr:  true,
		},
		{
			name:     "set",
			template: `{{ dict "a" 1 | set "b" 2 | get "b" }}`,
			want:     "2",
		},
		{
			name:     "set legacy order",
			template: `{{ set (dict "a" 1) "b" 2 }}`,
			wantErr:  true,
		},
		{
			name:     "unset",
			template: `{{ dict "a" 1 | unset "a" | hasKey "a" }}`,
			want:     "false",
		},
		{
			name:     "unset legacy order",
			template: `{{ unset (dict "a" 1) "a" }}`,
			wantErr:  true,
		},
		{
			name:     "hasKey",
			template: `{{ dict "a" 1 | hasKey "a" }}`,
			want:     "true",
		},
		{
			name:     "hasKey legacy order",
			template: `{{ hasKey (dict "a" 1) "a" }}`,
			wantErr:  true,
		},
		{
			name:     "pick",
			template: `{{ dict "a" 1 "b" 2 | pick "a" | len }}`,
			want:     "1",
		},
		{
			name:     "pick legacy order",
			template: `{{ pick (dict "a" 1 "b" 2) "a" }}`,
			wantErr:  true,
		},
		{
			name:     "omit",
			template: `{{ dict "a" 1 "b" 2 | omit "a" | hasKey "a" }}`,
			want:     "false",
		},
		{
			name:     "omit legacy order",
			template: `{{ omit (dict "a" 1 "b" 2) "a" }}`,
			wantErr:  true,
		},
		// slices registry
		{
			name:     "append",
			template: `{{ list "a" | append "b" | join "," }}`,
			want:     "a,b",
		},
		{
			name:     "append legacy order",
			template: `{{ append (list "a") "b" }}`,
			wantErr:  true,
		},
		{
			name:     "prepend",
			template: `{{ list "b" | prepend "a" | join "," }}`,
			want:     "a,b",
		},
		{
			name:     "prepend legacy order",
			template: `{{ prepend (list "b") "a" }}`,
			wantErr:  true,
		},
		{
			name:     "slice",
			template: `{{ list "a" "b" "c" | slice 1 2 | join "," }}`,
			want:     "b",
		},
		{
			name:     "slice legacy order",
			template: `{{ slice (list "a" "b" "c") 1 2 }}`,
			wantErr:  true,
		},
		{
			name:     "without",
			template: `{{ list "a" "b" "c" | without "b" | join "," }}`,
			want:     "a,c",
		},
		{
			name:     "without legacy order",
			template: `{{ without (list "a" "b" "c") "b" }}`,
			wantErr:  true,
		},
		// regex registry: the four functions whose order changed from regexp
		{
			name:     "regexReplaceAll",
			template: `{{ regexReplaceAll "a" "o" "banana" }}`,
			want:     "bonono",
		},
		{
			name:     "regexReplaceAllLiteral",
			template: `{{ regexReplaceAllLiteral "a" "$1" "banana" }}`,
			want:     "b$1n$1n$1",
		},
		{
			name:     "regexSplit",
			template: `{{ regexSplit "," -1 "a,b,c" | join "|" }}`,
			want:     "a|b|c",
		},
		{
			name:     "regexFindAll",
			template: `{{ regexFindAll "[0-9]" -1 "a1b2" | join "," }}`,
			want:     "1,2",
		},
		// numeric registry
		{
			name:     "div by zero",
			template: `{{ div 1 0 }}`,
			wantErr:  true,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Render(tt.template, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected a render error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSproutNoticesRouteThroughTrace pins that sprout's own diagnostics never
// reach stdout. Not parallel: it swaps os.Stdout and the process-global trace
// level.
func TestSproutNoticesRouteThroughTrace(t *testing.T) {
	t.Cleanup(func() { trace.Configure(nil, trace.LevelOff) })

	var offBuf, debugBuf bytes.Buffer
	var offErr, debugErr error
	stdout := captureStdout(t, func() {
		// Built inside the capture: sprout's default logger binds os.Stdout at
		// construction, so a map cached before the swap would hide a leak.
		fm := buildFuncMap()
		// addf carries a deprecation notice, logged on every call.
		trace.Configure(&offBuf, trace.LevelOff)
		offErr = execTemplate(fm, `{{ addf 1 2 }}`)
		trace.Configure(&debugBuf, trace.LevelDebug)
		debugErr = execTemplate(fm, `{{ addf 1 2 }}`)
	})

	if offErr != nil || debugErr != nil {
		t.Fatalf("render failed: off=%v debug=%v", offErr, debugErr)
	}
	if stdout != "" {
		t.Errorf("sprout wrote to stdout: %q", stdout)
	}
	if offBuf.Len() != 0 {
		t.Errorf("notice emitted without --debug: %q", offBuf.String())
	}
	if got := debugBuf.String(); !strings.Contains(got, "addf") {
		t.Errorf("notice missing from the debug trace: %q", got)
	}
}

func execTemplate(fm template.FuncMap, src string) error {
	tmpl, err := template.New("").Funcs(fm).Parse(src)
	if err != nil {
		return err
	}
	return tmpl.Execute(io.Discard, nil)
}

// captureStdout returns everything written to os.Stdout while fn runs.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer func() { _ = r.Close() }()

	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()

	orig := os.Stdout
	os.Stdout = w
	func() {
		defer func() { os.Stdout = orig }()
		fn()
	}()
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	return <-out
}

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
			name:     "with path no leading slash",
			template: `{{ appURL "app.local" 80 false "?SPX_KEY=dev" }}`,
			want:     "http://app.local/?SPX_KEY=dev",
		},
		{
			name:     "with path leading slash not doubled",
			template: `{{ appURL "app.local" 80 false "/api/v1" }}`,
			want:     "http://app.local/api/v1",
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

func TestHermeticBoundary(t *testing.T) {
	cases := []struct {
		name      string
		template  string
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
		{
			name:      "shuffle removed from strings registry",
			template:  `{{ shuffle "abc" }}`,
			shouldErr: true,
		},
		{
			name:      "hello removed from std registry",
			template:  `{{ hello }}`,
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
			note:      "sprout date requires 2 args (layout, date), errors on zero args",
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

func TestFuncMapCaching(t *testing.T) {
	t.Parallel()
	// FuncMap() returns a per-call shallow clone: mutations must not bleed across calls.
	fm1 := FuncMap()
	fm2 := FuncMap()

	// Mutation isolation: adding to fm1 must not affect fm2.
	fm1["__probe__"] = func() {}
	if _, leaked := fm2["__probe__"]; leaked {
		t.Error("FuncMap clone is broken: mutation of fm1 polluted fm2")
	}

	// Function values must originate from the same cached buildFuncMap call.
	fm3 := FuncMap()
	p1 := reflect.ValueOf(fm1["appURL"]).Pointer()
	p3 := reflect.ValueOf(fm3["appURL"]).Pointer()
	if p1 != p3 {
		t.Error("appURL function pointer differs across FuncMap calls; OnceValue cache is not hit")
	}
}

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
