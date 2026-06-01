package linters

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"testing"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	userpkg "github.com/semsemyonoff/dwe/internal/core/project/user"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// children unwraps the single lintersGroup returned by All() into its child
// validators. The group wrapper exists for parallel execution; the child set
// is what tests inspect.
func children(vs []validate.Validator) []validate.Validator {
	if len(vs) == 0 {
		return nil
	}
	if g, ok := vs[0].(*lintersGroup); ok {
		return g.Children()
	}
	return vs
}

func ids(vs []validate.Validator) []string {
	cs := children(vs)
	out := make([]string, 0, len(cs))
	for _, v := range cs {
		out = append(out, v.ID())
	}
	sort.Strings(out)
	return out
}

func TestAll_NilCfgNilErr_SynthesizesAllBuiltins(t *testing.T) {
	got := All(nil, nil, t.TempDir(), nil)
	want := []string{HadolintID, ShellcheckID}
	if diff := fmt.Sprint(ids(got)); diff != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
}

func TestAll_NilCfgErrNotExist_SynthesizesAllBuiltins(t *testing.T) {
	got := All(nil, fs.ErrNotExist, t.TempDir(), nil)
	want := []string{HadolintID, ShellcheckID}
	if fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
}

func TestAll_CorruptConfig_ReturnsErrorValidator(t *testing.T) {
	got := All(nil, errors.New("yaml: bad token"), t.TempDir(), nil)
	cs := children(got)
	if len(cs) != 1 {
		t.Fatalf("expected one error validator on corrupt config, got %d", len(cs))
	}
	ev, ok := cs[0].(*linterErrorValidator)
	if !ok {
		t.Fatalf("expected *linterErrorValidator, got %T", cs[0])
	}
	diags := ev.Run(validate.Context{})
	if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
		t.Fatalf("expected one Error diagnostic, got %+v", diags)
	}
	if diags[0].Domain != Domain {
		t.Fatalf("expected domain %q, got %q", Domain, diags[0].Domain)
	}
}

func TestAll_CorruptConfig_TopLevelDomainLevelGlobal(t *testing.T) {
	// Regression: _config error was previously a child of lintersGroup, so
	// Registry.Run's DomainLevel/Global checks never reached it under scoped
	// queries like `dwe validate linters shellcheck`.
	got := All(nil, errors.New("yaml: bad token"), t.TempDir(), nil)
	if len(got) != 1 {
		t.Fatalf("expected 1 top-level validator, got %d", len(got))
	}
	if _, ok := got[0].(*lintersGroup); ok {
		t.Fatal("_config error must not be wrapped in lintersGroup")
	}
	dl, ok := got[0].(validate.DomainLevelValidator)
	if !ok {
		t.Fatalf("_config error must implement DomainLevelValidator, got %T", got[0])
	}
	if !dl.IsDomainLevel() {
		t.Fatal("IsDomainLevel must return true for _config error")
	}
	gv, ok := got[0].(validate.GlobalValidator)
	if !ok {
		t.Fatalf("_config error must implement GlobalValidator, got %T", got[0])
	}
	if !gv.IsGlobal() {
		t.Fatal("IsGlobal must return true for _config error")
	}
}

func TestAll_EmptyConfig_SynthesizesAllBuiltins(t *testing.T) {
	cfg := &config.ValidateConfig{}
	got := children(All(cfg, nil, t.TempDir(), nil))
	want := []string{HadolintID, ShellcheckID}
	if fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
}

func TestAll_PartialConfig_OtherBuiltinsStillAutodetected(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: ShellcheckID, Type: "builtin", Flags: []string{"--severity=warning"}},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	want := []string{HadolintID, ShellcheckID}
	if fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
}

func TestAll_ExplicitEntryTakesPrecedence(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: ShellcheckID, Type: "builtin", Paths: []string{"custom"}},
	}}
	cs := children(All(cfg, nil, t.TempDir(), nil))
	// First validator (insertion order) should be the user-configured shellcheck;
	// hadolint is auto-detected and appended after.
	if cs[0].ID() != ShellcheckID {
		t.Fatalf("expected user-configured shellcheck first, got %s", cs[0].ID())
	}
	lv, ok := cs[0].(*linterValidator)
	if !ok {
		t.Fatalf("expected *linterValidator, got %T", cs[0])
	}
	if !lv.pathsExplicit {
		t.Fatalf("expected pathsExplicit=true on user-configured entry")
	}
	if len(lv.entry.Paths) != 1 || lv.entry.Paths[0] != "custom" {
		t.Fatalf("paths = %v, want [custom]", lv.entry.Paths)
	}
}

