# Info dashboard: subgroups + per-item `decorative`

## Overview

Rework the `devbox info` schema so that:

1. **`type: subheader` is removed.** It was only ever a styled label and could not group items or auto-hide.
2. **`type: subgroup` is introduced.** A container item with its own `title`, `items`, `when`, `hide_on_empty` (default `true`), and `decorative` flag. Recurses naturally.
3. **`decorative *bool`** moves onto every item type (`info`, `warning`, `definition`, `separator`, `subgroup`) with type-driven defaults. Replaces the hard-coded "which types count as content" switch in the renderer.
4. The `hide_on_empty` algorithm is unified: same logic runs on a section and on a subgroup, applied recursively.
5. Footer-suppression rule (render footer ⇔ ≥1 section produced output) is preserved unchanged.

**No backward compatibility.** `subheader` is not handled specially anywhere — it is simply not a valid type, and the generic unknown-type validation rejects it like any other typo. Existing in-tree YAML / fixtures / examples get migrated in this change.

## Context (from discovery)

- **Types**: `internal/config/info.go` — `InfoConfig`, `InfoSection`, `InfoItem` (flat struct with `Type string` discriminator), `InfoIndent` (custom UnmarshalYAML). Loader is lenient (`yaml.Unmarshal`, not `KnownFields(true)`).
- **Renderer**: `internal/ui/info.go` — `RenderInfo` walks sections, evaluates `when:` per item via `tpl.EvalCondition`, hard-codes the content set (`definition|warning|info|subheader`), and applies `HideOnEmpty` at section level. Footer suppression at lines 70-73.
- **Item dispatch**: `renderInfoItem` (switch on `item.Type`). Unknown types silently return empty string today — we're tightening this to a load-time error instead.
- **Condition evaluator**: `internal/tpl.EvalCondition` (template-based, NOT `internal/condition`). Empty `When` ⇒ truthy.
- **`subheader` usage to migrate (info-config surface only)**:
  - `internal/config/info.go:73-80` (comment + Text-field doc)
  - `internal/config/info_test.go:13,28,34` (fixture YAML uses `type: subheader`)
  - `internal/ui/info.go:29-30, 41, 120-125` (renderer cases + content-detection)
  - `docs/reference/config/info.md` (table row + dedicated section)
- **Stays untouched** (general-purpose, not info-specific):
  - `internal/ui/info.go:84-88` — `RenderSubheader()` public wrapper (used by `internal/pipeline/print.go:24,41` and `internal/command/command_cmd.go:443`).
  - `internal/ui/styles.go:25-26, 73-74, 139` — `styleSubheader`, `StyleSubheader`.
  - `internal/config/styles.go:31` — `Colors.SubHeader` palette key.
  - The subgroup title renders through the same `styleSubheader` to preserve the visual identity called out in the spec.
- **Existing tests**: `internal/config/info_test.go`, `internal/ui/info_test.go`. The latter has a `TestRenderInfo_DecorativeWarningKeepsSectionVisible` case that documents the old "warning without when: always counts as content" rule — that test gets reworked, not just renamed.

## Development Approach

- **Testing approach**: regular (code first, tests in the same task). Each task is self-contained.
- **No backward compatibility.** No deprecation period, no shim. Hard schema break with clear error messages.
- **Complete each task fully before moving to the next.** All unit tests in the affected package must pass before starting the next task.
- **Every task adds or updates tests** covering the change. Tests are a required deliverable, not optional.
- **Skills to apply**:
  - `golang-naming` — exported identifiers (`InfoSubgroup` accessors, `effectiveDecorative`, `renderBlock`) follow Go conventions; receiver names short.
  - `golang-error-handling` — load-time validation errors include section ID / item index for locality; wrap with `%w` where chaining a parser error.
  - `golang-safety` — `Decorative *bool` is nil-prone; accessor `IsDecorative()` returns the resolved boolean and is the only path callers use.
  - `golang-testing` — table-driven tests for both validation and rendering, fixtures in package-local `testdata/` where YAML is needed.
  - `golang-code-style` — formatting via gofmt/goimports; comments only where the *why* is non-obvious.
  - `golang-modernize` — use `min`/`max`/`slices` where the package already uses them; no new utility helpers unless reused.
