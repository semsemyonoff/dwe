package main

import (
	"context"
	"errors"
	"io"
	"os"

	"github.com/charmbracelet/fang"

	"devbox-cli/internal/command"
	"devbox-cli/internal/version"
)

func main() {
	root := command.NewRootCmd()

	// Custom error handler: suppress output for ErrSilent (command already
	// printed its own error), otherwise delegate to Fang's default styled output.
	errHandler := func(w io.Writer, styles fang.Styles, err error) {
		if errors.Is(err, command.ErrSilent) {
			return
		}
		fang.DefaultErrorHandler(w, styles, err)
	}

	err := fang.Execute(
		context.Background(),
		root,
		fang.WithVersion(version.Version),
		fang.WithCommit(version.Commit),
		fang.WithErrorHandler(errHandler),
	)
	if err != nil {
		os.Exit(1)
	}
}
