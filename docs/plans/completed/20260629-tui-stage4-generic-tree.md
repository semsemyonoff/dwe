# TUI Stage 4 — Plan 2: Generic `tui/tree` Engine Extraction

## Overview

Extract a generic, reusable tree **engine** (`internal/core/ui/tui/tree`) and refactor
both the command browser (`cmdbrowser`) and docs browser (`docstui`) trees onto it. This
is sub-stage 4.3 of the TUI framework milestone and the **remaining work of Stage 4** —
Plan 1 (docs relocation + Frame plugin migration) is complete and archived at
`docs/plans/completed/20260629-tui-stage4-docs-plugin-migration.md`.

The two trees today share a byte-identical scroll/nav core (`topIdx`,
`ensureFocusVisible`, `focusRow`, `clipToViewport`) held together only by "mirror
cmdbrowser exactly" discipline comments, plus structurally-identical visible-set rebuild
and directional collapse/expand logic. This plan replaces that discipline with a
**compiler-guaranteed** shared engine while leaving each consumer's rendering, node
construction, payload (counts/headings), and filter UX untouched.

**Problem it solves:** duplicated behavioral logic (~150–200 lines) that must be kept in
lockstep by hand; docs's loss of expansion state on locale change.

**Key benefit:** behavior changes (scroll feel, key semantics) happen once; a future
third tree consumer gets nav + scroll + expansion for free; docs gains an expansion-
preservation bugfix as a side effect.

**Scope decision (locked via brainstorm): "engine without filter".** We extract the
behavioral *engine*, NOT a renderer and NOT a filter. The filter is deliberately excluded
because the two filters are genuinely different UX — docs **reduces** the visible set
(two-phase mark/emit); cmdbrowser **dims + counts** within the full set. Unifying them is
negative value. The engine exposes a filter-agnostic `keep` predicate hook so docs can
still drive a reduced visible set without the engine ever knowing the word "filter".

**Intended executor: Sonnet** — mechanical pattern-following against a well-specified
engine and two existing trees. The one genuinely careful area is **Step C** (docs
locale/rebuild + filter rewire); call it out explicitly while implementing.

## Context (from discovery)

**Spec:** `docs/plans/specs/2026-06-23-tui-framework-milestone.md` — §4 "Generic tree
widget `internal/core/ui/tui/tree`" + the Stage 4 row. Milestone wording is "single,
generic tree widget shared by command and docs browsers"; this plan deliberately narrows
that to the engine (not the filter) per the scope decision above.

**Reference for tone/structure:** the completed Plan 1
(`docs/plans/completed/20260629-tui-stage4-docs-plugin-migration.md`), which states Plan 2
is the follow-up and that it already "aligns the docs tree's Frame-facing surface with
cmdbrowser's so Plan 2's extraction is a clean lift."

**Tree A — `internal/core/ui/cmdbrowser/`** (`tree.go`, `tree_render.go`, `tree_test.go`,
`tree_render_test.go`):
- Identity: string `id` (dot-path) + `nodesByID` map; cursor is `focusedID string`.
- Expansion: `expanded map[string]bool` (already map-keyed — near-zero change).
- Payload: `countAll`/`countPublic`, `leaves []int` (indices into a separate list panel).
- Filter: **external** — `filterState` passed into `renderRegion`; dims non-matches +
  shows `M/N` counts; does **NOT** reduce the visible set.
- Engine-bound methods today: `moveUp/moveDown/moveHome/moveEnd`, `onLeft`/`onRight`,
  `toggleFocused`, `ensureFocusVisible`, `focusRow`, `rebuildVisible`, `clipToViewport`,
  `indexOfFocused`, `focusedNode`.
- Consumer-only methods that **stay**: `itemsForFocus`, `nearestVisibleAncestor`,
  `setIncludePrivate`, `recomputeCounts`, `build`/`ensureGroup`, `renderTree`/`renderRegion`.
- Call sites: `actions.go`, `filter.go`, `tree_render.go`, `plugin.go`, `tree.go`.

**Tree B — `internal/core/ui/docstui/`** (`tree_widget.go`, `tree_render.go`,
`tree_filter.go`, `tree_widget_test.go`, `tree_index_test.go`, `tree_render_test.go`):
- Identity: pointer `*TreeNode` cursor.
- Expansion: `Expanded bool` field **on each node** (must change). Reads/writes at
  `actions.go:284`, `tree_widget.go:316,406,502-503,524-525,547`, `tree_render.go:89,97`.
- Payload: `docs.Node`, headings as sub-rows, multi-root groups, `index.md` folding,
  locale-dependent labels.
- Filter: **internal** — `ApplyFilter` + two-phase `markFilterMatches`/`emitFiltered`;
  **REDUCES** the visible set.
- Rebuilds the whole node graph on locale change (`Rebuild`), restoring cursor via
  `findByPath` and **losing** expansion (except cursor ancestors).
