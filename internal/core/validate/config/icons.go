package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/semsemyonoff/dwe/internal/core/project/config"
	"github.com/semsemyonoff/dwe/internal/core/ui/styles"
	"github.com/semsemyonoff/dwe/internal/core/validate"
)

// iconsValidator flags icons whose base codepoint has Emoji_Presentation = No
// (e.g. 🛢, 🗂, ⚙). Such glyphs render unpredictably across terminals — some
// honour VS16 and draw 2 cells, others ignore it and draw 1, breaking column
// alignment in status tables, multi-select menus, and the info dashboard.
// styles.SafeIcon already drops these at render time; this validator surfaces the
// issue at config time so authors can fix it instead of seeing icons silently
// disappear.
//
// Scope: cfg.Services[*].Icon, cfg.Services[*].Info.Paths[*].Icon, and every
// InfoItem.Icon under devbox/info.yml (recursively into subgroup items).
type iconsValidator struct{}

var _ validate.Validator = (*iconsValidator)(nil)

func (v *iconsValidator) ID() string {
	return "icons"
}

func (v *iconsValidator) Domain() string {
	return "config"
}

func (v *iconsValidator) Run(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	if ctx.Cfg != nil {
		diags = append(diags, v.runServices(ctx)...)
	}
	diags = append(diags, v.runInfo(ctx)...)

	return diags
}

func (v *iconsValidator) runServices(ctx validate.Context) []validate.Diagnostic {
	var diags []validate.Diagnostic

	// Deterministic order: sort service names so the validator output is
	// stable across runs (map iteration is randomized).
	for _, name := range slices.Sorted(maps.Keys(ctx.Cfg.Services)) {
		svc := ctx.Cfg.Services[name]
		svcFile := filepath.Join(ctx.ProjectRoot, "workspace", "services", name, "service.yml")
		file := relPath(ctx.ProjectRoot, svcFile)

		if d, ok := iconDiag(svc.Icon, file, "config.icons:services:"+name); ok {
			d.Message = fmt.Sprintf("service %q: %s", name, d.Message)
			diags = append(diags, d)
		}

		for _, p := range svc.Info.Paths {
			if d, ok := iconDiag(p.Icon, file, "config.icons:services:"+name); ok {
				d.Message = fmt.Sprintf("service %q path %q: %s", name, p.Name, d.Message)
				diags = append(diags, d)
			}
		}
	}

	return diags
}

func (v *iconsValidator) runInfo(ctx validate.Context) []validate.Diagnostic {
	infoPath := filepath.Join(ctx.ProjectRoot, "workspace", "info.yml")
	file := relPath(ctx.ProjectRoot, infoPath)

	// Silent skip when info.yml is absent — infoValidator already emits an
	// Info diagnostic for that case.
	if _, statErr := os.Stat(infoPath); statErr != nil {
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		// Other stat errors are surfaced by infoValidator too; skip here.
		return nil
	}

	infoCfg, err := config.LoadInfoConfig(infoPath)
	if err != nil {
		// Parse / decode errors are owned by infoValidator.
		return nil
	}

	var diags []validate.Diagnostic
	for si := range infoCfg.Sections {
		section := infoCfg.Sections[si]
		base := fmt.Sprintf("section[%d]", si)
		if section.Title != "" {
			base = fmt.Sprintf("section %q", section.Title)
		}
		diags = append(diags, walkInfoItems(section.Items, base, file)...)
	}
	return diags
}

func walkInfoItems(items []config.InfoItem, locPrefix, file string) []validate.Diagnostic {
	var diags []validate.Diagnostic
	for i := range items {
		item := items[i]
		itemLoc := fmt.Sprintf("%s.items[%d]", locPrefix, i)
		label := item.Name
		if label == "" {
			label = item.Title
		}
		if label != "" {
			itemLoc = fmt.Sprintf("%s (%q)", itemLoc, label)
		}
		if d, ok := iconDiag(item.Icon, file, "config.icons:info"); ok {
			d.Message = fmt.Sprintf("info %s: %s", itemLoc, d.Message)
			diags = append(diags, d)
		}
		if len(item.Items) > 0 {
			diags = append(diags, walkInfoItems(item.Items, itemLoc, file)...)
		}
	}
	return diags
}

// iconDiag returns a Diagnostic and ok=true when icon is ambiguous; ok=false
// otherwise. Callers prefix the Message with the location-specific lead-in
// (e.g. "service \"foo\":" or "info section[0].items[1]:").
//
// The Hint includes up to three suggested replacements when the curated map
// has an entry for the icon's base codepoint; falls back to a generic phrase
// otherwise.
func iconDiag(icon, file, target string) (validate.Diagnostic, bool) {
	if !styles.IsAmbiguousWidthIcon(icon) {
		return validate.Diagnostic{}, false
	}
	hint := buildIconHint(icon)
	return validate.Diagnostic{
		Severity: validate.SeverityWarning,
		Domain:   "config",
		Target:   target,
		File:     file,
		Message:  fmt.Sprintf("icon %q depends on terminal VS16 support and is dropped from output to keep columns aligned", icon),
		Hint:     hint,
	}, true
}

func buildIconHint(icon string) string {
	suggestions := styles.SuggestSafeIcons(icon, 3)
	if len(suggestions) == 0 {
		return "pick an icon whose base codepoint has Emoji_Presentation = Yes (renders as 2 cells in every terminal)"
	}
	return "try: " + strings.Join(suggestions, " ")
}
