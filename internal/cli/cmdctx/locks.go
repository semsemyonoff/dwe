package cmdctx

import (
	"errors"
	"fmt"

	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

// AcquireProjectLocksOrReport wraps [lock.AcquireProjectLocks] with a
// user-friendly error path for the held-lock case.
//
// On *lock.ProjectLockHeldError the error message is printed via w.Error and
// the typed error is returned unchanged (preserving its ExitCode of 2).
// Any other error is wrapped with "acquiring project locks: %w".
func AcquireProjectLocksOrReport(baseDir string, w *render.Writer) (release func(), err error) {
	release, err = lock.AcquireProjectLocks(baseDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			w.Error(phe.Error())
			return nil, phe
		}
		return nil, fmt.Errorf("acquiring project locks: %w", err)
	}
	return release, nil
}
