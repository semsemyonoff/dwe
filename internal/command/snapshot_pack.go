package command

import (
	"errors"
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/lock"
	"devbox-cli/internal/render"
	"devbox-cli/internal/snapshot"

	"github.com/spf13/cobra"
)

// newSnapshotPackCmd: `devbox snapshot pack <name> [--out=<path>] [--exclude=<glob>...]`.
func newSnapshotPackCmd(flags *rootFlags) *cobra.Command {
	var (
		outPath  string
		excludes []string
	)
	cmd := &cobra.Command{
		Use:               "pack <name>",
		Short:             "Pack a snapshot into a .tar.gz archive",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: snapshotNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotPack(cmd, flags, args[0], outPath, excludes)
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "destination .tar.gz path (default ./snapshots/<name>.tar.gz)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "exclude entries matching the given glob (may be repeated; appended to pack.exclude from snapshot.yml)")
	return cmd
}

func runSnapshotPack(cmd *cobra.Command, flags *rootFlags, name, outPath string, cliExcludes []string) error {
	baseDir := flags.ProjectRoot()

	if err := snapshot.ValidateName(name); err != nil {
		return err
	}

	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
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

	snapDir := snapshot.SnapshotDir(baseDir, snapCfg, name)
	snapshotsRoot := snapshot.SnapshotsDir(baseDir, snapCfg)

	excludes := mergeExcludes(snapCfg, cliExcludes)

	res, err := snapshot.Pack(snapshotsRoot, snapDir, name, outPath, excludes)
	if err != nil {
		return err
	}

	stderr := cmd.ErrOrStderr()
	_, _ = fmt.Fprintf(stderr, "snapshot %q packed → %s (%d bytes, sha256=%s)\n",
		name, res.OutPath, res.SizeBytes, res.Sha256)
	if len(res.SkippedEntries) > 0 {
		_, _ = fmt.Fprintf(stderr, "skipped %d entries via exclude globs\n", len(res.SkippedEntries))
	}
	return nil
}

// mergeExcludes appends CLI excludes onto the snapshot config's pack.exclude
// list (config entries first to preserve their relative ordering). CLI flags
// never replace the config — they extend it.
func mergeExcludes(snapCfg *config.SnapshotConfig, cliExcludes []string) []string {
	var configExcludes []string
	if snapCfg != nil {
		configExcludes = snapCfg.Pack.Exclude
	}
	out := make([]string, 0, len(configExcludes)+len(cliExcludes))
	out = append(out, configExcludes...)
	out = append(out, cliExcludes...)
	return out
}
