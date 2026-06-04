package cmdctx

import (
	"errors"

	pipeline "github.com/semsemyonoff/dwe/internal/core/execution/pipeline"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// WarnSilentLog prints the "Full output saved to" hint when a pipeline run
// failed with the silent sentinel (pipeline.ErrSilent) and on-disk logging was
// enabled. It is the shared tail used by the deploy and reset run paths after
// pipeline.RunWithOptions returns an error.
func WarnSilentLog(w *render.Writer, err error, logEnabled bool, logPath string) {
	if errors.Is(err, pipeline.ErrSilent) && logEnabled {
		w.Warning("Full output saved to: " + logPath)
	}
}
