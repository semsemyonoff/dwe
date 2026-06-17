package config

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func runFormalBlocksValidator(t *testing.T, root string) []validate.Diagnostic {
	t.Helper()
	return (&formalBlocksValidator{}).Run(validate.Context{ProjectRoot: root})
}

func TestFormalBlocks_TypoUnderStop_Warns(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", `project:
  name: demo
stop:
  port_release_timeot: 0
`)
	diags := runFormalBlocksValidator(t, root)

	d := findDiag(diags, `unknown field "port_release_timeot" under "stop"`)
	require.NotNil(t, d, "expected a warning for the typo'd stop field")
	require.Equal(t, validate.SeverityWarning, d.Severity)
	require.Equal(t, "config", d.Domain)
	require.Equal(t, "workspace.yml", d.File)
	require.Equal(t, 4, d.Line, "port_release_timeot is on line 4")
	require.Contains(t, d.Hint, "port_release_timeout")
}

func TestFormalBlocks_CorrectFields_NoWarning(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", `project:
  name: demo
  prefix: dwe
stop:
  port_release_timeout: 2m
update:
  mode: on
`)
	diags := runFormalBlocksValidator(t, root)
	require.Empty(t, diags, "well-formed formal blocks must produce no warnings, got %+v", diags)
}

func TestFormalBlocks_ComposeExtraInLocal_NotFalsePositive(t *testing.T) {
	// compose.extra is yaml:"-" (injected from local.yml), so it must NOT be
	// flagged as unknown.
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", "project:\n  name: demo\n")
	writeProjectFile(t, root, "workspace/local.yml", `compose:
  extra:
    - compose/dev.local.yml
`)
	diags := runFormalBlocksValidator(t, root)
	require.Empty(t, diags, "compose.extra in local.yml must not warn, got %+v", diags)
}

func TestFormalBlocks_TypoAcrossLayers(t *testing.T) {
	// A typo in defaults.yml (under bridge) and one in local.yml (under update)
	// are both surfaced with the correct file attribution.
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", "project:\n  name: demo\n")
	writeProjectFile(t, root, "workspace/defaults.yml", `bridge:
  vars_writeable:
    - vars.db.host
`)
	writeProjectFile(t, root, "workspace/local.yml", `update:
  moed: on
`)
	diags := runFormalBlocksValidator(t, root)

	bd := findDiag(diags, `unknown field "vars_writeable" under "bridge"`)
	require.NotNil(t, bd)
	require.Equal(t, "workspace/defaults.yml", bd.File)

	ud := findDiag(diags, `unknown field "moed" under "update"`)
	require.NotNil(t, ud)
	require.Equal(t, "workspace/local.yml", ud.File)
}

func TestFormalBlocks_MergeKeyNotFlagged(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", `_anchor: &base
  use_https: true
project:
  name: demo
runtime:
  <<: *base
`)
	diags := runFormalBlocksValidator(t, root)
	// The merge key "<<" must not be reported, and use_https (merged in) is known.
	require.Empty(t, findDiag(diags, `"<<"`), "merge key must never be flagged")
}

func TestFormalBlocks_TypoInsideMergeKey(t *testing.T) {
	// A typo hidden inside a `<<` merge mapping must still be flagged — the
	// loader merges the keys in and silently drops the typo.
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", `project:
  name: demo
runtime:
  <<:
    use_htps: true
`)
	diags := runFormalBlocksValidator(t, root)
	d := findDiag(diags, `unknown field "use_htps" under "runtime"`)
	require.NotNil(t, d, "typo inside a << merge mapping must be flagged")
	require.Equal(t, validate.SeverityWarning, d.Severity)
	// The merge value's keys are valid (use_https) → no false positive there.
	require.Nil(t, findDiag(diags, `"<<"`), "the merge key itself must never be flagged")
}

func TestFormalBlocks_TypoViaMergeAlias(t *testing.T) {
	// `<<: *anchor` where the anchor mapping carries a typo.
	root := t.TempDir()
	writeProjectFile(t, root, "workspace.yml", `defaults: &d
  port_release_timeot: 0
project:
  name: demo
stop:
  <<: *d
`)
	diags := runFormalBlocksValidator(t, root)
	require.NotNil(t, findDiag(diags, `unknown field "port_release_timeot" under "stop"`),
		"typo merged in via *alias must be flagged")
}

func TestFormalBlocks_AbsentFilesNoPanic(t *testing.T) {
	root := t.TempDir() // no workspace.yml at all
	require.NotPanics(t, func() { runFormalBlocksValidator(t, root) })
}

func TestFormalBlockFields_DerivedFromStructs(t *testing.T) {
	// The field sets are reflected off the backing structs, so a struct field
	// addition flows through automatically. Spot-check the derivation and the two
	// deliberate exceptions (compose.extra is yaml:"-" but allowed; ui is owned by
	// uiValidator so it is NOT in the table).
	require.True(t, formalBlockFields["stop"]["port_release_timeout"])
	require.True(t, formalBlockFields["update"]["mode"])
	require.True(t, formalBlockFields["bridge"]["vars_writable"])
	require.True(t, formalBlockFields["docs"]["mermaid"])
	require.True(t, formalBlockFields["docs"]["cache_size_mb"])

	// compose.base is struct-decoded; compose.extra is yaml:"-" yet re-added.
	require.True(t, formalBlockFields["compose"]["base"])
	require.True(t, formalBlockFields["compose"]["extra"], "compose.extra must be allowed despite yaml:\"-\"")

	// ui is intentionally excluded (uiValidator owns it) — guard against a
	// re-introduction that would double-report.
	_, hasUI := formalBlockFields["ui"]
	require.False(t, hasUI, "ui must not be in formalBlockFields — uiValidator owns the ui: block")
}
