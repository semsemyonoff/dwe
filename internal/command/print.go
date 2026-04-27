package command

import (
	"errors"
	"os"

	"devbox-cli/internal/render"
	"devbox-cli/internal/ui"

	"github.com/spf13/cobra"
)

// runConfirm is the package-level wrapper for ui.RunConfirm; swappable in tests.
var runConfirm = ui.RunConfirm

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
	cmd.AddCommand(newPrintConfirmCmd())

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

func newPrintConfirmCmd() *cobra.Command {
	var (
		continueOnRefusal bool
		okMsg             string
		stopMsg           string
	)

	cmd := &cobra.Command{
		Use:          "confirm <message>",
		Short:        "Print an interactive yes/no confirmation prompt; exits 1 on refusal",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := render.NewWriter(cmd.OutOrStdout())
			stdin := cmd.InOrStdin()
			var confirmed bool
			if ui.IsInteractiveFn(stdin) {
				result, err := runConfirm(args[0], okMsg, stopMsg)
				if err != nil {
					if errors.Is(err, ui.ErrCancelled) {
						confirmed = false
					} else {
						return err
					}
				} else {
					confirmed = result
				}
			} else {
				confirmed = w.Confirm(args[0], stdin)
			}
			if confirmed {
				w.Success(okMsg)
			} else {
				w.Error(stopMsg)
				if !continueOnRefusal {
					os.Exit(1)
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&continueOnRefusal, "continue", false, "exit 0 on refusal instead of 1")
	cmd.Flags().StringVar(&okMsg, "ok-msg", "Continuing", "message to print on confirmation")
	cmd.Flags().StringVar(&stopMsg, "stop-msg", "Stopping", "message to print on refusal")

	return cmd
}
