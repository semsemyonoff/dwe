package statustui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"devbox-cli/internal/command/statusview"
	"devbox-cli/internal/config"
)

func TestMapRunError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		expectNil bool
		expectErr error
	}{
		{
			name:      "nil error",
			err:       nil,
			expectNil: true,
		},
		{
			name:      "ErrInterrupted maps to nil",
			err:       tea.ErrInterrupted,
			expectNil: true,
		},
		{
			name:      "ErrProgramKilled maps to nil",
			err:       tea.ErrProgramKilled,
			expectNil: true,
		},
		{
			name:      "ErrProgramPanic returns non-nil",
			err:       tea.ErrProgramPanic,
			expectNil: false,
		},
		{
			name:      "panic wrapped in ErrProgramKilled returns non-nil",
			err:       fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic),
			expectNil: false,
		},
		{
			name:      "arbitrary error is returned verbatim",
			err:       errors.New("some error"),
			expectNil: false,
			expectErr: errors.New("some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mapRunError(tt.err)
			if tt.expectNil {
				require.Nil(t, result)
			} else {
				require.NotNil(t, result)
				if tt.expectErr != nil {
					require.Equal(t, tt.expectErr.Error(), result.Error())
				}
			}
		})
	}
}

func TestRun_NotATerminal_ReturnsError(t *testing.T) {
	origIsTerminalFn := isTerminalFn
	defer func() { isTerminalFn = origIsTerminalFn }()

	isTerminalFn = func(fd uintptr) bool {
		return false
	}

	ctx := context.Background()
	deps := Deps{
		Cfg:         &config.DevboxConfig{},
		ProjectName: "test",
	}

	err := Run(ctx, deps)
	require.Error(t, err)
	require.Equal(t, "statustui: not a terminal", err.Error())
}

// TestRun_ReloadThenQuit_CancelsInflightContext tests that when the user
// quits mid-reload, in-flight data-fetch operations (CollectDaemons,
// CollectGitWorkspace) are canceled via context. Tests by calling buildTabsCmd
// directly with a cancellable context, then canceling and confirming that
// collectDaemons responds promptly.
//
// Note: This test does not use t.Parallel() because it mutates package-level
// globals (collectDaemonsFn, collectGitWorkspaceFn).
func TestRun_ReloadThenQuit_CancelsInflightContext(t *testing.T) {
	// Save and restore the package-level seams
	origCollectDaemons := collectDaemonsFn
	origCollectGitWorkspace := collectGitWorkspaceFn

	defer func() {
		collectDaemonsFn = origCollectDaemons
		collectGitWorkspaceFn = origCollectGitWorkspace
	}()

	// Channels to signal test events
	started := make(chan struct{})
	exited := make(chan struct{})

	// Override collectDaemonsFn to block on context cancellation
	collectDaemonsFn = func(ctx context.Context, _ *config.DevboxConfig, _ *config.DockerConfig) ([]statusview.DaemonRow, []error) {
		close(started) // Signal that collectDaemons has started
		<-ctx.Done()   // Wait for context cancellation
		close(exited)  // Signal that collectDaemons has exited
		return nil, nil
	}

	// Override collectGitWorkspaceFn to return immediately
	// (if git was slow, we wouldn't reach the daemons stage)
	collectGitWorkspaceFn = func(ctx context.Context, _ *config.DevboxConfig, _ string) []statusview.GitWorkspaceRow {
		return nil
	}

	// Create a cancellable context for buildTabs
	buildCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deps := Deps{
		Cfg:         &config.DevboxConfig{},
		ProjectName: "test",
	}

	// Run buildTabsCmd in a goroutine so it can execute concurrently
	cmdFunc := buildTabsCmd(buildCtx, deps, 1)
	go func() {
		_ = cmdFunc() // Run the command; ignore the result
	}()

	// Wait for collectDaemons to start
	select {
	case <-started:
		// Good, collectDaemons is running
	case <-time.After(2 * time.Second):
		t.Fatal("collectDaemons did not start within timeout")
	}

	// Cancel the context (simulates quit-during-load)
	cancel()

	// Assert that the collectDaemons stub exits within 100ms
	select {
	case <-exited:
		// Good, collectDaemons exited cleanly via context cancellation
	case <-time.After(100 * time.Millisecond):
		t.Fatal("collectDaemons did not exit within 100ms after context cancellation")
	}
}
