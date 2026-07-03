package widgets

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	huh "charm.land/huh/v2"
)

// resetHooks clears the package-level hooks. Used as t.Cleanup so each test
// starts and ends with no hooks installed.
func resetHooks(t *testing.T) {
	t.Helper()
	ClearHuhHooks()
	t.Cleanup(ClearHuhHooks)
}

// TestRunConfirm_SeamSwapBypassesHooks documents the post-RunHuhForm
// boundary: prompt hooks now live inside the seam-default implementation
// (defaultRunConfirmForm -> RunHuhForm), not in the RunConfirm wrapper, so a
// seam-swapped test (as used throughout confirm_test.go) never triggers
// them. The hook-pairing contract itself is covered by the TestRunHuhForm_*
// tests below, which exercise the real production path.
func TestRunConfirm_SeamSwapBypassesHooks(t *testing.T) {
	resetHooks(t)

	origConfirm := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = origConfirm })

	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		order = append(order, "form")
		return true, nil
	}
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "form" {
		t.Errorf("expected only the seam to run (hooks live in RunHuhForm now), got %v", order)
	}
}

// TestConfirmRun_SeamSwapBypassesHooks mirrors TestRunConfirm_SeamSwapBypassesHooks
// for the ConfirmRun wrapper: hooks live in the seam default (via RunHuhForm), not
// in the wrapper, so a seam-swapped test must not observe them fire. A non-empty
// values map is required so ConfirmRun hits runConfirmRunFormFn rather than falling
// back to RunConfirm.
func TestConfirmRun_SeamSwapBypassesHooks(t *testing.T) {
	resetHooks(t)

	orig := runConfirmRunFormFn
	t.Cleanup(func() { runConfirmRunFormFn = orig })

	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)

	runConfirmRunFormFn = func(title, summary string) (bool, error) {
		order = append(order, "form")
		return true, nil
	}
	if _, err := ConfirmRun("?", map[string]string{"k": "v"}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "form" {
		t.Errorf("expected only the seam to run (hooks live in RunHuhForm now), got %v", order)
	}
}

// TestRunSelector_SeamSwapBypassesHooks guards the same wrapper-fires-no-hooks
// contract for RunSelector.
func TestRunSelector_SeamSwapBypassesHooks(t *testing.T) {
	resetHooks(t)

	orig := runSelectFormFn
	t.Cleanup(func() { runSelectFormFn = orig })

	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)

	runSelectFormFn = func(title string, opts []huh.Option[int]) (int, error) {
		order = append(order, "form")
		return 0, nil
	}
	if _, err := RunSelector("?", []SelectorItem{{Label: "a"}}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "form" {
		t.Errorf("expected only the seam to run (hooks live in RunHuhForm now), got %v", order)
	}
}

// TestRunMultiSelect_SeamSwapBypassesHooks guards the same wrapper-fires-no-hooks
// contract for RunMultiSelect (needs a toggleable item so the form is not skipped).
func TestRunMultiSelect_SeamSwapBypassesHooks(t *testing.T) {
	resetHooks(t)

	orig := runMultiSelectFormFn
	t.Cleanup(func() { runMultiSelectFormFn = orig })

	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)

	runMultiSelectFormFn = func(title string, opts []huh.Option[string]) ([]string, error) {
		order = append(order, "form")
		return nil, nil
	}
	if _, err := RunMultiSelect("?", []MultiSelectItem{{Key: "a", Label: "A"}}); err != nil {
		t.Fatal(err)
	}
	if len(order) != 1 || order[0] != "form" {
		t.Errorf("expected only the seam to run (hooks live in RunHuhForm now), got %v", order)
	}
}

func TestSnapshotHuhHooks_NilSafe(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

	// No hooks installed — must not panic.
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
}

func TestHuhHooks_ConcurrentSetAndSnapshot(t *testing.T) {
	resetHooks(t)

	const writers, readers, iters = 100, 100, 200

	var wg sync.WaitGroup
	var invokes atomic.Int64

	for range writers {
		wg.Go(func() {
			for j := range iters {
				if j%2 == 0 {
					SetHuhHooks(func() {}, func() {})
				} else {
					ClearHuhHooks()
				}
			}
		})
	}

	for range readers {
		wg.Go(func() {
			for range iters {
				before, after := snapshotHuhHooks()
				if before != nil {
					before()
				}
				if after != nil {
					after()
				}
				invokes.Add(1)
			}
		})
	}

	wg.Wait()
	if invokes.Load() == 0 {
		t.Fatal("expected at least one snapshot invocation")
	}
}

