package command

import (
	"fmt"
	"os"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/docs/mermaid"

	"github.com/spf13/cobra"
)

func newDocsCacheCmd(_ *cmdctx.RootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage mermaid diagram cache",
		Long:  `Manage the mermaid diagram cache used by the docs subsystem.`,
	}

	cmd.AddCommand(newDocsCacheClearCmd())
	return cmd
}

func newDocsCacheClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clear",
		Short: "Clear the mermaid diagram cache",
		Long:  `Remove all cached mermaid diagrams from disk.`,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsCacheClear(cmd)
		},
		SilenceUsage: true,
	}

	return cmd
}

func runDocsCacheClear(cmd *cobra.Command) error {
	cacheDir, err := mermaid.CacheDir()
	if err != nil {
		return fmt.Errorf("determining cache directory: %w", err)
	}

	// Count files before removal
	count := 0
	entries, err := os.ReadDir(cacheDir)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() {
				count++
			}
		}
	} else if os.IsNotExist(err) {
		// Cache dir doesn't exist — nothing to clear
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cache directory does not exist; nothing to clear\n")
		return nil
	}

	// Remove the cache directory
	if err := os.RemoveAll(cacheDir); err != nil {
		return fmt.Errorf("removing cache directory: %w", err)
	}

	// Recreate the cache directory
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return fmt.Errorf("recreating cache directory: %w", err)
	}

	switch count {
	case 0:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Cache directory was empty\n")
	case 1:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed 1 cached diagram\n")
	default:
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed %d cached diagrams\n", count)
	}

	return nil
}
