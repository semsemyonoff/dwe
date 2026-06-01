package registry

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/tpl"
)

// stubEval replaces evalHideFn with a deterministic stub for tests.
// Returns ok==true (hidden) when expr is in truthy; returns ok==false
// (visible) when in falsy; returns err for entries in errs.
func stubEval(t *testing.T, truthy map[string]bool, errs map[string]error) {
	t.Helper()
	orig := evalHideFn
	evalHideFn = func(expr string, _ *tpl.RenderContext, _ string) (bool, error) {
		if err, ok := errs[expr]; ok {
			return false, err
		}
		return truthy[expr], nil
	}
	t.Cleanup(func() { evalHideFn = orig })
}

func TestApplyVisibility_CommandLevel(t *testing.T) {
	stubEval(t, map[string]bool{
		"hide-me": true,
		"keep-me": false,
	}, nil)

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  hidden_cmd:
    type: shell
    cmd: echo hidden
    hide: hide-me
  visible_cmd:
    type: shell
    cmd: echo visible
    hide: keep-me
  no_hide_cmd:
    type: shell
    cmd: echo plain
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	cases := map[string]bool{
		"a.hidden_cmd":  true,
		"a.visible_cmd": false,
		"a.no_hide_cmd": false,
	}
	for id, want := range cases {
		def, err := reg.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if def.Hidden != want {
			t.Errorf("Get(%q).Hidden = %v, want %v", id, def.Hidden, want)
		}
	}
}

func TestApplyVisibility_GroupCascade(t *testing.T) {
	stubEval(t, map[string]bool{
		"hide-group": true,
	}, nil)

	reg := mustRegistry(t, map[string]string{
		"db.yml": `
group:
  title: Database
  hide: hide-group
commands:
  migrate:
    type: shell
    cmd: echo migrate
  seed:
    type: shell
    cmd: echo seed
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	for _, id := range []string{"db.migrate", "db.seed"} {
		def, err := reg.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if !def.Hidden {
			t.Errorf("Get(%q).Hidden = false, want true (cascaded from group)", id)
		}
	}

	// Group node itself is Hidden.
	gn, ok := reg.groups["db"]
	if !ok {
		t.Fatal("expected group db to exist")
	}
	if !gn.Hidden {
		t.Error("group db: Hidden = false, want true")
	}
}

func TestApplyVisibility_CascadeWinsOverChildOverride(t *testing.T) {
	// Group is hidden; child explicitly says hide: "" or even hide: visible-expr.
	// Cascade must win (per design).
	stubEval(t, map[string]bool{
		"hide-group":   true,
		"visible-expr": false,
	}, nil)

	reg := mustRegistry(t, map[string]string{
		"svc.yml": `
group:
  hide: hide-group
commands:
  always_visible:
    type: shell
    cmd: echo no
    hide: visible-expr
  no_hide:
    type: shell
    cmd: echo no
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	for _, id := range []string{"svc.always_visible", "svc.no_hide"} {
		def, err := reg.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if !def.Hidden {
			t.Errorf("Get(%q).Hidden = false, want true (cascade wins over child)", id)
		}
	}
}

func TestApplyVisibility_Idempotent(t *testing.T) {
	stubEval(t, map[string]bool{
		"hide-me": true,
	}, nil)

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  c1:
    type: shell
    cmd: echo
    hide: hide-me
  c2:
    type: shell
    cmd: echo
`,
	})

	for i := range 3 {
		if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
			t.Fatalf("ApplyVisibility iter %d: %v", i, err)
		}
		c1, err := reg.Get("a.c1")
		if err != nil {
			t.Fatalf("Get(a.c1): %v", err)
		}
		c2, err := reg.Get("a.c2")
		if err != nil {
			t.Fatalf("Get(a.c2): %v", err)
		}
		if !c1.Hidden || c2.Hidden {
			t.Fatalf("iter %d: unstable Hidden flags: c1=%v c2=%v", i, c1.Hidden, c2.Hidden)
		}
	}
}

func TestApplyVisibility_EvalErrorsFailOpen(t *testing.T) {
	// Fail-open contract: per-expression evaluation failures are logged
	// (via slog.Warn) and the failing entry's Hidden is left UNCHANGED.
	// ApplyVisibility must NOT abort the pass — otherwise one bad hide
	// expression would brick `dwe commands` for unrelated commands.
	boom := errors.New("boom")
	stubEval(t, map[string]bool{"good": true}, map[string]error{"broken": boom})

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  broken_cmd:
    type: shell
    cmd: echo
    hide: broken
  good_cmd:
    type: shell
    cmd: echo
    hide: good
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility must not return per-expression eval errors, got: %v", err)
	}

	broken, err := reg.Get("a.broken_cmd")
	if err != nil {
		t.Fatalf("Get(a.broken_cmd): %v", err)
	}
	// Hidden was zero-valued before ApplyVisibility; failure leaves it untouched.
	if broken.Hidden {
		t.Error("broken_cmd: Hidden should stay at its prior value (false) on eval error")
	}

	good, err := reg.Get("a.good_cmd")
	if err != nil {
		t.Fatalf("Get(a.good_cmd): %v", err)
	}
	if !good.Hidden {
		t.Error("good_cmd should still be Hidden — unrelated bad expression must not poison the pass")
	}
}

func TestApplyVisibility_EvalErrorLogsViaSlogWarn(t *testing.T) {
	// Locks down the diagnostic contract: ApplyVisibility emits a slog.Warn
	// record when an individual hide expression fails to evaluate. Without
	// this assertion, a future refactor could silently drop the warning and
	// leave operators with broken hide: expressions blind to the failure.
	boom := errors.New("boom")
	stubEval(t, nil, map[string]error{"broken": boom})

	// Capture slog output by swapping the default logger to a buffer-backed
	// text handler for the duration of the test.
	var buf bytes.Buffer
	captured := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	prev := slog.Default()
	slog.SetDefault(captured)
	t.Cleanup(func() { slog.SetDefault(prev) })

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  cmd:
    type: shell
    cmd: echo
    hide: broken
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected a WARN-level slog record; got %q", out)
	}
	if !strings.Contains(out, "id=a.cmd") {
		t.Errorf("expected slog record to include id=a.cmd; got %q", out)
	}
	if !strings.Contains(out, "expr=broken") {
		t.Errorf("expected slog record to include expr=broken; got %q", out)
	}
	if !strings.Contains(out, "err=boom") {
		t.Errorf("expected slog record to include err=boom; got %q", out)
	}

	// Sanity: handler was actually wired (context is a no-op consumer here).
	_ = context.Background()
}

func TestApplyVisibility_EvalError_PreservesPriorHidden(t *testing.T) {
	// Defensive: when ApplyVisibility is re-applied on a registry whose
	// previous pass left Hidden=true, a transient eval failure on the
	// second pass MUST leave Hidden=true (not flip to false). Today
	// only fresh registries are passed in production; this guards the
	// behaviour for any future code path that re-applies in place.
	boom := errors.New("boom")
	stubEval(t, nil, map[string]error{"flaky": boom})

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  flaky_cmd:
    type: shell
    cmd: echo
    hide: flaky
`,
	})

	// Simulate a prior successful pass that produced Hidden=true.
	def, _ := reg.Get("a.flaky_cmd")
	def.Hidden = true

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	if !def.Hidden {
		t.Error("eval error must NOT flip a previously-set Hidden=true to false")
	}
}

