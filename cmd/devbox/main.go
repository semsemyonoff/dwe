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
	"devbox-cli/internal/core/execution/pipeline"
	"devbox-cli/internal/core/project/config"
	"devbox-cli/internal/core/project/project"
	"devbox-cli/internal/shared/prompt"
	"devbox-cli/internal/shared/version"
	"devbox-cli/internal/ui"
)

func main() {
	if isPromptInvocation(os.Args) {
		os.Exit(prompt.Run(os.Stdout, os.Args[2:]))
	}

	root := command.NewRootCmd()

	// Custom error handler: suppress output for ErrSilent (command already
	// printed its own error) and for ExitCode-bearing errors (which have already
	// printed their own diagnostics table). Otherwise delegate to Fang's default styled output.
	errHandler := func(w io.Writer, styles fang.Styles, err error) {
		if errors.Is(err, pipeline.ErrSilent) {
			return
		}
		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) {
			// validation or other exit-code error: diagnostics already printed
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
	configPath, explicit := configPathFromArgs()
	if cs := loadHelpColorScheme(configPath, explicit); cs != nil {
		opts = append(opts, fang.WithColorSchemeFunc(cs))
	}

	err := fang.Execute(
		context.Background(),
		root,
		opts...,
	)
	if err != nil {
		var ec interface{ ExitCode() int }
		if errors.As(err, &ec) {
			os.Exit(ec.ExitCode())
		}
		os.Exit(1)
	}
}

// isPromptInvocation returns true only for `devbox prompt` and `devbox prompt --check`.
// Any other shape (e.g. `prompt --help`, `prompt foo`) returns false so cobra handles
// help and unknown-arg errors normally.
func isPromptInvocation(argv []string) bool {
	if len(argv) < 2 || argv[1] != "prompt" {
		return false
	}
	rest := argv[2:]
	if len(rest) == 0 {
		return true
	}
	return len(rest) == 1 && rest[0] == "--check"
}

// configPathFromArgs extracts the --config / -c flag value from os.Args before
// cobra parses them. Uses pflag (the same parser Cobra uses internally) so flag
// semantics match exactly. Returns ("", false) when the flag is absent or
// unparseable, and (path, true) when explicitly supplied.
func configPathFromArgs() (path string, explicit bool) {
	fs := pflag.NewFlagSet("pre-parse", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	cp := fs.StringP("config", "c", "", "")
	_ = fs.Parse(os.Args[1:])
	f := fs.Lookup("config")
	if f != nil && f.Changed {
		return *cp, true
	}
	return "", false
}

// loadHelpColorScheme loads devbox/styles.yml relative to the located project root
// and returns a fang ColorSchemeFunc if any help colors are configured. Returns nil
// when no overrides are set, the file cannot be read, or no project is found.
// This function is intentionally silent under all failure modes — it runs before
// cobra parses flags, so Fang's default colors apply whenever styles cannot be loaded.
func loadHelpColorScheme(configPath string, explicit bool) fang.ColorSchemeFunc {
	locatePath := ""
	if explicit {
		locatePath = configPath
	}
	resolved, found, err := project.Locate(locatePath)
	if err != nil || !found {
		return nil
	}
	stylesPath := filepath.Join(resolved.Root, "devbox", "styles.yml")
	if _, statErr := os.Stat(stylesPath); statErr != nil {
		return nil
	}
	stylesCfg, err := config.LoadStylesConfig(stylesPath)
	if err != nil || stylesCfg == nil {
		return nil
	}
	// Side-effect: ensure the 7-token palette is resolved before we read
	// ui.ColorAccent() / ui.ColorMuted() below. ApplyStyles is the canonical
	// resolution point and is otherwise called from the cobra root PreRunE,
	// which has not run yet at this point in startup.
	ui.ApplyStyles(stylesCfg)

	accent := ui.ColorAccent()
	muted := ui.ColorMuted()

	return func(ld lipglossv2.LightDarkFunc) fang.ColorScheme {
		cs := fang.DefaultColorScheme(ld)
		cs.Title = lipglossv2.Color(accent)
		cs.Command = lipglossv2.Color(accent)
		cs.Flag = lipglossv2.Color(accent)
		cs.Program = lipglossv2.Color(accent)
		// Description and Base stay at fang's defaults (charmtone.Ash on dark,
		// Charcoal on light) so command/flag descriptions read as primary text
		// rather than dim. Argument placeholders ([command], [--flags]) and
		// dimmed arguments are intentionally muted — they're secondary cues
		// inside the usage line and shouldn't compete with command names.
		mutedCol := lipglossv2.Color(muted)
		cs.Argument = mutedCol
		cs.DimmedArgument = mutedCol
		return cs
	}
}

// ensure lipglossv2.Color satisfies image/color.Color at compile time.
var _ color.Color = lipglossv2.Color("")
