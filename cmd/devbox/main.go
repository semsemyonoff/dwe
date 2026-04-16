package main

import (
	"context"
	"errors"
	"image/color"
	"io"
	"os"
	"path/filepath"

	"charm.land/fang/v2"
	lipglossv2 "charm.land/lipgloss/v2"
	"github.com/spf13/pflag"

	"devbox-cli/internal/command"
	"devbox-cli/internal/config"
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

	opts := []fang.Option{
		fang.WithVersion(version.Version),
		fang.WithCommit(version.Commit),
		fang.WithErrorHandler(errHandler),
	}

	// Load styles.yml early to customise the Fang help color scheme.
	// Resolve relative to the --config flag so it works from any directory.
	if cs := loadHelpColorScheme(configPathFromArgs()); cs != nil {
		opts = append(opts, fang.WithColorSchemeFunc(cs))
	}

	err := fang.Execute(
		context.Background(),
		root,
		opts...,
	)
	if err != nil {
		os.Exit(1)
	}
}

// configPathFromArgs extracts the --config / -c flag value from os.Args before
// cobra parses them. Uses pflag (the same parser Cobra uses internally) so flag
// semantics match exactly. Returns the default "devbox.yml" when the flag is
// absent or unparseable.
func configPathFromArgs() string {
	fs := pflag.NewFlagSet("pre-parse", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	cp := fs.StringP("config", "c", "devbox.yml", "")
	_ = fs.Parse(os.Args[1:])
	return *cp
}

// loadHelpColorScheme loads devbox/styles.yml relative to configPath and returns
// a fang ColorSchemeFunc if any help colors are configured. Returns nil when no
// overrides are set or the file cannot be read.
func loadHelpColorScheme(configPath string) fang.ColorSchemeFunc {
	stylesPath := filepath.Join(filepath.Dir(configPath), "devbox", "styles.yml")
	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	if err != nil || stylesCfg == nil {
		return nil
	}

	h := stylesCfg.Colors.Help
	if h.Title == "" && h.Command == "" && h.Flag == "" &&
		h.Program == "" && h.Description == "" && h.Argument == "" {
		return nil
	}

	return func(ld lipglossv2.LightDarkFunc) fang.ColorScheme {
		cs := fang.DefaultColorScheme(ld)
		if h.Title != "" {
			cs.Title = lipglossv2.Color(h.Title)
		}
		if h.Command != "" {
			cs.Command = lipglossv2.Color(h.Command)
		}
		if h.Flag != "" {
			cs.Flag = lipglossv2.Color(h.Flag)
		}
		if h.Program != "" {
			cs.Program = lipglossv2.Color(h.Program)
		}
		if h.Description != "" {
			c := lipglossv2.Color(h.Description)
			cs.Description = c
			cs.Base = c
		}
		if h.Argument != "" {
			c := lipglossv2.Color(h.Argument)
			cs.Argument = c
			cs.DimmedArgument = c
		}
		return cs
	}
}

// ensure lipglossv2.Color satisfies image/color.Color at compile time.
var _ color.Color = lipglossv2.Color("")
