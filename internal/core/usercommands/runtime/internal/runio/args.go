package runio

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// RenderArgvWithArgs renders an argv vector, splicing ArgsToken element-wise.
//
// The splice is deliberately restricted to an element that is *exactly*
// `${args}`. There, the caller's arguments are already separate argv entries and
// must land as N entries — quoting them into one would hand the child a single
// argument containing spaces. An element that merely *contains* `${args}`
// (`--filter=${args}`) is rendered as an ordinary template expression, where
// tpl's shell-quoting join applies; that is a fringe form, but silently
// producing a mangled argument would be worse than defining it.
//
// An empty argument set splices to nothing, so the token vanishes rather than
// leaving an empty string argument behind — `go test -race ""` would be a
// different (and failing) command from `go test -race`.
func RenderArgvWithArgs(argv []string, rc *tpl.RenderContext) ([]string, error) {
	out := make([]string, 0, len(argv)+len(rc.Args))
	for i, arg := range argv {
		if arg == model.ArgsToken {
			out = append(out, rc.Args...)
			continue
		}
		rendered, err := tpl.RenderCommand(arg, rc)
		if err != nil {
			return nil, fmt.Errorf("render argv[%d]: %w", i, err)
		}
		out = append(out, rendered)
	}
	return out, nil
}
