package docs

import (
	"fmt"
	"os"
	"path/filepath"

	"devbox-cli/internal/cli/cmdctx"
	coredocs "devbox-cli/internal/core/docs"
	"devbox-cli/internal/core/docs/llmstxt"
	"devbox-cli/internal/core/project/config"
	userpkg "devbox-cli/internal/core/project/user"
	"devbox-cli/internal/core/usercommands"
	"devbox-cli/internal/shared/i18n"

	"github.com/spf13/cobra"
)

type docsLlmsTxtFlags struct {
	output           string
	lang             string
	includeInternals bool
	noProject        bool
}

func newDocsLlmsTxtCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsLlmsTxtFlags{}

	cmd := &cobra.Command{
		Use:   "llms-txt",
		Short: "Emit an llms.txt project index for AI agents",
		Long: `Emit a single llms.txt document to stdout (or --output PATH).

The document follows the llms.txt spec: a dense ~2-5KB index that gives an AI
agent a complete picture of "what this devbox project is and where to find more
detail," without having to ingest the full embedded docs tree.

Works both inside a project (project-aware: includes services, commands, URLs)
and outside one (project-agnostic: generic devbox reference).

Examples:
  devbox docs llms-txt
  devbox docs llms-txt --output llms.txt
  devbox docs llms-txt --include-internals
  devbox docs llms-txt --no-project`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsLlmsTxt(cmd, flags, df)
		},
	}

	cmd.Flags().StringVar(&df.output, "output", "", "Write output to PATH instead of stdout")
	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from userconfig / $LANG / en)")
	cmd.Flags().BoolVar(&df.includeInternals, "include-internals", false, "Include internals architecture docs section")
	cmd.Flags().BoolVar(&df.noProject, "no-project", false, "Force project-agnostic output even if devbox.yml exists")

	return cmd
}

func runDocsLlmsTxt(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsLlmsTxtFlags) error {
	projectRoot := rflags.ProjectRoot()
	if df.noProject {
		projectRoot = ""
	}

	// Resolve locale: flag → user config lang → $LANG → en.
	var cfgLang string
	if projectRoot != "" {
		if ucfg, err := userpkg.Load(projectRoot); err == nil && ucfg != nil {
			cfgLang = ucfg.Language
		}
	}
	locale := i18n.ResolveLocale(df.lang, cfgLang, os.Getenv("LANG"))

	// Collect doc topics from embedded sources.
	docTopics := coredocs.AllTopics(coredocs.Sources(projectRoot), locale)

	// Assemble Opts — project sections only when a project root is available.
	opts := llmstxt.Opts{
		ProjectRoot:   projectRoot,
		Locale:        locale,
		IncludeIntern: df.includeInternals,
		DocTopics:     docTopics,
	}

	if projectRoot != "" {
		cfg, err := config.LoadConfig(rflags.ConfigPath)
		if err == nil && cfg != nil {
			opts.ProjectName = cfg.Project.Name

			tr := i18n.TranslatorOrNop(rflags.I18n)
			opts.Services = collectServiceSummaries(cfg, tr, locale)
			opts.InfoSnapshot = collectInfoSummary(cfg, projectRoot)

			if rflags.ConfigPath != "" {
				if reg, regErr := usercommands.LoadRegistryFromConfigPath(rflags.ConfigPath); regErr == nil {
					opts.Commands = collectCommandSummaries(reg, tr, locale)
				}
			}
		}
	}

	out, err := llmstxt.Generate(opts)
	if err != nil {
		return fmt.Errorf("generating llms.txt: %w", err)
	}

	if df.output == "" {
		_, err = fmt.Fprint(cmd.OutOrStdout(), out)
		return err
	}

	// Write to file, creating parent dirs as needed.
	if mkErr := os.MkdirAll(filepath.Dir(df.output), 0o755); mkErr != nil {
		return cmdctx.ErrWrap("llms_txt_write_failed", mkErr).WithDetail("path", df.output)
	}
	if writeErr := os.WriteFile(df.output, []byte(out), 0o644); writeErr != nil {
		return cmdctx.ErrWrap("llms_txt_write_failed", writeErr).WithDetail("path", df.output)
	}
	return nil
}
