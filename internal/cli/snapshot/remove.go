package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/model"
	"github.com/semsemyonoff/dwe/internal/core/usercommands/registry"
	snapshotpkg "github.com/semsemyonoff/dwe/internal/core/workflow/snapshot"
	"github.com/semsemyonoff/dwe/internal/core/workflow/snapshot/meta"
	"github.com/semsemyonoff/dwe/internal/shared/render"

	"github.com/spf13/cobra"
)

// newSnapshotRemoveCmd: `dwe snapshot remove <name> [-y]`.
func newSnapshotRemoveCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		yes    bool
		noLive bool
		silent bool
	)
	cmd := &cobra.Command{
		Use:               "remove <name>",
		Short:             "Delete a snapshot (runs remove: workflow if defined)",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: snapshotNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotRemove(cmd, flags, args[0], yes, noLive, silent)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip remove confirmation")
	cmd.Flags().BoolVar(&noLive, "no-live", false, "disable the per-step live UI; emit plain stdout")
	cmdctx.AddSilent(cmd, &silent)
	return cmd
}

func runSnapshotRemove(cmd *cobra.Command, flags *cmdctx.RootFlags, name string, yes, noLive, silent bool) (err error) {
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
	if err := requireSnapshotConfig(snapCfg, "remove", baseDir); err != nil {
		return err
	}

	// Only load the registry when a remove: workflow is defined; the package
	// guards on Registry != nil so nil is safe when no workflow runs.
	var reg *registry.Registry
	if snapCfg.Remove != nil {
		reg, err = usercommands.LoadRegistryFromConfigPath(flags.ConfigPath)
		if err != nil {
			return fmt.Errorf("loading command registry: %w", err)
		}
		_ = reg.ApplyVisibility(cfg, baseDir)
	}

	releaseLocks, err := cmdctx.AcquireProjectLocksOrReport(baseDir, render.Stdout())
	if err != nil {
		return err
	}
	defer releaseLocks()

	defer installSnapshotNotifier(baseDir, "snapshot:remove", cfg.Project.Name, silent, &err, func(e error) bool {
		return errors.As(e, new(*snapshotpkg.RemoveCancelledError))
	})()

	ctx, stop := signalAwareContext(cmd.Context())
	defer stop()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	params := snapshotpkg.RemoveParams{
		Cfg:            cfg,
		SnapCfg:        snapCfg,
		Registry:       reg,
		BaseDir:        baseDir,
		Name:           name,
		SkipConfirm:    yes,
		NonInteractive: !widgets.IsInteractiveFn(os.Stdin),
		Stdout:         stdout,
		Stderr:         stderr,
		ConfirmRemove: func(m *meta.Manifest) (bool, error) {
			if !widgets.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "snapshot remove needs confirmation; pass --yes to proceed non-interactively")
				return false, nil
			}
			prompt := fmt.Sprintf("Remove snapshot %q?", name)
			if m != nil && m.Description != "" {
				prompt = fmt.Sprintf("Remove snapshot %q (%s)?", name, m.Description)
			}
			return widgets.RunConfirm(prompt, "Remove", "Cancel")
		},
		StepObserverFactory: func(steps []model.WorkflowStep) snapshotpkg.StepObserverCloser {
			return newSnapshotLiveObserver("snapshot remove: "+name, noLive, steps)
		},
	}

	res, runErr := snapshotpkg.Remove(ctx, params)
	if runErr != nil {
		if errors.As(runErr, new(*snapshotpkg.RemoveCancelledError)) {
			_, _ = fmt.Fprintln(stderr, "snapshot remove cancelled")
			return runErr
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			return &snapshotInterruptedError{wrapped: runErr}
		}
		return runErr
	}
	_, _ = fmt.Fprintf(stderr, "snapshot %q removed (dir=%s)\n", name, res.SnapshotDir)
	if res.ClearedCurrent {
		_, _ = fmt.Fprintln(stderr, "current pointer cleared")
	}
	return nil
}
