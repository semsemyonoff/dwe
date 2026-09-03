# Secrets: private-key onboarding on a new machine

Plan A of two follow-ups to the encrypted-secrets feature
(`docs/plans/completed/20260902-secrets.md`), driven by the verification run on
the real annotated workspace `../ficbird`. Plan B (`secrets-polish`: YAML
format-preserving writes, `deploy plan` redaction, `validate secrets` OK rows)
is a separate document and is **out of scope here**.

## Overview

A workspace with secrets is cloned to a new machine. The public
`secrets.recipient` is in git; the private identity is not. Today the only
path is knowing `pbpaste | dwe secrets key import` by heart and being at the
project root. `dwe run` fails on `.env` rendering *before* preflight, so the
`secrets.unresolved` gate never even shows; `dwe deploy` shows the gate as a
wall. `secrets status` says `Identity — none` when the real problem is a
truncated `DWE_AGE_KEY`, sending the reader to look for a missing key instead
of a broken one.

This plan adds:

1. **Interactive `secrets key import`** — `--file` → stdin when it is not a
   TTY → a hidden prompt. Immediate in-form validation (parse + recipient
   match), whole-keyfile paste tolerance, and a post-import report of what
   became readable.
2. **One shared "no key → offer to enter it" gate** (`keygate`), wired into the
   `dwe deploy` menu and into `RunRun` (`dwe run` / `dwe restart`). Binary
   choice: enter the key now or abort with the fix instruction. Never shown
   without a TTY, with `--yes`, with `--output json`, or under
   `DWE_NONINTERACTIVE`.
3. **Honest identity diagnostics** — a set-but-unparsable source is reported
   as *invalid*, a foreign key as *wrong recipient*; `secrets status` prints
   the fix hint.
4. **Keyfile housekeeping** — `secrets key list` and `secrets key remove`.
5. Documentation for the new-developer path, the skill, internals, changelog.

The gate runs on the **raw layers before `LoadConfig`**, so the config is
loaded once, already decrypted; there is no reload step and no window where a
wizard proceeds with "still unresolved" state.

## Context (from discovery)

- `internal/shared/secrets/identity.go` — `LoadIdentity(recipient)` order:
  `DWE_AGE_KEY` → `DWE_AGE_KEY_FILE` → `~/.config/dwe/keys/<recipient>.key`;
  first PRESENT source wins, no fall-through (`finishLoad`). On any failure it
  returns `SourceNone`. `ParseIdentity` → `firstKeyLine` takes the first
  non-blank, non-`#` line; a keyfile pasted into a single-line field arrives
  as `# public key: age1… AGE-SECRET-KEY-1…` on ONE line and is skipped as a
  comment. Parse failures wrap `ErrCorrupt` — the same sentinel used for a
  damaged marker. `WriteKeyfile` is `O_EXCL` 0600, dir 0700.
  `LoadAnyIdentity(exclude)` is the sorted `*.key` scan used by half-rekey
  recovery (skips unreadable files silently).
- `internal/core/project/config/secrets.go` — `ReasonNoIdentity`,
  `ReasonWrongIdentity`, `ReasonCorrupt`; `layers.go` `identityReason` /
  `unresolvedReason` map sentinels → reasons.
- `internal/cli/secrets/` — `key.go` `readIdentityText` (`--file` → refuse a
  TTY stdin → `io.ReadAll`), `runKeyImport` (parse → recipient check →
  locks → `WriteKeyfile` → `identity for X stored at Y`); `status.go`
  `identityDisplay` (header: `none (looked at …)` for every failure),
  `identityPayload`; `secrets.go` `identitySet` + `classifyMarker` /
  `classifyBytes`, `collectInventory`, `collectAgeFiles` / `inspectAgeFile`
  (filesystem scan of `workspace/templates/config/**.age` with
  `pathsafe.ContainedRel` + `CheckNoSymlinks` + regular-file `Lstat`);
  `set.go` `promptForSecret` (the `ask.FieldPassword` precedent, seam
  `var runAsk = ask.Run`, non-interactive gate
  `flags.Output == "json" || cmdctx.NonInteractiveEnv() || !widgets.IsInteractiveFn(stdin)`).
- `internal/cli/deploy/menu.go` — `runDeployMenu`: TTY gate at :76, single
  `config.LoadConfigOrWrap` at :86 for the whole menu, `runPreWizardPreflightFn`
  at :227 inside `case menuWizard`. The wizard reloads cfg at :274 after
  writing answers (existing reload precedent, not needed for the gate).
- `internal/core/workflow/lifecycle/run.go` — `RunRun`: `LoadConfigOrWrap`
  :136 → `renderAndSourceDotEnv` :145 (**before** preflight :168 and locks
  :175) → …; `RunContext.Yes` exists; the package already calls
  `widgets.RunConfirm` (git-pull prompt :218), so `core/workflow` → `core/ui`
  has precedent (also `usercommands/runtime/confirmation.go`,
  `execution/builtin/interaction/confirm.go`), despite the aspirational
  `ui-is-sink` rule in `packages.md` § Dependency Rules.
- `internal/shared/envfile/render.go` `checkNotMarker` — the actual refusal
  `dwe run` hits today (`value at … is an undecrypted secret — see 'dwe secrets status'`).
- `internal/core/validate/secrets/secrets.go` — `reasonPhrase`,
  `identityHint(recipient)` (the canonical fix text), `UnresolvedValidator()`
  cherry-pick used by `preflight.Run` and `runPreWizardPreflight`.
- `internal/core/ui/ask/ask.go` — `Run(ctx, title, fields, opts)`; `Validate`
  on a field keeps the huh form open on error (native retry), Esc →
  `widgets.ErrCancelled`. No one-field helper: a one-element `[]Field` is the
  idiom.
- `internal/cli/root.go` `allowedWithoutProject` (:398, doc at :394) — the
  allowlist for commands that run outside a project. It only catches
  `project.ErrNotFound` from discovery (:251-259); an explicit `-c /bad/path`
  stays fatal even for allowlisted commands.
- `internal/core/ui/widgets/confirm.go:38` `RunConfirm(title, affirmative,
  negative string)` — takes no writer; drives `os.Stdin`/`os.Stdout` itself.
- `internal/cli/secrets/secrets.go` `identitySet` also owns `decrypt` (:216),
  `decryptBytes` (:247, used by `get.go:61` and `files.go:188`) and
  `reason()` (:168 — `packages.md:287` pins it as the CLI-side mirror of
  `config.identityReason`; "the two must agree"); `configPackKind` (:61),
  `reasonStaleKey` (:56), `relToRoot` (:451) travel with the move.
- `config.SecretsState.IdentitySource` **already exists**
  (`config/secrets.go:54`, filled at `layers.go:128`, pinned by
  `config/secrets_test.go:75,382`); today it is empty on failure.
- `config.RecipientFromLayers` (`layers.go:166`) returns the value as written;
  validity is the caller's question (`ValidateLayerRoots` runs in
  `LoadLayersWithSecrets`, not in `LoadRawLayers`).
- `internal/cli/docs/agentsmd_test.go` — `agentsMdBudget = 40*1024` (40960 B)
  against `AGENTS.md` at 40882 B: **78 bytes of headroom**;
  `agentsMdMaxLineLen` 600 runes; `TestAgentsMdPointersResolve` requires every
  `§ \`path\`` pointer to match a line-leading `` - `path` `` bullet in `packages.md`.
- `internal/cli/lifecycle/run.go:46` and `restart.go:101` already branch on
  `flags.Output == "json"` — `dwe run --output json` is a real invocation.
- `RunRestart` (`run.go:343`) calls `RunStop` BEFORE `RunRun`;
  `internal/cli/service/service_plan.go:457` reaches `RunRestart` with `Yes: true`.
- `age` parse errors echo input characters (`bech32.Decode`: `invalid
  character data part: s[%d]=%v`; `x25519.go:148`: `unknown type %q`) — any
  message that interpolates them can leak private-key bytes.
- Requirements R1–R8 referenced below are the user's brainstorm list for this
  plan; they are restated inline where cited (there is no separate spec file).
- Render: `internal/core/ui/render/secrets.go` (`SecretsStatus`,
  `secretsField`, tables) + goldens `testdata/secrets_status{,_keyless,_empty}.golden`.