// --- RunWithPromptHooks tests ---

func TestRunWithPromptHooks_Order(t *testing.T) {
	resetHooks(t)
	var order []string
	SetHuhHooks(
		func() { order = append(order, "before") },
		func() { order = append(order, "after") },
	)
	err := RunWithPromptHooks(func() error {
		order = append(order, "fn")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "before" || order[1] != "fn" || order[2] != "after" {
		t.Errorf("order=%v", order)
	}
}

func TestRunWithPromptHooks_AfterFiresOnError(t *testing.T) {
	resetHooks(t)
	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })
	sentinel := errors.New("boom")
	err := RunWithPromptHooks(func() error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Errorf("want sentinel, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire on error")
	}
}

func TestRunWithPromptHooks_NilSafe(t *testing.T) {
	resetHooks(t)
	if err := RunWithPromptHooks(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithPromptHooks_SurvivesMidClear(t *testing.T) {
	resetHooks(t)
	var afterCalled bool
	SetHuhHooks(func() {}, func() { afterCalled = true })
	err := RunWithPromptHooks(func() error {
		ClearHuhHooks()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when cleared mid-fn (snapshot semantics)")
	}
}

// --- RunHuhForm tests ---

// blockingForm returns a form that blocks reading from a never-closing input,
// so a short context timeout deterministically produces a non-abort error from
// form.RunWithContext.
func blockingForm(t *testing.T) *huh.Form {
	t.Helper()
	r, _ := io.Pipe() // never written to: read blocks until the reader is closed
	t.Cleanup(func() { _ = r.Close() })
	return huh.NewForm(huh.NewGroup(huh.NewInput().Title("Foo"))).
		WithInput(r).
		WithOutput(io.Discard)
}

func TestRunHuhForm_HooksFireExactlyOnce(t *testing.T) {
	resetHooks(t)
	var before, after atomic.Int64
	SetHuhHooks(
		func() { before.Add(1) },
		func() { after.Add(1) },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := RunHuhForm(ctx, blockingForm(t)); err == nil {
		t.Fatal("expected a context-related error from a form that never submits")
	}
	if before.Load() != 1 {
		t.Errorf("before hook fired %d times, want 1", before.Load())
	}
	if after.Load() != 1 {
		t.Errorf("after hook fired %d times, want 1", after.Load())
	}
}

func TestRunHuhForm_HooksFireOnError(t *testing.T) {
	resetHooks(t)
	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := RunHuhForm(ctx, blockingForm(t)); err == nil {
		t.Fatal("expected a context-related error")
	}
	if !afterCalled {
		t.Error("after hook must fire even when the form returns an error")
	}
}

func TestRunHuhForm_NonAbortErrorPassesThrough(t *testing.T) {
	resetHooks(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	err := RunHuhForm(ctx, blockingForm(t))
	if err == nil {
		t.Fatal("expected a context-related error")
	}
	if errors.Is(err, ErrCancelled) {
		t.Errorf("a context timeout must not be translated to ErrCancelled, got %v", err)
	}
}

func TestRunHuhForm_AbortTranslatesToErrCancelled(t *testing.T) {
	resetHooks(t)

	form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("Foo"))).
		WithInput(nil).
		WithOutput(io.Discard).
		WithAccessible(false)

	// Mirrors huh's own TestAbort: queue a ctrl+c keypress, then cancel the
	// context so RunWithContext exits immediately with ErrUserAborted.
	form.Update(tea.KeyPressMsg(tea.Key{Mod: tea.ModCtrl, Code: 'c'}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunHuhForm(ctx, form)
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", err)
	}
	if errors.Is(err, huh.ErrUserAborted) {
		t.Error("RunHuhForm must not leak the raw huh.ErrUserAborted sentinel")
	}
}

func TestRunHuhForm_NoHooksInstalled(t *testing.T) {
	resetHooks(t)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// Must not panic when no hooks are installed.
	if err := RunHuhForm(ctx, blockingForm(t)); err == nil {
		t.Fatal("expected a context-related error")
	}
}