- Already has `topIdx`/`ensureFocusVisible`/`focusRow`/`clipToViewport` byte-identical to
  cmdbrowser (with "mirror cmdbrowser exactly" comments).
- Exported method call sites: `Tree.{MoveUp,MoveDown,MoveStart,MoveEnd,Collapse,Expand,
  Toggle,Cursor,SetCursor,VisibleNodes,ApplyFilter,Rebuild}` across `actions.go`,
  `plugin.go`, `tree_filter`-adjacent code, `model.go`.

**Framework package:** `internal/core/ui/tui` (`Region`, `Plugin`, `RenderFrame`,
`BuildHelp`). The new subpackage `tui/tree` is a peer leaf — depends on nobody in the
project (pure generic data structure), consumed by `cmdbrowser` + `docstui`.

**Conventions:** table-driven tests next to code; `make build` after `docs/` edits
(embedded-docs sync); **`make test` only** (never bare `go test ./...` — embedded docs are
gitignored/generated); `make lint` clean before done.

## Development Approach

- **Testing approach: Regular** (write/port code in each task, then add/adapt tests in the
  same task). For a behavior-preserving refactor the engine + adapters land first, tests
  follow to lock behavior. The engine itself (Task 1) is effectively test-first-friendly
  but tracked as Regular.
- Complete each task fully before the next; small, focused changes.
- **Every task MUST include new/updated tests** as separate checklist items.
- **All tests must pass before starting the next task** (`make test`).
- Keep this plan in sync with reality; note deviations with ➕/⚠️.
- **Behavior-preservation is the prime directive:** both browsers must look and behave
  identically. The one *intended* behavior change is docs preserving expansion across
  locale change (a bugfix) — document it, do not let other drift slip in.

## Testing Strategy

- **Unit tests**: required every task.
  - **Engine unit tests (Task 1) are the primary safety net** — exercised against a fake
    node type so the engine is validated with zero consumer coupling.
  - cmdbrowser/docstui tree tests are adapted to the engine-backed surface but keep their
    existing behavioral coverage (nav, collapse/expand, filter, scroll, locale, headings).
- **Golden frame tests**: **both** browsers' existing golden frame tests
  (`cmdbrowser/plugin_golden_test.go`, `docstui/plugin_golden_test.go` at width buckets
  60/79/80/99/100) MUST remain **byte-for-byte unchanged** — that IS the
  "behavior preserved" acceptance criterion. If a golden changes, the refactor leaked.
- **Docs-specific regression**: a test asserting **expansion + cursor survive `SetRoots`**
  keyed by `Key` (locks the locale/rebuild fix and guards the riskiest area).
- **No project e2e suite** (CLI/TUI Go project) — golden + unit tests are the net.
- Run `make build` before `make test` whenever `docs/` is touched; `make lint` clean at end.

## Progress Tracking

- Mark items `[x]` immediately when done.
- New tasks get a ➕ prefix; blockers get ⚠️.
- Update this plan if implementation deviates.

## Solution Overview

A new generic package `internal/core/ui/tui/tree` provides `Engine[N comparable]` plus a
3-method `Adapter[N]` interface. The engine owns the **behavior** — visible-set, cursor,
scroll (`topIdx`), a `parent` map, expansion state (keyed by a stable string `Key`),
directional collapse/expand, navigation, and scroll/clip. Each consumer keeps its own node
graph, row rendering, payload, and filter UX, supplying only a tiny adapter.

`cmdbrowser.treeModel` and `docstui.TreeWidget` become **thin wrappers** holding an
`*Engine[N]` and an adapter, delegating the engine-owned methods and deleting their local
copies. Rendering (`renderTree` / `renderAllRows`) and filter UX are untouched. docs drops
its per-node `Expanded` field in favor of `engine.IsExpanded(node)`, which keys expansion
by stable `Key` and therefore preserves it across the locale rebuild.

## Key Design Decisions (locked via brainstorm)

1. **Generics, not interface boxing.** `Engine[N comparable]` with `N = *treeNode` /
   `*TreeNode` (pointers are comparable). `VisibleNodes()`/`Cursor()` return **concrete**
   `N`/`[]N` — consumers never type-assert.
2. **3-method adapter interface** (not closures, for a discoverable/testable contract):
   ```go
   type Adapter[N comparable] interface {
       Children(N) []N    // ordered child nodes
       Key(N) string      // STABLE id, survives rebuild
       Expandable(N) bool  // false for leaves / heading rows
   }
   ```
   `Key` must be **unique & stable across rebuild**. cmd → dot-path `id`. docs → **explicit
   kind-prefixed** keys to avoid collisions (Codex finding #2: group nodes use
   `Path: root.Name` and heading rows **share the parent file's `docs.Node`**, so a bare
   `rootName+path` collides):
   - group node → `"group\x00" + rootName`
   - file/dir node → `"node\x00" + rootName + "\x00" + path`
   - heading row → `"heading\x00" + rootName + "\x00" + path + "\x00" + strconv.Itoa(index)`
     (index = sibling position in the parent file's heading list, **not** heading text —
     duplicate headings stay unique and locale-stable).
