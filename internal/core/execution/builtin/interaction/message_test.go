package interaction

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"devbox-cli/internal/core/execution/builtin/spec"

	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/shared/render"
)

// makeMessageCtx returns an spec.ExecContext with a captured output buffer.
func makeMessageCtx(t *testing.T) (spec.ExecContext, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	ctx := spec.ExecContext{
		Config:      &config.DevboxConfig{},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(buf),
	}
	return ctx, buf
}

// ---- Validate ---------------------------------------------------------------

func TestMessageValidate(t *testing.T) {
	b := Message{}

	cases := []struct {
		name    string
		with    map[string]any
		wantErr bool
	}{
		{"missing level", map[string]any{"text": "hello"}, true},
		{"missing text", map[string]any{"level": "info"}, true},
		{"empty level", map[string]any{"level": "", "text": "hi"}, true},
		{"empty text", map[string]any{"level": "info", "text": ""}, true},
		{"invalid level", map[string]any{"level": "debug", "text": "hi"}, true},
		{"valid info", map[string]any{"level": "info", "text": "hello"}, false},
		{"valid success", map[string]any{"level": "success", "text": "ok"}, false},
		{"valid warning", map[string]any{"level": "warning", "text": "warn"}, false},
		{"valid error", map[string]any{"level": "error", "text": "err"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := b.Validate(tc.with)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

// ---- Describe ---------------------------------------------------------------

func TestMessageDescribe(t *testing.T) {
	b := Message{}
	got := b.Describe(map[string]any{"level": "info", "text": "hello"})
	want := "builtin: message(level=info, text=hello)"
	if got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
}

// ---- Run: all four levels ---------------------------------------------------

func TestMessageRun_AllLevels(t *testing.T) {
	b := Message{}

	levels := []string{"info", "success", "warning", "error"}
	for _, level := range levels {
		t.Run(level, func(t *testing.T) {
			ctx, buf := makeMessageCtx(t)
			err := b.Run(context.Background(), map[string]any{"level": level, "text": "test message"}, ctx)
			if err != nil {
				t.Fatalf("Run() unexpected error: %v", err)
			}
			if !strings.Contains(buf.String(), "test message") {
				t.Errorf("output does not contain message text; got: %q", buf.String())
			}
		})
	}
}

// ---- Run: template evaluation in text --------------------------------------

func TestMessageRun_TemplateEvaluation(t *testing.T) {
	b := Message{}
	ctx, buf := makeMessageCtx(t)
	ctx.Config = &config.DevboxConfig{
		Project: config.ProjectConfig{Name: "myproject"},
	}

	err := b.Run(context.Background(), map[string]any{
		"level": "info",
		"text":  "project is {{.Project.Name}}",
	}, ctx)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "myproject") {
		t.Errorf("template not evaluated; output: %q", buf.String())
	}
}

func TestMessageRun_PlainTextNoTemplate(t *testing.T) {
	b := Message{}
	ctx, buf := makeMessageCtx(t)

	err := b.Run(context.Background(), map[string]any{"level": "success", "text": "no template here"}, ctx)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "no template here") {
		t.Errorf("plain text not found; output: %q", buf.String())
	}
}
