package docs

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	coredocs "github.com/semsemyonoff/dwe/internal/core/docs"
	"github.com/semsemyonoff/dwe/internal/core/docs/llmstxt"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

type docsLlmsTxtFlags struct {
	out              string
	lang             string
	includeInternals bool
	noProject        bool
}

func newDocsLlmsTxtCmd(flags *cmdctx.RootFlags) *cobra.Command {
	df := &docsLlmsTxtFlags{}

	cmd := &cobra.Command{
		Use:   "llms-txt",
		Short: "Emit an llms.txt project index for AI agents",
		Long: `Emit a single llms.txt document to stdout (or --out PATH).

The document follows the llms.txt spec: a dense briefing (project-agnostic part
capped at 12KB) that gives an AI agent a complete picture of "what this dwe
project is and where to find more detail," without having to ingest the full
embedded docs tree. Besides the doc index it carries the builtin inventory, the
diagnostic flags, the template-syntax map and the reserved env names.

Works both inside a project (project-aware: includes services, commands, URLs)
and outside one (project-agnostic: generic dwe reference).

Examples:
  dwe docs llms-txt
  dwe docs llms-txt --out llms.txt
  dwe docs llms-txt --include-internals
  dwe docs llms-txt --no-project`,
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDocsLlmsTxt(cmd, flags, df)
		},
	}

	cmd.Flags().StringVar(&df.out, "out", "", "Write output to PATH instead of stdout")
	cmd.Flags().StringVar(&df.lang, "lang", "", "Language code (default: from userconfig / $LANG / en)")
	cmd.Flags().BoolVar(&df.includeInternals, "include-internals", false, "Include internals architecture docs section")
	cmd.Flags().BoolVar(&df.noProject, "no-project", false, "Force project-agnostic output even if workspace.yml exists")

	return cmd
}

func runDocsLlmsTxt(cmd *cobra.Command, rflags *cmdctx.RootFlags, df *docsLlmsTxtFlags) error {
	projectRoot := rflags.ProjectRoot()
	if df.noProject {
		projectRoot = ""
	}

	// Resolve locale: flag → user config lang → $LANG → en.
	locale := i18n.ResolveLocale(df.lang, docsCfgLang(projectRoot), os.Getenv("LANG"))

	// Collect doc topics from embedded sources.
	docTopics := coredocs.AllTopics(coredocs.Sources(projectRoot), locale)

	// Assemble Opts — project sections only when a project root is available.
	opts := llmstxt.Opts{
		ProjectRoot:      projectRoot,
		IncludeIntern:    df.includeInternals,
		DocTopics:        docTopics,
		Builtins:         collectBuiltinSummaries(),
		Conditions:       collectConditionSummaries(),
		ReservedEnvNames: config.ReservedExportNames,
	}

	if projectRoot != "" {
		cfg, err := config.LoadConfig(rflags.ConfigPath)
		if err == nil && cfg != nil {
			opts.ProjectName = cfg.Project.FullName()

			tr := i18n.TranslatorOrNop(rflags.I18n)
			opts.Services = collectServiceSummaries(cfg)
			opts.InfoSnapshot = collectInfoSummary(cfg)

			if rflags.ConfigPath != "" {
				if reg, regErr := usercommands.LoadRegistryFromConfigPath(rflags.ConfigPath); regErr == nil {
					// Apply visibility so hidden commands do not appear in the
					// agent-facing snapshot. Best-effort: a hide-eval error
					// keeps the reg available but with whatever Hidden flags
					// were set before the failure.
					_ = reg.ApplyVisibility(cfg, projectRoot)
					opts.Commands = collectCommandSummaries(reg, tr, locale)
				}
			}
		}
	}

	out, err := llmstxt.Generate(opts)
	if err != nil {
		return fmt.Errorf("generating llms.txt: %w", err)
	}

	if df.out == "" {
		_, err = fmt.Fprint(cmd.OutOrStdout(), out)
		return err
	}

	// Write to file, creating parent dirs as needed.
	if mkErr := os.MkdirAll(filepath.Dir(df.out), 0o755); mkErr != nil {
		return cmdctx.ErrWrap("llms_txt_write_failed", mkErr).WithDetail("path", df.out)
	}
	if writeErr := os.WriteFile(df.out, []byte(out), 0o644); writeErr != nil {
		return cmdctx.ErrWrap("llms_txt_write_failed", writeErr).WithDetail("path", df.out)
	}
	return nil
}
