package lifecycle

import (
	"context"
	"io"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/bridge"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/usercommands"
	lifecyclepkg "github.com/semsemyonoff/dwe/internal/core/workflow/lifecycle"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"

	"github.com/spf13/cobra"
)

// init stubs PreflightFunc so CLI integration tests don't require a real
// Docker / git environment on the host. The bridge seams are stubbed for the
// same reason: the real prepare hook probes docker for image architectures
// and spawns a detached daemon via os.Executable() — re-executing the test
// binary (the documented recursion hazard).
func init() {
	lifecyclepkg.PreflightFunc = func(_ context.Context, _ *config.DweConfig, _ *usercommands.Registry, _, _ string, _ bool, _ io.Writer) error {
		return nil
	}
	lifecyclepkg.BridgePrepareFunc = func(bridge.PrepareOptions) error { return nil }
	lifecyclepkg.BridgeStopDaemonFunc = func(string) (bool, error) { return false, nil }

	// Per-service stop/restart resolve the real container name via compose
	// labels (docker.LookupServiceContainer), which would spawn `docker ps`.
	// Stub the seam so tests never touch a real daemon. The default reproduces
	// the historical "<project>-<service>" shape so existing name-assertion
	// tests keep verifying that the project name + service flow through
	// correctly; tests exercising the not-deployed path override it to "".
	lookupContainerFn = func(_ string, _ []string, projectName, service string) (string, error) {
		return projectName + "-" + service, nil
	}
}

// stubRunPhases replaces RunPhasesFunc with a no-op for the duration of t.
// Required for tests that exercise the built-in default run pipeline, which
// contains a type:dwe step whose os.Executable() invocation would otherwise
// recursively re-execute the test binary.
func stubRunPhases(t *testing.T) {
	t.Helper()
	prev := lifecyclepkg.RunPhasesFunc
	t.Cleanup(func() { lifecyclepkg.RunPhasesFunc = prev })
	lifecyclepkg.RunPhasesFunc = func(_ *config.DweConfig, _ *usercommands.Registry, _ string, _ []config.DeployPhase, _, _ string, _ bool, _ bool, _ i18n.Translator, _ string) error {
		return nil
	}
}

const (
	groupEnvironment = "environment"
	groupPipelines   = "pipelines"
)

// buildLifecycleTestRoot constructs a minimal cobra root that mirrors the
// composition root for lifecycle commands. It registers the environment +
// pipelines groups, attaches all four lifecycle subcommands, and exposes the
// persistent `--config` flag bound to flags.ConfigPath.
//
// It exists because importing the cli root package here would be an import
// cycle.
func buildLifecycleTestRoot(flags *cmdctx.RootFlags) *cobra.Command {
	root := &cobra.Command{Use: "dwe"}
	root.PersistentFlags().StringVar(&flags.ConfigPath, "config", "", "path to workspace.yml")
	root.AddGroup(
		&cobra.Group{ID: groupEnvironment, Title: "Environment Commands:"},
		&cobra.Group{ID: groupPipelines, Title: "Pipeline Commands:"},
	)
	root.AddCommand(NewRunCmd(groupEnvironment, flags))
	root.AddCommand(NewStopCmd(groupEnvironment, flags))
	root.AddCommand(NewRestartCmd(groupEnvironment, flags))
	root.AddCommand(NewResetCmd(groupPipelines, flags))
	return root
}