- Tests / seams: `stubStdoutTTY`, `isolateHome`, `initProject`, `writeAgeFile`
  in `internal/cli/secrets/secrets_test.go`; `widgets.IsInteractiveFn`
  overridden inline; `runAsk` stub returning `ask.NewResultForTest(...)`;
  `TestRunPreWizardPreflight_SecretsUnresolvedBlocks` (menu_test.go:320)
  fakes identity purely through `DWE_AGE_KEY` / `HOME`.
- Docs: `docs/reference/config/secrets.md` (sections "Keys: where the identity
  lives", "Getting started", "Subcommands", "Validation and preflight"), ru
  mirror `docs/i18n/ru/reference/config/secrets.md`, `skills/dwe/SKILL.md`
  (:43, :101, :164, :252, :265), `docs/internals/packages.md`, `AGENTS.md`
  "Encrypted secrets" bullet (budget pinned by `TestAgentsMdBudget`).

## Decisions (final — taken in the brainstorm)

- Prompt input is **hidden** (`ask.FieldPassword`). Immediate validation makes
  the "pasted twice and cannot see it" argument moot; `key export` already
  warns about terminal scrollback, a visible key field would contradict it.
- **No attempt limit.** Retry is huh's native in-form behaviour; Esc cancels
  and the command exits with the fix instruction. (Deviates from the literal
  R1.3 wording on purpose.)
- The gate runs **before `LoadConfig`** on raw layers. No reload.
- Entry points this round: `dwe deploy` menu (covers the wizard and the
  menu's own run) and `RunRun` (`dwe run`, `dwe restart`). Direct
  `dwe deploy run` and `dwe render env` keep today's hard error + hint.
- `key list` + `key remove <recipient>`. **No heuristic prune**: a keyfile in
  `~/.config/dwe/keys/` may belong to another project on the same machine, so
  "unused" is not computable; `list` marks only "current project".
- `ParseIdentity` extracts the `AGE-SECRET-KEY-1[a-z0-9]+` token from
  **anywhere** in the text (covers the joined-line paste and every existing
  shape).
- Menu gate placement: at `runDeployMenu` entry, after the TTY check and
  before the menu's single `LoadConfigOrWrap` — one site, no reload, and every
  menu action beyond `plan` needs plaintext anyway.
- **First token wins** in `ParseIdentity`; a second token is ignored, never
  an error (a multi-identity `DWE_AGE_KEY_FILE` and a commented-out old key
  above the live one are both documented `age` shapes that parse today).
- **A present-but-unusable env source is never "fixed" by a prompt.**
  `LoadIdentity` is first-present-source-wins, so with a poisoned
  `DWE_AGE_KEY` / `DWE_AGE_KEY_FILE` a freshly written keyfile would not be
  consulted. `Ensure` explains instead of offering the import.
- **Fixed-state enums, never `age` error text**, on every surface that
  describes an identity source (`key list` states, the `status` header, the
  validator phrase). DWE-authored wording only.
- **Locks**: `Ensure` (inside a project) and `key remove` (when a project is
  resolved) take `lock.AcquireProjectLocks(baseDir)` around the keyfile
  write/delete, same rule and rationale as `key import` (an import or a
  removal racing `rekey`). `Ensure` runs before `RunRun`'s own lock
  acquisition (:175), so there is no self-deadlock.
- **Consulted source lands in Task 5, not Task 1**: changing `LoadIdentity`'s
  error-path `Source` earlier would make `identityDisplay` print a green
  `keyfile (…)` header for a failed lookup between the two tasks.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes; no refactors beyond what a task names
- **CRITICAL: every task MUST include new/updated tests** for code changes in
  that task — success and error scenarios, new cases for new paths, updated
  cases where behaviour changes
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run `make test` (never bare `go test ./...` on a fresh checkout — the
  embedded docs tree is generated); for focused work `make embedded-docs` once,
  then `go test ./internal/...`
- backward compatibility is a hard requirement: a project **without** secrets
  must produce byte-identical output on every touched entry point
  (`dwe run`, `dwe deploy`, `secrets status`); `pbpaste | dwe secrets key import`
  and `--file` keep their existing first text line and existing JSON fields
  unchanged (the readability report line and its two counters are additive);
  every config-load error keeps today's wrapper and code (the gate skips
  itself on a raw-layer error and lets `LoadConfigOrWrap` speak)
- tests never touch `~/.config`: `isolateHome(t)` / `t.Setenv("HOME", …)`,
  identities via `DWE_AGE_KEY` or `DWE_AGE_KEY_FILE`
- any test that must prove "no prompt" installs a stub at the relevant
  seam (`secretsprompt.runAsk`, `cli/secrets.promptIdentityFn`, the
  `Options.Prompt`/`Options.Confirm` hooks, `keygateEnsureFn`,
  `KeygateEnsureFunc`) that **fails the test** if reached, with
  `widgets.IsInteractiveFn` forced to true so the negative is meaningful
- code and config comments in English

## Testing Strategy

- **unit tests**: required for every task (see above)
- **golden tests**: `secrets status` text (three identity-header variants +
  hint), `secrets key list`; regenerate only when the change is intentional
- **negative-substring tests**: every output asserts the absence of
  `AGE-SECRET-KEY-` where the identity must not leak (prompt errors, status,
  list, gate messages)
- **no-prompt tests (R3.2)**: one per entry point, scoped to the flags that
  command actually has — `key import`: non-TTY, `--output json`,
  `DWE_NONINTERACTIVE=1`; deploy menu: `--output json`,
  `DWE_NONINTERACTIVE=1` (non-TTY is already refused at :76); `RunRun` /
  `RunRestart`: non-TTY, `--yes`, `--output json`, `DWE_NONINTERACTIVE=1`,
  nil hooks
- **leak tests** cover coded-error `message`/`hint`/`details`, text stdout,
  stderr and JSON — not only rendered tables — and assert the absence of the
  typed value's last 20 characters, not just the `AGE-SECRET-KEY-` prefix
- secret-bearing tests never use `t.Parallel` where a package-level seam is
  stubbed
- **e2e**: none in this project; the acceptance pass is manual on `../ficbird`

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope

## Solution Overview

```
dwe secrets key import      dwe deploy (menu)          dwe run            dwe restart
  --file | non-TTY stdin      runDeployMenu entry        RunRun entry       RunRestart entry
        │                           │                        │              (BEFORE RunStop)
        │                           └──── keygate.Ensure ────┴──────────────────┘
        │                                   │  raw layers, before LoadConfig;
        │                                   │  decision + keyfile write only
        ▼                                   ▼  (prompt/confirm are injected)
 secretsprompt.PromptIdentity  ◀────── Options.Prompt / Options.Confirm
   one FieldPassword form, in-form validate     (set by cli/deploy and cli/lifecycle)
        │
        ▼
 secrets.WriteKeyfile → keygate.Inventory(identity) → "N markers, M files readable"
```

Two packages, split along the documented `ui-is-sink` rule
(`packages.md` § Dependency Rules: no new `core/** → core/ui/**` import):

- `internal/core/workflow/keygate/` — **decision, scanning, keyfile write**.
  Imports `core/project/config`, `shared/secrets`, `shared/lock`,
  `shared/pathsafe`. No `core/ui`, no `cli/*`. Owns `Inventory`
  (moved from `internal/cli/secrets/secrets.go`: `identitySet`,
  `classifyMarker`/`classifyBytes`, `collectAgeFiles`/`inspectAgeFile`, so
  the gate, `key import` and `status` share one scanner and one
  classification), `HasEncryptedSurface`, `Ensure`, `NonInteractiveEnv`.
  The interactive pieces arrive as two function values in `Options`
  (`Prompt`, `Confirm`); when either is nil the gate behaves as
  non-interactive.
- `internal/core/ui/secretsprompt/` — **the form and the confirmation**,
  importable only from `cli/*` (sink rule). `PromptIdentity` (one
  `ask.FieldPassword`, in-form validation) and `ConfirmImport` (an
  `ask.FieldConfirm` form built with `ask.RunOptions{Input, Output}` so the
  caller's streams are honoured — `widgets.RunConfirm` takes no streams).
  Shared by `key import` (`cli/secrets`), the deploy menu (`cli/deploy`) and
  `cli/lifecycle`, which hands both functions to `RunRun`/`RunRestart`
  through new `RunContext` fields. Cross-sibling CLI imports stay at zero.

The `DWE_NONINTERACTIVE` truthiness set `{"1","true"}` is duplicated in
`keygate.NonInteractiveEnv()` for core callers (`cmdctx` is off-limits);
a test pins it equal to `cmdctx.NonInteractiveEnv()`'s set.

## Technical Details

### `shared/secrets` (Task 1)

```go
// ErrInvalidIdentity: an identity source was PRESENT but its content is not
// an age X25519 identity. Distinct from ErrCorrupt (a damaged payload) and
// from ErrNoIdentity (no source at all) — the three drive three different
// user messages.
var ErrInvalidIdentity = errors.New("invalid identity")

// ParseIdentity accepts: a bare AGE-SECRET-KEY-1… line, a whole keyfile
// (`# public key:` comment + key), age-keygen output, CRLF, surrounding
// whitespace, and a keyfile whose lines were joined by a paste into a
// single-line field. Extraction: the FIRST token matching
// `AGE-SECRET-KEY-1[AC-HJ-NP-Z02-9]{58}` (bech32 upper charset; age uses
// upper-case), anywhere in the text; later tokens are ignored (first wins,
// as today). No match → ErrInvalidIdentity. age refusing the token →
// ErrInvalidIdentity with DWE-authored text ("not a valid age X25519
// identity"); the age error is NOT interpolated — its text echoes input
// characters.
```

`ListKeyfiles() ([]KeyfileInfo, error)` — scan of `KeysDir()` sorted by
**filename**; `KeyfileInfo{Path, Recipient, State}` with `State` ∈
`KeyfileOK`, `KeyfileUnreadable` (I/O error; path only), `KeyfileUnparsable`
(no detail), `KeyfileMisnamed` (parses, but the identity's recipient differs
from the filename stem — listed under the PARSED recipient; `key remove`
targets only the canonical `<recipient>.key`, so a misnamed file is reported
with its path and left for the user). No error text is carried. Missing dir →
empty, nil. `LoadAnyIdentity` stays as is.

`IdentityHint(recipient string) string` and `DisplayKeyfilePath(recipient)
string` move into this leaf from their two unexported copies
(`validate/secrets/secrets.go:240` `identityHint`, `cli/secrets/key.go:189`
`identityError`'s hint + `displayKeyfilePath`), so `keygate`, the validator
and the CLI share one wording without a cross-sibling import. Both copies
become calls.

**Task 5 (not Task 1)** changes `LoadIdentity(recipient) (Identity, Source, error)`
to return the **consulted** `Source` alongside the error (`SourceEnv`,
`SourceEnvFile`, `SourceKeyfile`); `SourceNone` only when `recipient == ""`.
Existing callers check `err` first; the one that does not — `identityPayload`
→ `identityDisplay` — is rewritten in the same task.

### `core/project/config` (Task 2)

`ReasonInvalidIdentity = "invalid_identity"`. `identityReason` (`layers.go:271`)
maps `ErrInvalidIdentity` → it, and its doc comment ("a malformed keyfile as
`ErrCorrupt`") is corrected. `unresolvedReason` is **not** touched — it only
sees `secrets.Decrypt` failures, which cannot carry a parse-identity error.
The CLI mirror `identitySet.reason()` (`cli/secrets/secrets.go:168`) gets the
same arm in the same task, and the `packages.md:287` sentence is updated.
`internal/cli/vars/secrets.go:51` `unresolvedNote` has a `default:` fallback,
so `vars inspect` degrades to `unresolved (invalid_identity)` — acceptable,
stated in the docs task. `SecretsState.IdentitySource` (already present)
becomes non-empty on failure in Task 5, together with the `LoadIdentity`
change.

### `core/workflow/keygate` (Task 3)

```go
// PromptFunc / ConfirmFunc are supplied by the CLI (implemented in
// core/ui/secretsprompt). Either nil ⇒ the gate is non-interactive.
type PromptFunc  func(ctx context.Context, recipient string) (secrets.Identity, error)
type ConfirmFunc func(ctx context.Context, explanation string) (bool, error)

type Options struct {
    BaseDir        string
    Layers         []config.Layer // raw layers (config.LoadRawLayers); nil → gate skipped
    Interactive    bool           // caller-evaluated: widgets.IsInteractiveFn(stdin)
    Yes            bool           // --yes (only run/restart define it)
    OutputJSON     bool           // --output json (root persistent flag)
    NonInteractive bool           // DWE_NONINTERACTIVE — caller passes
                                  // cmdctx.NonInteractiveEnv() or keygate.NonInteractiveEnv()
    Prompt         PromptFunc
    Confirm        ConfirmFunc
    Out            io.Writer      // the success report; nil → discard
}

// Ensure returns (false, nil) when nothing had to be done: raw layers absent
// or invalid (the caller's LoadConfigOrWrap reports it unchanged), no or
// malformed recipient, no encrypted surface, a usable identity already
// present, or a non-interactive context (the caller's existing failure path
// then fires). It returns (true, nil) after a verified import, and a non-nil
// error only when the user declined (ErrAborted carrying secrets.IdentityHint
// text), a present env source is unusable (ErrEnvSourceUnusable), an
// existing keyfile is unusable (ErrKeyfileUnusable), or the import failed.
func Ensure(ctx context.Context, opts Options) (imported bool, err error)

// HasEncryptedSurface is the cheap probe: len(config.CollectMarkers(layers)) > 0
// or at least one *.age file under workspace/templates/config/. No
// decryption, no identity, and a walk error counts as "no surface" (the gate
// must never introduce a failure the caller would not have hit) — this is
// what keeps a healthy `dwe run` free of a per-invocation decrypt scan.
func HasEncryptedSurface(baseDir string, layers []config.Layer) bool

// Inventory classifies every marker and every .age file under
// workspace/templates/config/ against ids. Moved from internal/cli/secrets;
// the walk can fail, so it keeps its error channel.
func Inventory(baseDir string, layers []config.Layer, ids IdentitySet) (Result, error)

// Exported consumer API for cli/secrets (get, files, rekey, status):
type IdentitySet struct{ … }
func LoadIdentitySet(recipient string) IdentitySet
func (s IdentitySet) Decrypt(marker string) (string, error)     // was identitySet.decrypt
func (s IdentitySet) DecryptBytes(b []byte) ([]byte, error)      // was identitySet.decryptBytes
func (s IdentitySet) Reason() string                             // was identitySet.reason
func CollectAgeFiles(root string) ([]AgeFile, error)             // was collectAgeFiles
// Row types keep their json tags — they ARE the `secrets status` JSON contract:
type MarkerRow struct { Layer, Path, State string; Reason string `json:"reason,omitempty"` } // was markerRow
type FileRow   struct { File, State string; Reason string `json:"reason,omitempty"` }        // was fileRow
const StateDecrypted, StateUnresolved, StateDecryptable, StateNotDecryptable, ReasonStaleKey // were unexported
```

`secretsprompt.PromptIdentity(ctx, recipient, in, out)` runs the one-field
hidden form; `Validate` = `ParseIdentity` + recipient match (both age1…
values in the mismatch text, a DWE-authored parse message otherwise); Esc →
`widgets.ErrCancelled`. `secretsprompt.ConfirmImport(ctx, explanation, in,
out)` is an `ask.FieldConfirm` form. Today `ask.Field` has no button labels
(`ask.go` `case FieldConfirm` never calls `.Affirmative()`/`.Negative()`),
so `ask.Field` gains optional `Affirmative`/`Negative` strings — empty keeps
huh's defaults, so every existing `FieldConfirm` site is byte-identical —
and this form sets `Enter key` / `Abort`. Both functions take streams;
`cli/*` wrap them into `PromptFunc`/`ConfirmFunc` closures over
`cmd.InOrStdin()`/`cmd.OutOrStdout()`. Seams for tests live where the
closures are built (`cli/secrets`: `var promptIdentityFn`; `cli/deploy`:
`keygateEnsureFn`; `cli/lifecycle`: the `RunContext` fields themselves).

Order inside `Ensure`:

1. `opts.Layers == nil` or `config.ValidateLayerRoots(layers) != nil` →
   `(false, nil)` (`LoadRawLayers` does not validate; a misplaced `secrets:`
   block must surface as today's config error, not as a prompt).
   `recipient == ""` or `secrets.ParseRecipient(recipient) != nil` →
   `(false, nil)` (the `secrets.recipient` validator reports a malformed
   one; a prompt whose match can never succeed must not open).
2. `secrets.LoadIdentity(recipient)` ok → `(false, nil)`.
3. `!HasEncryptedSurface(...)` → `(false, nil)`. (R2.4; cheap, no decrypt.)
4. `os.Getenv(DWE_AGE_KEY) != "" || os.Getenv(DWE_AGE_KEY_FILE) != ""` →
   `ErrEnvSourceUnusable` with a fixed, source-specific message (`$DWE_AGE_KEY
   is set but does not hold the identity for <recipient>; unset it or fix it —
   a keyfile is not consulted while it is set`). No prompt: first-present-
   source wins, so an import could not be used. Fires in every mode,
   including non-interactive: it is more precise than today's message and
   carries the same exit code.
5. Keyfile already present at `KeyfilePath(recipient)` (it failed step 2, so
   it is unreadable or holds another key) → `ErrKeyfileUnusable` naming the
   path and `dwe secrets key remove <recipient>`; no prompt, every mode.
6. `!opts.Interactive || opts.NonInteractive || opts.Yes || opts.OutputJSON || opts.Prompt == nil || opts.Confirm == nil`
   → `(false, nil)`. (R3.1 — the caller's existing failure fires.)
7. `opts.Confirm(ctx, explanation)` — the explanation is count-free (the
   probe is a bool): "this project has encrypted values that need the age
   identity for `<recipient>`", why the key is needed and where it is looked
   up (`secrets.IdentityHint`). Counts appear only in the post-import report.
   Decline or `ErrCancelled` → `ErrAborted` wrapping the hint. (R2.3)
8. `opts.Prompt(ctx, recipient)` (`ErrCancelled` → `ErrAborted`) →
   `lock.AcquireProjectLocks(opts.BaseDir)` → `secrets.WriteKeyfile` →
   release → **verify** with `secrets.LoadIdentity(recipient)` (error →
   explanatory failure, not `(true, nil)`) → `Inventory` with the new
   identity → write `identity for <recipient> stored at <path>` +
   `N encrypted value(s) and M .age file(s) are now readable` to `opts.Out`
   → `(true, nil)`.

Nothing in `keygate` or `secretsprompt` traces or logs the submitted text or
a parser error: the gate runs before `LoadConfig`, i.e. before
`trace.RegisterRedaction` is installed, so there is no redactor to rely on.

### `key import` (Task 4)

`readIdentityText`: `--file` → `os.ReadFile`; else `!widgets.IsInteractiveFn(stdin)`
(covers `pbpaste |`, CI; `--output json` and `DWE_NONINTERACTIVE` are
additionally checked so a JSON run at a TTY still errors as today) →
`io.ReadAll`; else → `promptIdentityFn` (package seam, default
`secretsprompt.PromptIdentity` over `cmd.InOrStdin()`/`cmd.OutOrStdout()`).
`secrets_identity_source_required` remains for the JSON-at-TTY and
`DWE_NONINTERACTIVE` cases; `widgets.ErrCancelled` from the form maps to the
typed `secrets_import_cancelled` (exit ≠ 0) carrying `secrets.IdentityHint`.
`key import` has no `--yes` flag; the no-prompt matrix for it is non-TTY /
`--output json` / `DWE_NONINTERACTIVE`. After `WriteKeyfile`: `Inventory` with
the new identity; `keyImportJSON` gains `markers_readable int`,
`files_readable int` (no `omitempty` — zero is information); text renderer
appends the R1.5 line. Compatibility contract: the existing first text line
and the existing JSON fields are unchanged; the second line and the two
counters are additive.

Existing keyfile: there is no pre-check today — the no-clobber guard is the
`O_EXCL` inside `secrets.WriteKeyfile`, surfaced as
`secrets_keyfile_write_failed` AFTER parse and recipient check. That order
stays for `--file` and stdin (`TestKeyImport_RejectsMismatch` pipes a foreign
key into a project whose keyfile exists and expects
`secrets_identity_mismatch`). **Only the interactive branch** adds a
`Stat(KeyfilePath(recipient))` pre-check before opening the form, so nobody
types a key into a form whose write is doomed. (R1.6)

### `secrets status` header (Task 5)

`identityJSON` gains `Reason string json:"reason,omitempty"` and
`Hint string json:"hint,omitempty"`; `Source` is now the consulted source
also on failure. `identityDisplay`:

| reason              | header                                                              |
|---------------------|---------------------------------------------------------------------|
| usable              | `keyfile (~/.config/dwe/keys/age1….key)` / `$DWE_AGE_KEY` (as today) |
| `no_identity`       | `none (looked at <keyfile>, $DWE_AGE_KEY, $DWE_AGE_KEY_FILE)` (as today) |
| `invalid_identity`  | `invalid ($DWE_AGE_KEY is set but holds no age identity)`            |
| `wrong_identity`    | `wrong recipient (keyfile <path>: holds age1…, project uses age1…)`  |

The invalid-source wording is DWE-authored and fixed per source
(`$DWE_AGE_KEY`, `$DWE_AGE_KEY_FILE <path>`, `keyfile <path>`); the `age`
parse error is never interpolated (it echoes input bytes). `identityJSON.Error`
(kept, additive change) follows the same rule: it carries the fixed wording,
never `IdentityErr.Error()` verbatim. Text mode appends, when not usable, one
trailing line with `secrets.IdentityHint` (same text as the validator).
`render.SecretsStatusView` gains `IdentityHint`. Goldens: `secrets_status`
(usable) and `secrets_status_empty` stay byte-identical;
`secrets_status_keyless` gains the hint line; `secrets_status_invalid` and
`secrets_status_wrong` are new. This task also carries the `LoadIdentity`
consulted-source change and the `SecretsState.IdentitySource`-on-failure
change (see Decisions).

### `key list` / `key remove` (Task 6)

- `dwe secrets key list [--output json]` — rows: `RECIPIENT | FILE | STATE`,
  state ∈ `ok`, `unreadable`, `unparsable`, `misnamed`, `current project`
  (when a project is resolved and the recipient matches). Fixed strings only
  — no error text. For an unreadable/unparsable file the recipient column
  shows the filename stem (the recipient is the filename by construction),
  never file content; for `misnamed` it shows the parsed recipient. Sorted by
  filename (one order, shared with `ListKeyfiles`). JSON:
  `{"keys":[{recipient,file,state,current}]}`, `[]` never `null`. Renderer
  `render.SecretsKeyList` returns a string; golden.
- `dwe secrets key remove <recipient> [--force] [-y]` — resolves
  `secrets.KeyfilePath(recipient)` only (canonical filename; a misnamed file
  is never targeted); refuses when the recipient is the current project's
  without `--force` (`secrets_key_in_use`); non-interactive without `--yes`
  → `secrets_confirmation_required`; `widgets.RunConfirm` otherwise (seam
  `var runConfirm`); project locks around `os.Remove` when a project is
  resolved; missing file → `secrets_key_not_found`. JSON DTO
  `{recipient, keyfile, removed: true}` via `cmdctx.WriteData`; text
  `removed <path>`. Both typed refusals are proper envelopes in JSON mode.
- Both added to `allowedWithoutProject`. The bridge policy matches on the
  top-level name only (`bridgeCommandAllowed`), so both are blocked with no
  code change; adding two rows to `bridgepolicy_test.go` is optional.

### Wiring (Tasks 7, 8)

`runDeployMenu` (menu.go): after the TTY gate at :76 and BEFORE
`LoadConfigOrWrap` at :86:

```go
layers, _ := config.LoadRawLayers(flags.ConfigPath) // error → layers == nil → gate skipped;
                                                      // LoadConfigOrWrap below reports today's
                                                      // `loading config: …` error unchanged
in, out := cmd.InOrStdin(), cmd.OutOrStdout()
if _, err := keygateEnsureFn(ctx, keygate.Options{
    BaseDir: baseDir, Layers: layers,
    Interactive: widgets.IsInteractiveFn(in),
    OutputJSON: flags.Output == "json", NonInteractive: cmdctx.NonInteractiveEnv(),
    Prompt:  func(ctx context.Context, r string) (secrets.Identity, error) { return secretsprompt.PromptIdentity(ctx, r, in, out) },
    Confirm: func(ctx context.Context, why string) (bool, error) { return secretsprompt.ConfirmImport(ctx, why, in, out) },
    Out: out,
}); err != nil { return err }
```

Placed at menu entry, the gate also covers `menuPlan` / `menuPlanService`;
that is accepted and tested (a plan on an unresolved project would otherwise
print `<encrypted>`-derived commands). `ErrAborted` / `ErrEnvSourceUnusable`
/ `ErrKeyfileUnusable` map to a typed `cmdctx.Err("secrets_no_identity")`
with the hint (same code `identityError` uses today). `runPreWizardPreflight`
is untouched: it remains the non-interactive wall.

`RunRun` (run.go): before `config.LoadConfigOrWrap` at :136, and
**`RunRestart` before `RunStop`** (:352) — a missing key must not tear the
stack down and only then fail. The second call inside the nested `RunRun`
short-circuits at step 2 (identity now usable) and is harmless.

```go
layers, _ := config.LoadRawLayers(ctx.ConfigPath) // same skip-on-error rule
if _, err := KeygateEnsureFunc(ctx.Ctx, keygate.Options{
    BaseDir: workDir, Layers: layers,
    Interactive: widgets.IsInteractiveFn(os.Stdin), // same probe RunRun's git-pull prompt uses
    Yes: ctx.Yes, OutputJSON: ctx.OutputJSON, NonInteractive: keygate.NonInteractiveEnv(),
    Prompt: ctx.KeyPrompt, Confirm: ctx.KeyConfirm, Out: os.Stdout,
}); err != nil { return err }
```

`var KeygateEnsureFunc = keygate.Ensure` next to `PreflightFunc`.
`RunContext` gains `OutputJSON bool` (set from `flags.Output == "json"` in
`internal/cli/lifecycle/run.go:36` and `restart.go:93` — `--output` is a root
persistent flag; both files already branch on it) and `KeyPrompt
keygate.PromptFunc` / `KeyConfirm keygate.ConfirmFunc` (closures over the
cobra streams, built in the same two files; nil in every other caller — e.g.
`service_plan.go:457`, which also passes `Yes: true` — so those never
prompt; pinned by a test). The notifier `defer` treats `ErrAborted` /
`ErrEnvSourceUnusable` / `ErrKeyfileUnusable` like `*preflight.Error` (no
desktop notification).

### Error codes (new)

`secrets_key_in_use`, `secrets_key_not_found`, `secrets_confirmation_required`,
`secrets_import_cancelled`; reuse `secrets_no_identity`,
`secrets_identity_invalid`, `secrets_identity_mismatch`,
`secrets_identity_source_required`, `secrets_keyfile_write_failed`. All four
new codes join the error-code list in `secrets.md` (`:566`, ru `:584`).

## What Goes Where

- **Implementation Steps** (`[ ]`): code, tests, docs in this repo.
- **Post-Completion**: the manual pass on `../ficbird`, skill re-sync.

## Implementation Steps

### Task 1: Widen `ParseIdentity`, add `ErrInvalidIdentity`, `ListKeyfiles`, shared hint

**Files:**
- Modify: `internal/shared/secrets/identity.go`
- Modify: `internal/shared/secrets/identity_test.go` (`:353` asserts `ErrCorrupt` for a garbage keyfile → becomes `ErrInvalidIdentity`)
- Modify: `internal/core/validate/secrets/secrets.go` (`identityHint` → call), `internal/cli/secrets/key.go` (`identityError` hint → call), `internal/cli/secrets/secrets.go` (`displayKeyfilePath` at `:461` → call)

- [x] add `ErrInvalidIdentity`; `ParseIdentity` extracts the FIRST
      `AGE-SECRET-KEY-1…` token anywhere in the text (regexp, bech32 charset,
      fixed length), later tokens ignored; returns `ErrInvalidIdentity` with
      DWE-authored text on no token / age parse failure (the age error is
      wrapped for `errors.Is` chains only if its text is provably free of
      input bytes — otherwise dropped); delete `firstKeyLine`
- [x] extract `secrets.IdentityHint(recipient)` and
      `secrets.DisplayKeyfilePath(recipient)` from the unexported copies
      (`validate/secrets/secrets.go:240`, `cli/secrets/key.go:189`,
      `cli/secrets/secrets.go:461`); all call sites become calls, wording
      unchanged
- [x] add `ListKeyfiles() ([]KeyfileInfo, error)` (sorted, `State` enum
      `ok`/`unreadable`/`unparsable`/`misnamed`, no error text, missing dir → empty)
- [x] tests: `TestParseIdentity` table gains joined-line paste
      (`# public key: age1… AGE-SECRET-KEY-1…`), CRLF, surrounding whitespace,
      a multi-identity keyfile (first wins), a commented-out old key above the
      live one (first token wins — document this as the accepted change from
      today's "first non-comment line"), garbage (`ErrInvalidIdentity`, not
      `ErrCorrupt`) whose error text does not contain the last 20 characters
      of the input; `TestListKeyfiles` (ok + unreadable + unparsable rows,
      missing dir; a misnamed file → `KeyfileMisnamed` with the parsed recipient; the unparsable file's content appears nowhere in the result)
- [x] run `go test ./internal/shared/secrets/... ./internal/cli/secrets/... ./internal/core/validate/...` — must pass before task 2

### Task 2: `invalid_identity` reason in config and the validator phrase

**Files:**
- Modify: `internal/core/project/config/secrets.go`, `layers.go`
- Modify: `internal/core/project/config/secrets_test.go` (or the layers test that pins reasons)
- Modify: `internal/cli/secrets/secrets.go` (`identitySet.reason()`), `status_test.go`
- Modify: `internal/core/validate/secrets/secrets.go`, `secrets_test.go`
- Modify: `docs/internals/packages.md` (the `:287` mirror sentence)

- [x] add `ReasonInvalidIdentity`; map `ErrInvalidIdentity` in
      `identityReason` (fix its doc comment) AND in the CLI mirror
      `identitySet.reason()`; leave `unresolvedReason` alone
- [x] `reasonPhrase` gains the invalid phrase with fixed wording per source
      (`$DWE_AGE_KEY is set but holds no age identity` — no parse error text);
      `unresolvedMarkerDiags` groups it like the other reasons
      (➕ `reasonPhrase` took a third `source` argument; since
      `SecretsState.IdentitySource` is empty on a failed load until Task 5,
      `invalidIdentityPhrase` re-derives the source from the environment along
      `LoadIdentity`'s own precedence and honours a recorded source when present)
- [x] tests: a layer load with a truncated `DWE_AGE_KEY` yields
      `invalid_identity` on every marker (not `corrupt`) —
      `TestLoadLayersWithSecrets_identityFailureIsNeverCorrupt`
      (`config/secrets_test.go:227`) currently expects `no_identity` for a
      malformed keyfile and is updated to `invalid_identity` (the "never
      corrupt" half still holds); `secrets status` JSON rows say
      `unresolved`/`invalid_identity` (header unchanged until Task 5);
      validator emits one `secrets.unresolved:invalid_identity` diagnostic
      whose message names `$DWE_AGE_KEY`, whose hint is `IdentityHint`, and
      which does not contain the env value; `vars inspect` on the same
      project shows `unresolved (invalid_identity)` via the existing
      `default:` fallback (`cli/vars/secrets.go:51`) — pinned, no new wording
- [x] run `go test ./internal/core/project/config/... ./internal/core/validate/... ./internal/cli/secrets/...` — must pass before task 3
      (plus full `make test` + `make lint`; ➕ the reason enum is a documented
      user-facing contract, so the new row also landed in
      `docs/reference/config/secrets.md`, its ru mirror, `skills/dwe/SKILL.md`
      and `CHANGELOG.md` — Task 9 keeps the onboarding prose)

### Task 3: `keygate` (decision + scan) and `secretsprompt` (form) packages

**Files:**
- Create: `internal/core/workflow/keygate/keygate.go` (doc, `Options`, `PromptFunc`/`ConfirmFunc`, `Ensure`, `ErrAborted`, `ErrEnvSourceUnusable`, `ErrKeyfileUnusable`, `NonInteractiveEnv`)
- Create: `internal/core/workflow/keygate/inventory.go` (moved `IdentitySet` + exported methods, `Inventory`, `HasEncryptedSurface`, `CollectAgeFiles`, row types, state/reason constants)
- Create: `internal/core/workflow/keygate/keygate_test.go`, `inventory_test.go`
- Create: `internal/core/ui/secretsprompt/prompt.go` (`PromptIdentity`, `ConfirmImport`, `var runAsk = ask.Run` seam), `prompt_test.go`
- Modify: `internal/core/ui/ask/ask.go`, `ask_test.go` (optional `Field.Affirmative`/`Negative` for `FieldConfirm`; empty → huh defaults, existing sites byte-identical)
- Modify: `internal/cli/cmdctx/noninteractive_test.go` (pins `keygate.NonInteractiveEnv()` equal to `cmdctx.NonInteractiveEnv()` — the pin lives on the cli side so `keygate`'s test package never imports `cmdctx`)
- Modify: `internal/cli/secrets/secrets.go`, `status.go`, `get.go`, `files.go`, `rekey.go` (consume the moved inventory; delete the local copies)
- Modify: `internal/cli/secrets/status_test.go`, `secrets_test.go`, `rekey_test.go` (`:52` calls `collectAgeFiles` directly; the symlink inventory test at `secrets_test.go:292` MOVES to `inventory_test.go` rather than being duplicated)

- [x] move `identitySet` → `keygate.IdentitySet` (+ `LoadIdentitySet`) with
      its methods `decrypt`, `decryptBytes`, `reason()`, plus
      `classifyMarker`/`classifyBytes`, `collectAgeFiles`/`inspectAgeFile`,
      `markerRow`/`fileRow`, `configPackKind`, `reasonStaleKey`, `relToRoot`
      and the state/reason constants; `cli/secrets` (incl. `get.go:61`,
      `files.go:188`) becomes a consumer — **no behaviour change**,
      `secrets status` JSON and goldens byte-identical
      (➕ `cli/secrets` keeps its local vocabulary through type aliases
      (`markerRow`/`fileRow`/`inventory`) and `var relToRoot = keygate.RelToRoot`,
      so the ~15 unrelated call sites in `init/set/rekey/files` stayed untouched;
      `collectInventory` is now a four-line wrapper)
- [x] implement `HasEncryptedSurface` (markers via `config.CollectMarkers`,
      `.age` via the moved walker with an early `return true`, walk error →
      false) and `NonInteractiveEnv()` (pinned equal to `cmdctx`'s set from
      `cmdctx`'s own test file, never from `keygate`'s)
- [x] implement `secretsprompt.PromptIdentity` (one `FieldPassword`, title
      `dwe secrets › key import`, description naming the recipient,
      `Validate` = `ParseIdentity` + recipient match with both age1… values
      in the mismatch text; the private key never appears in any error) and
      `secretsprompt.ConfirmImport` (`ask.FieldConfirm`, `Enter key`/`Abort`,
      streams from the caller); no `core/ui` import in `keygate`
- [x] implement `Ensure` in the 8-step order given in Technical Details,
      with `ErrAborted`, `ErrEnvSourceUnusable`, `ErrKeyfileUnusable`;
      `ValidateLayerRoots` guard; project locks around the keyfile write;
      post-write `LoadIdentity` verification; success report uses the
      re-run inventory; no trace/log of the submitted text
      (➕ `ErrCancelled` is NOT matched by name: `keygate` must stay free of
      `core/ui`, and a sentinel duplicated into a third package would be worse
      than the alternative — **any** `Prompt`/`Confirm` error becomes
      `ErrAborted`, whose message is DWE-authored and drops the cause, since
      the only two causes are a cancel (adds nothing) and a form failure
      (the one error whose text travelled next to the typed key).
      ➕ `Result.Readable()` carries the two report counters, and the
      confirmation sentence is the exported `keygate.Explanation(recipient)`,
      so the wording is pinned in one place)
- [x] tests (prompt, in `secretsprompt`): stubbed `runAsk` returns a
      matching identity → returned; the form-level `Validate` func
      (exercised directly) yields the two-recipient message for a foreign
      key and the fixed parse message for garbage; `ErrCancelled`
      propagates; no error text contains the last 20 characters of the typed
      input; `ConfirmImport` honours the passed streams
- [x] tests (gate): table over {nil layers, layers failing
      `ValidateLayerRoots` (a `secrets:` block in `defaults.yml`), no
      recipient, malformed recipient, recipient+no markers+no files, usable
      keyfile, usable env, `Interactive=false`, `Yes`, `OutputJSON`,
      `NonInteractive`, nil `Prompt`, nil `Confirm`} → `(false, nil)` and
      `Prompt`/`Confirm` stubs that fail the test if called; poisoned
      `DWE_AGE_KEY` / `DWE_AGE_KEY_FILE` → `ErrEnvSourceUnusable`, no prompt,
      message names the variable and not its value; existing
      wrong-recipient keyfile → `ErrKeyfileUnusable` naming `key remove`, no
      prompt; interactive+unresolved+confirm → keyfile written 0600, `(true,
      nil)`, report has the right counts, `LoadIdentity` verified;
      interactive+decline → `ErrAborted` with hint text;
      `HasEncryptedSurface` never calls `secrets.Decrypt` (a marker with
      garbage ciphertext still yields true, no error); `Inventory` keeps its
      error on an unwalkable templates dir
- [x] tests (inventory): the moved `cli/secrets` cases keep passing; add a
      direct `Inventory` test with one marker + one `.age` file + one
      symlinked `.age` (reported, not skipped)
- [x] run `go test ./internal/core/workflow/keygate/... ./internal/cli/secrets/...` — must pass before task 4

### Task 4: Interactive `secrets key import` with the readability report

**Files:**
- Modify: `internal/cli/secrets/key.go`
- Modify: `internal/cli/secrets/key_test.go`
- Modify: `docs/reference/config/secrets.md` (subcommand section only; the new-machine section is Task 9)

- [x] `readIdentityText`: `--file` → non-TTY stdin → `promptIdentityFn` (default `secretsprompt.PromptIdentity`);
      keep `secrets_identity_source_required` for `--output json` at a TTY and
      `DWE_NONINTERACTIVE`; add a keyfile-exists pre-check ONLY on the
      interactive branch (`--file`/stdin keep parse → recipient check →
      `O_EXCL` write; `TestKeyImport_RejectsMismatch` stays green unchanged);
      update the command's `Long`/examples (`pbpaste |` stays the first example)
      (➕ the branch point moved UP into a new `resolveIdentity`, because the
      prompt yields a `secrets.Identity` and not text: `readIdentityText` now
      only serves `--file`/piped stdin, `promptIdentity` owns the JSON /
      `DWE_NONINTERACTIVE` refusal, the keyfile pre-check and the
      `ErrCancelled` mapping, and the recipient check is the shared
      `identityMismatchError` so all three branches produce one wording)
- [x] after `WriteKeyfile`, run `keygate.Inventory` with the new identity;
      `keyImportJSON` gains `markers_readable`, `files_readable`; text output
      becomes two lines (`identity for … stored at …` + `N encrypted value(s)
      and M .age file(s) are now readable`)
      (➕ `runKeyImport` loads the raw layers ONCE — `requireRecipient` →
      `loadRawLayers` + `recipientOrErr` — and reuses them for the report, so
      the counters describe the same tree the recipient check ran against; an
      inventory error degrades to zero counters rather than failing a command
      whose keyfile is already written)
- [x] tests: `TestKeyImport_FromStdin` / `_FromFile` keep their existing
      assertions on the first line and JSON fields; new `TestKeyImport_Prompt`
      (IsInteractiveFn=true + `promptIdentityFn` stub → keyfile 0600 +
      counts); `TestKeyImport_PromptCancelled` → `secrets_import_cancelled`,
      keyfile absent; `TestKeyImport_NoPromptWhenNonInteractive` (json /
      `DWE_NONINTERACTIVE` / non-TTY → stub fails the test if called, typed
      error as today); `TestKeyImport_ExistingKeyfileRefusedBeforePrompt`
      (interactive branch only; `TestKeyImport_RejectsMismatch` unchanged);
      `TestKeyImport_ReportCounts` on a fixture with 2 markers + 1 `.age`;
      leak assertions on error envelopes in JSON mode
      (➕ the leak assertion lives on `TestKeyImport_PromptRejectsForeignIdentity`
      and covers message + hint + every detail + the real
      `cmdctx.WriteError` envelope bytes, via a new `jsonErrorEnvelope` helper;
      the case itself runs in text mode, since `--output json` refuses the
      prompt before it can be reached)
- [x] run `go test ./internal/cli/secrets/...` — must pass before task 5
      (plus full `make test` + `make lint`; ➕ the ru mirror
      `docs/i18n/ru/reference/config/secrets.md` was updated in the same
      commit — the `key import` section, the JSON row and the error-code list
      would otherwise describe a command that no longer exists)

### Task 5: `secrets status` — honest identity header and fix hint

**Files:**
- Modify: `internal/shared/secrets/identity.go`, `identity_test.go` (consulted source on error)
- Modify: `internal/core/project/config/layers.go`, `secrets_test.go` (`IdentitySource` on failure)
- Modify: `internal/cli/secrets/status.go`, `status_test.go`
- Modify: `internal/core/ui/render/secrets.go`, `secrets_test.go`
- Modify/Create: `internal/core/ui/render/testdata/secrets_status*.golden`

- [x] `LoadIdentity` returns the consulted `Source` with every error
      (`SourceEnv`, `SourceEnvFile`, `SourceKeyfile`; `SourceNone` only for
      an empty recipient); `finishLoad` threads it; doc comment states the
      contract; `layers.go:128` records it in `SecretsState.IdentitySource`
      on failure too (`config/secrets_test.go:382` — no-secrets project keeps
      an empty source — still holds because the lookup is skipped)
      (➕ a recipient mismatch is now the typed `secrets.WrongIdentityError`
      (`Source`/`Have`/`Want`, unwraps to `ErrWrongIdentity`, message
      unchanged): the header must name both recipients, and re-parsing them
      out of a sentence would be the one place a display surface could drift
      from the loader. ➕ `secrets.SourceLabel(src, recipient)` is the single
      place a source is NAMED for display — locations only, never content)
- [x] `identityJSON` gains `Reason`, `Hint`; `identityPayload` fills the
      consulted source + reason on failure; `identityDisplay` renders the
      four variants from the table in Technical Details (same commit as the
      `LoadIdentity` change — never a green `keyfile (…)` header for a failed
      lookup)
      (➕ `identityDisplay` switches on `Reason` FIRST and puts
      `identityJSON.Error` verbatim inside the parentheses, so the text header
      and the JSON payload cannot word the same failure differently; the
      parenthetical therefore reads `wrong recipient (keyfile <path> holds the
      identity for age1…, but the project uses age1…)` rather than the plan's
      shorter draft. ➕ `identityErrorText` composes that sentence from fixed
      per-source wording; its ONE pass-through is a bare filesystem error
      (permission, missing home), which carries a path and an OS message but
      never file content — swallowing it would report a permissions problem as
      "no key on this machine". ➕ a set `DWE_AGE_KEY_FILE` pointing at nothing
      renders `none ($DWE_AGE_KEY_FILE <path>, which does not exist)`: the
      lookup stopped at the first source, so the generic "looked at …" list
      would describe a search that never happened. ➕ `keygate.IdentityReason`
      extracted so `Result.IdentityReason()` and `IdentitySet.Reason()` share
      one mapper)
- [x] `render.SecretsStatusView` gains `IdentityHint`; text appends the hint
      line when set (R6); `secretsNoneNote` path unchanged
      (➕ the hint CLOSES the report rather than sitting under the Identity
      line: it applies to every unresolved row below it, and a two-line header
      pushes the inventory off a short screen)
- [x] tests: `TestIdentityDisplay` covers all four variants;
      `TestStatus_JSON_*` assert `identity.reason`/`identity.hint` for keyless,
      invalid env, wrong keyfile; `TestStatus_ExitsZeroWithUnresolvedSecrets`
      still exit 0 and now contains the hint; render goldens regenerated +
      two new; `TestStatus_NeverPrintsKeyMaterial` extended with a truncated
      `DWE_AGE_KEY` whose *content* must not be echoed either
      (➕ the truncated-key leak case is its own test,
      `TestStatus_NeverEchoesABrokenIdentitySource`, because it needs a
      different fixture (no keyfile, poisoned env) than the plaintext leak
      test; ➕ `TestLoadIdentityReportsConsultedSourceOnFailure` +
      `TestSourceLabel` pin the leaf contract, and the config table test now
      pins `IdentitySource` per failure mode)
- [x] run `go test ./internal/cli/secrets/... ./internal/core/ui/render/...` — must pass before task 6
      (plus full `make test` + `make lint`; ➕ the identity header, the hint
      line and the two new JSON fields are a user-facing contract, so
      `docs/reference/config/secrets.md`, its ru mirror and `CHANGELOG.md`
      landed in the same commit — Task 9 keeps the onboarding prose;
      `docs/internals/packages.md` gained the consulted-source sentence)

### Task 6: `secrets key list` and `secrets key remove`

**Files:**
- Modify: `internal/cli/secrets/key.go`, `key_test.go`
- Modify: `internal/core/ui/render/secrets.go`, `secrets_test.go`, `testdata/secrets_key_list.golden`
- Modify: `internal/cli/root.go` (`allowedWithoutProject`), `root_test.go` (or wherever the allowlist is pinned)
- Modify: `internal/cli/bridgepolicy_test.go` (pin `secrets key list/remove` blocked)

- [ ] `key list`: `secrets.ListKeyfiles` → rows with `current` computed from
      the resolved project's recipient when a project is present (use
      `flags.ConfigPath != ""` / the raw layers; tolerate no project);
      `render.SecretsKeyList` + its `SecretsKeyListAt(width)` sibling (the
      responsive-tables contract; `SecretsStatus`/`SecretsStatusAt` is the
      precedent in the same file); JSON `{"keys":[…]}` with `[]` never
      `null`; empty → `No identities in <dir>.`
- [ ] `key remove <recipient> [--force] [-y]` per Technical Details;
      confirmation via `widgets.RunConfirm` (seam `var runConfirm`); project
      locks around the delete when a project is resolved; JSON DTO
      `{recipient, keyfile, removed}`
- [ ] add both to `allowedWithoutProject`
- [ ] tests: list with five files (current, foreign, unreadable, unparsable,
      misnamed) inside and outside a project, text golden + JSON, the
      unparsable file's content absent from both; remove: happy path text +
      JSON, missing file, current recipient refused without `--force`,
      allowed with it, non-interactive without `--yes` → typed envelope and
      file still present, confirm-decline → no-op, misnamed file never
      targeted; optional bridge-policy rows
- [ ] run `go test ./internal/cli/...` — must pass before task 7

### Task 7: Gate the `dwe deploy` menu

**Files:**
- Modify: `internal/cli/deploy/menu.go`, `menu_test.go`

- [ ] add `var keygateEnsureFn = keygate.Ensure`; call it after the TTY gate
      and before `LoadConfigOrWrap` with the closures over the cobra streams,
      mapping `ErrAborted` / `ErrEnvSourceUnusable` / `ErrKeyfileUnusable`
      to the typed `secrets_no_identity` error with `secrets.IdentityHint`
- [ ] tests: interactive + unresolved → stub called with the right
      `Options` (`Interactive`, `OutputJSON`, `NonInteractive`, non-nil
      hooks) and, on `imported=true`, the menu proceeds to
      `runPreWizardPreflightFn` with a cfg that has no unresolved markers
      (identity injected by the stub via `DWE_AGE_KEY`); stub returning
      `ErrAborted` → typed error, no preflight; a broken `workspace.yml` →
      stub receives nil layers and the error text is today's `loading
      config: …`; project without secrets → stub called but the existing
      menu output is byte-identical (captured-before comparison); the
      `menuPlan` path is gated too (documented); `--output json` /
      `DWE_NONINTERACTIVE` → `Options` carry the flags;
      `TestRunPreWizardPreflight_SecretsUnresolvedBlocks` untouched
- [ ] run `go test ./internal/cli/deploy/...` — must pass before task 8

### Task 8: Gate `RunRun` (`dwe run`, `dwe restart`)

**Files:**
- Modify: `internal/core/workflow/lifecycle/run.go`, `run_test.go`
- Modify: `internal/cli/lifecycle/run.go`, `restart.go` (`RunContext.OutputJSON`, `KeyPrompt`, `KeyConfirm`), `run_test.go`, `restart_test.go`
- Modify: `internal/cli/service/service_plan_test.go` (no-prompt pin for the toggle executor)

- [ ] `RunContext` gains `OutputJSON`, `KeyPrompt`, `KeyConfirm`;
      `var KeygateEnsureFunc = keygate.Ensure`; `RunRun` loads raw layers
      and calls it BEFORE `config.LoadConfigOrWrap`; `RunRestart` calls it
      BEFORE `RunStop` (the nested `RunRun` call short-circuits); the
      notifier `defer` treats the three gate errors like `*preflight.Error`
      (no desktop notification)
- [ ] `cli/lifecycle/run.go` and `restart.go` fill the three new fields
      (closures over the cobra streams, `flags.Output == "json"`)
- [ ] add `var runStopFn = RunStop` next to `PreflightFunc` (there is no
      stop seam today — `RunRestart` calls `RunStop` directly at :352) and
      route the restart call through it
- [ ] tests (core): with a stub that installs `DWE_AGE_KEY` and returns
      `imported=true`, `.env` renders plaintext on the SAME invocation; stub
      returning `ErrAborted` → error before any `.env` write AND, on the
      restart path, before `RunStop` is reached (`runStopFn` stub fails the
      test if called); a broken `workspace.yml` → stub receives nil layers
      and `RunRun` returns today's `loading config: …` error; project
      without secrets → stub invoked, run output unchanged; `Yes: true` →
      `Options.Yes`; nil hooks → gate non-interactive
- [ ] tests (cli): `dwe run --output json` and `dwe restart --output json`
      at a stubbed TTY → `Options.OutputJSON == true`, prompt stub fails the
      test if called; `service_plan` toggle executor reaches `RunRestart`
      with nil hooks and `Yes: true`
- [ ] run `go test ./internal/core/workflow/lifecycle/... ./internal/cli/lifecycle/... ./internal/cli/service/...` — must pass before task 9

### Task 9: Reference docs, skill, internals, changelog

**Files:**
- Modify: `docs/reference/config/secrets.md`, `docs/i18n/ru/reference/config/secrets.md`
- Modify: `docs/reference/config/validate.md` (the `invalid_identity` reason in the secrets row)
- Modify: `skills/dwe/SKILL.md`
- Modify: `docs/internals/packages.md`, `AGENTS.md`
- Modify: `CHANGELOG.md`

- [ ] `secrets.md`: new section **"New developer / new machine"** (three
      sources, lookup order, "the first present source must match the
      recipient — there is no fall-through", the interactive import walkthrough
      with the readability report, what the wizard / `dwe run` / `dwe restart`
      offer — and that `restart` offers before stopping — what CI sees
      instead, why a set-but-broken env var is reported rather than
      prompted); add the section to the manual `## Contents` TOC (`:12`);
      `key import` section gains the prompt + report; new `key list` /
      `key remove` sections; status section documents the three failure
      headers and the hint; the `unresolved: …` reason rows (`:211-213`)
      gain `invalid_identity`; JSON section gains the new fields; error-code
      list (`:566`) gains the four new codes; ru mirror (`:220-222`, `:584`)
      updated in the same commit
- [ ] `SKILL.md`: interactive import is a **human handoff** — the agent never
      types a key; on `<encrypted>` run `secrets status --output json`, read
      `identity.reason`/`identity.hint`, hand off; never edit yml to "fix" a
      marker; `key list` in the READ table; the `secrets.unresolved` gate
      may now be an interactive offer when a human runs `dwe run`/`dwe deploy`
- [ ] `packages.md`: new top-level `` - `internal/core/workflow/keygate/` ``
      and `` - `internal/core/ui/secretsprompt/` `` bullets (the `§` pointer
      test requires line-leading bullets) — contracts: runs BEFORE
      `LoadConfig` on raw layers, skips itself on any raw-load/validation
      error, never prompts non-interactively or with nil hooks, never offers
      an import a present env source would shadow, `keygate` is `core/ui`-free
      (the `ui-is-sink` rule is honoured, not amended); `cli/secrets` section
      updated for the moved inventory, the `identitySet.reason()` mirror
      sentence (`:287` — which also still says `ParseIdentity` "skips blank /
      `#` lines" and gives "a malformed keyfile as `ErrCorrupt`" as
      `identityReason`'s rationale, and names `classifyMarker` / `identitySet.decrypt` as living in `cli/secrets`; all three are rewritten, not appended to)
      and new subcommands; `shared/secrets` section for `ErrInvalidIdentity`
      + first-token `ParseIdentity` + consulted source + `ListKeyfiles` +
      `IdentityHint`; `ui/ask` section for `Field.Affirmative`/`Negative`;
      `cli/deploy` and `workflow/lifecycle` sections name the seams
      (`keygateEnsureFn`, `KeygateEnsureFunc`, `runStopFn`) and the
      restart-before-stop order
- [ ] `AGENTS.md` "Encrypted secrets" bullet: **net-zero byte delta** —
      tighten an existing clause to pay for one sentence on the gate
      (`wc -c AGENTS.md` ≤ 40960, line ≤ 600 runes); run
      `go test ./internal/cli/docs/ -run TestAgentsMd` in this task
- [ ] `CHANGELOG.md` `## [Unreleased]`: interactive `key import` + report,
      wizard / `run` / `restart` key offer, `status` identity header +
      hint, `invalid_identity` reason, `key list` / `key remove`,
      `ParseIdentity` first-token rule
- [ ] `make build` (refreshes embedded docs), `go test ./internal/core/docs/... ./internal/cli/docs/...`
      — must pass before task 10

### Task 10: Verify acceptance criteria

- [ ] unit level: `make test`, `make lint`
- [ ] scratch project (`dwe init` + `secrets init` + one `set`), then
      `HOME=$(mktemp -d)`:
  - `bin/dwe secrets key import` at a TTY → paste the WHOLE keyfile
    (comment + key + trailing newline) → report `1 encrypted value(s) and 0
    .age file(s) are now readable`; `secrets status` green
  - paste a foreign key, then garbage → in-form error each time, keyfile
    absent; Esc → typed error with the hint, exit ≠ 0
  - `cat key | bin/dwe secrets key import` and `--file key` → first output
    line identical to the pre-change binary
  - `DWE_AGE_KEY=AGE-SECRET-KEY-1TRUNC bin/dwe secrets status` → header
    `invalid ($DWE_AGE_KEY …)`, hint line, exit 0, the env value absent
    from the output; same under `--output json` →
    `identity.reason == "invalid_identity"`
  - `DWE_AGE_KEY=AGE-SECRET-KEY-1TRUNC bin/dwe run` at a TTY → no prompt,
    the message names `$DWE_AGE_KEY` and says a keyfile would not be
    consulted
  - `bin/dwe run < /dev/null`, `bin/dwe run -y`, `bin/dwe run --output json`,
    `DWE_NONINTERACTIVE=1 bin/dwe run`, `bin/dwe deploy --output json` → no
    prompt, today's error text
  - `bin/dwe deploy` at a TTY → offer → paste → wizard continues in the same
    invocation; `bin/dwe run` at a TTY → offer → paste → `.env` rendered, run
    proceeds; `bin/dwe restart` at a TTY on a running stack → offer appears
    BEFORE any container stops; declining leaves the stack running
  - broken `workspace.yml` (unknown top-level key) → `bin/dwe run` and
    `bin/dwe deploy` print today's `loading config: …` error, no prompt
  - `bin/dwe secrets key list` inside and outside the project; `key remove`
    of the current recipient refused, `--force` works
- [ ] project WITHOUT secrets: `bin/dwe run`, `bin/dwe deploy`, `secrets status`
      output diffed against the pre-change binary — identical
- [ ] real workspace `../ficbird` (annotated `defaults.yml`, one marker, one
      `.age` source): repeat the import / status / run / deploy checks with a
      temp `HOME`; do not commit anything there

### Task 11: [Final] Update documentation

- [ ] re-read `docs/reference/config/secrets.md` end-to-end for consistency
      with the shipped messages (copy the exact strings from the goldens)
- [ ] `AGENTS.md` / `packages.md` reflect any ➕ deviations recorded above
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification**
- The `../ficbird` pass in Task 10 is the sign-off; record its output in the
  PR description (the report line, the three status headers, the wizard offer).
- Screen-share sanity check that the hidden field never echoes and that the
  in-form error for a foreign key names both recipients.

**External**
- `skills/dwe/SKILL.md` is loaded from GitHub `main` by the installed skill;
  after merge, re-sync the local copy (see memory note *dwe skill install path*).
- Plan B (`secrets-polish`) follows; it touches `local/` writers and
  `pipeline.StepCommand`, none of which this plan modifies, so the two can
  land in either order.
