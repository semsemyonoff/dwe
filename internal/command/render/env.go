package render

import (
	"fmt"

	"devbox-cli/internal/command/cmdctx"
	"devbox-cli/internal/config"
	"devbox-cli/internal/envfile"

	"github.com/spf13/cobra"
)

func newEnvCmd(flags *cmdctx.RootFlags) *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "env",
		Short: "Generate .env from exports.env spec (stdout or --output <file>)",
		Long: `Evaluate the exports.env rules from the merged config and write the resulting .env content.

Rules are declared in devbox/defaults.yml under 'exports.env'. Each rule maps a config
dot-path to an environment variable name with optional format and conditional logic.

Output goes to stdout by default; use --output to write directly to a file.`,
		Example: `  devbox render env
  devbox render env -o .env`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRenderEnv(flags, outputPath)
		},
		SilenceUsage: true,
	}
	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "write to file instead of stdout")
	return cmd
}

func runRenderEnv(flags *cmdctx.RootFlags, outputPath string) error {
	cfg, err := config.LoadConfig(flags.ConfigPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if outputPath == "" {
		content, err := envfile.BuildContent(cfg)
		if err != nil {
			return err
		}
		fmt.Print(content)
		return nil
	}
	return envfile.Write(cfg, outputPath)
}
