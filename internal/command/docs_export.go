package command

import (
	"fmt"
	"os"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/docs"
	"devbox-cli/internal/docs/export"
	"devbox-cli/internal/shared/i18n"
	"devbox-cli/internal/userconfig"

	"github.com/spf13/cobra"
)

type docsExportFlags struct {
	lang             string
	includeProject   bool
	includeInternals bool
	force            bool
}

func newDocsExportCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsExportFlags{}

	cmd := &cobra.Command{
		Use:   "export <dir>",
		Short: "Export documentation to a directory",
		Long: `Export all documentation files to a target directory.

Files are organized by source (devbox, project, internals).
Missing translations are exported with a banner indicating the original language.

Examples:
  devbox docs export /tmp/docs
  devbox docs export /tmp/docs --lang ru
  devbox docs export /tmp/docs --include-project --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsExport(cmd, flags, df, args[0])
		},
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from --lang flag / userconfig / $LANG / en)")
	cmd.Flags().BoolVar(&df.includeProject, "include-project", false, "Include project docs (from ./docs/)")
	cmd.Flags().BoolVar(&df.includeInternals, "include-internals", false, "Include internals docs")
	cmd.Flags().BoolVar(&df.force, "force", false, "Overwrite non-empty target directory")

	return cmd
}

func runDocsExport(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsExportFlags, targetDir string) error {
	// Load user config to get the configured language
	var cfgLang string
	projectRoot := rflags.ProjectRoot()
	if projectRoot != "" {
		ucfg, err := userconfig.Load(projectRoot)
		if err == nil && ucfg != nil {
			cfgLang = ucfg.Language
		}
	}

	// Resolve the locale: use --lang flag, fallback to userconfig, then env
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	// Load sources for documentation
	allRoots := docs.Sources(projectRoot)

	// Build the roots list based on options
	roots := []docs.DocRoot{}
	for _, r := range allRoots {
		// Always include devbox
		if r.Name == "devbox" {
			roots = append(roots, r)
		}
		// Optionally include project
		if r.Name == "project" && df.includeProject {
			roots = append(roots, r)
		}
	}

	// Export the documentation tree
	opts := export.Opts{
		Lang:             locale,
		IncludeProject:   df.includeProject,
		IncludeInternals: df.includeInternals,
		Force:            df.force,
	}

	if err := export.Tree(targetDir, roots, opts); err != nil {
		return fmt.Errorf("export failed: %w", err)
	}

	// Report success
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Documentation exported to %s\n", targetDir)

	return nil
}
