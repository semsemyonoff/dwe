# `dwe vars` — working with the `vars:` sandbox

`vars:` is the single formalized home for free-form, project-specific values in
the merged 3-layer config (see [`workspace.md` → Strict root + the `vars:`
sandbox](workspace.md#strict-root--the-vars-sandbox)). Because every custom
value now lives under one namespace, `dwe vars` is a first-class tool to
enumerate, read, edit, and trace those values.

## Contents

- [Data model](#data-model)
- [Subcommands](#subcommands)
  - [`dwe vars list`](#dwe-vars-list)
  - [`dwe vars get`](#dwe-vars-get)
  - [`dwe vars inspect`](#dwe-vars-inspect)
  - [`dwe vars set`](#dwe-vars-set)
  - [`dwe vars` (no args) — TUI browser](#dwe-vars-no-args--tui-browser)
- [Encrypted values](#encrypted-values)
- [Output is not redacted](#output-is-not-redacted)
- [Comment-preserving `local.yml` writes](#comment-preserving-localyml-writes)
- [The static usage scan](#the-static-usage-scan)
- [JSON output](#json-output)
- [Container behavior and `bridge.vars_writable`](#container-behavior-and-bridgevars_writable)
- [Related commands](#related-commands)

## Data model

A **var** is a leaf dot-path under `vars:` — e.g. `vars.db.password`. Nested
maps are **namespaces** (tree nodes), not vars. Given:

```yaml
vars:
  db:
    user: root
    password: secret
  app:
    timeout: 30
```

the leaves (vars) are `vars.db.user`, `vars.db.password`, and `vars.app.timeout`;
`vars.db` and `vars.app` are namespaces.

Every var is resolved across the three config layers:

| Layer | Source | Meaning |
|-------|--------|---------|
| **default** | `workspace.yml` / `defaults.yml` | The tracked, team-shared default |
| **local** | `workspace/local.yml` | The gitignored, per-developer override |
| **current** | post-merge | What `${vars.x}` actually resolves to (local wins) |

The **origin** of a var is the highest layer that yields a value — the file that
"wins" the merge for that path.

> **The `vars.` prefix is optional.** Under `dwe vars` every path is a var, so
> `get`, `set`, `inspect`, and the `list` namespace filter all accept the
> shorthand: `dwe vars get db.host` is identical to `dwe vars get vars.db.host`.
> The prefix is also stripped from display output (it stays canonical in JSON,
> completion, and storage). A non-`vars` head is normalized into the sandbox
> (`project.name` → `vars.project.name`), never resolved against real project
> config — so the read confinement and container-write allowlist still hold.

## Subcommands

### `dwe vars list`

```
dwe vars list [namespace]
```

Flat list of every `vars.*` leaf with its current value and a layer badge
(`local` when a `local.yml` override is in effect, otherwise `default`). An
optional `namespace` argument filters to a subtree (e.g. `dwe vars list db`),
mirroring `dwe commands list`.

### `dwe vars get`

```
dwe vars get <var>
```

Print a single value. A leaf path prints the scalar; a namespace path
(`vars.db`) prints the whole subtree as YAML. The value shown is the
**current** (post-merge) value. A path that resolves to nothing is a typed
`vars_not_found` error. Reads are **confined to `vars.*`** — a path whose first
segment is not `vars` (e.g. `project.name`) is reported as `vars_not_found`
rather than resolved against the rest of the project config, so the
container-reachable `vars` surface cannot leak arbitrary host config.

### `dwe vars inspect`

```
dwe vars inspect <var>
```

The full picture for one var:

- **Per-layer values** — default (team-shared), local override, current (post-merge).
- **Origin** — the project-relative file that wins the merge.
- **Every static usage** — each place the var is referenced, as `file:line`
  with the matched line text. See [the static usage scan](#the-static-usage-scan).

A var that resolves nowhere *and* has no usages is `vars_not_found`. Inspect
matches both an exact path and a namespace prefix: `dwe vars inspect db`
surfaces usages of `vars.db.host`, `vars.db.user`, etc. Like `get`, inspection
is **confined to `vars.*`** — a non-`vars` path is `vars_not_found`.

### `dwe vars set`

```
dwe vars set <var> [value]
```

Write a var override into `workspace/local.yml`, preserving surrounding comments
and formatting (see [comment-preserving writes](#comment-preserving-localyml-writes)).

- **Path-confined to `vars.*`** — any `<var>` whose first segment is not `vars`
  is rejected with `vars_path_invalid`. This is also the container trust
  boundary (see [container behavior](#container-behavior-and-bridgevars_writable)).
- **Value coercion** — the `value` argument is parsed as a single YAML scalar.
  Typed scalars become typed: `true` / `false` → bool, `42` → int, `1.5` → float,
  bare text → string. **Explicitly quoting** keeps it a string: `set x '"42"'`
  writes the string `"42"`. Maps (`{a: b}`) and sequences (`[a]`) are rejected —
  a var is a leaf. Pinned ambiguous cases:

  | Input | Result |
  |-------|--------|
  | `""` (empty arg, shell-stripped) | YAML null |
  | `'""'` (quoted empty literal) | empty string |
  | `null`, `~` | YAML null |
  | `yes` / `no` / `on` / `off` | string (not a YAML-1.1 bool) |
  | `0755`, `01` (leading zero) | string (no lossy octal reinterpretation) |
  | `1.2.3` | string |
  | `2024-01-02` (bare timestamp) | string |

- **No `value` (interactive)** — opens a `huh` form with one input field and
  inspect-style context (the current per-layer values). Submit writes through
  the same path. In JSON / non-interactive mode, omitting `value` is a typed
  `vars_value_required` error (no form is opened).

`set` acquires the project locks (symmetry with `dwe services enable/disable`,
which share the same `local.yml` writer — a lock-free `set` could race a
lock-holding toggle on the same file). It runs **no preflight** (it is not a
lifecycle/stack mutation). On any post-write failure the previous `local.yml`
bytes are restored, then the config is reloaded so subsequent output reflects
the write.

### `dwe vars` (no args) — TUI browser

Running `dwe vars` with no subcommand opens an interactive browser (the same
widget as `dwe commands`): a namespace tree of all vars, an inspect overlay, and
an **edit** action (Enter on a leaf).

**Edit-and-stay (≥ 80-column terminals).** Enter opens the `set` form **as an
overlay over the browser** — the tree stays visible (dimmed) beneath it. Type the
new value (invalid input — a map, a sequence, an uncoercible scalar — is rejected
inline), press Enter to save, and the overlay closes: the edited row refreshes in
place (new value + layer badge), the inspect overlay reflects the new value, and
the status line flashes a `✓ <path> = <value>` confirmation for ~2 seconds. `esc`
cancels back to the browser with its state (cursor, expansion, filter) intact;
`ctrl+c` quits the whole browser. If the project locks are held (e.g. a paused
`dwe deploy run`), the save fails and the status line flashes the lock-held error
— the browser stays open and `local.yml` is untouched.

Two observable behaviours differ from the standalone `dwe vars set`:

- **Confirmations are a transient status flash, not stdout.** After quitting the
  browser the terminal shows **no** record of the edits — nothing is printed on
  exit. (Use `dwe vars get <path>` or re-open the browser to confirm a value.)
- **Edit-and-stay applies only to the ≥ 80-column overlay path.** On a narrower
  terminal the browser uses a flat fallback selector that **exits** to run the
  `set` form and then re-opens — the old exit-after-commit loop, one edit at a
  time.

In a **non-interactive** context — no TTY, `DWE_NONINTERACTIVE=1`, or running
inside a container — the bare command falls back to `dwe vars list`. A namespace
argument (`dwe vars vars.db`) also lists rather than browsing.

## Encrypted values

A `vars.*` value in a **tracked** layer may be an encrypted `ENC[age:…]` marker
rather than plaintext — that is how a team-shared credential lives in git. The
config loader decrypts markers in memory, so a machine that holds the project
identity sees ordinary plaintext everywhere in `dwe vars` and nothing below
applies. See [`secrets.md`](secrets.md) for the full model.

When a value **cannot** be decrypted here (no identity, the wrong one, or a
damaged payload), `dwe vars` shows `<encrypted>` — never the ciphertext:

- **`list`** renders `<encrypted>` for the value and appends ` (encrypted)` to
  the layer badge. The JSON entry gains `"encrypted": true` (omitted otherwise,
  so output for a project without secrets is byte-identical to before). The
  JSON `layer` field stays clean (`local` / `default`) — the suffix is display,
  `encrypted` is the machine answer.
- **`get`** on a leaf prints `<encrypted>` and exits 0; on a **namespace** it
  prints the subtree with every unresolved marker replaced recursively, in both
  text and JSON.
- **`inspect`** masks *every* per-layer field (default / local / current, text
  and JSON) that holds an unresolved marker, and adds a `secret:` line naming
  the identity source or the reason there is none.
- The **TUI browser** shows the same placeholder and badge, and its edit form
  never offers a marker as an editable-looking current value.

The flag is derived from the **origin** layer, not from "any layer": a
`defaults.yml` marker overridden by a plaintext `local.yml` value renders the
plaintext, and a `local.yml` marker over a plaintext default renders
`<encrypted>`.

### `vars set` vs `secrets set`

| | [`dwe vars set`](#dwe-vars-set) | [`dwe secrets set`](secrets.md#dwe-secrets-set) |
|---|---|---|
| Writes to | `workspace/local.yml` (gitignored) | `workspace/defaults.yml` or `workspace.yml` (tracked) |
| Stored as | plaintext | `ENC[age:…]` marker |
| Coercion | YAML scalar (`true` → bool, `42` → int) | none — always a string |
| `vars.` prefix | optional | required |
| Needs | nothing | the project's recipient |

`vars set` keeps writing plaintext, and a `local.yml` plaintext value **wins**
over an encrypted default. When the path you are setting shadows a marker in a
lower layer, `set` prints one note to stderr (suppressed in JSON mode):

```text
note: vars.telegram.token is an encrypted secret in workspace/defaults.yml; this plaintext override wins locally
```

That is a legitimate thing to do — a developer pointing at their own bot token
— but it is worth knowing you have stopped reading the shared value.

## Output is not redacted

`dwe vars list`, `get`, and `inspect` print effective values **verbatim** —
nothing is masked. Some projects legitimately keep third-party credentials (API
tokens, service DSNs) in `workspace/local.yml` / `.env`, because those cannot be
faked locally; such values appear in full in the output.

Masking is deliberately not offered: it would raise access by exactly zero bits
— anyone who can run `dwe vars list` can read `workspace/local.yml` — and both
files are already gitignored and written `0600`. What it *would* change is where
the values travel, so that is the thing to watch: prefer `dwe vars get <path>`
over a full `list` before pasting into an AI-agent session, a screenshot, or a
demo.

The one placeholder in this output is **not** masking: `<encrypted>` means the
value could not be decrypted on this machine, so there is no plaintext to print.
A decrypted secret prints in full like any other value.

## Comment-preserving `local.yml` writes

`dwe vars set` writes `workspace/local.yml` through a `yaml.Node` round-trip:
load the file as a node tree, patch only the targeted path, and re-serialize.
Comments and key ordering are preserved; only the edited value node changes.
Coercion is honoured at the node level — overwriting a quoted string with
`true`/`42` emits a **bare** scalar so it reloads typed.

Blank lines are **not** preserved: the document is re-encoded, and the YAML
library keeps blank lines only inside comment blocks, so an edit may also
re-indent the file to its own indent step. That is acceptable for `local.yml`,
which is gitignored developer state. `dwe secrets set` / `init` / `rekey` write
tracked layer files and therefore use a different, line-splicing writer that
does preserve blank lines — see [`secrets.md`](secrets.md#subcommands).

This writer backs **`dwe vars set`**, **`dwe services enable/disable`**, and
the **setup wizard**, so comments in `local.yml` survive every DWE-driven edit.
Map-over-scalar collisions are rejected (to avoid silently
discarding developer data), with the one documented exception of the legacy
bare-int port leaf being upgraded to a `{port: N}` map.

## The static usage scan

`inspect` (and the browser's inspect overlay) reports every place a var is
statically referenced. The scan is **field-aware**, not a blanket file grep — it
walks each config file and only inspects the fields the runtime actually
renders, so it avoids false positives (comments, quoted literals, non-rendered
fields) and false negatives (references in structural keys). Two reference
syntaxes are tracked:

1. **`${vars.x}` template references** — in fields rendered via the `${...}`
   engine (declarative command `cmd` / `argv` / `compose_args` /
   `argv_append_from` / `env` / `with`, pipeline step `timeout` /
   `files_gate.command`, `info.yml` `text` /
   `value`, scalar `when:`, `docker.yml` `project_name`, confirm prompts) and in
   config render templates under `workspace/templates/config/**` (arbitrary text
   → line scan for both `${vars.x}` and `{{ resolve .Raw "vars.x" }}`). These are
   the templates the config render subsystem materializes; the sibling ide / ai /
   git packs use the raw `{{ }}` substrate (no `${...}` resolution) and are not
   scanned. Internal whitespace (`${ vars.x }`) and a leading digit do not match
   — the scanner reuses the renderer's own pattern.
2. **Structural `vars.x` dot-paths** — the values of `from:` / `default_from:`,
   and references inside a typed `when.expr` (not a bare `when: vars.x` scalar).

Matching is by exact path **or** namespace prefix: `${vars.db.host}` counts
toward both `vars.db.host` and `vars.db`.

The top-level `vars:` block itself is **not** scanned: its values are config
data resolved by dot-path, never re-rendered, so a `${vars.x}` or `from:` that
appears *inside* `vars:` is not a runtime usage.

**Caveat (printed in the output):** dynamically-built paths and Go-template
field accesses (`.Vars.x` / `.Raw.vars.x` in `{{ ... }}` templates) are **not
tracked**. The scan covers the `${...}` and structural-dot-path forms only.

## JSON output

Every read-only subcommand routes through `--output json` (with `--pretty`) and
keeps stdout clean — typed errors serialize to a `{"error":{…}}` envelope on
stderr.

| Command | Shape |
|---------|-------|
| `get` | `{"var": "...", "value": <any>, "encrypted": <bool>}` |
| `list` | `{"vars": [{"path": "...", "value": <any>, "layer": "local\|default", "encrypted": <bool>}]}` |
| `inspect` | `{"var": "...", "layers": {"default": <any>, "default_set": <bool>, "local": ..., "local_set": ..., "current": ..., "current_set": ...}, "origin": "...", "encrypted": <bool>, "secret": "...", "usages": [{"file": "...", "line": N, "kind": "...", "text": "..."}]}` |
| `set` (with value) | `{"var": "...", "value": <any>}` |

The `*_set` booleans on `inspect` layers distinguish an explicit `null` value
from an absent layer. `set` with no value in JSON mode is the
`vars_value_required` error (no form).

`encrypted` and `secret` are `omitempty`: a project with no encrypted values
emits exactly the fields it did before — see
[Encrypted values](#encrypted-values).

## Container behavior and `bridge.vars_writable`

`dwe vars` is reachable from inside a bridged container, but writes are
**deny-by-default**:

- `get` / `list` / `inspect` are always reachable.
- `set` is reachable, but a container `set` only succeeds when the target var
  matches an entry in the project's `bridge.vars_writable` allowlist. A
  non-matching var is rejected with `vars_not_container_writable`. From the
  **host**, `set` is unrestricted.
- The TUI is auto-disabled in a container (the non-interactive fallback to
  `list`).

### The `bridge.vars_writable` config block

`bridge.vars_writable` is a **top-level** config block — distinct from the
per-service `services.<name>.bridge:` block in `service.yml`, which controls
container *enablement*. This top-level block is project-wide container-write
**policy**: which vars a containerized `dwe vars set` may write to the host
`local.yml`.

```yaml
# workspace.yml (or any layer — 3-layer merged)
bridge:
  vars_writable:
    - vars.db.password      # exact path
    - vars.app.*            # prefix wildcard (dot-boundary)
```

`bridge` is part of the strict root allowlist; it is merged across the three
layers like any other formalized block. Pattern semantics use a real dot
boundary, **not** naive prefix matching:

- An **exact** pattern (`vars.db.password`) matches only that identical path.
- A **`vars.x.*` wildcard** matches `target` only when `target` begins with
  `base + "."`. So `vars.db.*` allows `vars.db.host` but **denies** `vars.db`,
  `vars.dbx.host`, and `vars.database.host`.

An empty or absent `vars_writable` list means **no container writes** — the safe
default. Malformed patterns fail closed (deny).

**What allowlisting a var actually grants.** Pipeline step fields (`cmd:`,
`when.cmd`, `check.cmd`, `timeout:`) are rendered through the `${...}`
substrate, and a rendered `cmd:` is handed to the **host's** `sh -c` as program
text — substitution is textual, never shell-quoted (see
[Templates in step fields](deploy/index.md)). So a var that is both
container-writable **and** referenced from a pipeline command lets the container
choose part of a host command line: a value of `x; some-command` runs
`some-command` on the host at the next `dwe deploy run`. That is the natural
combination — a container usually wants to write a var precisely because a
pipeline reads it — so treat `vars_writable` as delegating host-shell input to
whoever can reach the container, and keep the list to vars that are consumed as
data (config renders, `exports.env`) rather than spliced into commands.

### `render config` from a container

So a container can regenerate service configs after a `set`, **`dwe render
config`** is also reachable from a container (the other render subcommands —
`env` / `ide` / `ai` / `git` — stay host-only). Because `render config
--harvest` mutates host state (`.dwe/generated.yml`), a containerized `render
config --harvest` is rejected with `render_harvest_host_only` — only the
read-only render crosses the boundary.

## Related commands

- `dwe vars get <var>` — print a var's current value
- `dwe vars list [namespace]` — enumerate `vars.*` leaves
- `dwe vars inspect <var>` — per-layer values, origin, and usages
- `dwe vars set <var> [value]` — write a `local.yml` override (comment-preserving)
- `dwe secrets set <vars.path> [value]` — write an **encrypted** value into a tracked layer ([`secrets.md`](secrets.md))
- `dwe secrets status` — report every encrypted value and whether it can be read here
- `dwe render env` / `dwe render config` — regenerate `.env` / service configs from the merged config
- `dwe services enable` / `disable` — service toggles (comment-preserving)