3. **Expansion keyed by `Key` string** in the engine (`expanded map[string]bool`) — NOT by
   node pointer (pointers change on rebuild). cmdbrowser already map-keyed; docs **deletes**
   the `Expanded` field and routes every read through `engine.IsExpanded(node)`. This is a
   **bugfix**: docs currently loses expansion on locale change; stable-Key keying makes
   `SetRoots` preserve it automatically.
4. **Cursor carried across rebuild by `Key`.** Engine stores `cursor N`; on `SetRoots` it
   re-finds the new-generation node whose `Key` matches the old cursor; **if gone it sets
   the cursor to zero and does NOT auto-park** — the consumer chooses the fallback. This
   preserves docs's two-tier restore (Codex finding #1): docs's old `Rebuild` falls back
   from a vanished heading row to its **parent file**, not to the first visible row. docs
   therefore, after `SetRoots`, tries `SetCursorByKey(headingKey)` → on false
   `SetCursorByKey(parentFileKey)` → on false `ParkCursorIfHidden()`. Replaces docs's manual
   `findByPath` cursor transfer. **Re-parking
   on a plain visible rebuild is NOT automatic** — it is a separate `ParkCursorIfHidden()`
   (docs-only) so cmdbrowser's deliberate off-screen-cursor + `nearestVisibleAncestor`
   resolution is preserved (see Technical Details "Cursor re-park policy"). The zero value
   of `N` (`nil`) is the **root/none sentinel** that maps to cmdbrowser's `focusedID == ""`
   (see Technical Details "Root/none cursor sentinel").
5. **Engine builds a `parent` map** from the `Children()` walk (no 4th adapter method).
   Powers "step to parent" (collapse) and "step into first child" (expand). Top-level nodes
   passed to `SetRoots` have no parent → `h`/`←` is a no-op there (current behavior). The
   invisible synthetic root stays a consumer detail; consumers pass the root's children as
   engine roots.
6. **Filter hook without a filter concept.** `RebuildVisible(keep func(N) bool)`:
   - `keep == nil` → all expanded nodes (cmdbrowser; it never reduces the set).
   - `keep != nil` → include a node if `keep(node)` OR any descendant is kept
     (ancestor-inclusion = the two-phase mechanics Plan 1 fixed). docs passes only a
     label-match predicate; query / `/`-mode / `TreeFilter` / counts stay in docs.
   The engine has **zero** filter/query/count concepts — just an optional per-node
   visibility predicate.
7. **Rendering is NOT in the engine.** The engine returns `VisibleNodes()` + `Cursor()` +
   `Clip(...)`; consumers render each row themselves (glyphs, counts, truncation, styles
   genuinely differ). This is the load-bearing boundary.
8. **Map-keying discipline:** `expanded` keyed by **`Key` (string)**; the `parent` map may
   be keyed by **`N`** (rebuilt per generation, pointers valid within a generation); cursor
   stored as **`N`** and re-resolved by `Key` on rebuild. Be explicit in code comments.
9. **Both filters stay per-consumer and untouched** (docs internal reduce-set via the keep
   predicate; cmdbrowser external). No filter code moves into the engine. **Note**:
   cmdbrowser's filter is more than dim+counts — it also **snapshots, auto-collapses, and
   restores the expansion map** (`filter.go` `newFilterState`/`applyAutoCollapse`/
   `restoreExpansion`, operating in id-space over `tm.expanded` + `tm.nodesByID`). With
   expansion now engine-owned, that machinery routes through the engine's bulk/key
   expansion accessors (`ExpandedSnapshot`/`RestoreExpanded`/`SetExpandedByKey`) — the
   filter logic itself stays in cmdbrowser, only its storage backend moves.

## Technical Details

