# TUI Keymap Reference

This document is the single source of truth for the action taxonomy, default key
bindings, help-section ordering, mouse vocabulary, and cross-surface design decisions
for the three full-screen TUIs (command browser, docs browser, status dashboard) and
the `huh` forms.

It corresponds to the Stage 1 lock of `internal/core/ui/tui/`. The code is
authoritative; this doc describes it and is kept byte-stable by the package tests.

---

## 1. Action taxonomy

### 1.1 Framework built-ins

Registered automatically by `NewRegistry`. The framework handles these directly —
they never reach the plugin's `HandleAction`.

| Action ID     | Keys            | Aliases | Section    | Description           |
|---------------|-----------------|---------|------------|-----------------------|
| `help`        | `?`             | —       | General    | Toggle help modal     |
| `quit`        | `q`, `ctrl+c`   | —       | General    | Quit                  |
| `focus.next`  | `tab`           | —       | Navigation | Focus next panel      |
| `focus.prev`  | `shift+tab`     | —       | Navigation | Focus previous panel  |

Section registration order: Navigation first (FocusNext, FocusPrev), then General
(Help, Quit). This order drives the help modal layout — see §3.

**`esc` is never a quit key:** `esc` only ever **closes the visible overlay** (the
frame's modal-input policy consumes it before the registry is consulted) and is a
no-op in normal mode (no overlay) — it forwards to `plugin.Update`, which ignores
it. `esc` must never exit the TUI; quitting is `q` / `ctrl+c` only. See §5 for the
capturing-overlay variant.

### 1.2 Stdlib shared actions

Exported from `internal/core/ui/tui/actions.go`. These are **opt-in** — not
auto-registered by `NewRegistry`. Plugins call `RegisterStandard(reg, ...)` in their
`Actions` hook and interpret the IDs in `HandleAction`.

| Action ID       | Keys              | Section    | Description      |
|-----------------|-------------------|------------|------------------|
| `nav.up`        | `up`, `k`         | Navigation | Move up          |
| `nav.down`      | `down`, `j`       | Navigation | Move down        |
| `nav.left`      | `left`, `h`       | Navigation | Move left        |
| `nav.right`     | `right`, `l`      | Navigation | Move right       |
| `nav.top`       | `g`, `home`       | Navigation | Go to top        |
| `nav.bottom`    | `G`, `end`        | Navigation | Go to bottom     |
| `nav.page-up`   | `pgup`, `b`       | Navigation | Page up          |
| `nav.page-down` | `pgdown`, `f`     | Navigation | Page down        |
| `select`        | `enter`           | General    | Select           |
| `reload`        | `ctrl+r`          | General    | Reload           |
| `filter`        | `/`               | Filter     | Filter           |
| `inspect`       | `i`               | Inspect    | Inspect          |

`nav.top` and `nav.bottom` carry **dual binds** (`g`/`home` and `G`/`end`
respectively) to unify the cmdbrowser (`home`/`end`) and docs-browser (`g`/`G`)
muscle memory without loss for either. See §4 for the cross-surface rationale.

`nav.page-up` / `nav.page-down` carry `b`/`f` (docs-browser page bindings) and
`pgup`/`pgdown` (the canonical bubbletea key strings — `KeyPgDown.String()` is
`"pgdown"`, not `"pgdn"`) as dual binds for the same reason.

### 1.3 Plugin-local actions (by surface)

These are owned by each surface's plugin and registered in their migration stages
(Stages 3–5b). They are documented here as a reference target drawn from the existing
key inventories in the source files.

**Command browser** (`internal/core/ui/cmdbrowser/`):

| Key       | Current description |
|-----------|---------------------|
| `y`       | Skip confirm        |
| `e`       | Edit params         |
| `space`   | Toggle              |
| `backspace`| Delete (filter)    |

**Docs browser** (`internal/core/ui/docstui/`):

As of TUI Stage 4 this is a `tui.Plugin`. Plugin-local actions registered via `Actions()`:

**Diagrams** section:

| Key | Description                                         |
|-----|-----------------------------------------------------|
| `]` | Next diagram (moves the cursor to its row)          |
| `[` | Prev diagram (moves the cursor to its row)          |
| `o` | Open diagram (system viewer)                        |
| `y` | Copy diagram source                                 |
| `E` | Show render error (full mmdc log, in an overlay)    |

The active diagram is the one **under the viewport cursor** (`syncActiveDiagram` →
`activeDiagramForCursor`), not a topmost-visible heuristic. `[`/`]` move the cursor
onto the prev/next diagram's row (`jumpToDiagram`) and the others act on that
selection. `E` opens a `CapturesInput` overlay (mirrors the cmdbrowser inspect
overlay) showing the captured `mmdc` error for the current diagram; it is a no-op
when the diagram rendered fine or rendering is disabled (`Prefetch.RenderError`).

**Locales** section:

| Key | Description       |
|-----|-------------------|
| `L` | Language cycle    |
| `e` | Show English      |

**Tree navigation** (plugin-local — stdlib `nav.left`/`nav.right` are NOT registered;
`tree.collapse`/`tree.expand` own these keys instead):

| Key            | Description                                        |
|----------------|----------------------------------------------------|
| `h`, `←`       | Collapse (step to parent if already collapsed)     |
| `l`, `→`       | Expand (step into first child if already expanded) |

**Half-page scroll** (plugin-local `nav.halfpage.up`/`nav.halfpage.down`, **Navigation**
section): vim `ctrl+u`/`ctrl+d` scroll the focused pane by half its visible height
(`navHalfPage` — viewport `ScrollBy(±VisibleHeight/2)`, tree jumps `treeInner.Height/2`
rows). Added so keyboard reading is the primary scroll path and the mouse wheel is a bonus.

| Key      | Description     |
|----------|-----------------|
| `ctrl+d` | Half page down  |
| `ctrl+u` | Half page up    |

**Viewport line cursor**: when the viewport is focused, `j`/`k`/`↑`/`↓`/page/half-page
move a reading **cursor** (a left-margin `▎` glyph), not the raw scroll offset; the
viewport scrolls only enough to keep the cursor on screen (`syncViewportToCursor`,
revdiff-style). A click positions the cursor on the clicked row; the mouse wheel scrolls
freely and re-pins the cursor into view at burst settle (`flushWheel` → `pinCursorToWindow`).
The glyph is drawn only while the viewport is focused (`applyCursorGlyph`), overwriting
glamour's margin space so it is width-neutral.

**Internal links** (`enter` on the cursor row, or a click): glamour emits OSC-8 hyperlinks;
relative `.md` links (optionally `#anchor`) navigate to the target topic (`followLink` →
tree `SetCursor`/`selectCursor`, anchor → H2/H3 heading scroll). External links
(`http(s)`/`mailto`/`tel`) are left to the terminal's own OSC-8 handling. Pure same-page
anchors (`[x](#frag)`) emit no OSC-8 and are not navigable. See `links.go`.

**Intentional keymap change (Stage 4)**: the pre-migration docs browser toggled
expansion on BOTH `h` and `l` (each called `Tree.Toggle`). Stage 4 adopts directional
semantics for cross-surface unification with the cmdbrowser: `h`/`←` collapse (or step
to parent when already collapsed), `l`/`→` expand (or step into first child when already
expanded). Locked in `actions_test.go`.

**Reload**: `ctrl+r` only (stdlib `ActionReload`). The pre-migration `r` binding is
dropped — no alias kept.

**Filter** (`/`): inline-capture mode (`CapturingInput()` returns true while active).
Raw keys edit the query; `enter` commits, `esc` cancels. Mirrors the cmdbrowser filter
pattern — see §5.

**Focus toggle** (`tab`): framework built-in `ActionFocusNext` cycles between the tree
and viewport panels.

**Status dashboard** (`internal/core/ui/statustui/`):

As of TUI Stage 5b this is a `tui.Plugin` (single panel: tab strip + shared
viewport). Plugin-local actions registered via `Actions()`, **Tabs** section:

| Key            | Description |
|----------------|-------------|
| `left`, `h`    | Prev tab    |
| `right`, `l`   | Next tab    |
| `1`            | Services tab |
| `2`            | Deploy tab  |
| `3`            | Topology tab |
| `4`            | Git tab     |
| `5`            | Daemons tab |

Tab-jump keys (`1`–`5`) and prev/next are plugin-local and have no stdlib
equivalent. `tab.prev`/`tab.next` no-op (return `(nil, true)`) until the first
load completes, mirroring the legacy guard against navigating before `m.tabs` is
populated.

**Reload**: `ctrl+r` only (stdlib `ActionReload`). The pre-migration `r` binding
is dropped — no alias kept, mirroring the docs browser's `r`→`ctrl+r` change.

**Intentional keymap change (Stage 5b)**: the pre-migration status dashboard bound
`tab`/`shift+tab` to next/prev tab. Since `tab`/`shift+tab` are framework
`focus.next`/`focus.prev` built-ins (§1.1), and this surface has only one panel,
they are now harmless no-ops instead of switching tabs. Tab navigation moves
entirely to `left`/`right` (+`h`/`l`) and `1`–`5`, matching the Tabs table above.

**Accepted help-modal wart**: the Frame help modal still lists Navigation
`focus.next`/`focus.prev` (`tab`/`shift+tab`) even though they are inert on this
single-panel surface — the built-ins are always registered by `NewRegistry` and
are not something a plugin can opt out of. Not a blocker; documented here per the
Stage 5b plan.

**Status line**: the Frame owns brand/project (left) and the `?` help hint
(right); the plugin's `StatusContext()` supplies only the middle segment (health
indicator + "loaded X ago" / loading / reloading state), replacing the old
hand-rolled title bar + status bar chrome.

