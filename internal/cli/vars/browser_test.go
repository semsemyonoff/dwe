package vars

import (
	"io"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/cli/cmdctx"
	"github.com/semsemyonoff/dwe/internal/core/ui/cmdbrowser"
	"github.com/semsemyonoff/dwe/internal/core/ui/widgets"
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
	items, leaves := buildVarsBrowserItems(cfg, flags)

	if len(items) != len(leaves) {
		t.Fatalf("items (%d) and leaves (%d) must be parallel", len(items), len(leaves))
	}
	if len(items) != 3 {
		t.Fatalf("want 3 leaves, got %d", len(items))
	}

	byID := map[string]cmdbrowser.Item{}
	for i, it := range items {
		if it.ID != leaves[i] {
			t.Errorf("item[%d].ID=%q, leaf=%q (must match)", i, it.ID, leaves[i])
		}
		byID[it.ID] = it
	}

	host, ok := byID["vars.db.host"]
	if !ok {
		t.Fatal("vars.db.host missing from items")
	}
	// vars.db.host is overridden in local.yml → "local" badge, value override-host.
	if host.Type != "local" {
		t.Errorf("vars.db.host badge: want local, got %q", host.Type)
	}
	if host.Description != "override-host" {
		t.Errorf("vars.db.host description: want override-host, got %q", host.Description)
	}
	if host.Inspect == nil {
		t.Fatal("Inspect closure must be set")
	}
	if got := host.Inspect(80); got == "" {
		t.Error("Inspect(80) returned empty for a resolvable var")
	}

	// vars.app.name comes only from workspace.yml → "default" badge.
	if name := byID["vars.app.name"]; name.Type != "default" {
		t.Errorf("vars.app.name badge: want default, got %q", name.Type)
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
	// The list table contains the leaf paths and the override value.
	for _, want := range []string{"vars.app.name", "vars.db.host", "override-host"} {
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
	if !strings.Contains(out, "vars.db.host") || strings.Contains(out, "vars.app.name") {
		t.Errorf("namespace-filtered list wrong\ngot:\n%s", out)
	}
}
