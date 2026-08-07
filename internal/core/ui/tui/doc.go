// Package tui is the shared full-screen TUI framework — the chrome, layout,
// input, and overlay layer that the three full-screen TUIs (command browser,
// docs browser, status dashboard) run on.
//
// It owns a Bubble Tea Model ([Frame]) parameterised by a [Plugin]. The frame
// computes screen geometry once per resize, draws bordered body panels plus a
// bottom status line, composites modal overlays (help, inspect, embedded forms)
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
// nothing else may import it.
package tui