**Narrow terminal fallback**: below the framework's minimum width, `tui.Run`
returns `tui.ErrTooNarrow`; `statustui.Run` passes it up and `cli/status`
renders the plain-text status (`renderDefaultStatus`) instead of a blocking
"too small" screen — mirrors `--no-tui` output.

---

## 2. Binding semantics

### 2.1 Keys vs Aliases

`Binding.Keys` — canonical keys. They dispatch via `Match` **and** appear in the
help modal.

`Binding.Aliases` — additional physical keys that dispatch via `Match` but are
**hidden from the help modal**. Purpose: muscle-memory compatibility without
cluttering the help display (a second key for an action). Note: `esc` is **not** a
quit alias — it only closes overlays and never exits the TUI.

Both go through `Registry.Register`'s pre-commit duplicate guard: an alias colliding
with any existing key/alias, or with the binding's own canonical `Keys`, is an error.

### 2.2 Rebindable (metadata only)

`Binding.Rebindable bool` marks whether a project may override `Keys` via a future
rebinding config. No config loader is built yet — this is documented metadata only.
See §7 for the future schema sketch.

### 2.3 Mouse (wired in Stage 2; pointer-routing added in Stage 4)

`Binding.Mouse string` holds a mouse trigger bound to the same action, resolved
via `Registry.MatchMouse(event string) (Action, bool)`. After the Stage 4 wheel
overhaul `double-click` is the only registry-bound mouse event:

