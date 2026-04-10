package main

import (
	"os"

	"devbox-cli/internal/command"
	"devbox-cli/internal/render"
)

func main() {
	root := command.NewRootCmd()
	root.SilenceErrors = true
	if err := root.Execute(); err != nil {
		render.NewWriter(os.Stderr).Error(err.Error())
		os.Exit(1)
	}
}
