package service

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"

	"github.com/stretchr/testify/require"
)

// TestServicesListSubcommandExists guards the help/surface mismatch that made
// `dwe services list` fail: the top-level `dwe --help` line for services has
// always read "Toggle optional services (interactive) or list / enable /
// disable", but only enable and disable were registered, so following the help
// produced `Unknown command "list" for "dwe services"`.
func TestServicesListSubcommandExists(t *testing.T) {
	root := NewCmd("", &cmdctx.RootFlags{Locale: "en"})

	var names []string
	for _, sub := range root.Commands() {
		names = append(names, sub.Name())
	}

	require.Contains(t, names, "list",
		"the top-level help advertises `list`; it must exist as a subcommand")
	require.Contains(t, names, "enable")
	require.Contains(t, names, "disable")
}

// TestServicesShortDescriptionMatchesSurface pins the help text against the
// registered verbs, so a future verb rename cannot silently re-open the gap.
func TestServicesShortDescriptionMatchesSurface(t *testing.T) {
	root := NewCmd("", &cmdctx.RootFlags{Locale: "en"})

	registered := map[string]bool{}
	for _, sub := range root.Commands() {
		registered[sub.Name()] = true
	}

	for _, verb := range []string{"list", "enable", "disable"} {
		if !strings.Contains(root.Short, verb) {
			continue
		}
		require.True(t, registered[verb],
			"`dwe services` short help promises %q but no such subcommand is registered", verb)
	}
}

// TestServicesListRejectsArgs: the listing takes no positional args. A stray one
// (e.g. `dwe services list backend`, guessing a per-service filter) must fail
// loudly rather than silently listing everything.
func TestServicesListRejectsArgs(t *testing.T) {
	cmd := newServiceListCmd(&cmdctx.RootFlags{Locale: "en"})

	require.NotNil(t, cmd.Args)
	require.Error(t, cmd.Args(cmd, []string{"backend"}))
	require.NoError(t, cmd.Args(cmd, nil))
}
