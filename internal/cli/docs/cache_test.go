package docs

import (
	"bytes"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/spf13/cobra"
)

func TestDocsCacheCommand(t *testing.T) {
	t.Run("cache subcommand is registered", func(t *testing.T) {
		flags := &cmdctx.RootFlags{}
		cmd := newDocsCacheCmd(flags)
		if cmd.Name() != "cache" {
			t.Errorf("expected 'cache', got %s", cmd.Name())
		}
		if cmd.Commands() == nil || len(cmd.Commands()) == 0 {
			t.Errorf("expected at least one subcommand")
		}
		// Find the clear subcommand
		found := false
		for _, sub := range cmd.Commands() {
			if sub.Name() == "clear" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("clear subcommand not found")
		}
	})

	t.Run("clear command has correct cobra setup", func(t *testing.T) {
		cmd := newDocsCacheClearCmd()
		if cmd.Use != "clear" {
			t.Errorf("expected Use='clear', got %q", cmd.Use)
		}
		if cmd.Short == "" {
			t.Errorf("expected Short description")
		}
		if cmd.Long == "" {
			t.Errorf("expected Long description")
		}
		if cmd.Args == nil {
			t.Errorf("expected Args to be set")
		}
		if cmd.RunE == nil {
			t.Errorf("expected RunE to be set")
		}
	})

	t.Run("parent cache command does not have RunE", func(t *testing.T) {
		flags := &cmdctx.RootFlags{}
		cmd := newDocsCacheCmd(flags)
		if cmd.RunE != nil {
			t.Errorf("cache parent command should not have RunE (cobra prints help by default)")
		}
	})

	t.Run("clear command respects NoArgs validation", func(t *testing.T) {
		cmd := newDocsCacheClearCmd()
		// Verify Args is set (Args should validate no arguments)
		if cmd.Args == nil {
			t.Errorf("clear command should have Args validation set")
		}
	})

	t.Run("runDocsCacheClear produces output", func(t *testing.T) {
		// Create a test command with a custom output buffer
		cmd := &cobra.Command{
			Use: "test",
		}
		stdout := &bytes.Buffer{}
		cmd.SetOut(stdout)

		// Run the clear function (it will use the real cache dir, but that's ok for this test)
		// Just verify it runs without panicking
		_ = runDocsCacheClear(cmd)
		// Just ensure it produced some output
		if stdout.Len() == 0 {
			t.Errorf("expected some output from clear command")
		}
	})

	t.Run("clear command sets SilenceUsage", func(t *testing.T) {
		cmd := newDocsCacheClearCmd()
		if !cmd.SilenceUsage {
			t.Errorf("expected SilenceUsage to be true")
		}
	})

	t.Run("clear command short description is not empty", func(t *testing.T) {
		cmd := newDocsCacheClearCmd()
		if cmd.Short == "" {
			t.Errorf("expected non-empty Short description")
		}
	})

	t.Run("clear command long description is not empty", func(t *testing.T) {
		cmd := newDocsCacheClearCmd()
		if cmd.Long == "" {
			t.Errorf("expected non-empty Long description")
		}
	})

	t.Run("cache command short description is not empty", func(t *testing.T) {
		flags := &cmdctx.RootFlags{}
		cmd := newDocsCacheCmd(flags)
		if cmd.Short == "" {
			t.Errorf("expected non-empty Short description for cache command")
		}
	})

	t.Run("cache command long description is not empty", func(t *testing.T) {
		flags := &cmdctx.RootFlags{}
		cmd := newDocsCacheCmd(flags)
		if cmd.Long == "" {
			t.Errorf("expected non-empty Long description for cache command")
		}
	})
}