- **CRITICAL: update this plan file when scope changes during implementation.**

## Testing Strategy

- **Unit tests** for every task. Go is the only test layer here; there are no e2e/UI tests in this repo.
- **Renderer tests** in `internal/ui/info_test.go` — table-driven on `(sectionConfig, expectedRendered, expectedSectionPresent)` triples.
- **Loader/validation tests** in `internal/config/info_test.go` — explicit cases for: `subheader` rejection, subgroup-without-items rejection, decorative pointer round-trip (nil / true / false).
- Re-run `make test` before declaring the plan complete.

## Progress Tracking

- Mark completed items with `[x]` immediately when done — not batched.
- Use `➕` prefix for newly discovered tasks, `⚠️` for blockers.
- If the design pivots (e.g. we end up needing a tagged-union after all), update this plan in the same commit.

## What Goes Where

- **Implementation Steps** (checkboxes): everything in this repo — config types, loader validation, renderer rewrite, tests, docs.
- **Post-Completion** (no checkboxes): manual smoke run of `devbox info` against a real project; verify visual parity for the subgroup title (former subheader styling).

## Implementation Steps

### Task 1: Extend `InfoItem` and `InfoSection` with subgroup + decorative fields

- [x] in `internal/config/info.go`, add fields to `InfoItem`:
  - `Title string \`yaml:"title,omitempty"\`` (subgroup header text — mirrors `InfoSection.Title`; supports template expressions; optional, may be empty for a title-less subgroup)
  - `Items []InfoItem \`yaml:"items,omitempty"\`` (subgroup children)
  - `HideOnEmpty *bool \`yaml:"hide_on_empty,omitempty"\`` (pointer for subgroup; default `true` when the item is a subgroup)
  - `Decorative *bool \`yaml:"decorative,omitempty"\``
- [x] add accessor `IsDecorative() bool` on `InfoItem` that resolves nil → type default (`separator` → true; everything else → false). Single source of truth — renderer never inspects `Decorative` directly.
- [x] add accessor `SubgroupHideOnEmpty() bool` on `InfoItem` returning `*HideOnEmpty` when set, else `true` (subgroup default).
- [x] update godoc on `InfoItem` to describe the five live types (`info`, `warning`, `definition`, `separator`, `subgroup`) and the `decorative` semantics. Document that `title:` is the subgroup header field (mirroring `InfoSection.Title`) and that `text:` is NOT used for subgroup headers. Remove the `subheader` line.
- [x] write tests in `internal/config/info_test.go`:
  - `IsDecorative` returns the type-default when `Decorative == nil`, for every type.
  - `IsDecorative` honors an explicit override in both directions (`decorative: true` on `info`, `decorative: false` on `separator`).
  - `SubgroupHideOnEmpty` returns `true` when nil, returns the explicit value otherwise.
  - YAML round-trip: a `type: subgroup` with `title:`, `items:`, `hide_on_empty:`, `decorative:` parses into the expected `InfoItem` field values (no `text:` involved).
- [x] run `go test ./internal/config/...` — must pass before next task.

### Task 2: Loader validation — reject unknown types, require `subgroup.items`

- [x] in `internal/config/info.go`, add a post-decode validation pass over all sections and (recursively) all subgroup children. Walk in declaration order so error messages can reference `section[<id|index>].items[<index>]` paths.
- [x] reject any `type` not in the valid set (`info|warning|definition|separator|subgroup`) with a single, generic error that lists the valid types. **No special-case branch for `subheader`** — it falls into the generic unknown-type path like any other typo. Removing it from the type set is the only "migration" needed; the loader doesn't owe users a custom hint.
- [x] reject `type: subgroup` with empty `items` with: `subgroup must declare items`.
- [x] keep `LoadInfoConfig` using lenient `yaml.Unmarshal` (matches the rest of the info/styles/docker loaders) — strictness here is via the explicit validator, not `KnownFields(true)`.
- [x] in `InfoIndent.UnmarshalYAML`, no change needed (negative-indent rejection is already enforced).
- [x] write tests in `internal/config/info_test.go`:
  - YAML containing `type: subgroup` with no `items:` returns the "must declare items" error.
  - YAML containing an unknown `type` (use `made_up_type` as the canonical case; add `subheader` as a parameterized sub-case in the same table-driven test, NOT as a dedicated test function) is rejected with the valid-types list.
  - YAML containing `type: subgroup` with a nested unknown type is also rejected (recursion through subgroup children).
  - Valid YAML (subgroup with items, decorative override, recursion) round-trips cleanly.
