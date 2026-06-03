package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/notify"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	snapshotpkg "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// snapshotRestoreFn and snapshotRollbackFn are seams for the underlying
// workflow calls so tests can stub them without seeding real archive files.
var (
	snapshotRestoreFn  = snapshotpkg.Restore
	snapshotRollbackFn = snapshotpkg.Rollback
)

// newSnapshotRestoreCmd: `dwe snapshot restore <name> [-y]`.
func newSnapshotRestoreCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		yes    bool
		noLive bool
		silent bool
	)
	cmd := &cobra.Command{
		Use:               "restore <name>",
		Short:             "Restore a snapshot into the current project",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: snapshotNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotRestore(cmd, flags, args[0], yes, noLive, silent, "restore", "snapshot:restore")
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip restore confirmation")
	cmd.Flags().BoolVar(&noLive, "no-live", false, "disable the per-step live UI; emit plain stdout")
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}

// newSnapshotRollbackCmd: `dwe snapshot rollback [-y]`.
func newSnapshotRollbackCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		yes    bool
		noLive bool
		silent bool
	)
	cmd := &cobra.Command{
		Use:          "rollback",
		Short:        "Restore the snapshot named by rollback_target in workspace/snapshot.yml",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotRollback(cmd, flags, yes, noLive, silent)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip rollback confirmation")
	cmd.Flags().BoolVar(&noLive, "no-live", false, "disable the per-step live UI; emit plain stdout")
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}

func runSnapshotRestore(cmd *cobra.Command, flags *cmdctx.RootFlags, name string, yes, noLive, silent bool, operation, notifyOp string) (err error) {
	baseDir := flags.ProjectRoot()

	if err := meta.ValidateName(name); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	if snapCfg == nil {
		return fmt.Errorf("snapshot %s: no workspace/snapshot.yml found at %s", operation, config.SnapshotConfigPath(baseDir))
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}
	_ = reg.ApplyVisibility(cfg, baseDir)

	releaseLocks, err := lock.AcquireProjectLocks(baseDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			render.Stdout().Error(phe.Error())
			return phe
		}
		return fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLocks()

	if !silent {
		start := time.Now()
		ucfg, ucfgErr := userpkg.Load(baseDir)
		if ucfgErr != nil {
			slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
			ucfg = nil
		}
		n := cmdctx.NewNotifier(ucfg)
		defer func() {
			if errors.As(err, new(*snapshotpkg.RestoreCancelledError)) {
				return
			}
			n.Notify(context.Background(), notify.Event{
				Kind:      notify.OpCommand,
				Operation: notifyOp,
				Outcome:   notify.OutcomeFromErr(err),
				Duration:  time.Since(start),
				Err:       err,
				Project:   cfg.Project.Name,
			})
		}()
	}

	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	params := snapshotpkg.RestoreParams{
		Cfg:            cfg,
		SnapCfg:        snapCfg,
		Registry:       reg,
		BaseDir:        baseDir,
		Name:           name,
		SkipConfirm:    yes,
		NonInteractive: !widgets.IsInteractiveFn(os.Stdin),
		Stdout:         stdout,
		Stderr:         stderr,
		Operation:      operation,
		ConfirmRestore: func(rc snapshotpkg.RestoreConfirmContext) (bool, error) {
			if !widgets.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "snapshot restore needs confirmation; pass --yes to proceed non-interactively")
				return false, nil
			}
			prompt := fmt.Sprintf("Restore snapshot %q?", rc.Manifest.Name)
			var notes []string
			if rc.ConfigDiverged {
				notes = append(notes, "config_hash diverged from current project")
			}
			if !rc.ServicesDiff.IsEmpty() {
				notes = append(notes, "services diff: "+snapshotpkg.FormatServicesDiff(rc.ServicesDiff))
			}
			if len(notes) > 0 {
				prompt += " (" + strings.Join(notes, "; ") + ")"
			}
			return widgets.RunConfirm(prompt, "Restore", "Cancel")
		},
		StepObserverFactory: func(steps []model.WorkflowStep) snapshotpkg.StepObserverCloser {
			return newSnapshotLiveObserver("snapshot "+operation+": "+name, noLive, steps)
		},
	}

	var (
		res    *snapshotpkg.RestoreResult
		runErr error
	)
	if operation == "rollback" {
		res, runErr = snapshotRollbackFn(ctx, params)
	} else {
		res, runErr = snapshotRestoreFn(ctx, params)
	}

	if runErr != nil {
		if errors.As(runErr, new(*snapshotpkg.RestoreCancelledError)) {
			_, _ = fmt.Fprintf(stderr, "snapshot %s cancelled\n", operation)
			return runErr
		}
		writeRestoreOutcome(stderr, operation, res)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return &snapshotInterruptedError{wrapped: runErr}
		}
		return runErr
	}

	writeRestoreOutcome(stderr, operation, res)
	// Post-restore state is arbitrary — invalidate the prompt stack cache so
	// the next prompt refresh / `dwe status` reflects ground truth.
	_ = promptcache.Remove(baseDir)
	return nil
}

func runSnapshotRollback(cmd *cobra.Command, flags *cmdctx.RootFlags, yes, noLive, silent bool) error {
	// rollback resolves the target name from snapshot.yml; pass an empty name
	// to runSnapshotRestore and dispatch through snapshotpkg.Rollback below.
	baseDir := flags.ProjectRoot()
	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	if snapCfg == nil {
		return fmt.Errorf("snapshot rollback: no workspace/snapshot.yml found at %s", config.SnapshotConfigPath(baseDir))
	}
	if snapCfg.RollbackTarget == "" {
		return fmt.Errorf("snapshot rollback: rollback_target is not set in workspace/snapshot.yml")
	}
	if err := meta.ValidateName(snapCfg.RollbackTarget); err != nil {
		return fmt.Errorf("snapshot rollback: rollback_target %q in workspace/snapshot.yml: %w", snapCfg.RollbackTarget, err)
	}
	return runSnapshotRestore(cmd, flags, snapCfg.RollbackTarget, yes, noLive, silent, "rollback", "snapshot:rollback")
}

func writeRestoreOutcome(w io.Writer, operation string, res *snapshotpkg.RestoreResult) {
	if res == nil {
		return
	}
	switch res.Status {
	case meta.StatusOk:
		verb := operation + "d"
		if operation == "rollback" {
			verb = "rolled back"
		}
		_, _ = fmt.Fprintf(w, "snapshot %q %s in %dms\n", res.Manifest.Name, verb, res.DurationMs)
	case meta.StatusInterrupted:
		_, _ = fmt.Fprintf(w, "snapshot %s %q interrupted; pre-restore backup kept at %s\n", operation, res.Manifest.Name, res.BackupDir)
	default:
		_, _ = fmt.Fprintf(w, "snapshot %s %q failed; pre-restore backup kept at %s\n", operation, res.Manifest.Name, res.BackupDir)
	}
}
