package render

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/envfile"

	"github.com/spf13/cobra"
)

func newEnvCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "env",
		Short: "Generate .env from exports.env spec (stdout or --out <file>)",
		Long: `Evaluate the exports.env rules from the merged config and write the resulting .env content.

Rules are declared in workspace/defaults.yml under 'exports.env'. Each rule maps a config
dot-path to an environment variable name with optional format and conditional logic.

Output goes to stdout by default; use --out to write directly to a file.`,
		Example: `  dwe render env
  dwe render env --out .env`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenderEnv(cmd, flags, outputPath)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVar(&outputPath, "out", "", "write to file instead of stdout")
	return cmd
}

func runRenderEnv(cmd *cobra.Command, flags *cmdctx.RootFlags, outputPath string) error {
	cfg, err := config.LoadConfigOrWrap(flags.ConfigPath)
	if err != nil {
		return err
	}

	warnUnresolvedExports(cmd, flags, cfg)

	if outputPath == "" {
		content, err := envfile.BuildContent(cfg, flags.ProjectRoot())
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(cmd.OutOrStdout(), content)
		return nil
	}
	return envfile.Write(cfg, flags.ProjectRoot(), outputPath)
}

// warnUnresolvedExports reports, on stderr, every export rule this render emits
// as an empty assignment because its from: does not resolve.
//
// It runs before the content is written so the warning is on screen at the
// moment the user reads the `NAME=` line it explains — the whole point, since
// an empty value is otherwise indistinguishable from one the config really
// declares as empty. stderr keeps `dwe render env > .env` byte-identical, and
// JSON mode stays silent per the output-mode contract.
func warnUnresolvedExports(cmd *cobra.Command, flags *cmdctx.RootFlags, cfg *config.DweConfig) {
	if flags.Output == "json" {
		return
	}
	for _, rule := range envfile.UnresolvedRules(cfg) {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: exports.env[%s]: from %q does not resolve — rendered empty\n",
			rule.Name, rule.From)
	}
}