func TestApplyVisibility_EmptyHideIsVisible(t *testing.T) {
	// Critical contract: empty hide must NOT call evalHideFn (it would return
	// hidden=true by EvalCommandCondition's empty-string semantics).
	stubEval(t, map[string]bool{
		"": true, // would be wrong if evalHideFn ever gets called with ""
	}, nil)

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  c:
    type: shell
    cmd: echo
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}
	def, err := reg.Get("a.c")
	if err != nil {
		t.Fatalf("Get(a.c): %v", err)
	}
	if def.Hidden {
		t.Error("command without hide: was marked Hidden — evalHideFn was called for empty expr")
	}
}

func TestApplyVisibility_DaemonGroupCascadesHide(t *testing.T) {
	// A daemon's source `hide:` must propagate onto the synthetic group's
	// Meta.Hide so ApplyVisibility cascades to all 4 synthetics from one
	// evaluation, and the synthetic group node itself becomes Hidden
	// (preventing phantom group headers under `dwe commands list --all`).
	stubEval(t, map[string]bool{"hide-daemon": true}, nil)

	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  worker:
    type: daemon
    service: app
    daemon:
      container_template: dwe-${project.name}-worker
    hide: hide-daemon
`,
	})

	if err := reg.ApplyVisibility(&config.DweConfig{Raw: map[string]any{}}, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility: %v", err)
	}

	gn, ok := reg.groups["a.worker"]
	if !ok {
		t.Fatal("expected synthetic group a.worker to exist")
	}
	if !gn.Hidden {
		t.Error("synthetic daemon group must be Hidden via Meta.Hide cascade")
	}

	for _, suffix := range []string{"start", "stop", "logs", "restart"} {
		id := "a.worker." + suffix
		def, err := reg.Get(id)
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if !def.Hidden {
			t.Errorf("synthetic %q must be Hidden via group cascade", id)
		}
	}
}

func TestApplyVisibility_NilCfgTolerated(t *testing.T) {
	// nil cfg should still work; expressions referencing config will likely
	// return falsy. We only assert it doesn't panic and returns nil error
	// for empty/falsy expressions.
	stubEval(t, nil, nil)
	reg := mustRegistry(t, map[string]string{
		"a.yml": `
commands:
  c:
    type: shell
    cmd: echo
    hide: nope
`,
	})

	if err := reg.ApplyVisibility(nil, "/proj"); err != nil {
		t.Fatalf("ApplyVisibility(nil): %v", err)
	}
}
