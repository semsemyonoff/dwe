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

// AcquireProjectLocksSilent wraps [lock.AcquireProjectLocks] with the same
// error contract as [AcquireProjectLocksOrReport] — a *lock.ProjectLockHeldError
// is returned unchanged (ExitCode 2 preserved), any other error is wrapped with
// "acquiring project locks: %w" — but writes NOTHING. It is the sanctioned
// variant for call sites where a live alt-screen (an in-TUI form overlay) makes
// stderr diagnostics unsafe: printing the lock-held banner mid-frame would
// corrupt the TUI. The caller surfaces the returned error itself (e.g. as an
// in-TUI status flash).
func AcquireProjectLocksSilent(baseDir string) (release func(), err error) {
	release, err = lock.AcquireProjectLocks(baseDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			return nil, phe
		}
		return nil, fmt.Errorf("acquiring project locks: %w", err)
	}
	return release, nil
}
