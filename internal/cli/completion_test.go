package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

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
