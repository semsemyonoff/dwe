package builtin

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/containers"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/env"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/fs"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/interaction"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/services"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/source"
	"github.com/semsemyonoff/dwe/internal/core/execution/builtin/spec"
	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/shared/render"
)

func TestIsInteractive(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"confirm", true},
		{"docker_daemon_logs", true},
		{"message", false},
		{"docker_daemon_start", false},
		{"docker_daemon_stop", false},
		{"service_dirs_ensure", false},
		{"unknown_builtin_xyz", false},
		{"", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsInteractive(tc.name); got != tc.want {
				t.Errorf("IsInteractive(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestKnownNames_AllRegistered(t *testing.T) {
	names := knownNames()
	if len(names) == 0 {
		t.Fatal("expected at least one registered builtin")
	}
	// names must be sorted
	for i := 1; i < len(names); i++ {
		if names[i] < names[i-1] {
			t.Errorf("knownNames not sorted: %v", names)
			break
		}
	}
	for _, expected := range []string{"confirm", "message", "service_dirs_ensure"} {
		if !slices.Contains(names, expected) {
			t.Errorf("expected %q in knownNames, got: %v", expected, names)
		}
	}
}

func TestInventory_EveryEntryHasSummary(t *testing.T) {
	inv := Inventory()
	if len(inv) != len(registry) {
		t.Fatalf("Inventory returned %d entries, registry has %d", len(inv), len(registry))
	}
	for _, e := range inv {
		if strings.TrimSpace(e.Summary) == "" {
			t.Errorf("builtin %q has no Summary; add one to its Builtins() map entry", e.Name)
		}
	}
}

func TestInventory_SortedAndMatchesRegistry(t *testing.T) {
	inv := Inventory()
	for i := 1; i < len(inv); i++ {
		if inv[i].Name < inv[i-1].Name {
			t.Fatalf("Inventory not sorted at %d: %q after %q", i, inv[i].Name, inv[i-1].Name)
		}
	}
	for _, e := range inv {
		entry, ok := registry[e.Name]
		if !ok {
			t.Errorf("Inventory reports unregistered builtin %q", e.Name)
			continue
		}
		if entry.Kind != e.Kind {
			t.Errorf("%s: Inventory kind %v, registry kind %v", e.Name, e.Kind, entry.Kind)
		}
	}
}

func TestInventory_CoversEveryKind(t *testing.T) {
	// The internal kinds must be enumerated too: a reader needs to know why
	// docker_daemon_start is rejected from user-authored YAML.
	want := map[string]Kind{
		"message":             KindAction,
		"shell":               KindPredicate,
		"docker_daemon_start": KindInternal,
	}
	got := make(map[string]Kind, len(want))
	for _, e := range Inventory() {
		if _, ok := want[e.Name]; ok {
			got[e.Name] = e.Kind
		}
	}
	for name, kind := range want {
		if got[name] != kind {
			t.Errorf("Inventory[%q].Kind = %v, want %v", name, got[name], kind)
		}
	}
}

func TestKindString(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindAction, "action"},
		{KindPredicate, "predicate"},
		{KindInternal, "internal"},
		{spec.Kind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.kind.String(); got != tc.want {
			t.Errorf("Kind(%d).String() = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestValidate_UnknownBuiltin(t *testing.T) {
	err := Validate("nonexistent_builtin", nil, CtxUserYAML)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

func TestValidate_KnownBuiltin_NoParams(t *testing.T) {
	// confirm builtin has no required params.
	err := Validate("confirm", nil, CtxUserYAML)
	if err != nil {
		t.Errorf("unexpected error for confirm with nil params: %v", err)
	}
}

func TestDescribe_UnknownBuiltin(t *testing.T) {
	desc := Describe("nonexistent_builtin", nil)
	if !strings.Contains(desc, "nonexistent_builtin") {
		t.Errorf("Describe should mention unknown name, got: %q", desc)
	}
}

func TestDescribe_KnownBuiltin(t *testing.T) {
	desc := Describe("confirm", nil)
	if desc == "" {
		t.Error("expected non-empty describe for confirm builtin")
	}
}

func TestRun_UnknownBuiltin(t *testing.T) {
	ctx := ExecContext{
		Config:      &config.DweConfig{},
		ProjectRoot: t.TempDir(),
		Output:      render.NewWriter(&bytes.Buffer{}),
	}
	err := Run(context.Background(), "nonexistent_builtin", nil, ctx, CtxUserYAML)
	if err == nil {
		t.Fatal("expected error for unknown builtin")
	}
	if !strings.Contains(err.Error(), "nonexistent_builtin") {
		t.Errorf("error should mention the unknown name, got: %v", err)
	}
}

// TestKindCategorization verifies that every registered builtin
// has the expected kind, and that kind/context gating works as intended.
// This is the single source of truth for the 20-entry registry categorization.
func TestKindCategorization(t *testing.T) {
	type kindCase struct {
		name        string
		kind        Kind
		userYAMLOK  bool // CtxUserYAML allowed?
		predicateOK bool // CtxPredicate allowed?
		internalOK  bool // CtxInternal allowed?
	}
	cases := []kindCase{
		// KindAction: allowed in body (CtxUserYAML) and check: (CtxPredicate)
		{"confirm", KindAction, true, true, false},
		{"message", KindAction, true, true, false},
		{"service_configs_copy", KindAction, true, true, false},
		{"service_configs_check", KindAction, true, true, false},
		{"service_dirs_ensure", KindAction, true, true, false},
		{"docker_remove_project_volumes", KindAction, true, true, false},
		{"docker_wait_healthy", KindAction, true, true, false},
		{"remove_paths", KindAction, true, true, false},
		// source_clone mutates, but kindAllowed deliberately permits actions in
		// CtxPredicate — so it IS reachable from check:. The boundary that holds
		// is validate.yml's own allowlist (see validate/checks).
		{"source_clone", KindAction, true, true, false},
		// KindPredicate: check: (CtxPredicate) AND step body (CtxUserYAML) —
		// a predicate body is an assertion (false fails the step)
		{"containers_running", KindPredicate, true, true, false},
		{"shell", KindPredicate, true, true, false},
		{"file_exists", KindPredicate, true, true, false},
		{"executable_in_path", KindPredicate, true, true, false},
		{"env_keys_present", KindPredicate, true, true, false},
		{"tcp_reachable", KindPredicate, true, true, false},
		{"config_keys_present", KindPredicate, true, true, false},
		// KindInternal: only engine-synthetic contexts (CtxInternal)
		{"docker_daemon_start", KindInternal, false, false, true},
		{"docker_daemon_logs", KindInternal, false, false, true},
		{"docker_daemon_stop", KindInternal, false, false, true},
		{"docker_stop_remove_container", KindInternal, false, false, true},
		{"daemons_reap", KindInternal, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry, ok := registry[tc.name]
			if !ok {
				t.Fatalf("builtin %q not in registry", tc.name)
			}
			if entry.Kind != tc.kind {
				t.Errorf("kind = %v, want %v", entry.Kind, tc.kind)
			}
			// Verify each context combination
			_, gotUserYAML := Get(tc.name, CtxUserYAML)
			if gotUserYAML != tc.userYAMLOK {
				t.Errorf("Get(%q, CtxUserYAML) = %v, want %v", tc.name, gotUserYAML, tc.userYAMLOK)
			}
			_, gotPredicate := Get(tc.name, CtxPredicate)
			if gotPredicate != tc.predicateOK {
				t.Errorf("Get(%q, CtxPredicate) = %v, want %v", tc.name, gotPredicate, tc.predicateOK)
			}
			_, gotInternal := Get(tc.name, CtxInternal)
			if gotInternal != tc.internalOK {
				t.Errorf("Get(%q, CtxInternal) = %v, want %v", tc.name, gotInternal, tc.internalOK)
			}
		})
	}
}

// TestGetKindMismatch verifies that kind-incompatible Get calls return (nil, false)
// and that Validate returns descriptive errors for context mismatches.
func TestGetKindMismatch(t *testing.T) {
	t.Run("internal builtin rejected from user YAML", func(t *testing.T) {
		b, ok := Get("docker_daemon_start", CtxUserYAML)
		if ok || b != nil {
			t.Error("docker_daemon_start must not be callable from CtxUserYAML")
		}
		err := Validate("docker_daemon_start", nil, CtxUserYAML)
		if err == nil {
			t.Fatal("expected error for internal builtin in user YAML context")
		}
		if !strings.Contains(err.Error(), "engine-internal") {
			t.Errorf("error should mention engine-internal, got: %v", err)
		}
	})

	t.Run("predicate builtin allowed as step body (assertion)", func(t *testing.T) {
		b, ok := Get("containers_running", CtxUserYAML)
		if !ok || b == nil {
			t.Error("containers_running must be callable from CtxUserYAML (predicate body = assertion)")
		}
		with := map[string]any{"services": []any{"app"}}
		if err := Validate("containers_running", with, CtxUserYAML); err != nil {
			t.Errorf("unexpected error for predicate builtin in body context: %v", err)
		}
	})

	t.Run("predicate builtin rejected from internal context", func(t *testing.T) {
		b, ok := Get("containers_running", CtxInternal)
		if ok || b != nil {
			t.Error("containers_running must not be callable from CtxInternal")
		}
		err := Validate("containers_running", nil, CtxInternal)
		if err == nil {
			t.Fatal("expected error for predicate builtin in internal context")
		}
		if !strings.Contains(err.Error(), "predicate") {
			t.Errorf("error should mention predicate, got: %v", err)
		}
	})

	t.Run("predicate builtin allowed in check position", func(t *testing.T) {
		b, ok := Get("containers_running", CtxPredicate)
		if !ok || b == nil {
			t.Error("containers_running must be callable from CtxPredicate (check: position)")
		}
	})

	t.Run("internal builtin rejected from check position", func(t *testing.T) {
		b, ok := Get("daemons_reap", CtxPredicate)
		if ok || b != nil {
			t.Error("daemons_reap must not be callable from CtxPredicate")
		}
	})

	t.Run("internal builtin allowed in internal context", func(t *testing.T) {
		b, ok := Get("daemons_reap", CtxInternal)
		if !ok || b == nil {
			t.Error("daemons_reap must be callable from CtxInternal")
		}
	})
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		name   string
		want   Kind
		wantOK bool
	}{
		{"file_exists", KindPredicate, true},
		{"tcp_reachable", KindPredicate, true},
		{"shell", KindPredicate, true},
		{"message", KindAction, true},
		{"service_dirs_ensure", KindAction, true},
		{"daemons_reap", KindInternal, true},
		{"unknown_builtin_xyz", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := KindOf(tc.name)
			if ok != tc.wantOK {
				t.Fatalf("KindOf(%q) ok = %v, want %v", tc.name, ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("KindOf(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

// TestPredicateAsUserCommand_Intentional pins the shared-context side effect of
// the predicate-as-body relaxation: CtxUserYAML also validates user-command
// type: builtin definitions (usercommands runtime), so predicate builtins are
// legal as user commands. This is intentional — commands and pipelines share
// the builtin registry; do NOT split the context to undo it.
func TestPredicateAsUserCommand_Intentional(t *testing.T) {
	for _, name := range []string{"file_exists", "tcp_reachable", "env_keys_present"} {
		if _, ok := Get(name, CtxUserYAML); !ok {
			t.Errorf("predicate %q must be reachable through CtxUserYAML (user-command type: builtin)", name)
		}
	}
	// KindInternal stays engine-only through the same context.
	if _, ok := Get("daemons_reap", CtxUserYAML); ok {
		t.Error("daemons_reap must remain unreachable from CtxUserYAML")
	}
}

// allBuiltinNames enumerates every builtin name expected in the registry after
// the subpackage refactor. Adding or removing a name requires updating this list.
var allBuiltinNames = []string{
	// root (cross-cutting predicates)
	"shell",
	"tcp_reachable",
	"http_check",
	"config_keys_present",
	// containers/
	"docker_daemon_start",
	"docker_daemon_logs",
	"docker_daemon_stop",
	"docker_stop_remove_container",
	"daemons_reap",
	"containers_running",
	"docker_wait_healthy",
	"docker_remove_project_volumes",
	// services/
	"service_configs_copy",
	"service_configs_check",
	"service_configs_render",
	"service_configs_render_check",
	"service_generated_harvest",
	"service_dirs_ensure",
	// fs/
	"file_exists",
	"remove_paths",
	// env/
	"env_keys_present",
	"executable_in_path",
	// interaction/
	"confirm",
	"message",
	// source/
	"source_clone",
}

// TestRegistryHasAllNames asserts the registry contains exactly the expected
// builtin names — guards against accidental drops or duplications when entries
// move between subpackages.
func TestRegistryHasAllNames(t *testing.T) {
	if got, want := len(registry), len(allBuiltinNames); got != want {
		t.Errorf("registry size = %d, want %d", got, want)
	}
	for _, name := range allBuiltinNames {
		if _, ok := registry[name]; !ok {
			t.Errorf("registry missing builtin %q", name)
		}
	}
}

// TestNoDuplicateRegistryNames asserts each subpackage's Builtins() map has
// keys disjoint from every other subpackage and from the root entries. Pairs
// with the panic guard in buildRegistry() to catch collisions at test time
// rather than at first program start.
func TestNoDuplicateRegistryNames(t *testing.T) {
	sources := []struct {
		name    string
		entries map[string]spec.Entry
	}{
		{"root", map[string]spec.Entry{
			"shell":               {Impl: Shell{}, Kind: spec.KindPredicate},
			"tcp_reachable":       {Impl: TCPReachable{}, Kind: spec.KindPredicate},
			"http_check":          {Impl: HTTPCheck{}, Kind: spec.KindPredicate},
			"config_keys_present": {Impl: ConfigKeysPresent{}, Kind: spec.KindPredicate},
		}},
		{"containers", containers.Builtins()},
		{"services", services.Builtins()},
		{"fs", fs.Builtins()},
		{"env", env.Builtins()},
		{"interaction", interaction.Builtins()},
		{"source", source.Builtins()},
	}
	owner := map[string]string{}
	for _, src := range sources {
		for name := range src.entries {
			if prev, dup := owner[name]; dup {
				t.Errorf("builtin %q registered by both %q and %q", name, prev, src.name)
				continue
			}
			owner[name] = src.name
		}
	}
}