- [x] migrate the existing `info_test.go` fixture (lines 13/28/34) to use `subgroup` instead of `subheader` so the loader happy-path test still parses.
- [x] run `go test ./internal/config/...` — must pass before next task.

### Task 3: Rewrite `RenderInfo` around a recursive `renderBlock`

- [ ] in `internal/ui/info.go`, extract the section-loop body into a helper with this exact signature (matches the Technical Details block below — do not drift):

  ```go
  func renderBlock(
      cfg *config.DevboxConfig,
      items []config.InfoItem,
      hideOnEmpty bool,
      title string,
      asSection bool, // true → renderSectionTitle(title); false → styleSubheader.Render(title)
  ) (out string, rendered bool, err error)
  ```

  - **Return semantics — locked**:
    - `rendered == true` ⇔ `out != ""` (produced non-empty output bytes). This is a hard biconditional, enforced by a final post-render check: if `out` ends up empty for any reason (title is empty AND no survivors), return `("", false, nil)` regardless of `hideOnEmpty`. A block that produces literally zero bytes is indistinguishable from a hidden block; conflating them simplifies the parent's logic and prevents "ghost contributors".
    - `rendered == true` does NOT mean "this block counts as content for the parent's `hide_on_empty` check". The parent decides content-vs-decorative for each child independently via `child.IsDecorative()`. For a subgroup child, the parent counts it as content iff the subgroup `rendered == true` AND the subgroup item's `IsDecorative()` returns false.
    - `decorative: true` on a subgroup is the spec's explicit knob for "never count this subgroup as parent content".
    - Edge case nailed down: title-less subgroup + `hide_on_empty: false` + all children filtered by `when:` → `out` is empty → `rendered=false` → parent ignores it. This is identical to setting `hide_on_empty: true`; "always show" cannot manufacture bytes out of nothing.
  - First pass: walk `items`, evaluating `when:`. For non-subgroup items that pass `when:`, push to survivors. For subgroup items that pass `when:`, recurse — if the recursive call returned `rendered=true`, push the rendered string as a pre-rendered survivor and remember it came from a subgroup.
  - Count `effectiveContent` = number of survivors where the originating `item.IsDecorative() == false`. (Subgroups carry their own `IsDecorative()` — default false — so a non-decorative subgroup that rendered is counted regardless of how many content items it had internally. That's the spec.)
  - If `effectiveContent == 0 && hideOnEmpty` → return `("", false, nil)`.
  - Otherwise build `out` using a `strings.Builder`:
    - If `title != ""`, write `renderSectionTitle(title)` (when `asSection`) or `styleSubheader.Render(title)` (otherwise) then `\n`. The subgroup title is read from `item.Title`, never `item.Text`; if `item.Title` is empty the heading is omitted.
    - For each survivor (in original order): write the survivor's rendered string, then write `\n`. **Always write the trailing `\n` even when the survivor string is empty** — this preserves the existing semantics where `separator` items render via `renderInfoItem` as `""` and become a visible blank line through the per-item newline. Matches the current code at `internal/ui/info.go:58-65`. Using `join(survivors, "\n")` would collapse empty separators and silently break the `decorative: false` separator test (where a separator must keep a section visible).
  - **Final emptiness check** (enforces the `rendered ⇔ out != ""` biconditional): if `out.Len() == 0` after assembly, return `("", false, nil)`. Otherwise return `(out.String(), true, nil)`. This catches the title-less / no-survivors / `hide_on_empty: false` corner. Note: a single non-decorative separator always yields `out == "\n"` (non-empty) → `rendered=true`, so the separator-counts-as-content test still passes.
  - Subgroup title goes through `tpl.Render` against `cfg` before styling (same template treatment as `Text`/`Value` on other items).
  - **Error propagation — required**: `RenderInfo` currently returns template/`when` errors to the caller; this contract must be preserved through the recursion. Every error path is propagated, none swallowed:
    - `tpl.EvalCondition(it.When, cfg)` failure → return `("", false, fmt.Errorf("section %q item %q when: %w", …))`.
    - `tpl.Render(it.Title, cfg)` failure on a subgroup → return wrapped error referencing the subgroup's location (parent path + item index).
    - Recursive `renderBlock(...)` call for a subgroup → if it returns a non-nil error, return immediately, propagating the wrapped error up. **Do not discard** the third return value (no blank-identifier on the error).
    - `renderInfoItem(cfg, it)` failure → return wrapped error referencing the item's location.
    - Error messages include the path so callers can locate the offending YAML (e.g. `section "tools" > subgroup[0] > item[2]: when: ...`).
- [ ] reshape the main `RenderInfo` loop to call `renderBlock` once per section with `hideOnEmpty=section.HideOnEmpty`. Concatenate non-empty results.
- [ ] remove the hard-coded `case "definition", "warning", "info", "subheader"` content set — content-vs-decorative is now `IsDecorative()`.
- [ ] drop the `case "subheader"` arm from `renderInfoItem`. The remaining arms (`definition`, `warning`, `info`, `separator`) keep their current bodies. Add a `case "subgroup"` arm? **No** — subgroups are handled in `renderBlock` before reaching `renderInfoItem`; if the renderer ever sees one in `renderInfoItem`, it's a bug (return an error rather than silently skipping).
- [ ] remove the `default:` silent-skip in `renderInfoItem` — unknown types are now load errors, so any unknown type reaching the renderer indicates an internal bug. Return an error.
- [ ] keep footer suppression as-is (`sb.Len() > 0` check) — semantics are preserved because a fully-hidden section now contributes zero bytes.
- [ ] **delete or rewrite the two existing tests that encode the silent-skip contract** (they will fail otherwise):
  - `TestRenderInfo_UnknownItemType_Ignored` at `internal/ui/info_test.go:174` — delete. Unknown types can no longer reach the renderer (loader rejects them in Task 2). Validation coverage moves to the loader test added in Task 2.
  - `TestRenderInfo_HideOnEmpty_UnknownTypeOnly` at `internal/ui/info_test.go:541` — delete for the same reason; the loader-rejection test in Task 2 already covers the failure mode (a typo in `type:` is now a load error, not a hidden section).
- [ ] also rework the existing `TestRenderInfo_DecorativeWarningKeepsSectionVisible` to reflect the new "decorative is explicit, not type-implied" rule (rename for clarity; keep the test direction inverted — bare `warning` still counts as content because its `decorative` default is `false`).
- [ ] write new tests in `internal/ui/info_test.go` covering the contract changes:
  - `decorative: true` on a `warning` + `hide_on_empty: true` on its section, when it's the only survivor → section is fully hidden.
  - `decorative: false` on `separator` → separator counts as content AND section renders. Specifically: section with `hide_on_empty: true`, no title, and a single `decorative: false` separator → `out` is non-empty (the trailing `\n` from the per-item newline write), `rendered=true`, section appears in the final output. This test would fail if anyone ever refactored the assembly loop to `strings.Join(survivors, "\n")` — guard the per-item newline contract.
  - Subgroup with all items filtered out by `when:` AND `hide_on_empty` default (`true`) → subgroup absent from output AND parent section does not count it.
  - Subgroup with `hide_on_empty: false`, a non-empty `title:`, and zero surviving items + `decorative` default (`false`) → subgroup renders (title only) AND parent counts it as content. Pair test with `decorative: true` on the same subgroup → still renders (title only), but parent does NOT count it.
  - **Title-less subgroup edge case**: subgroup with empty `title:`, `hide_on_empty: false`, and all children filtered by `when:` → subgroup produces empty output → `rendered=false` → subgroup absent from parent's output AND parent does NOT count it. This pins the `rendered ⇔ out != ""` biconditional and prevents regression to "ghost contributor" behavior. Pair with a sanity test: same subgroup but with one surviving content item → renders (no title, body only) and parent counts it.
  - Subgroup with at least one surviving content item → subgroup rendered; parent counts it as content.
  - Nested subgroup: inner subgroup empty → outer counts no contribution from it; inner has content → outer renders; section renders.
  - Footer still suppressed when every section ends up hidden; rendered when at least one survives.
  - **Error propagation tests** (guard the contract that template/`when` errors surface to the caller):
    - bad template in subgroup `title:` → `RenderInfo` returns a non-nil error mentioning the subgroup location.
    - bad expression in a nested item's `when:` (item lives inside a subgroup, ideally two levels deep) → error returned and includes the path through the subgroup.
    - bad template in a nested item's `value:` (definition inside subgroup) → error returned with item path.
    - bad template in a nested item's `text:` (info/warning inside subgroup) → error returned with item path.
    - Each test asserts both `err != nil` AND that `err.Error()` contains the section ID / item-type / path hint, so we catch silent swallowing if it ever regresses.
- [ ] run `go test ./internal/ui/...` — must pass before next task.

### Task 4: Update docs — `docs/reference/config/info.md`

- [ ] remove the `subheader` row from the "Item types" table and the dedicated `### Subheader` section.
- [ ] add a `### Subgroup` section documenting: `title` (optional; mirrors `section.title`; supports templates; the field is `title:`, **not** `text:`), `when`, `items` (required, non-empty), `hide_on_empty` (default `true`), `decorative` (default `false`), recursion, and a Tools/Services example whose YAML uses `title:` for the subgroup header.
- [ ] add a `### Decorative items` (or "Default `decorative` by type") subsection with a table:

  | Type | Default `decorative` |
  | --- | --- |
  | `info` | `false` |
  | `warning` | `false` |
  | `definition` | `false` |
  | `separator` | `true` |
  | `subgroup` | `false` |

  Note that override is bidirectional (`decorative: true` makes a content item not count; `decorative: false` makes a separator count).
- [ ] in "Section fields", cross-reference: subgroup's `hide_on_empty` defaults to `true` (opposite of section's `false`).
- [ ] in "Common pitfalls":
  - remove the bullet that says "items without `when:` always count as content" — superseded by `decorative`.
  - **Do NOT add a `subheader removed` migration note.** The type is simply gone from the valid-types list; users discover it through the generic unknown-type error like any typo. Calling it out in docs perpetuates special handling we've intentionally removed.
