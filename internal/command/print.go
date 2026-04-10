package command

import (
	"os"

	"devbox-cli/internal/render"

	"github.com/spf13/cobra"
)

func newPrintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "print <command>",
		Short:        "Print a formatted message to stdout",
		SilenceUsage: true,
	}

	cmd.AddCommand(newPrintSuccessCmd())
	cmd.AddCommand(newPrintWarningCmd())
	cmd.AddCommand(newPrintInfoCmd())
	cmd.AddCommand(newPrintErrorCmd())

	return cmd
}

func newPrintSuccessCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "success <message>",
		Short:        "Print a green success message",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			render.Stdout().Success(args[0])
			return nil
		},
	}
}

func newPrintWarningCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "warning <message>",
		Short:        "Print a yellow warning message",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			render.Stdout().Warning(args[0])
			return nil
		},
	}
}

func newPrintInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "info <message>",
		Short:        "Print a blue info message",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			render.Stdout().Info(args[0])
			return nil
		},
	}
}

func newPrintErrorCmd() *cobra.Command {
	var exitCode int

	cmd := &cobra.Command{
		Use:          "error <message>",
		Short:        "Print a red error message",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			render.Stdout().Error(args[0])
			if exitCode != 0 {
				os.Exit(exitCode)
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&exitCode, "exit-code", "e", 0, "exit with this code after printing (0 = continue)")
	return cmd
}
