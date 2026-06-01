package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	devconfig "github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

func TestIconsValidator_Services(t *testing.T) {
	t.Parallel()

	cfg := &devconfig.DweConfig{
		Services: map[string]devconfig.ServiceConfig{
			"good": {Icon: "📁"},
			"bad-bare": {
				Icon: "🛢",
				Info: devconfig.ServiceInfoBlock{
					Paths: []devconfig.ServiceInfoPath{
						{Name: "admin", Path: "/admin", Icon: "⚙"},
						{Name: "ok", Path: "/ok", Icon: "🔧"},
					},
				},
			},
			"bad-vs16": {Icon: "🗂️"},
			"no-icon":  {Icon: ""},
		},
	}

	ctx := validate.Context{
		ProjectRoot: t.TempDir(), // info.yml absent → no info diags
		Cfg:         cfg,
	}

	diags := (&iconsValidator{}).Run(ctx)

	// Expected ambiguous icons: bad-bare.Icon, bad-bare.Info.Paths[admin],
	// bad-vs16.Icon — three diagnostics in total.
	require.Len(t, diags, 3, "diag dump: %+v", diags)

	for _, d := range diags {
		assert.Equal(t, validate.SeverityWarning, d.Severity)
		assert.Equal(t, "config", d.Domain)
		assert.True(t, strings.HasPrefix(d.Target, "config.icons:services:"),
			"target prefix mismatch: %s", d.Target)
		assert.True(t, strings.HasSuffix(d.File, "service.yml"),
			"file suffix mismatch: %s", d.File)
		assert.NotEmpty(t, d.Hint, "every ambiguous diag should carry a hint")
	}

	// Check messages mention service names.
	hits := map[string]bool{}
	for _, d := range diags {
		switch {
		case strings.Contains(d.Message, `"bad-bare"`) && strings.Contains(d.Message, `path "admin"`):
			hits["bad-bare-path"] = true
		case strings.Contains(d.Message, `"bad-bare"`):
			hits["bad-bare-icon"] = true
		case strings.Contains(d.Message, `"bad-vs16"`):
			hits["bad-vs16-icon"] = true
		}
	}
	assert.True(t, hits["bad-bare-icon"], "missing bad-bare icon diag")
	assert.True(t, hits["bad-bare-path"], "missing bad-bare path diag")
	assert.True(t, hits["bad-vs16-icon"], "missing bad-vs16 icon diag")
}

func TestIconsValidator_HintHasSuggestions(t *testing.T) {
	t.Parallel()

	cfg := &devconfig.DweConfig{
		Services: map[string]devconfig.ServiceConfig{
			"drum": {Icon: "🛢"},
		},
	}
	ctx := validate.Context{ProjectRoot: t.TempDir(), Cfg: cfg}

	diags := (&iconsValidator{}).Run(ctx)
	require.Len(t, diags, 1)

	hint := diags[0].Hint
	assert.Contains(t, hint, "try:")
	// Curated map for 🛢 starts with 🪣.
	assert.Contains(t, hint, "🪣")
}

func TestIconsValidator_HintFallbackForUnmappedAmbiguous(t *testing.T) {
	t.Parallel()

	// U+26F0 ⛰ (mountain) is a text-default codepoint with no entry in the
	// curated replacement map. The validator should still flag it (warning)
	// and fall back to the generic hint phrasing.
	cfg := &devconfig.DweConfig{
		Services: map[string]devconfig.ServiceConfig{
			"peak": {Icon: "⛰"},
		},
	}
	ctx := validate.Context{ProjectRoot: t.TempDir(), Cfg: cfg}

	diags := (&iconsValidator{}).Run(ctx)
	require.Len(t, diags, 1)
	assert.Contains(t, diags[0].Hint, "Emoji_Presentation")
}

func TestIconsValidator_NilCfgStillRunsInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	infoDir := filepath.Join(dir, "workspace")
	require.NoError(t, os.MkdirAll(infoDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(infoDir, "info.yml"),
		[]byte(`sections:
  - title: Test
    items:
      - type: definition
        name: A
        value: a
        icon: "🛢"
      - type: definition
        name: B
        value: b
        icon: "📁"
      - type: subgroup
        title: Sub
        items:
          - type: definition
            name: Nested
            value: n
            icon: "⚙"
`),
		0o644,
	))

	ctx := validate.Context{ProjectRoot: dir, Cfg: nil}
	diags := (&iconsValidator{}).Run(ctx)

	require.Len(t, diags, 2, "expected diagnostics for 🛢 and nested ⚙; got: %+v", diags)

	var sawDrum, sawNestedGear bool
	for _, d := range diags {
		assert.Equal(t, validate.SeverityWarning, d.Severity)
		assert.Equal(t, "config.icons:info", d.Target)
		assert.True(t, strings.HasSuffix(d.File, "info.yml"), "file: %s", d.File)
		if strings.Contains(d.Message, "🛢") {
			sawDrum = true
			assert.Contains(t, d.Message, `"A"`)
		}
		if strings.Contains(d.Message, "⚙") {
			sawNestedGear = true
			assert.Contains(t, d.Message, `"Nested"`)
		}
	}
	assert.True(t, sawDrum, "missing 🛢 diag")
	assert.True(t, sawNestedGear, "missing nested ⚙ diag")
}

func TestIconsValidator_NoInfoFile(t *testing.T) {
	t.Parallel()

	ctx := validate.Context{ProjectRoot: t.TempDir(), Cfg: nil}
	diags := (&iconsValidator{}).Run(ctx)
	assert.Empty(t, diags, "absent info.yml + nil Cfg → no diagnostics")
}

func TestIconsValidator_DomainAndID(t *testing.T) {
	t.Parallel()
	v := &iconsValidator{}
	assert.Equal(t, "config", v.Domain())
	assert.Equal(t, "icons", v.ID())
}
