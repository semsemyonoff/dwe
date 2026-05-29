package snapshot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/ui/widgets"
	"devbox-cli/internal/core/workflow/snapshot/archive"
	"devbox-cli/internal/core/workflow/snapshot/meta"
	"devbox-cli/internal/shared/lock"
	"devbox-cli/internal/shared/render"

	"github.com/spf13/cobra"
)

// newSnapshotUnpackCmd: `devbox snapshot unpack <tar-path> [--as=<name>] [-y]`.
func newSnapshotUnpackCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var (
		asName   string
		yes      bool
		noVerify bool
	)
	cmd := &cobra.Command{
		Use:          "unpack <tar-path>",
		Short:        "Extract a packed snapshot archive into ./snapshots/",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotUnpack(cmd, flags, args[0], asName, yes, noVerify)
		},
	}
	cmd.Flags().StringVar(&asName, "as", "", "name to install the unpacked snapshot as (default derived from the archive basename)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "skip overwrite and verification confirmations")
	cmd.Flags().BoolVar(&noVerify, "no-verify", false, "skip post-extract artifact verification against manifest checksums")
	return cmd
}

func runSnapshotUnpack(cmd *cobra.Command, flags *cmdctx.RootFlags, tarPath, asName string, yes, noVerify bool) error {
	baseDir := flags.ProjectRoot()
	stderr := cmd.ErrOrStderr()

	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}

	// Resolve target name: prefer --as, fall back to deriving from archive basename.
	name := asName
	if name == "" {
		name = deriveNameFromTarPath(tarPath)
	}
	if err := meta.ValidateName(name); err != nil {
		return fmt.Errorf("snapshot unpack: invalid target name %q: %w", name, err)
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

	snapshotsRoot := meta.SnapshotsDir(baseDir, snapCfg)

	res, err := archive.Unpack(tarPath, snapshotsRoot, name, archive.UnpackOptions{
		AssumeYes: yes,
		NoVerify:  noVerify,
		ConfirmOverwrite: func() (bool, error) {
			if !widgets.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "target snapshot dir already exists; pass --yes to overwrite non-interactively")
				return false, nil
			}
			return widgets.RunConfirm(fmt.Sprintf("Overwrite existing snapshot %q?", name), "Overwrite", "Cancel")
		},
		ConfirmVerify: func(prompt string) (bool, error) {
			if !widgets.IsInteractiveFn(os.Stdin) {
				_, _ = fmt.Fprintln(stderr, "verification warnings present; pass --yes to continue non-interactively")
				return false, nil
			}
			return widgets.RunConfirm(prompt, "Continue", "Cancel")
		},
		Stderr: stderr,
	})
	if err != nil {
		if errors.As(err, new(*archive.UnpackCancelledError)) {
			_, _ = fmt.Fprintln(stderr, "snapshot unpack cancelled")
			return err
		}
		if errors.As(err, new(*archive.UnpackVerifyDeclinedError)) {
			_, _ = fmt.Fprintln(stderr, "snapshot unpack declined after verification warnings")
			return err
		}
		return err
	}

	cfg, cfgErr := config.LoadConfig(flags.ConfigPath)
	if cfgErr == nil && res.Manifest != nil && res.Manifest.Project.Name != "" && cfg.Project.Name != "" && res.Manifest.Project.Name != cfg.Project.Name {
		_, _ = fmt.Fprintf(stderr,
			"warning: archive project name %q differs from current project %q\n",
			res.Manifest.Project.Name, cfg.Project.Name)
	}

	_, _ = fmt.Fprintf(stderr, "snapshot %q unpacked into %s", name, res.SnapshotDir)
	switch res.Verification {
	case archive.VerificationSkipped:
		_, _ = fmt.Fprint(stderr, " (verification skipped)")
	case archive.VerificationClean:
		_, _ = fmt.Fprint(stderr, " (verified)")
	case archive.VerificationWarned:
		n := len(res.VerifyReport.Missing) + len(res.VerifyReport.HashMismatch) + len(res.VerifyReport.Extra)
		_, _ = fmt.Fprintf(stderr, " (verified with %d warnings)", n)
	}
	_, _ = fmt.Fprintln(stderr)
	return nil
}

// deriveNameFromTarPath returns the basename of tarPath with .tar.gz / .tgz
// stripped. Validation of the resulting name is left to the caller.
func deriveNameFromTarPath(tarPath string) string {
	b := filepath.Base(tarPath)
	low := strings.ToLower(b)
	switch {
	case strings.HasSuffix(low, ".tar.gz"):
		return b[:len(b)-len(".tar.gz")]
	case strings.HasSuffix(low, ".tgz"):
		return b[:len(b)-len(".tgz")]
	default:
		return b
	}
}