**Engine public surface (`internal/core/ui/tui/tree/tree.go`):**
```go
type Engine[N comparable] struct { /* a, roots, expanded, cursor, visible, topIdx, parent, byKey */ }
func New[N comparable](a Adapter[N]) *Engine[N]
func (e *Engine[N]) SetRoots(roots []N)              // (re)build graph + byKey; re-resolve cursor by its prior Key. If gone => cursor zero (does NOT auto-park; consumer decides fallback)
func (e *Engine[N]) RebuildVisible(keep func(N) bool) // visible-set walk; keep==nil => all expanded. DOES NOT re-park cursor
func (e *Engine[N]) ParkCursorIfHidden()             // if cursor not in visible (and visible non-empty) => visible[0]. docs-only
func (e *Engine[N]) VisibleNodes() []N
func (e *Engine[N]) Cursor() N                        // zero value (nil for *T) == "root/none" sentinel (cmd focusedID=="")
func (e *Engine[N]) SetCursor(n N)
func (e *Engine[N]) SetCursorByKey(key string) bool   // returns true if key resolved; key "" or unknown => zero-value cursor (root focus), returns false
func (e *Engine[N]) MoveUp(); MoveDown(); MoveHome(); MoveEnd()
func (e *Engine[N]) Collapse(); Expand()             // directional h/l semantics (Decision 5)
func (e *Engine[N]) Toggle()                          // flip expansion of cursor (no-op on leaves)
func (e *Engine[N]) IsExpanded(n N) bool
func (e *Engine[N]) SetExpanded(n N, b bool)          // node-based (docs ancestor-expand)
// --- bulk/key expansion accessors REQUIRED by cmdbrowser's filter (NOT speculative) ---
func (e *Engine[N]) ExpandedSnapshot() map[string]bool        // copy, for newFilterState
func (e *Engine[N]) RestoreExpanded(snapshot map[string]bool) // replace internal map (caller then RebuildVisible)
func (e *Engine[N]) SetExpandedByKey(key string, b bool)      // for applyAutoCollapse iterating consumer's nodesByID
func (e *Engine[N]) EnsureFocusVisible(height int)
func (e *Engine[N]) FocusRow(row int)
func (e *Engine[N]) Clip(full string, height int) string      // splits internally; matches both consumers' clipToViewport(full string, …)
```
(Adjust exact method set during Task 1 to exactly cover the call sites enumerated in
Context. The bulk expansion accessors + `SetCursorByKey` + `ParkCursorIfHidden` are
**required by enumerated call sites** — do not drop them as "speculative"; conversely do
not add anything *beyond* what a call site needs.)

