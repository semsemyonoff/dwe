package command

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/workflow/snapshot"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// newSnapshotCmd builds the `devbox snapshot` command group.
//
// Read-only subcommands (list, current, inspect) ship in this task; the
// mutating subcommands (create, restore, rollback, remove, pack, unpack) are
// added by later tasks in the snapshot subsystem plan.
func newSnapshotCmd(flags *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Capture, restore, and manage project snapshots",
		Long: `Capture the state of a devbox project (databases, indices, devbox
local config, deploy state) into a named directory under ./snapshots/<name>/,
and restore or roll back to it. Workflows live in devbox/snapshot.yml.`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newSnapshotListCmd(flags))
	cmd.AddCommand(newSnapshotCurrentCmd(flags))
	cmd.AddCommand(newSnapshotInspectCmd(flags))
	cmd.AddCommand(newSnapshotCreateCmd(flags))
	cmd.AddCommand(newSnapshotRestoreCmd(flags))
	cmd.AddCommand(newSnapshotRollbackCmd(flags))
	cmd.AddCommand(newSnapshotRemoveCmd(flags))
	cmd.AddCommand(newSnapshotPackCmd(flags))
	cmd.AddCommand(newSnapshotUnpackCmd(flags))
	return cmd
}

// newSnapshotListCmd: `devbox snapshot list [--json]`.
func newSnapshotListCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:          "list",
		Short:        "List snapshots in ./snapshots/",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotList(flags, cmd.OutOrStdout(), cmd.ErrOrStderr(), jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of a human-readable table")
	return cmd
}

func runSnapshotList(flags *cmdctx.RootFlags, out, errW io.Writer, jsonOut bool) error {
	baseDir := flags.ProjectRoot()
	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	entries, err := snapshot.ListSnapshots(baseDir, snapCfg)
	if err != nil {
		return err
	}
	current, readCurErr := snapshot.ReadCurrent(baseDir)
	if readCurErr != nil {
		_, _ = fmt.Fprintf(errW, "warning: could not read current snapshot pointer: %v\n", readCurErr)
	}

	if jsonOut {
		return writeSnapshotListJSON(out, entries, current)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(errW, "no snapshots found")
		return nil
	}
	headers := []string{"NAME", "CREATED", "SIZE", "VARIANT", "DESCRIPTION"}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		name := filepath.Base(e.Dir)
		if e.Manifest == nil {
			rows = append(rows, []string{name + " (corrupt)", "—", "—", "—", "manifest unreadable"})
			continue
		}
		marker := ""
		if e.Manifest.Name == current {
			marker = " *"
		}
		rows = append(rows, []string{
			e.Manifest.Name + marker,
			formatSnapshotTime(e.Manifest.CreatedAt),
			formatSnapshotSize(e.TotalSize),
			defaultDash(e.Manifest.Variant),
			defaultDash(e.Manifest.Description),
		})
	}
	_, _ = fmt.Fprintln(out, ui.RenderTable(headers, rows))
	return nil
}

// snapshotListJSONEntry is the shape used for `snapshot list --json`. Fields
// are explicit and stable — adding new fields is allowed, renaming or
// removing is a breaking change.
type snapshotListJSONEntry struct {
	Name        string `json:"name"`
	CreatedAt   string `json:"created_at,omitempty"`
	Description string `json:"description,omitempty"`
	Variant     string `json:"variant,omitempty"`
	TotalSize   int64  `json:"total_size"`
	Current     bool   `json:"current"`
	Corrupt     bool   `json:"corrupt,omitempty"`
	Dir         string `json:"dir"`
}

