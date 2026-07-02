package tui

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/semsemyonoff/dwe/internal/shared/i18n"
)

// testsupport.go exports a small cross-package test harness. The in-package
// helpers (newTestFrame, buildHelpOverlay, the Run capability seams) are
// unexported and unreachable from sibling packages such as cmdbrowser, which
// cannot be imported back into tui (cmdbrowser depends on tui — an import
// cycle). These exported entry points let a consumer package render a plugin
// inside a real Frame and build the help overlay deterministically, WITHOUT a
// terminal, so it can write golden + behavioural tests against the framework.
//
// This is production-package code (a normal .go file, not _test.go) only because
// Go test files are package-private; it has no production callers and exists
// purely for cross-package tests. Keep it free of any non-test responsibility.

// RenderFrame builds a [Frame] over p, applies a single WindowSizeMsg of w×h so
// geometry is populated, and returns the composited frame as it would render
// (the tea.View Content string). opts threads the public knobs (Brand/Project/
// Mouse/Translator/Locale) exactly as [Run] would — the terminal-capability
// gate and event loop are skipped. It returns the construction error from
// [newFrame] (duplicate action/key, empty/zero-weight panels) unchanged, so a
// consumer test can assert contract violations the same way Run surfaces them.
//
// The returned string is ANSI-styled; strip it (e.g. with the consumer's own
// ANSI-strip helper) for byte-stable goldens.
func RenderFrame(p Plugin, opts RunOptions, w, h int) (string, error) {
	frame, err := newFrame(p,
		withBrand(opts.Brand),
		withProject(opts.Project),
		withMouse(opts.Mouse),
		withTranslator(opts.Translator),
		withLocale(opts.Locale),
	)
	if err != nil {
		return "", fmt.Errorf("tui: building frame: %w", err)
	}
	frame.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return frame.View().Content, nil
}

// RenderFrameAfterSetup is like RenderFrame but applies additional messages
// after the initial WindowSizeMsg before snapshotting. Use this to drive the
// Frame into a non-default state (e.g. inject a tab key to switch panel focus
// or inject an async message to verify the Frame forwards it to the plugin)
// before golden tests. The messages are applied in order; each Update's returned
// Cmd is discarded (same as RenderFrame's treatment of the WindowSizeMsg Cmd).
func RenderFrameAfterSetup(p Plugin, opts RunOptions, w, h int, setup ...tea.Msg) (string, error) {
	frame, err := newFrame(p,
		withBrand(opts.Brand),
		withProject(opts.Project),
		withMouse(opts.Mouse),
		withTranslator(opts.Translator),
		withLocale(opts.Locale),
	)
	if err != nil {
		return "", fmt.Errorf("tui: building frame: %w", err)
	}
	frame.Update(tea.WindowSizeMsg{Width: w, Height: h})
	for _, msg := range setup {
		frame.Update(msg)
	}
	return frame.View().Content, nil
}

// BuildHelp builds the registry-generated help overlay for p at the given size,
// resolving the title/section/action strings through tr (nil → NopTranslator)
// for locale. w and h are the body-region dimensions the modal must fit within
// (the same width/height [buildHelpOverlay] clamps against); pass the inner body
// region you intend to composite the modal over. The returned [Overlay] carries
// the rendered content plus its measured cell dimensions.
//
// It returns the plugin's Actions error (duplicate action/key) unchanged so a
// consumer can assert a misconfigured action set fails before any render.
func BuildHelp(p Plugin, tr i18n.Translator, locale string, w, h int) (Overlay, error) {
	if tr == nil {
		tr = i18n.NopTranslator{}
	}
	if locale == "" {
		locale = "en"
	}
	reg := NewRegistry()
	if err := p.Actions(reg); err != nil {
		return Overlay{}, fmt.Errorf("tui: registering plugin actions: %w", err)
	}
	return buildHelpOverlay(reg, tr, locale, w, h), nil
}
