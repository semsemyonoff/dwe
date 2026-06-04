package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	snapshotpkg "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/promptcache"
	"github.com/semsemyonoff/dwe/internal/shared/render"
	"github.com/semsemyonoff/dwe/internal/shared/version"

	"github.com/spf13/cobra"
)

// newSnapshotCreateCmd: `dwe snapshot create <name> [-d desc] [--using=variant] [-y]`.
func newSnapshotCreateCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		description string
		variant     string
		yes         bool
		noLive      bool
		silent      bool
	)
	cmd := &cobra.Command{
		Use:          "create <name>",
		Short:        "Capture the current environment into a named snapshot",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotCreate(cmd, flags, args[0], description, variant, yes, noLive, silent)
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "human-readable description recorded in manifest.yml")
	cmd.Flags().StringVar(&variant, "using", "", "select a create-workflow variant from snapshot.yml")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip overwrite confirmation")
	cmd.Flags().BoolVar(&noLive, "no-live", false, "disable the per-step live UI; emit plain stdout")
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}

func runSnapshotCreate(cmd *cobra.Command, flags *cmdctx.RootFlags, name, description, variant string, yes, noLive, silent bool) (err error) {
	baseDir := flags.ProjectRoot()

	if err := meta.ValidateName(name); err != nil {
		return err
	}

	cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
	if err != nil {
		return err
	}

	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	if snapCfg == nil || snapCfg.Create == nil {
		return fmt.Errorf("snapshot create: no create: block defined in %s", config.SnapshotConfigPath(baseDir))
	}

	reg, err := usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading command registry: %w", err)
	}
	_ = reg.ApplyVisibility(cfg, baseDir)

	releaseLocks, err := cmdctx.AcquireProjectLocksOrReport(baseDir, render.Stdout())
	if err != nil {
		return err
	}
	defer releaseLocks()

	// Install notifier defer for end-of-run desktop notification. Suppress
	// the notification on user cancellation (overwrite refused) — it is
	// intentional, not a failure.
	defer installSnapshotNotifier(baseDir, "snapshot:create", cfg.Project.Name, silent, &err, func(e error) bool {
		return errors.As(e, new(*snapshotpkg.CreateCancelledError))
	})()

	// Install SIGINT/SIGTERM-aware context so the workflow can be cancelled
	// and we still write the manifest with status=interrupted in the defer.
	ctx, stop := signalAwareContext(cmd.Context())
	defer stop()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	params := snapshotpkg.CreateParams{
		Cfg:            cfg,
		SnapCfg:        snapCfg,
		Registry:       reg,
		BaseDir:        baseDir,
		Name:           name,
		Description:    description,
		Variant:        variant,
		DweVersion:     version.Version,
		SkipConfirm:    yes,
		NonInteractive: !widgets.IsInteractiveFn(os.Stdin),
		Stdout:         stdout,
		Stderr:         stderr,
		ConfirmOverwrite: func() (bool, error) {
			if !widgets.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "snapshot already exists; pass --yes to overwrite non-interactively")
				return false, nil
			}
			return widgets.RunConfirm(
				fmt.Sprintf("Snapshot %q already exists. Overwrite?", name),
				"Overwrite",
				"Cancel",
			)
		},
		StepObserverFactory: func(steps []model.WorkflowStep) snapshotpkg.StepObserverCloser {
			return newSnapshotLiveObserver("snapshot create: "+name, noLive, steps)
		},
	}

	res, runErr := snapshotpkg.Create(ctx, params)
	if runErr != nil {
		// Cancellation: silent, exit 0 — the manifest is left untouched.
		if errors.As(runErr, new(*snapshotpkg.CreateCancelledError)) {
			_, _ = fmt.Fprintln(stderr, "snapshot create cancelled")
			return runErr
		}
		// Manifest already persisted with status=interrupted; exit 130 for SIGINT.
		writeCreateOutcome(stderr, res)
		// Create workflow may have partially mutated container state before
		// failing; invalidate so the next prompt refresh or `dwe status`
		// reflects ground truth.
		_ = promptcache.Remove(baseDir)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return &snapshotInterruptedError{wrapped: runErr}
		}
		return runErr
	}

	writeCreateOutcome(stderr, res)
	// Create workflow may have stopped/restarted containers; invalidate so the
	// next prompt refresh reflects ground truth.
	_ = promptcache.Remove(baseDir)
	return nil
}

// writeCreateOutcome prints a single human-readable summary line of the
// create attempt to stderr (machine-readable manifest is on disk).
func writeCreateOutcome(w io.Writer, res *snapshotpkg.CreateResult) {
	if res == nil {
		return
	}
	switch res.Status {
	case meta.StatusOk:
		_, _ = fmt.Fprintf(w, "snapshot %q created at %s\n", res.Manifest.Name, res.SnapshotDir)
	case meta.StatusInterrupted:
		if res.BackupRestored {
			_, _ = fmt.Fprintf(w, "snapshot %q interrupted; previous snapshot restored at %s\n", res.Manifest.Name, res.SnapshotDir)
		} else {
			_, _ = fmt.Fprintf(w, "snapshot %q interrupted; partial directory kept at %s\n", res.Manifest.Name, res.SnapshotDir)
		}
	default:
		if res.BackupRestored {
			_, _ = fmt.Fprintf(w, "snapshot %q failed; previous snapshot restored at %s\n", res.Manifest.Name, res.SnapshotDir)
		} else {
			_, _ = fmt.Fprintf(w, "snapshot %q failed; partial directory kept at %s\n", res.Manifest.Name, res.SnapshotDir)
		}
	}
}
