// Package tui is the shared full-screen TUI framework skeleton — the chrome,
// layout, input, and overlay layer that the three existing full-screen TUIs
// (command browser, docs browser, status dashboard) will later migrate onto.
//
// It owns a Bubble Tea Model ([Frame]) parameterised by a [Plugin]. The frame
// computes screen geometry once per resize, draws bordered body panels plus a
// bottom status line, composites modal overlays (help, future inspect/filter)
// centred over the body, and dispatches keys through an action registry.
// Plugins supply panel content as strings and never touch the terminal envelope
// (alt-screen, mouse mode, cursor) — the framework owns those.
//
// # Layering
//
// This package lives under internal/core/ui (a sink layer, see
// docs/internals/packages.md). It depends on core/ui/styles, core/ui/widgets
// (for the prompt-hook wrapper around the full-screen program), and the
// charm.land v2 stack only. It is importable ONLY from core/ui/* and cli/;
// nothing else may import it. Stage 0 adds no importers — the package is
// exercised entirely through its own tests.
//
// # API Status
//
// The registry/keymap/overlay-input surface ([Binding], [Action], [Registry],
// stdlib actions, [CapturesInput]) is locked in Stage 1. Callers may depend on
// these contracts without restructuring registration sites in later stages.
//
// The [Plugin] contract is PINNED but not frozen: it stays stable through
// Stage 3, which may feed one revision back before it freezes for Stages 4–5b
// (see spec § 7).
//
// Forward pointers:
//   - Stage 2 wires the mouse vocabulary locked in [Binding.Mouse] against the
//     hit-testing seams.
//   - Stages 3–5b migrate each real surface onto the frozen contract.
package tui
