package runio

import (
	"context"
	"os/exec"

	"github.com/semsemyonoff/dwe/internal/shared/trace"
)

// TraceCommand echoes c's argv at Verbose+ through the trace sink, exactly as
// it is about to be spawned. Every runner calls it immediately before wiring
// the child's stdio (WireChildIO), so the echo lands ahead of the child's first
// byte and never races a parallel sub-step's PTY pump.
//
// ctx must be the context the runner received: inside a parallel sub-step it
// carries the per-sub-step printer (trace.WithLinePrinter), inside a pipeline
// the global printer frames the line above the footer, and outside both it
// falls through to stderr. Redaction happens inside trace.Command.
func TraceCommand(ctx context.Context, c *exec.Cmd) {
	if c == nil || len(c.Args) == 0 {
		return
	}
	trace.Command(ctx, c.Args[0], c.Args[1:]...)
}