- [ ] in the "Example: full info.yml" section, replace any `type: subheader` usage with a `subgroup` (e.g. the `Tools` block becomes a subgroup of definitions).
- [ ] grep `docs/` for stray references to `subheader` *as an info item type* and update; leave general-purpose references (styles palette key) intact.
- [ ] no checkbox for "verify docs build" — repo has no doc build step today.

### Task 5: Migrate in-tree fixtures and the canonical `info.yml` example

- [ ] grep the repo for any YAML using `type: subheader` outside of test fixtures already touched in Task 2:
  - `git grep -n -E 'type:[[:space:]]*subheader'` (POSIX character class works regardless of git grep's regex flavor; `\s` is not portable in default git grep mode).
- [ ] for each hit, decide: convert to `type: subgroup` with the same title, or fold into the parent section title. Update accordingly.
- [ ] if there's a canonical/example `devbox/info.yml` shipped or referenced from docs, migrate it too.
- [ ] run `make test` — repo-wide tests must pass before next task.

### Task 6: Verify acceptance criteria

- [ ] all seven test cases from the spec are covered:
  1. content-type item with `decorative: true` is the only survivor → section hidden.
  2. `decorative: false` on `separator` makes it count as content.
  3. subgroup with all items filtered by `when:` → subgroup absent; parent doesn't count it.
  4. subgroup with surviving items → rendered; parent counts it.
  5. Nested subgroup → recursion + empty-propagation correct.
  6. Loader rejects unknown types (`subheader`, `made_up_type`, etc.) via the generic unknown-type error — no special-cased branch for `subheader`.
  7. Loader rejects `type: subgroup` with no `items`.
- [ ] `make test` — all green.
- [ ] `make lint` — all green; no new lint waivers.
- [ ] `go vet ./...` — clean.

### Task 7: Final — docs sync check

- [ ] re-read `docs/reference/config/info.md` end-to-end for consistency after edits.
- [ ] **update `AGENTS.md` (canonical; `CLAUDE.md` is a symlink — do NOT edit it directly).** The current `InfoSection` paragraph mentions `HideOnEmpty bool`; revise it to reflect the new shape:
  - `InfoItem` now carries `Title string` + `Items []InfoItem` + `HideOnEmpty *bool` (subgroup) + `Decorative *bool` (all types) with accessors `IsDecorative()` and `SubgroupHideOnEmpty()`.
  - Valid types are `info|warning|definition|separator|subgroup` — `subheader` removed.
  - Loader validates types and rejects empty-`items` subgroups.
- [ ] verify `CLAUDE.md` still points at `AGENTS.md` (`readlink CLAUDE.md`) — sanity check that the symlink wasn't broken by editor tooling.

*ralphex automatically moves completed plans to `docs/plans/completed/`.*

## Technical Details

**`InfoItem` after Task 1** (flat struct, new fields are additive):

```go
type InfoItem struct {
    Type        string     `yaml:"type"`
    Text        string     `yaml:"text,omitempty"`
    Name        string     `yaml:"name,omitempty"`
    Value       string     `yaml:"value,omitempty"`
    Indent      InfoIndent `yaml:"indent,omitempty"`
    Icon        string     `yaml:"icon,omitempty"`
    When        string     `yaml:"when,omitempty"`

    // Subgroup-only:
    Title       string     `yaml:"title,omitempty"`
    Items       []InfoItem `yaml:"items,omitempty"`
    HideOnEmpty *bool      `yaml:"hide_on_empty,omitempty"`

    // All types:
    Decorative  *bool      `yaml:"decorative,omitempty"`
}

func (i InfoItem) IsDecorative() bool {
    if i.Decorative != nil {
        return *i.Decorative
    }
    return i.Type == "separator"
}

func (i InfoItem) SubgroupHideOnEmpty() bool {
    if i.HideOnEmpty != nil {
        return *i.HideOnEmpty
    }
    return true
}
```

**Renderer signature change** (Task 3):

```go
// renderBlock renders a section or a subgroup uniformly.
// hideOnEmpty: section.HideOnEmpty for sections, item.SubgroupHideOnEmpty() for subgroups.
// title:      section.Title for sections, item.Title for subgroups (NOT item.Text).
//             Subgroup title goes through tpl.Render before being passed in.
// asSection:  true → renderSectionTitle(title); false → styleSubheader.Render(title).
// Returns:
//   out      — the rendered text (empty iff the block produced nothing).
//   rendered — biconditional with `out != ""`: true iff `out` is non-empty.
//              Parent uses this purely to decide whether to append `out`; it
//              does NOT imply "counts as content" — see locked semantics in
//              Task 3. A title-less block with hide_on_empty:false and no
//              survivors yields ("", false, nil), not ("", true, nil).
//   err      — any template/when/recursion error, fully propagated.
func renderBlock(
    cfg *config.DevboxConfig,
    items []config.InfoItem,
    hideOnEmpty bool,
    title string,
    asSection bool,
) (out string, rendered bool, err error)
```

**Decision (locked)**: subgroup header field is `title:`, matching `InfoSection.Title` and the user's original spec pseudocode. `text:` is reserved for `info`/`warning` message bodies and is unused by `subgroup`. The validator does not enforce "no `text:` on subgroup" — leftover `text:` is silently ignored, same way an `info` item ignores `name:`/`value:` today.

**Algorithm** (matches the spec pseudocode):

```
// renderBlock returns (out, rendered):
//   rendered=true means "this block produced output bytes; parent should append them".
//   rendered=true does NOT imply "parent must count this block as content".
//   Parent counts a rendered child as content iff !child.IsDecorative().
//   For a non-decorative subgroup with hide_on_empty:false and zero content,
//   the subgroup still renders (title only) AND counts as parent content.
//   That's intentional symmetry: decorative:true on the subgroup is the spec'd
//   knob to opt out of being counted by the parent.
renderBlock(items, hideOnEmpty, title, asSection):
    survivors = []           // each entry: (rendered_string, isDecorative bool)
    contentCount = 0
    for idx, it in enumerate(items):
        ok, err = eval(it.when, cfg)
        if err != nil:
            return ("", false, wrap(err, "items[%d] when:", idx))
        if !ok: continue

        if it.type == "subgroup":
            renderedTitle, err = tpl.Render(it.title, cfg)
            if err != nil:
                return ("", false, wrap(err, "items[%d] (subgroup) title:", idx))
            sub, subRendered, err = renderBlock(
                it.items,
                it.SubgroupHideOnEmpty(),
                renderedTitle,
                false,                       // subgroup, not section
            )
            if err != nil:
                return ("", false, wrap(err, "items[%d] (subgroup):", idx))
            if !subRendered: continue
            survivors.append((sub, it.IsDecorative()))
            if !it.IsDecorative(): contentCount++
        else:
            itemOut, err = renderInfoItem(cfg, it)
            if err != nil:
                return ("", false, wrap(err, "items[%d] (%s):", idx, it.type))
            survivors.append((itemOut, it.IsDecorative()))
            if !it.IsDecorative(): contentCount++

    if contentCount == 0 && hideOnEmpty:
        return ("", false, nil)

    var sb strings.Builder
    if title != "":
        head = asSection ? renderSectionTitle(title) : styleSubheader.Render(title)
        sb.WriteString(head)
        sb.WriteByte('\n')
    // Per-item write + trailing '\n', even when the survivor's string is empty.
    // This preserves the separator-renders-as-blank-line semantics that the
    // current RenderInfo loop (info.go:58-65) provides. strings.Join would
    // swallow empty survivors and break the decorative:false separator case.
    for _, s := range survivors.strings:
        sb.WriteString(s)
        sb.WriteByte('\n')

    // Enforce rendered ⇔ out != "".  Handles the title-less / no-survivors /
    // hide_on_empty:false corner where the previous branch fell through
    // even though nothing was actually emitted.
    if sb.Len() == 0:
        return ("", false, nil)

    return (sb.String(), true, nil)
```

Notes:
- Error returns use `("", false, …)`. The empty string + `rendered=false` is the standard "nothing was produced" shape; callers MUST check `err` before inspecting `rendered` (Go convention — but worth restating because the renderer has three return values).
- `rendered ⇔ out != ""` is enforced by two return points (the `hideOnEmpty` short-circuit AND the final emptiness check). Any future refactor that introduces a third early return must preserve this biconditional, or the title-less edge case regresses.
- The assembly loop must write `\n` after **every** survivor — including those whose rendered string is `""` (separators). Switching to `strings.Join(survivors, "\n")` is an attractive refactor that silently breaks the `decorative: false` separator contract. Comment in code, regression test in `internal/ui/info_test.go`.

**Validation error message format**:

```
info: section[<id-or-index>].items[<index>]: unknown type %q; valid types: info, warning, definition, separator, subgroup
info: section[<id-or-index>].items[<index>]: subgroup must declare items
info: section[<id-or-index>].items[<index>].items[<index>]: <recursion path>
```

There are exactly two failure modes: "unknown type" (one branch, no `subheader` special case) and "empty subgroup items". Anything else is a valid value.

## Post-Completion

**Manual verification**:
- run `devbox info` against a real project that exercises sections + subgroups + filtered items and confirm:
  - subgroup titles render in the same bold-yellow style as the old subheader (visual parity).
  - sections with only decorative items collapse as expected.
  - the footer rule still holds end-to-end.

**No external system updates** — this is a single-binary CLI; no consumers depend on the info YAML schema as an API.
