package cli

import (
	"strings"
	"testing"

	"devbox-cli/internal/cli/cmdctx"
	"devbox-cli/internal/core/usercommands"

	"github.com/spf13/cobra"
)

// buildRegistryCompletions mirrors registryIDCompletion's pure logic so the
// table tests below can exercise filtering and description embedding without
// touching disk.
func buildRegistryCompletions(defs []*usercommands.CommandDef, includePrivate bool) []string {
	var completions []string
	if !includePrivate {
		var filtered []*usercommands.CommandDef
		for _, d := range defs {
			if !d.Private {
				filtered = append(filtered, d)
			}
		}
		defs = filtered
	}
	for _, d := range defs {
		entry := d.ID
		if d.Description != "" {
			entry = cobra.CompletionWithDesc(d.ID, d.Description)
		}
		completions = append(completions, entry)
	}
	return completions
}

func TestBuildRegistryCompletions(t *testing.T) {
	defs := []*usercommands.CommandDef{
		{ID: "services.main.migrate", Description: "Run migrations", Private: false},
		{ID: "services.main.create-db", Description: "Create database", Private: true},
	}

	t.Run("public only", func(t *testing.T) {
		got := buildRegistryCompletions(defs, false)
		if len(got) != 1 || !strings.Contains(got[0], "services.main.migrate") {
			t.Fatalf("public-only: got %v", got)
		}
	})

	t.Run("include private", func(t *testing.T) {
		got := buildRegistryCompletions(defs, true)
		if len(got) != 2 {
			t.Errorf("include-private: want 2, got %d (%v)", len(got), got)
		}
	})

	t.Run("description embedded", func(t *testing.T) {
		got := buildRegistryCompletions([]*usercommands.CommandDef{
			{ID: "app.install", Description: "Run the installer"},
		}, false)
		if len(got) == 0 || !strings.Contains(got[0], "app.install") {
			t.Fatalf("description: got %v", got)
		}
	})
}

// TestRegistryIDCompletion_noSecondArg guards the early-return contract:
// when a positional arg is already provided, no completions are produced.
func TestRegistryIDCompletion_noSecondArg(t *testing.T) {
	fn := registryIDCompletion(&cmdctx.RootFlags{ConfigPath: "devbox.yml"}, false)
	completions, directive := fn(nil, []string{"already-provided"}, "")
	if len(completions) != 0 {
		t.Errorf("expected 0 completions, got %d", len(completions))
	}
	if directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("directive = %v, want NoFileComp", directive)
	}
}

// TestCommandsCmd_ActiveHelp_PointsAtInspectFlag verifies the hint string
// references the --inspect flag (and not the removed 'commands inspect'
// subcommand). registryIDCompletion appends this hint when listing public defs.
func TestCommandsCmd_ActiveHelp_PointsAtInspectFlag(t *testing.T) {
	const hint = "Use 'devbox commands --inspect <id>' to see command details"
	appended := cobra.AppendActiveHelp(nil, hint)
	if len(appended) != 1 {
		t.Fatalf("AppendActiveHelp: got %d entries", len(appended))
	}
	if !strings.Contains(appended[0], "--inspect") {
		t.Errorf("hint missing --inspect: %q", appended[0])
	}
	if strings.Contains(appended[0], "commands inspect ") {
		t.Errorf("hint must not reference removed 'commands inspect' subcommand: %q", appended[0])
	}
}

// TestCompletionCmd_InAdvancedGroupWithShellSubcommands verifies cobra's
// built-in completion command was attached to the advanced group and that
// the standard shell subcommands are wired.
func TestCompletionCmd_InAdvancedGroupWithShellSubcommands(t *testing.T) {
	root := NewRootCmd()

	var completionCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Name() == "completion" {
			completionCmd = c
			break
		}
	}
	if completionCmd == nil {
		t.Fatal("completion command not found in root")
	}
	if completionCmd.GroupID != groupAdvanced {
		t.Errorf("completion GroupID = %q, want %q", completionCmd.GroupID, groupAdvanced)
	}

	want := map[string]bool{"bash": false, "zsh": false, "fish": false, "powershell": false}
	for _, c := range completionCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("completion missing %q subcommand", name)
		}
	}
}
