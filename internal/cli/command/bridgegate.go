package command

import (
	"fmt"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
)

// bridgeGuard rejects the invocation of a command that is not opted in to
// the container surface. def.BridgeHidden is resolved by ApplyVisibility and
// is true only for bridged invocations (DWE_INVOKED_FROM=container) of
// commands whose merged `bridge:` block does not admit the calling service —
// on the host this always returns nil. Unlike the Hidden guard the gate also
// covers inspect: from a container the command simply does not exist.
func bridgeGuard(def *usercommands.CommandDef) error {
	if def == nil || !def.BridgeHidden {
		return nil
	}
	return cmdctx.Err("command_not_bridged",
		fmt.Sprintf("command %q is not available from containers", def.ID)).
		WithDetail("id", def.ID).
		WithHint("Run it on the host, or opt it in with a `bridge:` block (`enabled: true`) on the command or its file group.")
}
