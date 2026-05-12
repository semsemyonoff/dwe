package command

import (
	"fmt"

	"devbox-cli/internal/config"
	"devbox-cli/internal/envfile"

	"github.com/spf13/cobra"
)

func newRenderCmd(flags *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "render",
		Short: "Render derived artifacts from the merged devbox config",
		Long: `Generate files derived from the merged devbox config (devbox.yml + defaults.yml + local.yml).

Subcommands:
  env  — generate .env from the exports.env spec
  ide  — generate IDE config files from template packs
  ai   — generate hub-level agents documentation from template packs`,
		Example: `  devbox render env -o .env
  devbox render ide
  devbox render ai`,
		SilenceUsage: true,
	}
	cmd.AddCommand(newRenderEnvCmd(flags))
	cmd.AddCommand(newRenderIDECmd(flags))
	cmd.AddCommand(newRenderAICmd(flags))
	return cmd
}

func newRenderEnvCmd(flags *rootFlags) *cobra.Command {
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

func runRenderEnv(flags *rootFlags, outputPath string) error {
	cfg, err := config.LoadConfig(flags.configPath)
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