func TestAll_EnabledFalse_StillRegisteredButRunsSilently(t *testing.T) {
	disabled := false
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: ShellcheckID, Type: "builtin", Enabled: &disabled},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	// Registered so that scoped runs like `validate linters shellcheck` still
	// match by ID; runtime gates on enabled at Run() time.
	want := []string{HadolintID, ShellcheckID}
	if fmt.Sprint(ids(got)) != fmt.Sprint(want) {
		t.Fatalf("ids = %v, want %v", ids(got), want)
	}
	// Verify the disabled shellcheck emits zero diagnostics at Run time.
	for _, v := range got {
		if v.ID() == ShellcheckID {
			if d := v.Run(validate.Context{}); len(d) != 0 {
				t.Fatalf("disabled shellcheck emitted diagnostics: %+v", d)
			}
		}
	}
}

func TestAll_UnknownBuiltin_ProducesErrorValidator(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: "nosuch", Type: "builtin", SourceLine: 42},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	var found bool
	for _, v := range got {
		if v.ID() == "nosuch" {
			ev, ok := v.(*linterErrorValidator)
			if !ok {
				t.Fatalf("expected *linterErrorValidator for unknown id, got %T", v)
			}
			diags := ev.Run(validate.Context{})
			if len(diags) != 1 || diags[0].Severity != validate.SeverityError {
				t.Fatalf("expected one error diagnostic, got %+v", diags)
			}
			if diags[0].Line != 42 {
				t.Fatalf("expected SourceLine 42 preserved, got %d", diags[0].Line)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("did not find validator for unknown id 'nosuch'")
	}
}

func TestAll_GenericWithUnknownID_UsesGenericAdapter(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: "yamllint", Type: "generic", Bin: "yamllint", Paths: []string{"."}},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	var lv *linterValidator
	for _, v := range got {
		if v.ID() == "yamllint" {
			lv, _ = v.(*linterValidator)
		}
	}
	if lv == nil {
		t.Fatalf("yamllint validator not registered")
	}
	if _, ok := lv.adapter.(*GenericAdapter); !ok {
		t.Fatalf("expected *GenericAdapter, got %T", lv.adapter)
	}
}

func TestAll_ReservedFlagShellcheck_ProducesErrorValidator(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
	}{
		{"long with =", []string{"--format=gcc"}},
		{"long with space", []string{"--format", "gcc"}},
		{"short attached", []string{"-fgcc"}},
		{"short with space", []string{"-f", "tty"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
				{ID: ShellcheckID, Type: "builtin", Flags: tc.flags, SourceLine: 7},
			}}
			got := children(All(cfg, nil, t.TempDir(), nil))
			var ev *linterErrorValidator
			for _, v := range got {
				if v.ID() == ShellcheckID {
					ev, _ = v.(*linterErrorValidator)
				}
			}
			if ev == nil {
				t.Fatalf("expected reserved-flag error validator for shellcheck, got non-error validator")
			}
		})
	}
}

func TestAll_ReservedFlagHadolint_ProducesErrorValidator(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: HadolintID, Type: "builtin", Flags: []string{"-ftty"}},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	var ev *linterErrorValidator
	for _, v := range got {
		if v.ID() == HadolintID {
			ev, _ = v.(*linterErrorValidator)
		}
	}
	if ev == nil {
		t.Fatalf("expected reserved-flag error validator for hadolint")
	}
}

func TestAll_ReservedFlagsAllowedForGeneric(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: "yamllint", Type: "generic", Bin: "yamllint", Paths: []string{"."}, Flags: []string{"--format=parsable"}},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	for _, v := range got {
		if v.ID() == "yamllint" {
			if _, isErr := v.(*linterErrorValidator); isErr {
				t.Fatalf("generic adapter should not reserve any flags")
			}
			return
		}
	}
	t.Fatalf("yamllint validator not registered")
}

func TestAll_GenericWithNoPaths_ProducesErrorValidator(t *testing.T) {
	cfg := &config.ValidateConfig{Linters: []config.LinterEntry{
		{ID: "yamllint", Type: "generic", Bin: "yamllint", SourceLine: 5},
	}}
	got := children(All(cfg, nil, t.TempDir(), nil))
	for _, v := range got {
		if v.ID() == "yamllint" {
			if _, isErr := v.(*linterErrorValidator); !isErr {
				t.Fatalf("expected error validator for generic linter with no paths, got %T", v)
			}
			return
		}
	}
	t.Fatalf("yamllint validator not present")
}

func TestAll_UserConfigBinaryOverride(t *testing.T) {
	// Test that a user-config with binary_shellcheck override is threaded through
	// to the linterValidator correctly.
	usercfg := &userpkg.Config{
		Binaries: map[string]string{
			"shellcheck": "/nonexistent/path/to/shellcheck",
		},
	}
	// When All() receives a user config, it should be passed to the validators.
	// If the override path doesn't exist, the validator will emit an error diagnostic
	// at Run time (not at assembly time).
	got := All(nil, nil, t.TempDir(), usercfg)
	if len(got) == 0 {
		t.Fatalf("expected validators (at least shellcheck), got none")
	}
	// The validators are assembled successfully even with a broken override path.
	// The actual error will surface at Run time.
	v := got[0]
	if g, ok := v.(*lintersGroup); ok {
		if len(g.Children()) == 0 {
			t.Fatalf("expected children in group, got none")
		}
	}
}
