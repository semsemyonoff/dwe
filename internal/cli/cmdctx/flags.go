// Package cmdctx holds the runtime state and flag helpers shared across
// every command package. It is the only package allowed to be imported by
// all command-domain packages (deploy, render, service, lifecycle, …).
package cmdctx

import (
	"path/filepath"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// RootFlags is populated by the root command's PersistentPreRunE and read
// by every subcommand. I18n and Locale are guaranteed non-nil after
// PersistentPreRunE completes; completion paths (__complete) bypass
// PersistentPreRunE so completion handlers must tolerate zero values.
type RootFlags struct {
	ConfigPath string
	Root       string
	StylesCfg  *config.StylesConfig
	Locale     string
	I18n       *i18n.Store
	Output     string // "text" (default) | "json"
	Pretty     bool   // indent JSON when Output=="json"
}

// ProjectRoot returns the resolved project root. Falls back to the config
// file's directory so tests constructing RootFlags directly (without
// running PersistentPreRunE) still work.
func (f *RootFlags) ProjectRoot() string {
	if f.Root != "" {
		return f.Root
	}
	if f.ConfigPath != "" {
		return filepath.Dir(f.ConfigPath)
	}
	return ""
}

// AddSkipPreflight registers --skip-preflight on a lifecycle command.
// Intentionally not on RootFlags — only lifecycle commands have a preflight
// to skip.
func AddSkipPreflight(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "skip-preflight", false,
		"bypass environment probes and project checks before running")
}

// AddSilent registers --silent to suppress the end-of-operation desktop
// notification on commands that send one.
func AddSilent(cmd *cobra.Command, target *bool) {
	cmd.Flags().BoolVar(target, "silent", false,
		"suppress the end-of-run desktop notification")
}
