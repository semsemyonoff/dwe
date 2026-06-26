// Package tui is the shared full-screen TUI framework skeleton — the chrome,
// layout, input, and overlay layer that the three existing full-screen TUIs
// (command browser, docs browser, status dashboard) will later migrate onto.
//
// It owns a Bubble Tea Model ([Frame]) parameterised by a [Plugin]. The frame
// computes screen geometry once per resize, draws bordered body panels plus a
// bottom status line, composites modal overlays (help, future inspect/filter)
// centred over the body, and dispatches keys through a provisional action
// registry. Plugins supply panel content as strings and never touch the
// terminal envelope (alt-screen, mouse mode, cursor) — the framework owns those.
//
// # Layering
//
// This package lives under internal/core/ui (a sink layer, see
// docs/internals/packages.md). It depends on core/ui/styles and the charm.land
// v2 stack only. It is importable ONLY from core/ui/* and cli/; nothing else may
// import it. This stage (Stage 0) adds no importers — the package is exercised
// entirely through its own tests.
//
// # Spike — provisional API
//
// This is a Stage 0 spike. The public surface (registry shape, Plugin contract,
// overlay/focus helpers) is intentionally NOT frozen. Fields marked "provisional"
// or "Stage 1/2" are documented placeholders that later stages lock down:
//
//   - Stage 1 locks the registry API (aliases, rebinding, mouse bindings).
//   - Stage 2 implements the mouse layer against the hit-testing seams left here.
//   - Stages 3–5b migrate each real surface onto the frozen contract.
package tui