- `"click"` — single click (frame-owned; intentionally NOT registered as a binding)
- `"double-click"` — double click (bound to `select`)

`"wheel-up"` and `"wheel-down"` are no longer valid `Binding.Mouse` values: wheel
events are dispatched immediately as `WheelMsg{Panel, Delta}` to the plugin
(pointer-routed by hit-zone, not focus-routed), bypassing `MatchMouse` entirely.
See §6 for the wheel mechanics.

`Registry.MatchMouse` scans registered bindings for a `Binding.Mouse` match; an
empty event never matches. `Register` rejects a second binding claiming an
already-bound `Mouse` event (mirroring the key/alias collision guards), so at most
one binding can own each event — the scan order is therefore unambiguous. `"click"`
is intentionally never registered — it is handled directly by the frame's
click-routing logic (see §6).

---

## 3. Help modal — section ordering and i18n

### 3.1 Section ordering

Sections appear in first-registration order within the registry. The `NewRegistry`
built-in order is:

1. **Navigation** — FocusNext, FocusPrev (registered first)
2. **General** — Help, Quit (registered second)

Plugin sections appear in registration order after the built-ins, in the order the
plugin's `Actions` hook calls `Register` / `RegisterStandard`. Expected plugin
section order for the migrated surfaces (Stages 3–5b):

1. Filter
2. Inspect
3. (surface-specific: View, Tabs, etc.)

The golden file at `internal/core/ui/tui/testdata/help_default.golden` locks the
built-in order byte-stable.

### 3.2 i18n namespace

The framework uses code-level English fallbacks (`i18n.NopTranslator`) and has no
live translation consumer yet. The namespace is reserved and confirmed in Stage 1:

