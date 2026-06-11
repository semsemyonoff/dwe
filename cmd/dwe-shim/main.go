// Command dwe-shim is the bridge shim mounted into dev containers as `dwe`.
// It forwards the invocation to the host-side dwe daemon over the bridge
// transport (unix socket, then TCP — see internal/shared/bridgeclient) and
// mirrors the subprocess exit code.
//
// Built static (CGO_ENABLED=0) for linux amd64/arm64 by scripts/build-shims.sh
// and embedded into the host dwe binary. Mirrors internal/shared/prompt's
// minimal-binary philosophy: no cobra, no lipgloss, no project model.
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/semsemyonoff/dwe/internal/shared/bridgeclient"
)

func main() {
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	os.Exit(bridgeclient.Run(bridgeclient.OptionsFromEnv(os.Args[1:], sigs)))
}