**Cursor re-park policy (Decision 4 detail — golden-critical).** docs re-parks the cursor
onto the first visible row when its current cursor falls out of the visible set
(today's `ensureCursorVisible`); cmdbrowser does **NOT** — its `rebuildVisible` tolerates an
off-screen `focusedID` and resolves it later at specific sites via `nearestVisibleAncestor`
(`plugin.go:414-415,435-436`). Therefore re-parking is a **separate** `ParkCursorIfHidden()`
call (docs invokes it after every rebuild; cmdbrowser never does) — it is **NOT** folded
into `RebuildVisible`. Folding it in would change cmdbrowser's filter-exit/commit cursor
landing and flip a golden.

**Root/none cursor sentinel (Decision 4 detail).** cmdbrowser's `focusedID == ""` is an
observable state meaning "focus the invisible root" — it drives `breadcrumb()` → "(root)"
(`plugin.go:189-203`) and `itemsForFocus()` → root-level commands. The invisible root is
**not** an engine node, so the engine cursor's zero value (`nil` for a pointer `N`)
represents it. cmdbrowser maps `nil` ↔ `""`; writes that today assign string ids
(`focusedID = savedFocusID / nearestVisibleAncestor(...) / nearestRestoredAncestor(...)`,
any of which may be `""`) go through `SetCursorByKey`. The engine keeps a `byKey map[string]N`
(built in `SetRoots`) so `SetCursorByKey` and cursor re-resolution are O(1).

**Directional semantics (ported verbatim from current code):** `Collapse` — if cursor
`Expandable` and expanded → collapse + rebuild; else step to parent (if parent exists and
isn't a root). `Expand` — if `Expandable` and collapsed with children → expand + rebuild;
else if expanded → step to first child. Heading rows / leaves are no-ops.

**Visible-set walk with ancestor-inclusion:** when `keep != nil`, a node is emitted iff
`keep(node)` or any descendant is emitted (pre-order, expansion ignored for inclusion but
the renderer still shows the reduced set). When `keep == nil`, emit nodes reachable through
expanded ancestors only (current `walkVisible`/`rebuildVisible`). `RebuildVisible` does
**NOT** touch the cursor (Decision 4 / "Cursor re-park policy"). docs separately calls
`ParkCursorIfHidden()` after rebuild (its current `ensureCursorVisible`); cmdbrowser does
not (it resolves an off-screen cursor via `nearestVisibleAncestor` at its own call sites).

**cmdbrowser wrapper mapping:** `tm.moveUp` → `e.MoveUp`, `onLeft` → `e.Collapse`,
`onRight` → `e.Expand`, `toggleFocused` → `e.Toggle`, `rebuildVisible` →
`e.RebuildVisible(nil)`, `ensureFocusVisible`/`focusRow`/`clipToViewport`/`indexOfFocused`
→ engine equivalents.
- **Cursor reads:** `focusedNode()` → `e.Cursor()` (treat `nil` as the root); `focusedID`
  → `e.Cursor().id` when non-nil, else `""`.
- **Cursor writes** (`focusedID = savedFocusID / nearestVisibleAncestor(...) /
  nearestRestoredAncestor(...)`, any possibly `""`) → `e.SetCursorByKey(id)`. cmdbrowser
  retains `nodesByID`/`root` and keeps mapping `""` ↔ root for `breadcrumb()`/`itemsForFocus()`.
- **Filter expansion** (`filter.go`): `newFilterState(b.tree.expanded, …)` →
  `e.ExpandedSnapshot()`; `applyAutoCollapse` iterates `tm.nodesByID` calling
  `e.SetExpandedByKey(id, matchCount[id] > 0)` then `e.RebuildVisible(nil)`;
  `restoreExpansion` → `e.RestoreExpanded(saved)` then `e.RebuildVisible(nil)`. The post-
  restore `if indexOfFocused() < 0 { focusedID = nearestVisibleAncestor(...) }` sequence in
  `exitFilter`/`commitFilter` stays — relies on `RebuildVisible` NOT re-parking.
- `itemsForFocus`/`nearestVisibleAncestor`/`setIncludePrivate`/`recomputeCounts`/`build`
  stay. `renderTree`/`renderRegion` read `e.VisibleNodes()` and `e.IsExpanded(node)`
  (replacing `tm.visible` / `tm.expanded[id]`).

**docstui wrapper mapping:** `Tree.MoveUp/MoveDown/MoveStart/MoveEnd/Collapse/Expand/Toggle`
delegate to engine; `Cursor`/`SetCursor`/`VisibleNodes` delegate; `Rebuild` rebuilds the
node graph then `e.SetRoots(newRoots)` followed by the **two-tier cursor restore**
(heading key → parent-file key → `ParkCursorIfHidden()`; Codex #1). After any visible
rebuild (filter or not) docs calls `e.ParkCursorIfHidden()` (its `ensureCursorVisible`).
`ApplyFilter` keeps `TreeFilter`; it calls `e.RebuildVisible(nil)` when the filter is
nil/inactive/empty-query and `e.RebuildVisible(matchPredicate)` only for a non-empty active
query (Codex #3), each followed by `ParkCursorIfHidden()`.
`expandAncestors` → `e.SetExpanded(parent, true)` up the chain. All
`node.Expanded` reads → `e.IsExpanded(node)` (incl. `tree_render.go:89,97`, `actions.go:284`,
`tree_widget.go:316`). All `node == tw.cursor` reads (`tree_render.go:76,126`) → `e.Cursor()`.
**Multi-root group nodes** (`tree_widget.go:240`, today `Expanded: true`) must be seeded
default-expanded in the engine (see Task 3) since the engine's map starts empty.

## What Goes Where

- **Implementation Steps** (`[ ]`): engine package + tests, cmdbrowser refactor + tests,
  docstui refactor + tests, internals-doc updates.
- **Post-Completion** (no checkboxes): manual smoke-test of both browsers in a real
  terminal.

## Implementation Steps

### Task 1: Create generic `tui/tree` engine + full unit tests (Step A)

**Files:**
- Create: `internal/core/ui/tui/tree/tree.go`
- Create: `internal/core/ui/tui/tree/tree_test.go`

- [x] create `Engine[N comparable]` + `Adapter[N]` (Decisions 1–2) with fields: adapter,
      `roots []N`, `expanded map[string]bool` (by Key), `cursor N`, `visible []N`,
      `topIdx int`, `parent map[N]N`, `byKey map[string]N`
- [x] implement `New`, `SetRoots` (build `parent` + `byKey` from `Children()` walk;
      re-resolve cursor by its prior `Key`; **if gone, set cursor to zero — do NOT auto-park**,
      the consumer decides the fallback) and `RebuildVisible(keep)` (expanded-walk when nil;
      ancestor-inclusion when set) — and a **separate** `ParkCursorIfHidden()` (NOT called by
      `RebuildVisible` or `SetRoots`) — Decisions 4–6, Technical Details "Cursor re-park policy"
- [x] implement nav (`MoveUp/MoveDown/MoveHome/MoveEnd`, `indexOf` helper), directional
      `Collapse`/`Expand`/`Toggle`, and accessors `VisibleNodes`/`Cursor`/`SetCursor`/
      `SetCursorByKey(key) bool` (returns true if key resolved; `""`/unknown → zero-value
      cursor = root sentinel, returns false). Guard every nav/collapse/expand/`indexOf`/
      `EnsureFocusVisible` path against a **zero/nil cursor** (no panic; behaves as "no
      focus" → first-visible on move, matching cmdbrowser's `indexOfFocused()<0` path)
- [x] implement expansion accessors: node-based `IsExpanded`/`SetExpanded`, and the
      **bulk/key** accessors required by cmdbrowser's filter — `ExpandedSnapshot()`,
      `RestoreExpanded(snapshot)`, `SetExpandedByKey(key,b)` (Decision 9)
- [x] implement `EnsureFocusVisible(height)`, `FocusRow(row)` (click-past-last = no-op),
      `Clip(full string, height int) string` — port verbatim from current cmdbrowser logic
- [x] write engine tests against a **fake node type**: nav + clamps; directional
      collapse/expand (`h` on expanded collapses; `h` on collapsed → parent; `l` on
      collapsed expands; `l` on expanded → first child); `EnsureFocusVisible`/`FocusRow`/
      `Clip` window math incl. click-past-last no-op
- [x] write engine tests: `RebuildVisible(nil)` vs keep-predicate ancestor-inclusion;
      **`RebuildVisible` does NOT move the cursor** even when the cursor falls out of the
      visible set; `ParkCursorIfHidden()` moves it only when called
- [x] write engine tests: **expansion + cursor survive `SetRoots` keyed by `Key`**; a
      `SetRoots` whose prior cursor key vanished leaves `Cursor()` zero (no auto-park);
      `SetCursorByKey` returns true on a known key, false + zero cursor on `""`/unknown;
      **nav/collapse/expand/`EnsureFocusVisible` are safe (no panic) with a zero/nil cursor**;
      `ExpandedSnapshot`/`RestoreExpanded`/`SetExpandedByKey` round-trip
- [x] `make test` — must pass before Task 2

### Task 2: Refactor cmdbrowser tree onto the engine (Step B)

**Files:**
- Modify: `internal/core/ui/cmdbrowser/tree.go`
- Modify: `internal/core/ui/cmdbrowser/tree_render.go`
- Modify: `internal/core/ui/cmdbrowser/actions.go`, `filter.go`, `plugin.go` (call sites)
- Modify: `internal/core/ui/cmdbrowser/tree_test.go`, `tree_render_test.go`

- [x] add a cmdbrowser `Adapter` (`Children` → `n.children`, `Key` → `n.id`, `Expandable`
      → `len(n.children) > 0`); give `treeModel` an `*tree.Engine[*treeNode]`; keep
      `treeNode` as payload (counts/leaves) and keep `build`/`ensureGroup`/
      `recomputeCounts`/`setIncludePrivate`/`itemsForFocus`/`nearestVisibleAncestor`
- [x] route construction (`newTreeModel`) through `engine.SetRoots(root.children)` +
      `RebuildVisible(nil)`; **the constructor's initial-focus write now goes through the
      engine** (`SetCursorByKey(firstVisible.id)`) — no parallel `focusedID` write;
      DELETED engine-owned methods (`moveUp/moveDown/moveHome/moveEnd`, `onLeft`/`onRight`,
      `toggleFocused`, `ensureFocusVisible`, `focusRow`, `rebuildVisible`,
      `clipToViewport`, `indexOfFocused`, `moveBy`) and forwarded callers to the engine
- [x] **cursor mapping** (golden-critical): `focusedNode()` → `e.Cursor()` (nil ⇒ root);
      `focusedID()` reads → `e.Cursor().id` or `""`; ALL `focusedID = …` writes →
      `e.SetCursorByKey(id)` (incl. the `""`/root and `nearestVisibleAncestor`/
      `nearestRestoredAncestor` cases, via new `focusVisible()` for the old `indexOfFocused()<0`
      check); kept `nodesByID`/`root` for `breadcrumb()` "(root)" and `itemsForFocus()`
- [x] **filter expansion via engine** (`filter.go`): `newFilterState(b.tree.expanded,…)` →
      `e.ExpandedSnapshot()`; `applyAutoCollapse` → iterate `nodesByID` calling
      `e.SetExpandedByKey(id, matchCount[id] > 0)` then `e.RebuildVisible(nil)`;
      `restoreExpansion` → `e.RestoreExpanded(saved)` then `e.RebuildVisible(nil)`; the
      `exitFilter`/`commitFilter` `if !focusVisible() { … nearestVisibleAncestor }`
      sequence lands the cursor identically (relies on `RebuildVisible` not re-parking)
- [x] update `tree_render.go` to read `e.VisibleNodes()` and `e.IsExpanded(node)` (replace
      `tm.visible` / `tm.expanded[id]`); kept the external dim/`M/N`-counts render exactly
- [x] adapt `tree_test.go`/`tree_render_test.go` to the engine-backed surface, preserving
      existing behavioral assertions (nav, collapse/expand, counts); clip/ensureFocusVisible
      unit tests now live in the engine package; added `TestBrowser_FilterRoundTripCursorStable`
      asserting filter open→type→exit leaves the cursor on the SAME row as before
- [x] verify `cmdbrowser/plugin_golden_test.go` goldens are **byte-for-byte unchanged**
- [x] `make test` — must pass before Task 3

### Task 3: Refactor docstui tree onto the engine (Step C) ⚠️ RISKY (locale/rebuild + filter)

**Files:**
- Modify: `internal/core/ui/docstui/tree_widget.go`
- Modify: `internal/core/ui/docstui/tree_render.go`, `actions.go` (`.Expanded` reads)
- Modify: `internal/core/ui/docstui/tree_filter.go`-adjacent / `ApplyFilter` path
- Modify: `internal/core/ui/docstui/tree_widget_test.go`, `tree_index_test.go`,
  `tree_render_test.go`, `actions_test.go`, `plugin_test.go`

- [x] add a docstui `Adapter`: `Children` → `node.Children`; **`Key` → kind-prefixed**
      (`group\x00<root>` / `node\x00<root>\x00<path>` / `heading\x00<root>\x00<path>\x00<idx>`,
      idx = heading sibling position — Decision 2 / Codex #2); **`Expandable` → true for any
      non-heading directory PLUS files with heading rows** (Codex #4 — NOT "dir-with-children";
      empty/index-only dirs must stay Enter/Toggle-able as today, `actions.go:275` +
      `tree_widget.go:537`), false for heading rows. Give `TreeWidget` an
      `*tree.Engine[*TreeNode]`
- [x] **DELETE the `Expanded` field** on `TreeNode`; route every read/write to the engine:
      `tree_widget.go:316` (walk) → engine visible walk; `:406` (`expandAncestors`) →
      `e.SetExpanded(parent,true)`; `:502-503`,`:524-525` (Collapse/Expand) → engine;
      `:547` (Toggle) → `e.Toggle`; `tree_render.go:89,97` + `actions.go:284` →
      `e.IsExpanded(node)`; **`tree_render.go:76,126`** (`node == tw.cursor`) → `e.Cursor()`
- [x] **seed default-expanded multi-root group nodes**: today `groupTreeNode` is created
      `Expanded: true` (`tree_widget.go:240`); with an empty engine map those groups would
      render collapsed. In `NewTreeWidget` (initial build only, NOT `Rebuild`) call
      `e.SetExpanded(groupNode, true)` for each group when `len(tw.roots) > 1`, so the map
      then persists user collapse/expand across locale `Rebuild`. **Ordering matters**: seed
      group expansion **after `SetRoots` but BEFORE the first `RebuildVisible(nil)`**, then
      `ParkCursorIfHidden()` — so the initial visible set + first-topic load are correct
- [x] rewrite `Rebuild(locale)` to rebuild the node graph then call `e.SetRoots(newRoots)`;
      DELETE the manual `findByPath` transfer but **preserve the two-tier cursor fallback**
      (Codex #1): after `SetRoots`, `e.SetCursorByKey(prevHeadingKey)` → on false
      `e.SetCursorByKey(prevParentFileKey)` → on false `e.ParkCursorIfHidden()` (a vanished
      heading must fall back to its parent file, NOT first-visible, per old `:166-174`); keep
      graph-construction (`rebuild`/`addNodeAsChild`/projection/index-folding/headings); do
      **not** re-seed group expansion here (preserve user collapse — keys are stable so the
      engine map carries it across `SetRoots`)
- [x] replace `markFilterMatches`/`emitFiltered` with the keep-predicate path, **guarding the
      empty-query branch** (Codex #3): call `e.RebuildVisible(nil)` when the filter is nil,
      inactive, OR active-with-empty-query (mirrors today's `filter.Active && Query != ""`
      gate at `tree_widget.go:300` — `Matches("")` returns true, so a naive keep would emit
      the whole tree); use `keep = func(n) bool { return tw.filter.Matches(nodeLabel(n)) }`
      ONLY for a non-empty active query. Follow every rebuild with `e.ParkCursorIfHidden()`.
      Keep `TreeFilter`, `/`-UX, `ApplyFilter`, ancestor-expand-on-commit
- [x] delegate `MoveUp/MoveDown/MoveStart/MoveEnd/Collapse/Expand/Toggle/Cursor/SetCursor/
      VisibleNodes` to the engine; DELETE the now-engine-owned local copies +
      `topIdx`/`ensureFocusVisible`/`focusRow`/`clipToViewport` (use engine)
- [x] adapt docs tree tests to `e.IsExpanded`; **add a test asserting expansion + cursor
      survive a locale `Rebuild`** (the intended bugfix); **multi-root initial-render test:
      group nodes expanded by default**; **heading-vanish test: cursor on a heading that
      disappears after `Rebuild` falls back to the PARENT FILE, not first-visible** (Codex #1);
      **empty-query filter test: opening `/` with no query keeps the expansion-respecting
      tree, NOT a fully-expanded one** (Codex #3); **empty/index-only directory stays
      Enter/Toggle-able** (Codex #4); preserve heading/index-folding/multi-root coverage
- [x] verify `docstui/plugin_golden_test.go` goldens are **byte-for-byte unchanged**
- [x] `make test` — must pass before Task 4

### Task 4: Update internals documentation (Step D)

**Files:**
- Modify: `docs/internals/packages.md`
- Modify: `docs/internals/tui-keymap.md` (only if keymap prose references the trees)
- Modify: `CLAUDE.md`/`AGENTS.md` (only if a load-bearing contract emerged)

- [x] add a `tui/tree` section to `packages.md`: engine contract (generics + 3-method
      adapter), the **"rendering NOT in the engine"** boundary (Decision 7), expansion
      **keyed by stable `Key`** (Decision 3/8), and the **keep-predicate-not-filter** hook
      (Decision 6); note both consumers are thin wrappers
- [x] update the docstui/cmdbrowser package notes in `packages.md` to reference the shared
      engine instead of the "mirror cmdbrowser exactly" duplication; mention the docs
      expansion-across-locale bugfix
- [x] add a Critical Patterns line to `CLAUDE.md`/`AGENTS.md` **only if** "rendering-not-in-
      engine / expansion-by-Key" is judged load-bearing — keep it tight; edit `AGENTS.md`
      (canonical), never the `CLAUDE.md` symlink (extended the existing `tui.Plugin`
      framework pattern with the engine contracts; `tui-keymap.md` unchanged — keymaps are
      identical, this is a behavior-preserving refactor)
- [x] `make build` (re-sync embedded docs) + `make test` (docs-subsystem tests) — must pass
- [x] (no separate code tests — documentation task; the build/test run is the gate)

### Task 5: Verify acceptance criteria

- [x] generic `tui/tree` engine exists; both `cmdbrowser.treeModel` and
      `docstui.TreeWidget` are thin wrappers delegating nav/scroll/expansion/collapse/expand
      to it; no duplicated engine logic remains (grep for deleted method names — clean: no
      `ensureFocusVisible`/`clipToViewport`/`indexOfFocused`/`moveBy`/`rebuildVisible` defs
      remain in cmdbrowser/docstui)
- [x] both browsers behave identically — nav, collapse/expand (directional `h`/`l`),
      scroll/clip, filter (cmdbrowser dim+counts; docs reduce-set), headings/index-folding/
      multi-root, counts (manual smoke-test deferred to Post-Completion; verified here by
      unit + golden tests — all pass)
- [x] **both browsers' golden frame tests are byte-for-byte unchanged** at buckets
      60/79/80/99/100 (`git diff 09caf5e4..HEAD -- '**/testdata/**golden*'` empty across the
      Plan 2 refactor commits)
- [x] cmdbrowser filter open→type→exit/commit lands the cursor on the identical row
      (`RebuildVisible` does not re-park; `nearestVisibleAncestor` flow intact —
      `TestBrowser_FilterRoundTripCursorStable` + `TestBrowser_FilterEnterCommitsKeepingExpansion`)
- [x] docs multi-root group nodes render expanded by default (seeding works —
      `TestMultiRootGroupsExpandedByDefault`)
- [x] docs expansion + cursor survive a locale `Rebuild` (intended bugfix test passes —
      `TestRebuildPreservesExpansionAndCursor`)
- [x] docs locale `Rebuild` on a vanished heading falls back to the parent file, not
      first-visible (`TestRebuildHeadingVanishFallsBackToParentFile`); opening `/` with an
      empty query does not fully expand the tree (`TestEmptyQueryFilterRespectsExpansion`)
- [x] docs `Key` scheme is collision-free (group/node/heading kind-prefixed) and stable
      across locale change (engine `SetRoots` key re-resolution tests +
      `TestSetRootsPreservesExpansionAndCursorByKey`)
- [x] the engine has zero filter/query/count concepts (grep `tui/tree` for those terms —
      only explanatory comments naming what it does NOT do); rendering lives only in the
      consumers
- [x] run full suite: `make build` then `make test` — pass (cmdbrowser/docstui/tui/tree all ok)
- [x] run `make lint` — clean (0 issues)

### Task 6: Finalize documentation + archive plan

- [x] confirm `docs/internals/packages.md` (+ `tui-keymap.md` if touched) reflect the final
      shape — `packages.md` carries the full `tui/tree` engine section plus updated
      cmdbrowser/docstui thin-wrapper notes (Task 4); `tui-keymap.md` intentionally untouched
      (behavior-preserving refactor, identical keymaps)
- [x] confirm Stage 4 is fully complete (Plan 1 archived + this Plan 2) and note Plan 2
      done in any Stage-4 tracking — Plan 1 archived at
      `docs/plans/completed/20260629-tui-stage4-docs-plugin-migration.md`; the milestone spec
      is pre-planning (not a live tracker) and the plans are self-tracking, so archiving this
      Plan 2 IS the Stage-4 completion record
- [x] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed` first)

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only.*

**Manual verification:**
- Smoke-test `dwe commands` (cmdbrowser): tree nav, expand/collapse with `h`/`l`/`←`/`→`,
  filter `/` (dim + `M/N` counts), include-private toggle, list panel still tracks focus,
  mouse wheel + click on tree rows.
- Smoke-test `dwe docs` (docstui): tree nav, expand/collapse, headings as sub-rows,
  `index.md` folding, multi-root groups, filter `/` (reduced set), **locale cycle `L`/`e`
  preserves expansion** (the bugfix), diagram open/copy, mouse wheel + click.
- Confirm both at a few terminal widths incl. the narrow `<40` "too small" path.

**Follow-up (separate work):**
- Stage 5 (statustui → lipgloss v2 → `Frame`) remains independent and unaffected.
- A future third tree consumer (e.g. status tabs) can adopt `tui/tree` directly.
