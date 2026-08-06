package runtime

import (
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"

	"github.com/stretchr/testify/require"
)

// TestNormalizeRenderContext_ArgsDefault pins the central ${args} default.
//
// Review caught this as a per-site rule that one dispatcher had missed: the
// workflow sub-step path builds its RenderContext inline, so a command with
// args.default rendered `go test` there and `go test ./...` under `dwe cmd` —
// a different command line, silently. Normalizing at the single point every
// dispatcher passes through is what makes that unreachable.
func TestNormalizeRenderContext_ArgsDefault(t *testing.T) {
	withDefault := func() *model.CommandDef {
		return &model.CommandDef{
			ID: "x.y", Type: model.CommandTypeShell,
			Argv: []string{"go", "test", "${args}"},
			Args: &model.ArgsSpec{Default: []string{"./..."}},
		}
	}

	t.Run("inline context with no Args gets the default", func(t *testing.T) {
		rc := RunContext{Cmd: withDefault(), Render: &tpl.RenderContext{}}
		normalizeRenderContext(&rc)
		require.Equal(t, []string{"./..."}, rc.Render.Args)
	})

	t.Run("nil context is created and filled", func(t *testing.T) {
		rc := RunContext{Cmd: withDefault(), Config: &config.DweConfig{Raw: map[string]any{"a": 1}}}
		normalizeRenderContext(&rc)
		require.NotNil(t, rc.Render)
		require.Equal(t, []string{"./..."}, rc.Render.Args)
		require.NotNil(t, rc.Render.Raw, "Raw back-fill must still work")
	})

	// The CLI resolves the caller's pass-through args before dispatch;
	// recomputing would throw them away.
	t.Run("caller-supplied args are preserved", func(t *testing.T) {
		rc := RunContext{
			Cmd:    withDefault(),
			Render: &tpl.RenderContext{Args: []string{"./internal/api"}},
		}
		normalizeRenderContext(&rc)
		require.Equal(t, []string{"./internal/api"}, rc.Render.Args)
	})

	t.Run("no args block leaves Args empty", func(t *testing.T) {
		rc := RunContext{
			Cmd:    &model.CommandDef{ID: "x.y", Type: model.CommandTypeShell, Cmd: "true"},
			Render: &tpl.RenderContext{},
		}
		normalizeRenderContext(&rc)
		require.Empty(t, rc.Render.Args)
	})

	t.Run("nil Cmd does not panic", func(t *testing.T) {
		rc := RunContext{Render: &tpl.RenderContext{}}
		require.NotPanics(t, func() { normalizeRenderContext(&rc) })
	})
}
