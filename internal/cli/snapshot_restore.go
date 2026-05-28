package command

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

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/notify"
	"devbox-cli/internal/core/project/config"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/core/ui"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/core/usercommands/model"
	"devbox-cli/internal/core/workflow/snapshot"
	"devbox-cli/internal/shared/lock"
	"devbox-cli/internal/shared/render"

	"github.com/spf13/cobra"
)

// newSnapshotRestoreCmd: `devbox snapshot restore <name> [-y]`.
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

// newSnapshotRollbackCmd: `devbox snapshot rollback [-y]`.
func newSnapshotRollbackCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		yes    bool
		noLive bool
		silent bool
	)
	cmd := &cobra.Command{
		Use:          "rollback",
		Short:        "Restore the snapshot named by rollback_target in devbox/snapshot.yml",
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

	if err := snapshot.ValidateName(name); err != nil {
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
		return fmt.Errorf("snapshot %s: no devbox/snapshot.yml found at %s", operation, config.SnapshotConfigPath(baseDir))
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

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
			if errors.As(err, new(*snapshot.RestoreCancelledError)) {
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

	params := snapshot.RestoreParams{
		Cfg:            cfg,
		SnapCfg:        snapCfg,
		Registry:       reg,
		BaseDir:        baseDir,
		Name:           name,
		SkipConfirm:    yes,
		NonInteractive: !ui.IsInteractiveFn(os.Stdin),
		Stdout:         stdout,
		Stderr:         stderr,
		Operation:      operation,
		ConfirmRestore: func(rc snapshot.RestoreConfirmContext) (bool, error) {
			if !ui.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "snapshot restore needs confirmation; pass --yes to proceed non-interactively")
				return false, nil
			}
			prompt := fmt.Sprintf("Restore snapshot %q?", rc.Manifest.Name)
			var notes []string
			if rc.ConfigDiverged {
				notes = append(notes, "config_hash diverged from current project")
			}
			if !rc.ServicesDiff.IsEmpty() {
				notes = append(notes, "services diff: "+snapshot.FormatServicesDiff(rc.ServicesDiff))
			}
			if len(notes) > 0 {
				prompt += " (" + strings.Join(notes, "; ") + ")"
			}
			return ui.RunConfirm(prompt, "Restore", "Cancel")
		},
		StepObserverFactory: func(steps []model.WorkflowStep) snapshot.StepObserverCloser {
			return newSnapshotLiveObserver("snapshot "+operation+": "+name, noLive, steps)
		},
	}

	var (
		res    *snapshot.RestoreResult
		runErr error
	)
	if operation == "rollback" {
		res, runErr = snapshot.Rollback(ctx, params)
	} else {
		res, runErr = snapshot.Restore(ctx, params)
	}

	if runErr != nil {
		if errors.As(runErr, new(*snapshot.RestoreCancelledError)) {
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
	return nil
}

func runSnapshotRollback(cmd *cobra.Command, flags *cmdctx.RootFlags, yes, noLive, silent bool) error {
	// rollback resolves the target name from snapshot.yml; pass an empty name
	// to runSnapshotRestore and dispatch through snapshot.Rollback below.
	baseDir := flags.ProjectRoot()
	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	if snapCfg == nil {
		return fmt.Errorf("snapshot rollback: no devbox/snapshot.yml found at %s", config.SnapshotConfigPath(baseDir))
	}
	if snapCfg.RollbackTarget == "" {
		return fmt.Errorf("snapshot rollback: rollback_target is not set in devbox/snapshot.yml")
	}
	if err := snapshot.ValidateName(snapCfg.RollbackTarget); err != nil {
		return fmt.Errorf("snapshot rollback: rollback_target %q in devbox/snapshot.yml: %w", snapCfg.RollbackTarget, err)
	}
	return runSnapshotRestore(cmd, flags, snapCfg.RollbackTarget, yes, noLive, silent, "rollback", "snapshot:rollback")
}

func writeRestoreOutcome(w io.Writer, operation string, res *snapshot.RestoreResult) {
	if res == nil {
		return
	}
	switch res.Status {
	case snapshot.StatusOk:
		verb := operation + "d"
		if operation == "rollback" {
			verb = "rolled back"
		}
		_, _ = fmt.Fprintf(w, "snapshot %q %s in %dms\n", res.Manifest.Name, verb, res.DurationMs)
	case snapshot.StatusInterrupted:
		_, _ = fmt.Fprintf(w, "snapshot %s %q interrupted; pre-restore backup kept at %s\n", operation, res.Manifest.Name, res.BackupDir)
	default:
		_, _ = fmt.Fprintf(w, "snapshot %s %q failed; pre-restore backup kept at %s\n", operation, res.Manifest.Name, res.BackupDir)
	}
}