func writeSnapshotListJSON(out io.Writer, entries []snapshot.Entry, current string) error {
	payload := make([]snapshotListJSONEntry, 0, len(entries))
	for _, e := range entries {
		j := snapshotListJSONEntry{
			Dir:       e.Dir,
			TotalSize: e.TotalSize,
		}
		if e.Manifest == nil {
			j.Name = filepath.Base(e.Dir)
			j.Corrupt = true
		} else {
			j.Name = e.Manifest.Name
			j.CreatedAt = e.Manifest.CreatedAt.UTC().Format(time.RFC3339)
			j.Description = e.Manifest.Description
			j.Variant = e.Manifest.Variant
			j.Current = e.Manifest.Name == current
		}
		payload = append(payload, j)
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// newSnapshotCurrentCmd: `devbox snapshot current`.
func newSnapshotCurrentCmd(flags *cmdctx.RootFlags) *cobra.Command {
	return &cobra.Command{
		Use:          "current",
		Short:        "Show the snapshot currently restored into the project",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotCurrent(flags, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}

func runSnapshotCurrent(flags *cmdctx.RootFlags, out, errW io.Writer) error {
	baseDir := flags.ProjectRoot()
	name, err := snapshot.ReadCurrent(baseDir)
	if err != nil {
		return err
	}
	if name == "" {
		_, _ = fmt.Fprintln(errW, "no current snapshot")
		return nil
	}
	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return err
	}
	manifestPath := snapshot.ManifestPath(baseDir, snapCfg, name)
	m, mErr := snapshot.LoadManifest(manifestPath)
	if mErr != nil {
		_, _ = fmt.Fprintln(out, name)
		_, _ = fmt.Fprintf(errW, "warning: manifest unreadable at %s: %v\n", manifestPath, mErr)
		return nil
	}
	_, _ = fmt.Fprintln(out, formatSnapshotSummary(m))
	return nil
}

// newSnapshotInspectCmd: `devbox snapshot inspect <name|tar-path> [--json]`.
func newSnapshotInspectCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:               "inspect <name|tar-path>",
		Short:             "Inspect a snapshot directory or a packed .tar.gz archive",
		Args:              cobra.ExactArgs(1),
		SilenceUsage:      true,
		ValidArgsFunction: snapshotNameCompletion(flags),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSnapshotInspect(flags, cmd.OutOrStdout(), args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON instead of human-readable text")
	return cmd
}

func runSnapshotInspect(flags *cmdctx.RootFlags, out io.Writer, arg string, jsonOut bool) error {
	baseDir := flags.ProjectRoot()
	m, source, err := loadInspectManifest(baseDir, arg)
	if err != nil {
		return err
	}

	currentHash := snapshot.ProjectConfigHash(baseDir)
	diverged := m.Project.ConfigHash != "" && currentHash != "" && m.Project.ConfigHash != currentHash

	// Diff captured services against the current project. Errors loading the
	// current config (or a manifest with no services captured) leave servicesDiff
	// nil so the section is omitted; the project-resolution invariant for
	// `inspect` guarantees configPath is set when invoked normally, but tests may
	// construct cmdctx.RootFlags without it.
	var servicesDiff *snapshot.ServicesDiff
	if len(m.Project.Services) > 0 && flags.ConfigPath != "" {
		if cfg, cfgErr := config.LoadConfig(flags.ConfigPath); cfgErr == nil && cfg != nil {
			d := snapshot.DiffServices(m.Project.Services, cfg.Services)
			servicesDiff = &d
		}
	}

	if jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(struct {
			Source             string                 `json:"source"`
			Manifest           *snapshot.Manifest     `json:"manifest"`
			CurrentConfigHash  string                 `json:"current_config_hash"`
			ConfigHashDiverged bool                   `json:"config_hash_diverged"`
			ServicesDiff       *snapshot.ServicesDiff `json:"services_diff,omitempty"`
		}{
			Source:             source,
			Manifest:           m,
			CurrentConfigHash:  currentHash,
			ConfigHashDiverged: diverged,
			ServicesDiff:       servicesDiff,
		})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "name:           %s\n", m.Name)
	fmt.Fprintf(&b, "source:         %s\n", source)
	fmt.Fprintf(&b, "created_at:     %s\n", formatSnapshotTime(m.CreatedAt))
	if m.Description != "" {
		fmt.Fprintf(&b, "description:    %s\n", m.Description)
	}
	if m.Variant != "" {
		fmt.Fprintf(&b, "variant:        %s\n", m.Variant)
	}
	if m.DevboxVersion != "" {
		fmt.Fprintf(&b, "devbox_version: %s\n", m.DevboxVersion)
	}
	fmt.Fprintf(&b, "project:        %s\n", m.Project.Name)
	switch {
	case m.Project.ConfigHash == "":
		fmt.Fprintf(&b, "config_hash:    (empty — predates any deploy)\n")
	case diverged:
		fmt.Fprintf(&b, "config_hash:    %s  DIVERGED (current=%s)\n", m.Project.ConfigHash, currentHash)
	default:
		fmt.Fprintf(&b, "config_hash:    %s\n", m.Project.ConfigHash)
	}
	if len(m.Artifacts) > 0 {
		fmt.Fprintln(&b, "artifacts:")
		for _, a := range m.Artifacts {
			fmt.Fprintf(&b, "  - %s  (%s, sha256=%s)\n", a.Path, formatSnapshotSize(a.Size), shortHash(a.Sha256))
		}
	}
	if servicesDiff != nil {
		if servicesDiff.IsEmpty() {
			fmt.Fprintf(&b, "services:       in sync (%d captured)\n", len(m.Project.Services))
		} else {
			fmt.Fprintf(&b, "services:       %s\n", snapshot.FormatServicesDiff(*servicesDiff))
		}
	} else if len(m.Project.Services) > 0 {
		fmt.Fprintf(&b, "services:       %d captured (current project not loaded)\n", len(m.Project.Services))
	}
	if m.LastCreate != nil {
		fmt.Fprintf(&b, "last_create:    %s @ %s", m.LastCreate.Status, formatSnapshotTime(m.LastCreate.At))
		if m.LastCreate.FailedStep != "" {
			fmt.Fprintf(&b, "  failed_step=%s", m.LastCreate.FailedStep)
		}
		fmt.Fprintln(&b)
	}
	if m.LastRestore != nil {
		fmt.Fprintf(&b, "last_restore:   %s @ %s", m.LastRestore.Status, formatSnapshotTime(m.LastRestore.At))
		if m.LastRestore.DurationMs > 0 {
			fmt.Fprintf(&b, "  duration=%dms", m.LastRestore.DurationMs)
		}
		if m.LastRestore.FailedStep != "" {
			fmt.Fprintf(&b, "  failed_step=%s", m.LastRestore.FailedStep)
		}
		fmt.Fprintln(&b)
	}
	_, _ = io.WriteString(out, b.String())
	return nil
}

// loadInspectManifest resolves arg into a manifest. The argument is treated as
// a tar-archive path when it ends in ".tar.gz" or ".tgz" *and* the file
// exists; otherwise it is treated as a snapshot name under the project's
// snapshots dir. The chosen source identifier is returned alongside.
func loadInspectManifest(baseDir, arg string) (*snapshot.Manifest, string, error) {
	if looksLikeTarArchive(arg) {
		if _, err := os.Stat(arg); err == nil {
			m, err := snapshot.ReadManifestFromTar(arg)
			if err != nil {
				return nil, "", err
			}
			return m, arg, nil
		}
	}
	if err := snapshot.ValidateName(arg); err != nil {
		return nil, "", err
	}
	snapCfg, err := loadSnapshotConfigOrNil(baseDir)
	if err != nil {
		return nil, "", err
	}
	manifestPath := snapshot.ManifestPath(baseDir, snapCfg, arg)
	m, err := snapshot.LoadManifest(manifestPath)
	if err != nil {
		return nil, "", err
	}
	return m, manifestPath, nil
}

func looksLikeTarArchive(s string) bool {
	low := strings.ToLower(s)
	return strings.HasSuffix(low, ".tar.gz") || strings.HasSuffix(low, ".tgz")
}

// snapshotNameCompletion returns shell completion for the snapshot <name>
// argument. Follows the CLAUDE.md completion contract (calls
// completionConfigPath before touching the project; returns NoFileComp on
// any error so tab-complete is never noisy).
func snapshotNameCompletion(flags *cmdctx.RootFlags) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		_, projectRoot, err := cmdctx.CompletionConfigPath(flags, cmd)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		snapCfg, err := config.LoadSnapshotConfig(config.SnapshotConfigPath(projectRoot))
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		entries, err := snapshot.ListSnapshots(projectRoot, snapCfg)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if e.Manifest != nil {
				names = append(names, e.Manifest.Name)
			} else {
				names = append(names, filepath.Base(e.Dir))
			}
		}
		sort.Strings(names)
		return names, cobra.ShellCompDirectiveNoFileComp
	}
}

