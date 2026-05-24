package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"devbox-cli/internal/config"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/notify"
	"devbox-cli/internal/render"
	"devbox-cli/internal/snapshot"
	"devbox-cli/internal/ui"
	"devbox-cli/internal/usercommands/model"
	"devbox-cli/internal/userconfig"
	"devbox-cli/internal/version"

	"github.com/spf13/cobra"
)

// newSnapshotCreateCmd: `devbox snapshot create <name> [-d desc] [--using=variant] [-y]`.
func newSnapshotCreateCmd(flags *rootFlags) *cobra.Command {
	var (
		description string
		variant     string
		yes         bool
		noLive      bool
	)
	cmd := &cobra.Command{
		Use:          "create <name>",
		Short:        "Capture the current environment into a named snapshot",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotCreate(cmd, flags, args[0], description, variant, yes, noLive)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description recorded in manifest.yml")
	cmd.Flags().StringVar(&variant, "using", "", "select a create-workflow variant from snapshot.yml")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip overwrite confirmation")
	cmd.Flags().BoolVar(&noLive, "no-live", false, "disable the per-step live UI; emit plain stdout")
	return cmd
}

func runSnapshotCreate(cmd *cobra.Command, flags *rootFlags, name, description, variant string, yes, noLive bool) (err error) {
	baseDir := flags.ProjectRoot()

	if err := snapshot.ValidateName(name); err != nil {
		return err
	}

	cfg, err := config.LoadConfig(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	if snapCfg == nil || snapCfg.Create == nil {
		return fmt.Errorf("snapshot create: no create: block defined in %s", config.SnapshotConfigPath(baseDir))
	}

	reg, err := loadCommandRegistry(flags.configPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}

	releaseLocks, err := lock.AcquireProjectLocks(baseDir)
	if err != nil {
		if phe, ok := errors.AsType[*lock.ProjectLockHeldError](err); ok {
			lhe := &lockHeldError{operation: phe.Operation, pid: phe.PID}
			render.Stdout().Error(lhe.Error())
			return lhe
		}
		return fmt.Errorf("acquiring project locks: %w", err)
	}
	defer releaseLocks()

	// Install notifier defer for end-of-run desktop notification. Suppress
	// the notification on user cancellation (overwrite refused) — it is
	// intentional, not a failure.
	start := time.Now()
	ucfg, ucfgErr := userconfig.Load(baseDir)
	if ucfgErr != nil {
		slog.Warn("userconfig load failed; notifications disabled for this run", "err", ucfgErr)
		ucfg = nil
	}
	n := newNotifier(ucfg)
	defer func() {
		if errors.As(err, new(*snapshot.CreateCancelledError)) {
			return
		}
		n.Notify(context.Background(), notify.Event{
			Kind:      notify.OpCommand,
			Operation: "snapshot:create",
			Outcome:   notify.OutcomeFromErr(err),
			Duration:  time.Since(start),
			Err:       err,
			Project:   cfg.Project.Name,
		})
	}()

	// Install SIGINT/SIGTERM-aware context so the workflow can be cancelled
	// and we still write the manifest with status=interrupted in the defer.
	parentCtx := cmd.Context()
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	ctx, stop := signal.NotifyContext(parentCtx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	params := snapshot.CreateParams{
		Cfg:            cfg,
		SnapCfg:        snapCfg,
		Registry:       reg,
		BaseDir:        baseDir,
		Name:           name,
		Description:    description,
		Variant:        variant,
		DevboxVersion:  version.Version,
		SkipConfirm:    yes,
		NonInteractive: !ui.IsInteractiveFn(os.Stdin),
		Stdout:         stdout,
		Stderr:         stderr,
		ConfirmOverwrite: func() (bool, error) {
			if !ui.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "snapshot already exists; pass --yes to overwrite non-interactively")
				return false, nil
			}
			return ui.RunConfirm(
				fmt.Sprintf("Snapshot %q already exists. Overwrite?", name),
				"Overwrite",
				"Cancel",
			)
		},
		StepObserverFactory: func(steps []model.WorkflowStep) snapshot.StepObserverCloser {
			return newSnapshotLiveObserver("snapshot create: "+name, noLive, steps)
		},
	}

	res, runErr := snapshot.Create(ctx, params)
	if runErr != nil {
		// Cancellation: silent, exit 0 — the manifest is left untouched.
		if errors.As(runErr, new(*snapshot.CreateCancelledError)) {
			_, _ = fmt.Fprintln(stderr, "snapshot create cancelled")
			return runErr
		}
		// Manifest already persisted with status=interrupted; exit 130 for SIGINT.
		writeCreateOutcome(stderr, res)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return &snapshotInterruptedError{wrapped: runErr}
		}
		return runErr
	}

	writeCreateOutcome(stderr, res)
	return nil
}

// writeCreateOutcome prints a single human-readable summary line of the
// create attempt to stderr (machine-readable manifest is on disk).
func writeCreateOutcome(w io.Writer, res *snapshot.CreateResult) {
	if res == nil {
		return
	}
	switch res.Status {
	case snapshot.StatusOk:
		_, _ = fmt.Fprintf(w, "snapshot %q created at %s\n", res.Manifest.Name, res.SnapshotDir)
	case snapshot.StatusInterrupted:
		_, _ = fmt.Fprintf(w, "snapshot %q interrupted; partial directory kept at %s\n", res.Manifest.Name, res.SnapshotDir)
	default:
		_, _ = fmt.Fprintf(w, "snapshot %q failed; partial directory kept at %s\n", res.Manifest.Name, res.SnapshotDir)
	}
}
