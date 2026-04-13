package main

import (
	"errors"
	"os"

	"devbox-cli/internal/command"
	"devbox-cli/internal/render"
)

func main() {
	root := command.NewRootCmd()
	root.SilenceErrors = true
	if err := root.Execute(); err != nil {
		// ErrSilent means the command already printed its own error — just exit.
		if !errors.Is(err, command.ErrSilent) {
			render.NewWriter(os.Stderr).Error(err.Error())
		}
		os.Exit(1)
	}
}
