package vars

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/ask"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
	"github.com/semsemyonoff/dwe/internal/shared/lock"
)

// TestBuildVarsBrowserItems asserts vars leaves map onto cmdbrowser.Items with
// the dot-path as ID (the namespace-tree key), the effective value in the
// description, and the originating layer as the type badge. The returned leaf
// slice is parallel to the items so Result.Idx indexes correctly.
func TestBuildVarsBrowserItems(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	items, leaves, _ := buildVarsBrowserItems(cfg, flags)

	if len(items) != len(leaves) {
		t.Fatalf("items (%d) and leaves (%d) must be parallel", len(items), len(leaves))
	}
	if len(items) != 3 {
		t.Fatalf("want 3 leaves, got %d", len(items))
	}

	// The browser ID is the DISPLAY path (vars. prefix stripped); resolution
	// uses the parallel leaves slice, which keeps the canonical full path.
	byID := map[string]cmdbrowser.Item{}
	for i, it := range items {
		if it.ID != strings.TrimPrefix(leaves[i], "vars.") {
			t.Errorf("item[%d].ID=%q, leaf=%q (ID must be the stripped leaf)", i, it.ID, leaves[i])
		}
		byID[it.ID] = it
	}

	host, ok := byID["db.host"]
	if !ok {
		t.Fatal("db.host missing from items")
	}
	// vars.db.host is overridden in local.yml → "local" badge, value override-host.
	if host.Type != "local" {
		t.Errorf("db.host badge: want local, got %q", host.Type)
	}
	if host.Description != "override-host" {
		t.Errorf("db.host description: want override-host, got %q", host.Description)
	}
	if host.Inspect == nil {
		t.Fatal("Inspect closure must be set")
	}
	if got := host.Inspect(80); got == "" {
		t.Error("Inspect(80) returned empty for a resolvable var")
	}

	// vars.app.name comes only from workspace.yml → "default" badge.
	if name := byID["app.name"]; name.Type != "default" {
		t.Errorf("app.name badge: want default, got %q", name.Type)
	}
}

// leafIdx returns the index of a leaf path within the parallel leaves slice.
func leafIdx(t *testing.T, leaves []string, path string) int {
	t.Helper()
	for i, l := range leaves {
		if l == path {
			return i
		}
	}
	t.Fatalf("leaf %q not found in %v", path, leaves)
	return -1
}

