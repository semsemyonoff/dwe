package pipeline

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/filesgate"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

func testRenderCtx() *tpl.RenderContext {
	return &tpl.RenderContext{
		Raw: map[string]any{
			"vars": map[string]any{
				"source": map[string]any{
					"repo": "https://example.com/repo.git",
					"dir":  "app",
				},
				"timeouts": map[string]any{
					"deploy": "90s",
				},
				"nested": map[string]any{
					"list": []any{
						map[string]any{"branch": "${vars.source.dir}"},
					},
				},
			},
			"project": map[string]any{"name": "demo"},
		},
	}
}

func TestHasKnownVarRef(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"known head vars", "${vars.source.repo}", true},
		{"known head project", "clone ${project.name}", true},
		{"unknown head shell style", "${HOME}", false},
		{"no ref at all", "echo hello", false},
		{"go template only, no dollar-brace", "{{.State.Status}}", false},
		{"mixed known and unknown", "${HOME} ${vars.x}", true},
		{"mixed unknown and go-template", "{{.State.Status}} ${CONTAINER}", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasKnownVarRef(tc.in); got != tc.want {
				t.Errorf("hasKnownVarRef(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderIfKnown(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("known head resolves", func(t *testing.T) {
		got, err := renderIfKnown("git clone ${vars.source.repo} ${vars.source.dir}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "git clone https://example.com/repo.git app"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("unknown head stays literal", func(t *testing.T) {
		got, err := renderIfKnown("echo ${HOME}", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "echo ${HOME}" {
			t.Errorf("got %q, want unchanged", got)
		}
	})

	t.Run("bare go-template idiom bypasses the renderer", func(t *testing.T) {
		in := "docker inspect -f '{{.State.Status}}' x"
		got, err := renderIfKnown(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error (should never enter the template engine): %v", err)
		}
		if got != in {
			t.Errorf("got %q, want unchanged %q", got, in)
		}
	})

	t.Run("mixed go-template and unknown-head stays unchanged", func(t *testing.T) {
		in := "docker inspect -f '{{.State.Status}}' ${CONTAINER}"
		got, err := renderIfKnown(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error (should never enter the template engine): %v", err)
		}
		if got != in {
			t.Errorf("got %q, want unchanged %q", got, in)
		}
	})

	t.Run("known head with malformed template errors", func(t *testing.T) {
		_, err := renderIfKnown("${vars.x}{{ if }}", ctx)
		if err == nil {
			t.Fatal("expected a render error, got nil")
		}
	})

	t.Run("empty string is a no-op", func(t *testing.T) {
		got, err := renderIfKnown("", ctx)
		if err != nil || got != "" {
			t.Errorf("got (%q, %v), want (\"\", nil)", got, err)
		}
	})
}

func TestRenderValue(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("recurses into nested maps and sequences", func(t *testing.T) {
		in := map[string]any{
			"repo": "${vars.source.repo}",
			"opts": map[string]any{
				"branch": "${vars.source.dir}",
				"tags":   []any{"${vars.source.dir}", "static"},
			},
			"count": 3,
		}
		got, err := renderValue(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		out, ok := got.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", got)
		}
		if out["repo"] != "https://example.com/repo.git" {
			t.Errorf("repo = %v", out["repo"])
		}
		opts, ok := out["opts"].(map[string]any)
		if !ok {
			t.Fatalf("expected nested map, got %T", out["opts"])
		}
		if opts["branch"] != "app" {
			t.Errorf("branch = %v", opts["branch"])
		}
		tags, ok := opts["tags"].([]any)
		if !ok || len(tags) != 2 || tags[0] != "app" || tags[1] != "static" {
			t.Errorf("tags = %v", opts["tags"])
		}
		if out["count"] != 3 {
			t.Errorf("count = %v, want unchanged 3", out["count"])
		}

		// Original input must be untouched (never mutated in place).
		if in["repo"] != "${vars.source.repo}" {
			t.Errorf("input mutated: repo = %v", in["repo"])
		}
		origOpts := in["opts"].(map[string]any)
		if origOpts["branch"] != "${vars.source.dir}" {
			t.Errorf("input mutated: branch = %v", origOpts["branch"])
		}
	})

	t.Run("non-string scalars pass through unchanged", func(t *testing.T) {
		for _, v := range []any{42, true, 3.14, nil} {
			got, err := renderValue(v, ctx)
			if err != nil {
				t.Fatalf("unexpected error for %v: %v", v, err)
			}
			if got != v {
				t.Errorf("renderValue(%v) = %v, want unchanged", v, got)
			}
		}
	})

	// Regression: a with: leaf authored as a pure Go template carries no
	// known-head ${...}, but usercommands.BuildRunContext used to render every
	// with: value at exec time and the pipeline now hands the map to
	// BuildPreRenderedRunContext, which never re-renders. Gating on the known
	// head alone would silently pass the literal template text to the command.
	t.Run("go-template-only leaves are still rendered", func(t *testing.T) {
		got, err := renderValue(`{{ resolve .Raw "vars.source.repo" }}`, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "https://example.com/repo.git" {
			t.Errorf("got %v, want the resolved repo", got)
		}
	})

	t.Run("leaves with neither form are untouched", func(t *testing.T) {
		got, err := renderValue("plain ${HOME} value", ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "plain ${HOME} value" {
			t.Errorf("got %v, want unchanged", got)
		}
	})
}

func TestRenderWith(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("nil/empty with is returned as-is", func(t *testing.T) {
		got, err := renderWith(nil, ctx)
		if err != nil || got != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("renders and does not mutate the input", func(t *testing.T) {
		in := map[string]any{"repo": "${vars.source.repo}"}
		got, err := renderWith(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got["repo"] != "https://example.com/repo.git" {
			t.Errorf("repo = %v", got["repo"])
		}
		if in["repo"] != "${vars.source.repo}" {
			t.Errorf("input mutated: %v", in["repo"])
		}
	})
}

func TestRenderAction(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("nil action renders to nil", func(t *testing.T) {
		got, err := renderAction(nil, ctx)
		if err != nil || got != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("renders cmd and with into a fresh copy", func(t *testing.T) {
		in := &config.Action{Type: "shell", Cmd: "echo ${vars.source.dir}", With: map[string]any{"x": "${vars.source.repo}"}}
		got, err := renderAction(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == in {
			t.Fatal("expected a new *Action, got the same pointer")
		}
		if got.Cmd != "echo app" {
			t.Errorf("Cmd = %q", got.Cmd)
		}
		if got.With["x"] != "https://example.com/repo.git" {
			t.Errorf("With[x] = %v", got.With["x"])
		}
		if in.Cmd != "echo ${vars.source.dir}" {
			t.Errorf("input mutated: Cmd = %q", in.Cmd)
		}
		if in.With["x"] != "${vars.source.repo}" {
			t.Errorf("input mutated: With[x] = %v", in.With["x"])
		}
	})
}

func TestRenderFilesGate(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("nil gate renders to nil", func(t *testing.T) {
		got, err := renderFilesGate(nil, ctx)
		if err != nil || got != nil {
			t.Errorf("got (%v, %v), want (nil, nil)", got, err)
		}
	})

	t.Run("renders command and with, preserves State, does not mutate input", func(t *testing.T) {
		in := &filesgate.FilesGate{
			Command: "cmd-${vars.source.dir}",
			With:    map[string]any{"x": "${vars.source.repo}"},
			State:   filesgate.StateReadable,
		}
		got, err := renderFilesGate(in, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == in {
			t.Fatal("expected a new *FilesGate, got the same pointer")
		}
		if got.Command != "cmd-app" {
			t.Errorf("Command = %q", got.Command)
		}
		if got.With["x"] != "https://example.com/repo.git" {
			t.Errorf("With[x] = %v", got.With["x"])
		}
		if got.State != filesgate.StateReadable {
			t.Errorf("State = %v, want preserved", got.State)
		}
		if in.Command != "cmd-${vars.source.dir}" {
			t.Errorf("input mutated: Command = %q", in.Command)
		}
	})
}

func TestRenderStepFields(t *testing.T) {
	ctx := testRenderCtx()

	t.Run("cmd with known heads resolves substituted", func(t *testing.T) {
		step := config.DeployStep{
			Type: "shell",
			Cmd:  "git clone ${vars.source.repo} ${vars.source.dir}",
		}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "git clone https://example.com/repo.git app"
		if got.Cmd != want {
			t.Errorf("Cmd = %q, want %q", got.Cmd, want)
		}
	})

	t.Run("command step's with renders too", func(t *testing.T) {
		step := config.DeployStep{
			Type: "command",
			Cmd:  "source_clone",
			With: map[string]any{"repo": "${vars.source.repo}"},
		}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.With["repo"] != "https://example.com/repo.git" {
			t.Errorf("With[repo] = %v", got.With["repo"])
		}
		if step.With["repo"] != "${vars.source.repo}" {
			t.Errorf("input With mutated: %v", step.With["repo"])
		}
	})

	t.Run("nested with value at depth >= 2 renders", func(t *testing.T) {
		step := config.DeployStep{
			Type: "command",
			Cmd:  "source_clone",
			With: map[string]any{
				"opts": map[string]any{
					"nested": []any{
						map[string]any{"branch": "${vars.source.dir}"},
					},
				},
			},
		}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		opts := got.With["opts"].(map[string]any)
		list := opts["nested"].([]any)
		leaf := list[0].(map[string]any)
		if leaf["branch"] != "app" {
			t.Errorf("nested branch = %v, want %q", leaf["branch"], "app")
		}
	})

	t.Run("unknown head cmd keeps literal", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "echo ${HOME}"}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cmd != "echo ${HOME}" {
			t.Errorf("Cmd = %q, want unchanged", got.Cmd)
		}
	})

	t.Run("bare go-template cmd resolves unchanged", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "docker inspect -f '{{.State.Status}}' x"}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cmd != step.Cmd {
			t.Errorf("Cmd = %q, want unchanged %q", got.Cmd, step.Cmd)
		}
	})

	t.Run("mixed go-template and unknown-head cmd resolves unchanged", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "docker inspect -f '{{.State.Status}}' ${CONTAINER}"}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Cmd != step.Cmd {
			t.Errorf("Cmd = %q, want unchanged %q", got.Cmd, step.Cmd)
		}
	})

	t.Run("check cmd and with render into a fresh copy", func(t *testing.T) {
		step := config.DeployStep{
			Type: "shell",
			Cmd:  "true",
			Check: &config.Action{
				Type: "shell",
				Cmd:  "test -d ${vars.source.dir}",
				With: map[string]any{"x": "${vars.source.repo}"},
			},
		}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Check == step.Check {
			t.Fatal("expected Check to be a fresh copy")
		}
		if got.Check.Cmd != "test -d app" {
			t.Errorf("Check.Cmd = %q", got.Check.Cmd)
		}
		if got.Check.With["x"] != "https://example.com/repo.git" {
			t.Errorf("Check.With[x] = %v", got.Check.With["x"])
		}
		if step.Check.Cmd != "test -d ${vars.source.dir}" {
			t.Errorf("input Check mutated: Cmd = %q", step.Check.Cmd)
		}
	})

	t.Run("files_gate.with renders into a fresh copy", func(t *testing.T) {
		step := config.DeployStep{
			Type: "shell",
			Cmd:  "true",
			FilesGate: &filesgate.FilesGate{
				With:  map[string]any{"x": "${vars.source.repo}"},
				State: filesgate.StateReadable,
			},
		}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.FilesGate == step.FilesGate {
			t.Fatal("expected FilesGate to be a fresh copy")
		}
		if got.FilesGate.With["x"] != "https://example.com/repo.git" {
			t.Errorf("FilesGate.With[x] = %v", got.FilesGate.With["x"])
		}
		if step.FilesGate.With["x"] != "${vars.source.repo}" {
			t.Errorf("input FilesGate mutated: %v", step.FilesGate.With["x"])
		}
	})

	t.Run("timeout with a known head renders", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "true", Timeout: "${vars.timeouts.deploy}"}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Timeout != "90s" {
			t.Errorf("Timeout = %q, want %q", got.Timeout, "90s")
		}
	})

	t.Run("plain timeout is untouched", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "true", Timeout: "30s"}
		got, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Timeout != "30s" {
			t.Errorf("Timeout = %q, want unchanged", got.Timeout)
		}
	})

	t.Run("a render error fails the whole step", func(t *testing.T) {
		step := config.DeployStep{Type: "shell", Cmd: "${vars.x}{{ if }}"}
		_, err := renderStepFields(step, ctx)
		if err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("resolving the same step twice is idempotent", func(t *testing.T) {
		step := config.DeployStep{
			Type: "command",
			Cmd:  "source_clone",
			With: map[string]any{"repo": "${vars.source.repo}"},
			Check: &config.Action{
				Type: "shell",
				Cmd:  "test -d ${vars.source.dir}",
			},
		}
		first, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("first render: unexpected error: %v", err)
		}
		second, err := renderStepFields(step, ctx)
		if err != nil {
			t.Fatalf("second render: unexpected error: %v", err)
		}
		if first.Cmd != second.Cmd || first.With["repo"] != second.With["repo"] || first.Check.Cmd != second.Check.Cmd {
			t.Errorf("render is not idempotent: first=%+v second=%+v", first, second)
		}
		// Re-rendering the ALREADY-RENDERED result must be a no-op — proves the
		// renderer never double-renders substituted values.
		third, err := renderStepFields(first, ctx)
		if err != nil {
			t.Fatalf("third render: unexpected error: %v", err)
		}
		if third.Cmd != first.Cmd || third.With["repo"] != first.With["repo"] {
			t.Errorf("double-render changed the result: first=%+v third=%+v", first, third)
		}
	})
}

func TestRenderStepFields_userCommandMixedFormsUntouched(t *testing.T) {
	// Regression: the pipeline-resolve gate must never change tpl.RenderCommand
	// semantics for the user-command path, which legitimately mixes ${...}
	// with raw {{ }} in one field (documented in docs/reference/templates.md).
	// This test exercises tpl.RenderCommand directly (the command path's own
	// call), not renderStepFields, to pin that behaviour is untouched by this
	// package's gate.
	ctx := &tpl.RenderContext{
		Raw:    map[string]any{"vars": map[string]any{"db": map[string]any{"user": "root"}}},
		Params: map[string]any{"database": "app"},
	}
	got, err := tpl.RenderCommand(`mariadb -u${vars.db.user}{{ with .Params.database }} -D{{ . }}{{ end }}`, ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "mariadb -uroot -Dapp"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