| Key pattern                    | Fallback              |
|--------------------------------|-----------------------|
| `tui.help.title`               | `"Help"`              |
| `tui.help.section.<id>`        | The English section label |
| `tui.help.action.<actionID>`   | `Binding.Desc`        |

No YAML translation keys are added yet — the rationale is the missing consumer, not
a hard error from the `ui:` validator (which only warns on `ui.commands` keys in
`workspace.yml` and is unrelated to the `tui.help.*` namespace). Real keys land in
the i18n translation store and its known-key list during the migration stages.

---

## 4. Cross-surface design decisions

### home/end + g/G unified

The cmdbrowser binds `home`/`end` for top/bottom; the docs-browser binds `g`/`G`.
After migration both will share `ActionTop` (`Keys: ["g", "home"]`) and `ActionBottom`
(`Keys: ["G", "end"]`) via dual bind — no muscle memory is lost for either surface.

### pgup/pgdn unified

The docs-browser binds `pgup`/`b`/`pgdn`/`f` (note: its `pgdn` is a latent bug —
physical PageDown emits `pgdown`, so only `f` pages down today); the cmdbrowser
binds `pgup`/`pgdown`. After migration both will share `ActionPageUp`
(`Keys: ["pgup", "b"]`) and `ActionPageDown` (`Keys: ["pgdown", "f"]`) via dual
bind — using the canonical bubbletea key string `pgdown` so physical PageDown
actually dispatches.

### `e`/`y` key overload — intentional

`e` = edit params (cmdbrowser) vs `e` = show English (docs-browser). Each surface
has its own plugin registry, so there is no physical conflict. This is intentional
and documented: per-plugin registries allow natural surface-specific bindings without
a shared key space.

Similarly `y` = skip confirm (cmdbrowser) vs `y` = copy diagram (docs-browser).

### `esc` rule — close overlay only, never quit

`esc` is **not** a quit key. Behavior:

1. When a **non-capturing** overlay is open: the frame's modal-input policy
   consumes `esc` to **close the overlay** (pop the top layer) before the registry
   is consulted. `?` toggles help closed and `q`/`ctrl+c` quit; all other keys are
   swallowed (no acting behind the modal).
2. When a **capturing** overlay is open (`CapturesInput: true`): `esc` routes to
   `captureClose` (close the overlay) — the registry is bypassed entirely. See §5.
3. In **normal mode** (no overlay): `esc` is a no-op — it forwards to `plugin.Update`
   (which ignores it) and never exits the TUI.

`esc` must never close the TUI; this matches the forms-guidance `esc`=cancel intent.

---

## 5. Capturing-overlay input policy

`Overlay.CapturesInput bool` (defined in `internal/core/ui/tui/plugin.go`, locked
in Stage 1) is set on overlays that need raw keyboard input — e.g. a filter/search
input box. While a capturing overlay is `Top()`:

- All raw input (including printable characters) routes to the plugin. The registry
  is bypassed.
- Only `ctrl+c` (hard-quit) and `esc` (close overlay) survive as framework actions.
- `?` does **not** open help.

This is modelled by `routeWhileCapturing(msg tea.Msg) captureDecision` in
`internal/core/ui/tui/frame.go`, which returns one of:

| Decision                | Meaning                                  |
|-------------------------|------------------------------------------|
| `captureSwallowToPlugin`| Route to plugin (registry bypassed)      |
| `captureHardQuit`       | `ctrl+c` — exit the program              |
| `captureClose`          | `esc` — dismiss the capturing overlay    |

The full `frame.Update` rewiring that calls `routeWhileCapturing` lands with the
Stage 3 filter consumer (the function signature is already the drop-in shape).

---

## 6. Mouse vocabulary

Locked in Stage 1; wired in Stage 2; pointer-routing overhauled in Stage 4.

### 6.1 Registry-bound mouse actions (wired in Stage 2)

Mouse is enabled when `RunOptions.Mouse = true` (per-program opt-in) and
`TERM != "dumb"` (the `mouseCapable()` gate). When enabled, the frame sets
`tea.MouseModeCellMotion` — click + wheel reporting, no motion spam. The mode
is a fixed framework choice; per-program code only sets the opt-in flag.

After the Stage 4 wheel overhaul, `double-click` is the only registry-bound mouse
action:

| Mouse event     | Action      |
|-----------------|-------------|
| `double-click`  | `select`    |

**Pointer-routed `WheelMsg` (Stage 4 overhaul)** — vertical wheel events are no
longer dispatched through `MatchMouse`. `handleMouse` acts on each
`tea.MouseWheelMsg` synchronously (no accumulator, no tick) and routes by the
hit-zone under the pointer:

- **Panel hit (`zonePanel`)**: a `WheelMsg{Panel: id, Delta: ±1}` is forwarded
  to `plugin.Update` immediately. `Delta` is -1 for an upward notch and +1 for a
  downward notch. The plugin decides the per-panel scroll amount (viewport panel:
  multi-line step; tree/list panel: one cursor row per notch). **Focus is NOT
  changed by a wheel event** — wheeling does not focus the panel under the pointer.
- **Help-hint / blank space**: swallowed.
- **Horizontal wheel** (`MouseWheelLeft`/`MouseWheelRight`, emitted by trackpads
  in CellMotion mode): swallowed.
- **Capturing overlay** (`CapturesInput: true`): the raw `tea.MouseWheelMsg` is
  forwarded to `plugin.Update` and `refreshCapturingOverlay` swaps in the
  re-rendered snapshot — mirroring the captured-key path so the inspect modal
  scrolls with the wheel.
- **Non-capturing overlay** (help) or active **inline filter**
  (`plugin.CapturingInput()` returns true, no overlay): wheel is swallowed.

`double-click` remains the only event routed through `Registry.MatchMouse`.

**Double-click** — a second left-click in the same panel + same cell within a
400ms window (`doubleClickWindow`), gated by `!lastClick.t.IsZero()` (the zero
`time.Time` is never a valid prior click; cleared in full after the Select fires
so triple-click → exactly one Select). Scoped to panel hits only; clicks on blank
space or the help-hint zone clear the record.

### 6.2 Frame-owned mouse behaviors (NOT in the registry)

These are handled by the frame itself, not by the action registry. They apply across
all surfaces:

| Mouse event               | Frame behavior                          |
|---------------------------|-----------------------------------------|
| Click on panel            | Move focus to clicked panel             |
| Click on help hint (status bar) | Open help modal (`ActionHelp`)   |
| Click inside modal        | Swallowed (body never acts behind it)   |
| Click outside modal       | Dismiss the overlay (click-away-to-close, mirrors `esc`) |

**Plugin-facing click forward (row-select / tab-switch)** — the `panelLocal(outer
Region, x, y int) (lx, ly int)` helper translates an absolute click to
panel-inner-local coordinates, but the forward to `plugin.HandleAction` for
row-select and tab-switch is **deferred to Stage 3** (the cmdbrowser pilot),
matching Stage 1's `routeWhileCapturing` deferral. The `Plugin` interface is
**PINNED, not frozen** through Stage 3 — unchanged this stage, the forward seam
lands with the first real consumer.

---

## 7. Forms guidance (Stage 6 reference target)

The `huh` forms used in `dwe deploy` (interactive menu), `setup` wizard, and `ask`
fields use `charm.land/huh/v2` defaults. No form code changes in Stage 1. This
section records the desired bindings as a reference for Stage 6 (forms unification):

| Desired behavior    | Desired key      | Notes                         |
|---------------------|------------------|-------------------------------|
| Quit / cancel form  | `q`, `esc`, `ctrl+c` | Align with registry quit |
| Confirm / select    | `enter`          | Aligns with `ActionSelect`    |

Currently huh forms handle their own key routing outside the action registry. Stage 6
will evaluate whether to wire form navigation through the registry or keep huh's
built-in bindings.

---

## 8. Future rebinding config (not implemented)

`Binding.Rebindable bool` is metadata only — no config loader exists yet. A sketch of
the future schema (YAGNI; no consumer until Stage 3+):

```yaml
# workspace/ui.yml (hypothetical)
keymap:
  quit:
    keys: ["q", "ctrl+c"]
  nav.up:
    keys: ["up", "k", "ctrl+p"]
```

Keyed by `Action` ID. The framework would load this at startup and merge it over the
default `standardBindings` table before each plugin calls `RegisterStandard`. The
`Rebindable: false` flag would prevent a specific binding from being overridden (e.g.
`ctrl+c` hard-quit should not be rebindable).