// holdProjectLock takes a raw exclusive flock on the deploy lock file (with the
// current PID written) so AcquireProjectLocks sees the project as held. Returns
// a cleanup func. Mirrors cmdctx/locks_test.go's acquireRawLock.
func holdProjectLock(t *testing.T, baseDir string) func() {
	t.Helper()
	path := lock.DeployLockPath(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("open lock file: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatalf("flock: %v", err)
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// TestVarsEditSpec_CommitUpdatesRowAndBadge exercises the EditSpec.Commit closure
// end-to-end: coercing the submitted value, writing it through the silent lock
// path, refreshing the row (value + layer badge), invalidating the inspect cache,
// and returning a ✓ flash.
func TestVarsEditSpec_CommitUpdatesRowAndBadge(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	items, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)

	// vars.app.name comes only from workspace.yml → badge "default" initially.
	idx := leafIdx(t, leaves, "vars.app.name")
	if items[idx].Type != "default" {
		t.Fatalf("pre-edit app.name badge: want default, got %q", items[idx].Type)
	}
	// Pre-populate the inspect cache for this path so we can assert invalidation.
	_ = items[idx].Inspect(80)
	if _, ok := inspectCache["vars.app.name"]; !ok {
		t.Fatal("inspect cache should be populated after Inspect()")
	}

	spec := newVarsEditSpec(flags, leaves, inspectCache)
	res := ask.NewResultForTest(map[string]any{"value": "renamed"})
	outcome, err := spec.Commit(idx, res)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	if outcome.Item.Description != "renamed" {
		t.Errorf("refreshed description: want renamed, got %q", outcome.Item.Description)
	}
	// The write lands in local.yml → badge flips default → local.
	if outcome.Item.Type != "local" {
		t.Errorf("refreshed badge: want local, got %q", outcome.Item.Type)
	}
	if !strings.Contains(outcome.Flash, "app.name = renamed") {
		t.Errorf("flash %q should contain the path=value confirmation", outcome.Flash)
	}
	if _, ok := inspectCache["vars.app.name"]; ok {
		t.Error("Commit must invalidate the edited leaf's inspect cache entry")
	}
	// The write is persisted.
	if got, ok := reloadVar(t, cfgPath, "vars.app.name"); !ok || got != "renamed" {
		t.Errorf("edit not written: got %v (ok=%v)", got, ok)
	}
}

// TestVarsEditSpec_CommitLockHeld asserts a held project lock surfaces as a
// returned error (the flash path) and leaves local.yml untouched.
func TestVarsEditSpec_CommitLockHeld(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	before := localYAML(t, root)

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)
	spec := newVarsEditSpec(flags, leaves, inspectCache)
	idx := leafIdx(t, leaves, "vars.db.host")

	cleanup := holdProjectLock(t, root)
	defer cleanup()

	res := ask.NewResultForTest(map[string]any{"value": "db.internal"})
	_, err = spec.Commit(idx, res)
	if err == nil {
		t.Fatal("expected lock-held error, got nil")
	}
	if _, ok := errors.AsType[*lock.ProjectLockHeldError](err); !ok {
		t.Fatalf("err = %T(%v), want *lock.ProjectLockHeldError", err, err)
	}
	// local.yml must be untouched — the write never ran.
	if after := localYAML(t, root); after != before {
		t.Errorf("local.yml changed under a held lock\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestVarsEditSpec_CommitInvalidScalar asserts a value CoerceScalar rejects (a
// map) is returned as an error without writing.
func TestVarsEditSpec_CommitInvalidScalar(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	before := localYAML(t, root)
	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)
	spec := newVarsEditSpec(flags, leaves, inspectCache)
	idx := leafIdx(t, leaves, "vars.db.host")

	res := ask.NewResultForTest(map[string]any{"value": "{a: b}"})
	if _, err := spec.Commit(idx, res); err == nil {
		t.Fatal("expected coercion error for a map value")
	}
	if after := localYAML(t, root); after != before {
		t.Errorf("local.yml changed on an invalid scalar\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestVarsEditSpec_IndexGuards asserts both closures reject an out-of-range idx.
func TestVarsEditSpec_IndexGuards(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)
	spec := newVarsEditSpec(flags, leaves, inspectCache)

	for _, idx := range []int{-1, len(leaves)} {
		if _, err := spec.BuildForm(idx); err == nil {
			t.Errorf("BuildForm(%d): expected out-of-range error", idx)
		}
		res := ask.NewResultForTest(map[string]any{"value": "x"})
		if _, err := spec.Commit(idx, res); err == nil {
			t.Errorf("Commit(%d): expected out-of-range error", idx)
		}
	}
}

// TestVarsEditSpec_BuildFormFields asserts BuildForm produces a runnable form
// and that the shared field builder carries the inspect description and the
// inline CoerceScalar validator.
func TestVarsEditSpec_BuildFormFields(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	cfg, err := loadConfigForVars(flags)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	_, leaves, inspectCache := buildVarsBrowserItems(cfg, flags)
	spec := newVarsEditSpec(flags, leaves, inspectCache)
	idx := leafIdx(t, leaves, "vars.db.host")

	form, err := spec.BuildForm(idx)
	if err != nil {
		t.Fatalf("BuildForm: %v", err)
	}
	if form == nil || form.Huh() == nil {
		t.Fatal("BuildForm returned a nil form / huh model")
	}

	// The shared field builder carries the per-layer description and an inline
	// validator that rejects a map but accepts a plain scalar.
	fields := buildVarSetFields(flags, "vars.db.host")
	if len(fields) != 1 {
		t.Fatalf("want 1 field, got %d", len(fields))
	}
	f := fields[0]
	if f.Kind != ask.FieldInput {
		t.Errorf("field kind: want FieldInput, got %v", f.Kind)
	}
	if !strings.Contains(f.Description, "current:") {
		t.Errorf("field description should carry per-layer info, got %q", f.Description)
	}
	if f.Validate == nil {
		t.Fatal("field must carry an inline validator")
	}
	if err := f.Validate("{a: b}"); err == nil {
		t.Error("validator should reject a map value")
	}
	if err := f.Validate("plain"); err != nil {
		t.Errorf("validator should accept a plain scalar, got %v", err)
	}
}

// TestBareVars_InteractiveRunsBrowser asserts that a bare `dwe vars` on a real
// terminal dispatches to the TUI browser (not the list).
func TestBareVars_InteractiveRunsBrowser(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	origInteractive := isInteractive
	origBrowser := runBrowser
	t.Cleanup(func() {
		isInteractive = origInteractive
		runBrowser = origBrowser
	})

	isInteractive = func(io.Reader) bool { return true }
	called := 0
	runBrowser = func(title string, items []cmdbrowser.Item, opts cmdbrowser.Options) (cmdbrowser.Result, error) {
		called++
		if opts.Mode != cmdbrowser.ModeEdit {
			t.Errorf("browser opts.Mode=%v, want ModeEdit", opts.Mode)
		}
		if len(items) != 3 {
			t.Errorf("browser items=%d, want 3", len(items))
		}
		// Cancel immediately so the edit loop exits without opening the form.
		return cmdbrowser.Result{}, widgets.ErrCancelled
	}

	out, _, err := runVarsCmd(t, flags)
	if err != nil {
		t.Fatalf("bare vars (interactive): %v", err)
	}
	if called != 1 {
		t.Errorf("runBrowser called %d times, want 1", called)
	}
	if out != "" {
		t.Errorf("cancelled browser should print nothing, got %q", out)
	}
}

// TestVarsBrowser_ClosesAfterEdit asserts the browser closes (does not reopen)
// after a committed edit, and that the edit was written. A committed edit shows
// the `✓ set …` confirmation as the final output.
func TestVarsBrowser_ClosesAfterEdit(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	origInteractive := isInteractive
	origBrowser := runBrowser
	origAsk := runAsk
	origIsInteractiveFn := widgets.IsInteractiveFn
	t.Cleanup(func() {
		isInteractive = origInteractive
		runBrowser = origBrowser
		runAsk = origAsk
		widgets.IsInteractiveFn = origIsInteractiveFn
	})

	isInteractive = func(io.Reader) bool { return true }
	widgets.IsInteractiveFn = func(io.Reader) bool { return true } // set form's own probe

	browserCalls := 0
	runBrowser = func(_ string, items []cmdbrowser.Item, _ cmdbrowser.Options) (cmdbrowser.Result, error) {
		browserCalls++
		for i, it := range items {
			if it.ID == "db.host" { // stripped display ID
				return cmdbrowser.Result{Idx: i, Action: cmdbrowser.ActionEdit}, nil
			}
		}
		t.Fatal("db.host item not found")
		return cmdbrowser.Result{}, nil
	}
	runAsk = func(_ context.Context, _ string, _ []ask.Field, _ ask.RunOptions) (ask.Result, error) {
		return ask.NewResultForTest(map[string]any{"value": "edited"}), nil
	}

	if _, _, err := runVarsCmd(t, flags); err != nil {
		t.Fatalf("bare vars edit: %v", err)
	}
	if browserCalls != 1 {
		t.Errorf("browser must close after a committed edit: opened %d times, want 1", browserCalls)
	}
	if got, _ := reloadVar(t, cfgPath, "vars.db.host"); got != "edited" {
		t.Errorf("edit not written through the browser: got %v", got)
	}
}

// TestVarsBrowser_ReopensAfterAbortedEdit asserts that aborting the edit form
// (widgets.ErrCancelled, committed=false) reopens the browser rather than closing it.
func TestVarsBrowser_ReopensAfterAbortedEdit(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	origInteractive := isInteractive
	origBrowser := runBrowser
	origAsk := runAsk
	origIsInteractiveFn := widgets.IsInteractiveFn
	t.Cleanup(func() {
		isInteractive = origInteractive
		runBrowser = origBrowser
		runAsk = origAsk
		widgets.IsInteractiveFn = origIsInteractiveFn
	})

	isInteractive = func(io.Reader) bool { return true }
	widgets.IsInteractiveFn = func(io.Reader) bool { return true }

	browserCalls := 0
	runBrowser = func(_ string, items []cmdbrowser.Item, _ cmdbrowser.Options) (cmdbrowser.Result, error) {
		browserCalls++
		// First open → select a var (the edit will abort); second open → quit.
		if browserCalls == 1 {
			for i, it := range items {
				if it.ID == "db.host" {
					return cmdbrowser.Result{Idx: i, Action: cmdbrowser.ActionEdit}, nil
				}
			}
		}
		return cmdbrowser.Result{}, widgets.ErrCancelled
	}
	runAsk = func(_ context.Context, _ string, _ []ask.Field, _ ask.RunOptions) (ask.Result, error) {
		return ask.Result{}, widgets.ErrCancelled // abort the form → no write
	}

	if _, _, err := runVarsCmd(t, flags); err != nil {
		t.Fatalf("bare vars aborted edit: %v", err)
	}
	if browserCalls != 2 {
		t.Errorf("aborted edit should reopen the browser: opened %d times, want 2", browserCalls)
	}
}

// TestBareVars_NonInteractiveFallsBackToList asserts that without a TTY the
// bare command lists leaves instead of opening the browser.
func TestBareVars_NonInteractiveFallsBackToList(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	origInteractive := isInteractive
	origBrowser := runBrowser
	t.Cleanup(func() {
		isInteractive = origInteractive
		runBrowser = origBrowser
	})

	isInteractive = func(io.Reader) bool { return false }
	runBrowser = func(string, []cmdbrowser.Item, cmdbrowser.Options) (cmdbrowser.Result, error) {
		t.Fatal("non-interactive bare vars must not open the browser")
		return cmdbrowser.Result{}, nil
	}

	out, _, err := runVarsCmd(t, flags)
	if err != nil {
		t.Fatalf("bare vars (non-interactive): %v", err)
	}
	// The list table contains the leaf paths (vars. prefix stripped) and the
	// override value.
	for _, want := range []string{"app.name", "db.host", "override-host"} {
		if !strings.Contains(out, want) {
			t.Errorf("list fallback output missing %q\ngot:\n%s", want, out)
		}
	}
}

// TestBareVars_NamespaceArgListsEvenInteractive asserts a namespace arg forces
// the list path even on a TTY (the browser takes no namespace filter).
func TestBareVars_NamespaceArgListsEvenInteractive(t *testing.T) {
	cfgPath, root := writeVarsFixture(t)
	flags := &cmdctx.RootFlags{ConfigPath: cfgPath, Root: root}

	origInteractive := isInteractive
	origBrowser := runBrowser
	t.Cleanup(func() {
		isInteractive = origInteractive
		runBrowser = origBrowser
	})

	isInteractive = func(io.Reader) bool { return true }
	runBrowser = func(string, []cmdbrowser.Item, cmdbrowser.Options) (cmdbrowser.Result, error) {
		t.Fatal("a namespace arg must list, not open the browser")
		return cmdbrowser.Result{}, nil
	}

	out, _, err := runVarsCmd(t, flags, "vars.db")
	if err != nil {
		t.Fatalf("vars vars.db (interactive): %v", err)
	}
	if !strings.Contains(out, "db.host") || strings.Contains(out, "app.name") {
		t.Errorf("namespace-filtered list wrong\ngot:\n%s", out)
	}
}
