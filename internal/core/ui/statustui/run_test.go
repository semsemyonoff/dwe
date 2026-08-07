package statustui

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/render"
	"github.com/semsemyonoff/dwe/internal/core/ui/statusview"
	"github.com/semsemyonoff/dwe/internal/core/ui/tui"
	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

func TestRun_ErrTooNarrow_PassesThroughUnchanged(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, tui.ErrTooNarrow
	}

	err := Run(context.Background(), Deps{})
	if !errors.Is(err, tui.ErrTooNarrow) {
		t.Errorf("Run() error = %v, want tui.ErrTooNarrow passed through unchanged", err)
	}
}

func TestRun_ErrNotTTY_PassesThroughUnchanged(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, tui.ErrNotTTY
	}

	err := Run(context.Background(), Deps{})
	if !errors.Is(err, tui.ErrNotTTY) {
		t.Errorf("Run() error = %v, want tui.ErrNotTTY passed through unchanged", err)
	}
}

func TestRun_CleanExit_ReturnsNil(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		return nil, nil
	}

	if err := Run(context.Background(), Deps{}); err != nil {
		t.Errorf("Run() = %v, want nil on clean exit", err)
	}
}

func TestRun_ClosePluginCancelsRunContext(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })

	var gotPlugin tui.Plugin
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		gotPlugin = p
		return nil, nil
	}

	if err := Run(context.Background(), Deps{}); err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}

	sp, ok := gotPlugin.(*plugin)
	if !ok {
		t.Fatalf("runStatusTUI plugin = %T, want *plugin", gotPlugin)
	}
	if err := sp.m.ctx.Err(); err != nil {
		t.Fatalf("model ctx.Err() before Close() = %v, want nil", err)
	}
	if err := sp.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := sp.m.ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("model ctx.Err() after Close() = %v, want context.Canceled", err)
	}
}

func TestRun_ThreadsBrandProjectAndI18n(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })

	var gotOpts tui.RunOptions
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		gotOpts = opts
		return nil, nil
	}

	tr := i18n.NopTranslator{}
	cfg := &config.DweConfig{Project: config.ProjectConfig{Name: "myproject"}}
	_ = Run(context.Background(), Deps{Cfg: cfg, Translator: tr, Locale: "ru"})

	// The status dashboard advertises itself like the other TUIs: a single Brand
	// string "{▪} DWE · <project> · Status" (via the shared BrandedTitleForConfig
	// helper, sourced from cfg — the bare project name, not the compose name) and
	// an empty Project field.
	wantBrand := render.BrandedTitleForConfig(cfg, statusTitleBase)
	if gotOpts.Brand != wantBrand {
		t.Errorf("RunOptions.Brand = %q, want %q", gotOpts.Brand, wantBrand)
	}
	if gotOpts.Project != "" {
		t.Errorf("RunOptions.Project = %q, want empty", gotOpts.Project)
	}
	if !gotOpts.Mouse {
		t.Error("RunOptions.Mouse = false, want true")
	}
	if gotOpts.Locale != "ru" {
		t.Errorf("RunOptions.Locale = %q, want %q", gotOpts.Locale, "ru")
	}
}

func TestRun_NilTranslatorFallsBackToNop(t *testing.T) {
	orig := runStatusTUI
	t.Cleanup(func() { runStatusTUI = orig })

	var gotOpts tui.RunOptions
	runStatusTUI = func(p tui.Plugin, opts tui.RunOptions) (any, error) {
		gotOpts = opts
		return nil, nil
	}

	_ = Run(context.Background(), Deps{})

	if gotOpts.Translator == nil {
		t.Fatal("RunOptions.Translator = nil, want i18n.NopTranslator fallback")
	}
	if _, ok := gotOpts.Translator.(i18n.NopTranslator); !ok {
		t.Errorf("RunOptions.Translator = %T, want i18n.NopTranslator", gotOpts.Translator)
	}
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
	collectDaemonsFn = func(ctx context.Context, _ *config.DweConfig, _ *config.DockerConfig, _ string) ([]statusview.DaemonRow, []error) {
		close(started) // Signal that collectDaemons has started
		<-ctx.Done()   // Wait for context cancellation
		close(exited)  // Signal that collectDaemons has exited
		return nil, nil
	}

	// Override collectGitWorkspaceFn to return immediately
	// (if git was slow, we wouldn't reach the daemons stage)
	collectGitWorkspaceFn = func(ctx context.Context, _ *config.DweConfig, _ string) []statusview.GitWorkspaceRow {
		return nil
	}

	buildCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deps := Deps{
		Cfg: &config.DweConfig{},
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
