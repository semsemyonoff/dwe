package snapshot

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/notify"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
)

// requireSnapshotConfig returns an error when snapCfg is nil, naming the
// operation and the expected workspace/snapshot.yml path. Shared by
// restore/rollback/remove, which all require an existing snapshot config.
// (create has its own guard — it also requires a create: block.)
func requireSnapshotConfig(snapCfg *config.SnapshotConfig, operation, baseDir string) error {
	if snapCfg == nil {
		return fmt.Errorf("snapshot %s: no workspace/snapshot.yml found at %s", operation, config.SnapshotConfigPath(baseDir))
	}
	return nil
}

// signalAwareContext derives a SIGINT/SIGTERM-aware context from parent,
// defaulting to context.Background when parent is nil. The caller must defer
// the returned stop func. Mirrors signal.NotifyContext's return signature.
func signalAwareContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}

// installSnapshotNotifier wires the end-of-run desktop notification for a
// snapshot command. It loads the user config (notifications disabled on load
// failure), builds a notifier, and returns a finalize closure the caller must
// defer. The closure reads *errp at return time and suppresses the notification
// when isCancelled(*errp) reports a user cancellation (intentional, not a
// failure). When silent is true it is a no-op.
//
// Usage: defer installSnapshotNotifier(baseDir, op, name, silent, &err, isCancelled)()
// — the load happens at defer evaluation, the notify at function return.
func installSnapshotNotifier(
	baseDir, operation, projectName string,
	silent bool,
	errp *error,
	isCancelled func(error) bool,
) func() {
	if silent {
		return func() {}
	}
	start := time.Now()
	ucfg, ucfgErr := userpkg.Load(baseDir)
	if ucfgErr != nil {
		slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
		ucfg = nil
	}
	n := cmdctx.NewNotifier(ucfg)
	return func() {
		if isCancelled(*errp) {
			return
		}
		n.Notify(context.Background(), notify.Event{
			Kind:      notify.OpCommand,
			Operation: operation,
			Outcome:   notify.OutcomeFromErr(*errp),
			Duration:  time.Since(start),
			Err:       *errp,
			Project:   projectName,
		})
	}
}
