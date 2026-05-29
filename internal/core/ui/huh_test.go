package ui

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	huh "charm.land/huh/v2"
)

// resetHooks clears the package-level hooks. Used as t.Cleanup so each test
// starts and ends with no hooks installed.
func resetHooks(t *testing.T) {
	t.Helper()
	ClearHuhHooks()
	t.Cleanup(ClearHuhHooks)
}

func TestSetHuhHooks_FiresAroundConfirm(t *testing.T) {
	resetHooks(t)

	origConfirm := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = origConfirm })
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return true, nil
	}

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
	if len(order) != 3 || order[0] != "before" || order[1] != "form" || order[2] != "after" {
		t.Errorf("expected before/form/after, got %v", order)
	}
}

func TestSetHuhHooks_AfterFiresOnError(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })

	sentinel := errors.New("form failed")
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, sentinel
	}
	_, err := RunConfirm("?", "Y", "N")
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when the form returns an error")
	}
}

func TestSetHuhHooks_AfterFiresOnCancel(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(nil, func() { afterCalled = true })

	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		return false, huh.ErrUserAborted
	}
	_, err := RunConfirm("?", "Y", "N")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", err)
	}
	if !afterCalled {
		t.Error("after hook must fire on user-cancel path")
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

func TestSnapshotHuhHooks_SurvivesMidPromptClear(t *testing.T) {
	resetHooks(t)

	orig := runConfirmFormFn
	t.Cleanup(func() { runConfirmFormFn = orig })

	var afterCalled bool
	SetHuhHooks(
		func() {},
		func() { afterCalled = true },
	)

	// Simulate a mid-prompt ClearHuhHooks. The snapshot taken at RunConfirm
	// entry guarantees the after hook still fires.
	runConfirmFormFn = func(title, affirmative, negative string) (bool, error) {
		ClearHuhHooks()
		return true, nil
	}
	if _, err := RunConfirm("?", "Y", "N"); err != nil {
		t.Fatal(err)
	}
	if !afterCalled {
		t.Error("after hook must fire even when hooks were cleared mid-prompt")
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