// loadSnapshotConfigOrNil reads devbox/snapshot.yml at baseDir. Missing file
// is not an error — the project may not have configured snapshots yet. Other
// load errors are returned wrapped.
func loadSnapshotConfigOrNil(baseDir string) (*config.SnapshotConfig, error) {
	cfg, err := config.LoadSnapshotConfig(config.SnapshotConfigPath(baseDir))
	if err != nil {
		return nil, fmt.Errorf("loading snapshot config: %w", err)
	}
	return cfg, nil
}

func formatSnapshotSummary(m *snapshot.Manifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s", m.Name)
	if m.Description != "" {
		fmt.Fprintf(&b, " — %s", m.Description)
	}
	if !m.CreatedAt.IsZero() {
		fmt.Fprintf(&b, " (created %s)", formatSnapshotTime(m.CreatedAt))
	}
	return b.String()
}

func formatSnapshotTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func formatSnapshotSize(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.2f GiB", float64(n)/(1024*1024*1024))
	}
}

func shortHash(h string) string {
	const n = 12
	if len(h) <= n {
		return h
	}
	return h[:n] + "…"
}

func defaultDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// snapshotInterruptedError wraps a context-cancellation error from a snapshot
// workflow and exposes ExitCode() == 130 so main.go uses the SIGINT convention.
type snapshotInterruptedError struct{ wrapped error }

func (e *snapshotInterruptedError) Error() string { return e.wrapped.Error() }
func (e *snapshotInterruptedError) Unwrap() error { return e.wrapped }
func (e *snapshotInterruptedError) ExitCode() int { return 130 }
