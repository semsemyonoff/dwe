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
| `quit`        | `q`, `ctrl+c`   | `esc`   | General    | Quit                  |
| `focus.next`  | `tab`           | —       | Navigation | Focus next panel      |
| `focus.prev`  | `shift+tab`     | —       | Navigation | Focus previous panel  |

Section registration order: Navigation first (FocusNext, FocusPrev), then General
(Help, Quit). This order drives the help modal layout — see §3.

**`esc` alias on `quit`:** `esc` is a hidden alias for `ActionQuit`. It dispatches
(Match resolves it) but is absent from the help modal, matching the existing
cmdbrowser and docs-browser muscle memory without cluttering the help display.
Precedence rule: when an overlay is open the frame's modal-input policy consumes
`esc` to **close the overlay** before the registry is consulted; `esc` only reaches
`ActionQuit` in normal mode (no overlay). See §5 for the capturing-overlay variant.

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
| `nav.page-down` | `pgdn`, `f`       | Navigation | Page down        |
| `select`        | `enter`           | General    | Select           |
| `reload`        | `ctrl+r`          | General    | Reload           |
| `filter`        | `/`               | Filter     | Filter           |
| `inspect`       | `i`               | Inspect    | Inspect          |

`nav.top` and `nav.bottom` carry **dual binds** (`g`/`home` and `G`/`end`
respectively) to unify the cmdbrowser (`home`/`end`) and docs-browser (`g`/`G`)
muscle memory without loss for either. See §4 for the cross-surface rationale.

`nav.page-up` / `nav.page-down` carry `b`/`f` (docs-browser page bindings) and
`pgup`/`pgdn` (cmdbrowser bindings) as dual binds for the same reason.

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

**Docs browser** (`internal/core/docs/tui/`):

| Key | Current description    |
|-----|------------------------|
| `]` | Next diagram           |
| `[` | Prev diagram           |
| `o` | Open diagram           |
| `y` | Copy diagram           |
| `L` | Language cycle         |
| `e` | Show English           |
| `r` | Reload                 |

Note: docs-browser uses `r` for reload while the stdlib uses `ctrl+r`. During the
Stage 3–5b migrations the surface will align to the stdlib ID (`reload`) and bind
both keys (the surface-specific `r` and the stdlib `ctrl+r`).

**Status dashboard** (`internal/core/ui/statustui/`):

| Key            | Current description |
|----------------|---------------------|
| `tab`, `right` | Next tab            |
| `shift+tab`, `left` | Prev tab       |
| `1`            | Services tab        |
| `2`            | Deploy tab          |
| `3`            | Topology tab        |
| `4`            | Git tab             |
| `5`            | Daemons tab         |
| `r`            | Reload              |

Tab-jump keys (`1`–`5`) are plugin-local and have no stdlib equivalent.

---

## 2. Binding semantics

### 2.1 Keys vs Aliases

`Binding.Keys` — canonical keys. They dispatch via `Match` **and** appear in the
help modal.

`Binding.Aliases` — additional physical keys that dispatch via `Match` but are
**hidden from the help modal**. Purpose: muscle-memory compatibility without
cluttering the help display. Example: `esc` as a hidden quit alias.

Both go through `Registry.Register`'s pre-commit duplicate guard: an alias colliding
with any existing key/alias, or with the binding's own canonical `Keys`, is an error.

### 2.2 Rebindable (metadata only)

`Binding.Rebindable bool` marks whether a project may override `Keys` via a future
rebinding config. No config loader is built yet — this is documented metadata only.
See §7 for the future schema sketch.

### 2.3 Mouse (Stage 2 seam)

`Binding.Mouse string` holds a placeholder for a mouse trigger bound to the same
action. Locked vocabulary (to be wired in Stage 2):

- `"wheel-up"` — scrolling up
- `"wheel-down"` — scrolling down
- `"click"` — single click
- `"double-click"` — double click

The field is not consulted by dispatch this stage. See §6 for frame-owned mouse
behaviors that are NOT in the registry.

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

3. Filter
4. Inspect
5. (surface-specific: View, Tabs, etc.)

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

The docs-browser binds `pgup`/`b`/`pgdn`/`f`; the cmdbrowser binds `pgup`/`pgdown`.
After migration both will share `ActionPageUp` (`Keys: ["pgup", "b"]`) and
`ActionPageDown` (`Keys: ["pgdn", "f"]`) via dual bind.

### `e`/`y` key overload — intentional

`e` = edit params (cmdbrowser) vs `e` = show English (docs-browser). Each surface
has its own plugin registry, so there is no physical conflict. This is intentional
and documented: per-plugin registries allow natural surface-specific bindings without
a shared key space.

Similarly `y` = skip confirm (cmdbrowser) vs `y` = copy diagram (docs-browser).

### `esc` precedence rule

`esc` is a hidden alias on `ActionQuit`. Precedence:

1. When a **non-capturing** overlay is open: the frame swallows `esc` as
   ActionHelp/ActionQuit (the only built-ins that act while a modal is open). In
   practice `esc` dismisses the overlay because the help action toggles it off.
2. When a **capturing** overlay is open (`CapturesInput: true`): `esc` routes to
   `captureClose` (close the overlay) — the registry is bypassed entirely. See §5.
3. In **normal mode** (no overlay): `esc` reaches `ActionQuit` via the alias.

This matches the existing cmdbrowser and docs-browser behavior and the forms-guidance
`esc`=cancel intent.

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

Locked in Stage 1; wired in Stage 2.

### 6.1 Registry-bound mouse actions (Stage 2)

Default mouse bindings for stdlib actions (to be wired in Stage 2):

| Mouse event     | Action      |
|-----------------|-------------|
| `wheel-up`      | `nav.up`    |
| `wheel-down`    | `nav.down`  |
| `double-click`  | `select`    |

### 6.2 Frame-owned mouse behaviors (NOT in the registry)

These are handled by the frame itself, not by the action registry. They apply across
all surfaces:

| Mouse event               | Frame behavior                          |
|---------------------------|-----------------------------------------|
| Click on panel            | Move focus to clicked panel             |
| Click on help hint (status bar) | Open help modal (`ActionHelp`)   |
| Click on tab              | Switch to clicked tab                   |
| Click outside modal       | Swallowed (does not close the modal)    |

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
