# Secrets: format-preserving YAML writes, plan redaction, positive validation

Plan B of two follow-ups to the encrypted-secrets feature
(`docs/plans/completed/20260902-secrets.md`), driven by the verification run on
the real annotated workspace `../ficbird`. Plan A
(`20260903-secrets-key-onboarding.md`) covers key onboarding and is
independent of this one; they can land in either order.

## Overview

Three defects that do **not** reproduce on a `dwe init` project and only
showed up on a real, annotated workspace:

1. **Every `secrets init` / `set` / `rekey` reformats the whole layer file.**
   The node writer re-encodes the document through yaml.v3, which (a) emits
   its default 4-space indent regardless of the file's own, (b) drops every
   blank line (yaml.v3 keeps them only inside comment blocks), and (c)
   rewrites `<<: *anchor` as `!!merge <<: *anchor`. One `secrets set` into a
   759-line annotated `defaults.yml` produced a 373+/386− diff. Comments and
   quotes survive, which is why the existing tests pass. The fix is a
   **position-guided byte splice**: yaml.v3 still parses and locates the
   target node, but only the bytes of that scalar (or the inserted lines)
   change. The whole-document encoder stays for the other writers.
2. **`dwe deploy plan` prints decrypted values.** `pipeline.StepCommand`
   renders the resolved command (`--set k=v` from `with:` carries plaintext),
   and none of its seven call sites pass through the `trace` redactor, which is
   installed only on the `-v`/`--debug` echo path. Plan output is exactly what
   gets pasted into tickets and PR descriptions. Redaction becomes a property
   of the display functions themselves, so every current and future print
   surface inherits it.
3. **`dwe validate secrets` on a healthy project says
   `validation skipped (no files found)`** with `diagnostics: []`. Neither
   validator in the domain ever emits `SeverityOK`, so a healthy state is
   indistinguishable from "the domain did not run" — on the one command a
   developer uses to check themselves after onboarding.

## Context (from discovery)

- `internal/core/project/local/local_node.go` — the sanctioned write path
  for all three layer files: `LoadYAMLNode`, `ApplyOverlay`
  (`applyOverlayToMapping`, `findMappingPair`, `mappingHasMergeKey`,
  `encodeValueNode`), `EncodeYAMLNode` (plain `yaml.Marshal`, **no
  `SetIndent`**), `WriteYAMLNode` → `local_yaml.go` `writeFileAtomic(path,
  data, policy)`, `ReplaceScalars` (value nodes only, skips aliases; sole
  caller `rekey`). Merge-key refusal: descending INTO a mapping that carries
  `<<` is rejected (`cannot set …: parent mapping uses a YAML merge key`).
- `internal/cli/secrets/set.go` `writeMarker` (encode → re-decode →
  `ValidateLayerRoots(stageLayers…)` → write), `init.go` `writeRecipient`,
  `rekey.go` `rekeyLayerFile` + `layerWritePolicy`. Tests
  `TestInit_PreservesCommentsAnchorsAndMode`,
  `TestSet_PreservesCommentsAndSiblings`,
  `TestWriteYAMLNode_PreservesAnchorsAndMergeKey` (asserts
  `Contains("<<: *svc")`, which `!!merge <<: *svc` satisfies — the bug is
  invisible to it).
- Probe (this session, yaml.v3 v3.0.1): a 2-space document with blank lines,
  a `# trailing` comment and `<<: *def` round-trips as 4-space, no blank
  lines, `!!merge <<: *def`; `SetIndent(2)` fixes only the indent. Sequences
  under a mapping are always indented by the encoder.
- `internal/core/execution/pipeline/step.go` `StepCommand(step, dweBin)`
  (:122; `case "command"` appends `--set k=%v` from `With`; `case "builtin"`
  goes through `builtin.Describe` at :137, and e.g. `confirm` formats its
  value with `%q` — `builtin/interaction/confirm.go:25` — so a secret with a
  quote, backslash or newline is transformed BEFORE any final redact could
  match it), `DisplayCheck` (:46, `FormatAction`), `DisplayPhaseWhen`.
  **`FormatAction` (:1151) and `FormatCondition` (:1134) live in
  `pipeline/executor.go`**, not `step.go`. `print.go` `printLeafStep`
  (:110–130) computes `cmd := StepCommand(...)` and then
  `UnresolvedTemplateRefs(cmd)` on that same string — so after redaction the
  unresolved scan sees `***`, which is fine (a secret's own `${…}` must not
  leak through a `[unresolved: …]` line either). Callers of `StepCommand`
  — 5 files / 7 sites, all display: `pipeline/print.go:114`,
  `workflow/deploy/print.go:43,45`, `workflow/reset/print.go:35,37`,
  `cli/deploy/deploy.go:165` (`buildPlanStepJSON`),
  `cli/lifecycle/reset.go:723` (`reset step --dry-run`). `FormatCondition`
  also feeds `Recorder.OnStepSkip(..., reason)` (`executor.go:602,781`), but
  `FileRecorder.OnStepSkip` (`file_recorder.go:242-250`) uses `reason` only
  for the `== "state"` early return and never persists it — journal bytes and
  the deployment hash are unaffected by redacting it.
- `internal/shared/trace/trace.go` — `redactor *secrets.Redactor`,
  `RegisterRedaction(values)` (union-only, process-global), `ResetRedaction()`
  (tests), unexported `redact(s)`; applied per argument in `trace.Command`
  (BEFORE quoting, :128-135) and to every line in `emit` (:171), so
  `trace.Decision(... FormatCondition(...))` at `executor.go:782` is already
  redacted today. Single installer: `config.LoadConfig` →
  `registerSecretRedaction` (`workspace.go:1729`); `LoadConfigSanitized`
  registers nothing. `secrets.Redactor` drops values shorter than
  `MinRedactRunes = 4` — a 1–3 rune secret is never redacted anywhere, by
  design. `internal/cli/deploy/menu_test.go:322-329` already loads a config
  carrying `s3cr3t-value`, so that plaintext is registered for the rest of
  the package's test binary.
- `internal/core/validate/secrets/secrets.go` — `recipientValidator` and
  `unresolvedValidator`; both append only through an `emit` that hard-codes
  `SeverityError`. `unresolvedValidator.Run` returns early at :139 when there
  are no `.age` sources (markers are reported from `SecretsState`, no
  identity load) and at :144 loads the identity for `.age` sources but
  discards the `Source`. `inventoryPhrase(markers, sources)` formats
  `"%d encrypted value(s) (e.g. %s) and %d encrypted config-pack source(s) (e.g. %s)"`
  — an error-message helper, not a counter line. `collectEncryptedSources(ctx)`
  lists `.age` sources of enabled app-service packs. Tests pinning silence:
  `TestRecipientValidator_validSetupIsSilent` (:169),
  `TestUnresolvedValidator_decryptedIsSilent` (:225),
  `TestUnresolvedValidator_ageSourceDecryptableIsSilent` (:277); CLI tests
  `internal/cli/validate/validate_test.go:1416-1422` and `:1437-1441` assert
  `Diagnostics[0]` / the scopes list is exactly `secrets/secrets.unresolved:no_identity`
  on a fixture with a VALID recipient — a recipient OK row will sort first.
  JSON scope is `domain + "/" + target` (`validate.go:213`).
- OK-row precedent: `internal/core/validate/tests/tests.go:84-95` (comment
  names this exact "validation skipped" problem); `config/workspace.go:630-637`
  emits OK only when nothing errored. `render.FormatSummary`
  (`diagnostics_table.go:312-332`) prints `validation skipped (no files
  found)` when all counters are zero. Preflight (`preflight.go:169-174`) and
  the pre-wizard gate (`internal/cli/deploy/menu.go:711`) filter
  `SeverityOK` before printing. JSON: `validateJSON{Summary{ok,info,warning,error}, Diagnostics[]}`;
  no golden exists for the secrets scope (`internal/cli/validate/testdata/`
  holds only `validate_config.json.golden`).
- `internal/cli/cmdctx/output.go:56` `ErrWrap` creates a NEW outer
  `CodedError`; the JSON serializer (`:114`) reports the outermost code. So
  `runInit` (`init.go:83`, `secrets_recipient_write_failed`) and `rekey`
  (`rekey.go:124,133`, `secrets_rekey_failed` / `secrets_recipient_write_failed`)
  would swallow an inner splice code unless they branch on `errors.Is` first.
- Text/help that currently calls the shell plan executable:
  `internal/core/workflow/deploy/print.go:10` (comment), `internal/cli/deploy/deploy.go:243`
  (cobra help, "script-friendly"), and `internal/core/workflow/deploy/print_test.go:458`
  (`TestPrintPlanShell_noUnresolvedAnnotation` — assertion-only; its `t.Errorf`
  string says "shell format must stay executable"). `local_node.go:15-32` calls the node
  writer "the sanctioned write path for all three layer files" and promises
  blank-line preservation — both statements change.
- Test file reality: deploy CLI tests are `internal/cli/deploy/plan_test.go`
  (no `deploy_test.go`); shell printer tests are
  `internal/core/workflow/{deploy,reset}/print_test.go`; there are NO golden
  directories under `internal/cli/deploy/`, `internal/cli/lifecycle/` or
  `internal/core/workflow/deploy/` — `plan_test.go:436` says "this is not a
  byte-for-byte golden". ru mirrors exist for `validate.md` and
  `deploy/index.md` (`docs/i18n/ru/reference/config/…`); a translation
  freshness gate covers them. `AGENTS.md` is 40882 B against
  `agentsMdBudget = 40960` (`internal/cli/docs/agentsmd_test.go`): 78 B of
  headroom.
- yaml.v3 node positions (probed, v3.0.1): `Node.Line/Column` of a scalar
  point at the start of its **properties** (`a: &x 1` → Column 4, on `&x`;
  same for `!!str`/`!tag`), `Column` counts **runes** (`scannerc.go:512`),
  `Style` is a bitmask (`TaggedStyle | DoubleQuotedStyle`), a multi-line
  plain scalar has `Style == 0` like a single-line one, flow collections
  report `Style == FlowStyle` on the COLLECTION node, no end position is
  exposed, and `yaml.Unmarshal` silently reads only the first document
  (`LoadYAMLNode:74-81` rejects multi-doc via `yaml.NewDecoder` + a second
  `Decode`). CRLF: positions ignore the `\r`. `config.CollectMarkers`
  records sequence elements by index (`vars.tokens.0`), so markers inside
  collections — including flow ones — are a supported shape that
  `ReplaceScalars` must handle or refuse explicitly.
- Docs: `docs/reference/config/secrets.md` §§ "Diagnostic redaction",
  "Validation and preflight", "Subcommands"; `docs/reference/config/validate.md`
  secrets row; `docs/internals/packages.md` §§ `internal/core/project/local/`,
  Core — Execution (`pipeline/`), Core — Validation, `internal/shared/trace/`.

## Decisions (final — taken in the brainstorm)

- **Byte splice guided by node positions** (Option 1). Not re-encode +
  `SetIndent` (leaves the blank-line loss, fails R9.2), not a second YAML
  library.
- Only `secrets init` / `set` / `rekey` move to the splice writer. `vars set`,
  the services toggle and the setup wizard **stay on the node writer**; the
  `!!merge` defect is fixed there separately (post-encode strip + tightened
  test) because the local.yml path is still exposed to it.
- Insertion of a NEW key goes at the **end of the nearest existing ancestor
  mapping**; a new top-level key (`secrets:` from `init`) goes at end of
  file. Multi-line (`|`, `>`) scalars, flow mappings `{…}` and a `null`
  parent with no children are **refused** with a "materialize it as a
  block mapping / single-line value first" error, file untouched.
- Redaction lives **inside** `pipeline.StepCommand`, `FormatAction` and
  `FormatCondition` (all display-only) — not in a parallel `Display*`
  function that new code could bypass. `--format shell` is redacted too:
  with `***` it is not executable when a step references a secret, which is
  accepted and documented (shell plan is "what will run", not an exec
  artifact).
- OK rows only when a recipient exists. A project with no `secrets:` block
  and no markers stays silent so `dwe validate` output for such projects is
  unchanged.
- **Positions are a locator, not a byte offset.** The Splicer converts
  `(Line, rune Column)` to a byte offset through a line-start table, then
  skips the node's raw properties (`&anchor`, `!tag`) to find the value token;
  properties are preserved verbatim.
- **The multi-line detector is an equality check**: the candidate span text
  must equal `node.Value` (after style-aware unquoting for quoted scalars);
  anything else is refused. yaml.v3 exposes no end position and `Style`
  cannot distinguish a wrapped plain scalar.
- **Flow context is refused for replacement too**, not only insertion: any
  ancestor collection with `Style&yaml.FlowStyle != 0` → `ErrUnsplicable`,
  in `SetScalar` and in `ReplaceScalars`.
- **Mapping end for insertion comes from raw lines** (next sibling key /
  dedent / EOF, honouring block-scalar continuation lines), never from a
  decoded `Node.Value`.
- **Empty / missing / comment-only document** is its own insertion case: the
  whole path is rendered at column 1 and appended after the preserved
  comment bytes.
- **Merge-key refusal only when the leaf key is absent** from the mapping's
  explicit pairs (`local_node.go:257` condition); an explicitly present key
  in a merge-carrying mapping is replaced normally, as today.
- **`!!merge` is fixed structurally**: clear `Tag` on every `<<` key node
  before `yaml.Marshal` (reusing `mappingHasMergeKey`'s predicate) — no
  textual post-processing.
- **Redaction order in `StepCommand`**: redact the string leaves of `With`
  (and `Cmd`) BEFORE `builtin.Describe` / `--set` formatting, then a final
  `trace.Redact` on the assembled string as belt-and-braces. Values shorter
  than `secrets.MinRedactRunes` are not redacted anywhere — the acceptance
  wording is "no registered secret ≥ 4 runes", not "no plaintext".
- **`UnresolvedTemplateRefs` runs on the redacted display string** (that is
  what the callers already do); a secret's own `${…}` must not resurface via
  the `unresolved` field.
- **Splice error codes survive the wrappers**: `init`, `set` and `rekey`
  branch on `errors.Is(err, local.ErrUnsplicable/ErrMultilineScalar/ErrVerify)`
  before their generic `ErrWrap`, so `secrets_write_unsupported` reaches JSON.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next; small, focused changes
- **CRITICAL: every task MUST include new/updated tests** (success and
  error paths; update existing cases when behaviour changes)
- **CRITICAL: all tests must pass before starting next task**
- **CRITICAL: update this plan file when scope changes during implementation**
- `make test` (never bare `go test ./...` on a fresh checkout); focused work
  via `make embedded-docs` once then `go test ./internal/...`
- backward compatibility: every existing `deploy plan` / `reset plan`
  assertion in `internal/cli/deploy/plan_test.go`,
  `internal/cli/lifecycle/reset_test.go` and
  `internal/core/workflow/{deploy,reset}/print_test.go` stays unchanged
  except the one that executes the shell plan; the node writer's output for
  `vars set` stays byte-identical except for the removed `!!merge ` prefix;
  existing `secrets` error strings (`inventoryPhrase`, reason phrases) are
  not reworded
- fixtures for the splice writer live in
  `internal/core/project/local/testdata/` and include one **ficbird-like**
  annotated file (2-space indent, blank lines between blocks, header
  comments, trailing comments, quoted keys, an anchor + `<<:` merge, a
  sequence, a multi-line `|` scalar elsewhere in the file)
- code and config comments in English

## Testing Strategy

- **unit tests**: required for every task
- **byte-diff assertions**: a replacement test asserts `before` and `after`
  are identical outside exactly one line (same line count, index-wise
  compare); an insertion test asserts `after == prefix + inserted + suffix`
  with `prefix`/`suffix` taken verbatim from `before` (index-wise compare
  cannot work — the suffix shifts). A small test helper in `local`, no new
  dependency. Never `Contains`
- **plan redaction tests**: substring + negative-substring assertions in the
  existing test files (`plan_test.go`, `reset_test.go`, the two
  `print_test.go`), matching those packages' style; no golden files
- **redactor hygiene**: every secret-bearing test calls
  `trace.ResetRedaction()` before registering and in `t.Cleanup`, uses a
  distinctive ≥4-rune plaintext that appears in no other fixture of the
  package, and never `t.Parallel` (the redactor is process-global,
  union-only)
- **negative-substring tests**: plan outputs assert the absence of the test
  plaintext and the presence of `***`; splice error paths assert the absence
  of plaintext and `AGE-SECRET-KEY-` in error, stdout, stderr and JSON
- **e2e**: none; manual pass on `../ficbird` (Post-Completion)

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix

## Solution Overview

### R9 — splice writer

```
src []byte ──yaml.Unmarshal──▶ *yaml.Node (Line/Column per node)
     │                               │
     │            resolve path ──────┘  (findMappingPair, merge-key rule reused)
     ▼
SpliceScalar   : replace [start,end) of the scalar token on its line
SpliceInsert   : insert rendered lines after the ancestor's last line
SpliceScalars  : N SpliceScalar, applied bottom-up so positions stay valid
     │
     ▼
verify: yaml.Unmarshal(result) parses AND value-at-path == expected
     │
     ▼
caller: ValidateLayerRoots(staged) → writeFileAtomic
```

New file `internal/core/project/local/local_splice.go` (package `local`, same
label/policy vocabulary as the node writer):

```go
// Splicer edits a layer file by replacing or inserting the bytes of single
// nodes, leaving every other byte — indentation, blank lines, comments,
// anchors, merge keys — untouched. It is the writer for edits whose diff
// must be reviewable (dwe secrets set/init/rekey). ApplyOverlay +
// WriteYAMLNode remain the writer for structural edits.
type Splicer struct { src []byte; doc *yaml.Node; label string; indent int }

func NewSplicer(path, label string) (*Splicer, error)       // reads + parses (yaml.NewDecoder, second Decode
                                                             // rejects multi-doc as LoadYAMLNode does);
                                                             // missing/empty/comment-only → Kind==0 doc, src kept verbatim
func (s *Splicer) SetScalar(path []string, value string) error  // replace or insert; re-parses src on success
func (s *Splicer) ReplaceScalars(fn func(string) (string, bool, error)) (int, error)
func (s *Splicer) Bytes() []byte                             // current result
func (s *Splicer) Write(path string, policy WritePolicy) error // verify + writeFileAtomic
```

**Locating a scalar.** A line-start byte table is built once per parse;
`(Line, Column)` → byte offset by advancing `Column-1` runes from the line
start (yaml.v3 counts runes, not bytes). From there the raw **properties**
are skipped and preserved: an optional `&anchor` and/or `!tag`/`!!tag`
token, each followed by spaces — yaml.v3 reports the node position at the
first property, not at the value. The value token then starts at the
current offset.

**Token span for replacement** (by `Style & (DoubleQuoted|SingleQuoted|Literal|Folded)`):
- double-quoted: scan for the closing `"` honouring `\` escapes; single-quoted:
  scan for a `'` not followed by `'`. The closing quote must lie on the same
  line as the opening one, else `ErrMultilineScalar` (quoted scalars may
  legally wrap).
- plain: candidate = from the value start to end of line, minus a trailing
  `\r`, minus a trailing ` #…` comment, right-trimmed of spaces/tabs. The
  **detector** is `candidate == node.Value`; a mismatch (a wrapped plain
  scalar, an unexpected token) → `ErrMultilineScalar`. For quoted scalars
  the same check runs on the unquoted text.
- `Literal`/`Folded` → `ErrMultilineScalar`.
- any ancestor collection with `Style & yaml.FlowStyle != 0` → `ErrUnsplicable`
  ("flow mapping/sequence at `<path>`; write it as a block collection").

Replacement text: `yaml.Marshal` of a fresh scalar node with the style
`encodeValueNode` would choose, newline trimmed; the `ENC[age:…]` marker
encodes plain. Only the value token is replaced — properties, the comment,
its spacing and the line ending are untouched. Line endings are handled
per line: the `\r` is excluded from the span and stays.

**Insertion** (`SetScalar` on a missing path). Walk the path from the root;
let `P` be the deepest EXISTING node and `k` the first missing segment:
- root document is empty / `Kind == 0` (missing, empty or comment-only file):
  render the whole path as a block subtree at column 1 and append it after
  the preserved bytes (a comment-only prefix is kept verbatim; a trailing
  newline is ensured first).
- `P` is a block mapping: `k` is added to `P`. If `P` is the root mapping,
  append at EOF (trailing newline ensured; one blank line before the new
  block when the file already separates top-level blocks with blank lines —
  detect: a blank line directly before some column-1 key). Otherwise insert
  after `P`'s **physical end**, found on raw lines: starting at the line of
  `P`'s last pair, advance while the next line is blank, a comment, or
  indented deeper than `P`'s key column (this covers block-scalar
  continuation lines and folded scalars, which the decoded `Value` cannot);
  the insertion point is the line after the last non-blank, non-comment
  line so found — blank lines and trailing comments that follow stay after
  the new lines and, on re-parse, remain attached where they were. Child
  column = `P`'s first key column; the new subtree is rendered with
  `yaml.NewEncoder` + `SetIndent(s.indent)` and re-indented by prefixing
  each line with `childColumn-1` spaces; lines are terminated with the
  file's dominant line ending (`\r\n` if the majority of existing lines end
  so).
- `P` is a null scalar (`vars:` with nothing under it), a sequence, a flow
  collection, or `P` carries a merge key and `k` is absent →
  `ErrUnsplicable` (message names the path and the fix: "materialize
  `<path>` as a block mapping in `<file>` first").

`s.indent` is detected as the column delta of the first nested block
mapping pair; default 2.

**Merge-key rule**: identical to `applyOverlayToMapping` (`local_node.go:257`)
— refuse only when the leaf key is absent AND `mappingHasMergeKey(P)`; an
explicitly present key is replaced. **Alias nodes**: `ReplaceScalars` visits
the anchored definition once (same as today). **Multiple edits**: after each
successful `SetScalar` the Splicer re-parses `Bytes()` so node offsets are
fresh. `ReplaceScalars` walks every value scalar of one snapshot and calls
`fn` FIRST; only for scalars `fn` accepted (`ok == true`) does it then
check splice-ability (flow ancestor, multi-line → error, never a silent
skip); a `|`/`>` scalar `fn` declines is simply left alone — a real
annotated layer always has some. Accepted spans are applied bottom-up; the
first error from `fn` or from the span check aborts with `Bytes()` unchanged
(the callback returns `(string, bool, error)` because `rekey`'s callback
calls `secrets.Encrypt`).

**Raw-line classifier order** (insertion): a line is tested for "indented
deeper than `P`'s key column" BEFORE "is a comment line", so a literal
scalar whose last content line starts with `#` at content indent is
continuation, not a comment.

**Alias parent**: an ancestor that is an `AliasNode` (`vars: *common`) →
`ErrUnsplicable` (the node writer refuses this today at `local_node.go:265-268`;
descending into the anchored definition would edit a shared block).

`Write` verifies before persisting: re-parse `Bytes()` (multi-doc rejected);
for every `SetScalar` recorded, the value at the path must equal the
requested value; for a `ReplaceScalars` edit, the set of scalars for which
`fn` returned `ok` must read back as the returned replacements
(`ErrVerify` otherwise — file untouched). Callers keep their own
`ValidateLayerRoots` staging on `Bytes()`.

**Node writer fix** (`EncodeYAMLNode`): before `yaml.Marshal`, walk the
document and clear `Tag` on every mapping key node with `Tag == "!!merge"`
(probe-confirmed: yaml.v3 then emits `<<: *x` cleanly). No regexp, no
post-processing. The merge-key test asserts `NotContains("!!merge")`. The
file's package comment (`local_node.go:15-32`) is rewritten: two writers,
which edits use which, and that the node writer does NOT preserve blank
lines.

### R10 — redaction as a display property

`trace.Redact(s string) string` exported (wraps the package redactor;
identity when none installed). Applied inside `pipeline.StepCommand`
(`step.go`), `pipeline.FormatAction` and `pipeline.FormatCondition`
(`executor.go`). In `StepCommand` the order matters: redact `step.Cmd` and
every string leaf of `step.With` **before** `builtin.Describe` and the
`--set k=v` formatting (a `%q`-quoted or escaped secret no longer equals the
registered string), then `trace.Redact` the assembled result as a final
guard. **The redaction writes into a deep copy** of `With` (nested maps and
slices included) — `DeployStep` is passed by value but `With` is a map
shared with the executor (`DeployStep.Action()` at `config/workspace.go:607`
hands the same map to `ExecAction`), and `cli/lifecycle/reset.go:723` calls
`StepCommand` unconditionally BEFORE the `--dry-run` branch and before the
real `ExecAction` at :749: an in-place redaction would make a real
`dwe reset step <addr>` execute with `***` as every secret param. A test
asserts the caller's `step.With` is unchanged after `StepCommand`.
`FormatAction`/`FormatCondition` format only `Cmd`/`Expr` strings (no
maps) and get a final `Redact`. Each of the three carries a comment:
display-only, redacted, never fed to execution (verified by grep of every
caller in Task 4; the one non-display consumer — `OnStepSkip`'s `reason` in
`pipeline/file_recorder.go` — never persists it, see Context).

`UnresolvedTemplateRefs` keeps being computed on the string the callers
already hand it — the redacted display string — so a secret containing
`${…}` cannot resurface through the `unresolved` field or the
`[unresolved: …]` line. `buildPlanStepJSON`'s `Cmd`, the two shell printers
and `reset step --dry-run` inherit the change with no edit.

Known limit, stated in docs and pinned by a test: values shorter than
`secrets.MinRedactRunes` (4) are not redacted on any surface.

A pipeline-level test resolves a fixture whose `cmd`, `with:` (incl. a
builtin `confirm` whose value holds `"`, `\` and a newline), `check:` and
shell `when:` all reference a secret var, registers the plaintext, and
asserts every display string contains `***` and not the plaintext.

### R11 — OK rows

`recipientValidator`: after its checks pass (`diags` empty) and
`secrets.recipient` is set → one `SeverityOK` with
`Target: "secrets.recipient"`, `File: workspace.yml`, `Message: ""` (matches
the config domain style; the table shows the target). A valid recipient next
to an unreadable marker still gets this row — the marker is the other
validator's finding.

`unresolvedValidator.Run` is restructured (the current early return at
`:139` skips marker-only projects before any source is known):

1. build the full inventory: decrypted + unresolved markers from
   `ctx.Cfg.SecretsState`, plus `collectEncryptedSources(ctx)`;
2. inventory empty → return nil (unchanged behaviour, no rows);
3. marker diagnostics as today (`unresolvedMarkerDiags`);
4. identity source: `ctx.Cfg.SecretsState.IdentitySource` when there are
   markers; for an `.age`-only project the `LoadIdentity` call at `:144`
   keeps its `Source` instead of discarding it;
5. `.age` source checks as today;
6. exactly when no diagnostic was produced → one `SeverityOK` with
   `Target: "secrets.unresolved"` and a NEW counter message
   `"%d encrypted value(s) and %d config-pack source(s) readable via %s"`
   (do not reuse `inventoryPhrase` — it is the error-message helper with
   `(e.g. …)` examples; its existing strings are not changed).

Mixed state (valid recipient, one unreadable marker): `secrets.recipient` OK
row + `secrets.unresolved:<reason>` error row, no unresolved OK row. No
recipient / no inventory → no rows. Preflight and the pre-wizard gate already
filter OK rows; both keep a buffered non-TTY test proving nothing extra is
printed. JSON scopes are `secrets/secrets.recipient` and
`secrets/secrets.unresolved`; the recipient row's `message` is empty; neither
row carries an identity path beyond the source word or any key material.

## Technical Details

### Files touched by R9

- Create `internal/core/project/local/local_splice.go`, `local_splice_test.go`,
  `testdata/annotated_defaults.yml` (+ expected outputs per case).
- Modify `internal/cli/secrets/set.go` (`writeMarker` → `Splicer`),
  `init.go` (`writeRecipient` → `Splicer`), `rekey.go` (`rekeyLayerFile` →
  `Splicer.ReplaceScalars`), their tests (assert line-diff, keep the existing
  comment/anchor/mode assertions).
- Modify `internal/core/project/local/local_node.go` (`EncodeYAMLNode`
  `!!merge` strip), `local_node_test.go`.

### Error vocabulary (R9)

`ErrMultilineScalar`, `ErrUnsplicable`, `ErrVerify` in `local`; the CLI maps
them to `secrets_write_unsupported` (hint: "make `<path>` a single-line
value / block mapping in `<file>` and retry") via an `errors.Is` branch
placed BEFORE the generic wrappers in all three commands — `set`'s write
path, `init`'s `secrets_recipient_write_failed` (`init.go:83`) and `rekey`'s
`secrets_rekey_failed` / `secrets_recipient_write_failed` (`rekey.go:124,133`,
which keep their recovery hint for every other error). I/O failures keep
today's codes.

### Files touched by R10

- Modify `internal/shared/trace/trace.go` (+ `trace_test.go`);
  `internal/core/execution/pipeline/step.go` (`StepCommand`) and
  `executor.go` (`FormatAction`, `FormatCondition`); new
  `pipeline/redact_test.go`; `internal/cli/deploy/plan_test.go`,
  `internal/cli/lifecycle/reset_test.go`,
  `internal/core/workflow/{deploy,reset}/print_test.go` gain secret-bearing
  substring assertions (no golden files — none exist in these packages and
  none are introduced). Wording: `internal/core/workflow/deploy/print.go:10`
  comment, `internal/cli/deploy/deploy.go:243` cobra help, and
  `workflow/deploy/print_test.go:458` (its `t.Errorf` string, not a comment)
  stop calling the shell plan executable / script-friendly.

### Files touched by R11

- Modify `internal/core/validate/secrets/secrets.go` (`emitOK` helper or an
  `emit` severity parameter, the restructured `unresolvedValidator.Run`),
  `secrets_test.go` (three `…IsSilent` tests → `…EmitsOK`);
  `internal/cli/validate/validate_test.go:1416-1422` and `:1437-1441` (the
  recipient OK row now sorts first) plus a healthy-fixture JSON test.

## What Goes Where

- **Implementation Steps**: code, tests, docs in this repo.
- **Post-Completion**: the `../ficbird` pass.

## Implementation Steps

### Task 1: Splicer core — scalar replacement

**Files:**
- Create: `internal/core/project/local/local_splice.go`
- Create: `internal/core/project/local/local_splice_test.go`
- Create: `internal/core/project/local/testdata/annotated_defaults.yml`

- [x] implement `NewSplicer` (read + `yaml.NewDecoder` with the second
      `Decode` multi-doc rejection copied from `LoadYAMLNode:74-81`;
      missing/empty/comment-only file → `Kind == 0` doc with `src` kept
      verbatim), the line-start byte table, rune-column → byte-offset
      conversion, property skipping (`&anchor`, `!tag`), dominant-EOL and
      indent detection, `Bytes()`
- [x] implement `SetScalar` for the **existing-scalar** case: path
      resolution via `findMappingPair`; flow-ancestor refusal; token span by
      style with the `candidate == node.Value` detector; replacement rendered
      from a fresh scalar node; `ErrMultilineScalar` for `|`/`>`/wrapped
      plain/wrapped quoted; re-parse after success
- [x] implement `Write` (re-parse `Bytes()` + value-at-path verification,
      then `writeFileAtomic`)
- [x] tests (table, byte-diff helper): plain → marker, `""` → marker,
      double-quoted with trailing `# comment` → only the token changes and
      the comment keeps its spacing, single-quoted with `''` escape,
      double-quoted with `\"`, anchored plain `a: &x 1` and anchored quoted
      value → anchor preserved, explicit `!!str` tag preserved, non-ASCII
      text (Cyrillic key/comment) earlier on the line and on earlier lines →
      correct offset, key present explicitly in a merge-carrying mapping →
      replaced, key that exists only via `<<:` → refused, multi-line literal
      / folded / wrapped plain / wrapped double-quoted → `ErrMultilineScalar`
      and `Bytes()` unchanged, scalar inside a flow mapping and inside a flow
      sequence → `ErrUnsplicable`, 4-space file stays 4-space, CRLF file
      keeps `\r\n` on the TOUCHED line, two consecutive `SetScalar` calls on
      different lines both land, multi-doc file → error
- [x] test: the annotated fixture — one `SetScalar` → exactly ONE line
      differs, byte-equal elsewhere (R9.4)
- [x] run `go test ./internal/core/project/local/...` — must pass before task 2

➕ Task 1 notes: an implicit-null leaf (`key:` with no value) is a supported
replacement target — the span is empty and the rendered value gains its own
separating space. `SetScalar` on a **missing** path currently returns
`ErrUnsplicable`; Task 2 replaces that branch with the insertion path.
`Splicer.eol` / `Splicer.indent` are detected in Task 1 and first consumed by
Task 2's insertion rendering.

### Task 2: Splicer — insertion and bulk replace

**Files:**
- Modify: `internal/core/project/local/local_splice.go`, `local_splice_test.go`

- [x] implement insertion in `SetScalar` per the three cases in Solution
      Overview: empty/`Kind == 0` root (append after preserved comment
      bytes), block-mapping parent (root → EOF with the blank-line
      heuristic; nested → physical end found on raw lines: skip blank /
      comment / deeper-indented lines after the last pair), child column,
      subtree rendered with `SetIndent(indent)` and re-indented, dominant
      EOL on inserted lines; `ErrUnsplicable` for flow / `null` / sequence
      parent and for a merge-carrying parent when the key is absent
- [x] implement `ReplaceScalars(fn func(string) (string, bool, error))` —
      on one snapshot call `fn` for every value scalar FIRST; check
      splice-ability (flow ancestor, multi-line → error, never a silent
      skip) only for scalars `fn` accepted; declined `|`/`>` scalars are
      left alone; apply accepted spans bottom-up and re-parse; alias nodes
      skipped; first error aborts with `Bytes()` unchanged
- [x] alias-parent refusal (`AliasNode` on the path → `ErrUnsplicable`) and
      the deeper-indent-before-comment classifier order in the raw-line scan;
      `Write` verification for `ReplaceScalars` edits (accepted scalars read
      back as their replacements)
- [x] tests (prefix/insert/suffix assertions): insert `vars.new.key` under
      an existing `vars:` whose last pair is (a) a plain scalar followed by
      a trailing comment line and a blank line before the next top-level key,
      (b) a folded `>` scalar, (c) a literal `|` scalar — in each case the
      new line lands after the pair's physical end and a re-parse shows the
      trailing comment still after the mapping; insert into a missing file,
      an empty file and a comment-only file (comment bytes preserved
      verbatim); `secrets.recipient` into a `workspace.yml` without a
      `secrets:` key → appended at EOF (the first missing segment is a ROOT
      key, path length 2); with / without blank-line separation; nested
      insert two levels deep (`vars.a.b.c` with only `vars.a` present)
      renders both levels; CRLF file → inserted lines end with `\r\n`; file
      without a final newline → newline added before the block, nothing
      else changes; flow-mapping / null / sequence parent → `ErrUnsplicable`;
      `ReplaceScalars` on a file with three markers AND an unrelated `|`
      scalar → exactly three lines differ, one of them the last line of a
      fixture WITHOUT a trailing newline (preserved as-is); a marker inside
      a flow sequence → error, bytes unchanged; `fn` returning an error →
      count 0, bytes unchanged; insertion under a mapping whose last pair is
      a literal scalar with a `#`-leading content line → lands after the
      block, not inside it; alias parent (`vars: *common`) →
      `ErrUnsplicable`; failed `Write` verification → file untouched
- [x] run `go test ./internal/core/project/local/...` — must pass before task 3

➕ Task 2 notes: a **top-level** insertion is asserted by exact byte comparison
(`want == src + block`) rather than through the prefix/insert/suffix helper —
appending makes the whole prior file the prefix, so equality is the stronger
statement and avoids the helper's ambiguity about which side of the file's final
empty line the block lands on. The nested cases use the line-based
`insertedLines`/`assertInsertion` helper as planned. `ReplaceScalars`
verification is keyed by the scalar's **dotted node path** (sequence elements by
index, e.g. `tokens.0`) rather than by walk position, so a later `SetScalar`
insertion cannot invalidate a recorded expectation; markers inside a BLOCK
sequence are therefore supported, flow ones are refused. `SetScalar` on a
missing path is no longer `ErrUnsplicable`, so that case left the Task 1
refused-shapes table.

### Task 3: Move `secrets init` / `set` / `rekey` onto the Splicer; fix `!!merge` in the node writer

**Files:**
- Modify: `internal/cli/secrets/set.go`, `init.go`, `rekey.go`
- Modify: `internal/cli/secrets/set_test.go`, `init_test.go`, `rekey_test.go`
- Modify: `internal/core/project/local/local_node.go`, `local_node_test.go` (`!!merge` fix; `ReplaceScalars` tests ported)

- [x] `writeMarker`: `NewSplicer` → `SetScalar` → `ValidateLayerRoots` on
      `Bytes()` (existing `stageLayers`) → `Write`; an `errors.Is` branch on
      the three splice sentinels BEFORE the generic wrap yields
      `secrets_write_unsupported` with the hint
- [x] `writeRecipient` (init + rekey phase 5) → Splicer insert / replace;
      `runInit` (`init.go:83`) and `runRekey` (`rekey.go:124,133`) branch on
      the sentinels before `secrets_recipient_write_failed` /
      `secrets_rekey_failed` so the code survives; the rekey recovery hint
      is kept on every other error
- [x] `rekeyLayerFile` → `Splicer.ReplaceScalars` with the error-returning
      callback (the `failure` closure goes away)
- [x] delete the package-level `local.ReplaceScalars(doc, fn)`
      (`local_node.go:147`) in THIS task, after `rekeyLayerFile` migrates:
      its callers are `rekey.go:250`, the two `local_node_test.go` tests
      (`TestReplaceScalars` :748, `TestReplaceScalars_NilInputs` :812) and
      the `rekey_test.go:461` helper `reencryptFileForTest` (builds
      crash-state fixtures) — port the helper and the two tests to the
      Splicer; the method and the function coexist until then
- [x] `EncodeYAMLNode`: clear `Tag` on `<<` key nodes before `yaml.Marshal`;
      tighten `TestWriteYAMLNode_PreservesAnchorsAndMergeKey` to
      `NotContains("!!merge")`; rewrite the `local_node.go:15-32` package
      comment (two writers, which edit uses which, blank lines are NOT
      preserved by the node writer)
- [x] tests: `TestSet_PreservesCommentsAndSiblings` and
      `TestInit_PreservesCommentsAnchorsAndMode` become byte-diff assertions
      on the annotated fixture (copy it into `internal/cli/secrets/testdata/`);
      `rekey` on the fixture with two markers → exactly two lines differ in
      `defaults.yml` and one in `workspace.yml`; `set` on a multi-line target
      and on a flow-mapping target → typed `secrets_write_unsupported` in
      text AND JSON (`init`, `set`, `rekey` each), file untouched, no
      plaintext / `AGE-SECRET-KEY-` in error, stdout, stderr or JSON; `set` of
      a NEW path into the fixture → only the inserted lines differ; `set`
      into a project without `defaults.yml` still creates it (the default
      target — `TestSet_WorkspaceFile` stays green)
- [x] run `go test ./internal/cli/secrets/... ./internal/core/project/local/...` — must pass before task 4

➕ Task 3 notes: `secrets set` through an existing scalar (`vars.db.host.port`
where `host` is a string) now reports `secrets_write_unsupported` instead of
`secrets_write_failed` — the node writer refused it as an overlay error, the
Splicer refuses it as a shape, and the new code is the more actionable of the
two; `TestSet_Refusals` was updated. The three sentinels are mapped by one
shared `spliceUnsupportedError` / `spliceWriteError` pair in
`internal/cli/secrets/secrets.go` rather than per command. Two fixtures were
added (`testdata/annotated_defaults.yml`, `annotated_workspace.yml`) instead of
one: `init` and rekey phase 5 write `workspace.yml`, whose annotated shape must
carry `schema_version`/`project:` to load at all. The ported node-writer
`ReplaceScalars` coverage landed as `TestSplicer_ReplaceScalars_RekeyPrimitiveShape`
plus `…_NilCallback` (a nil document is unreachable through a Splicer, which
always carries its own parse).

### Task 4: Redact plan surfaces through the display functions

**Files:**
- Modify: `internal/shared/trace/trace.go`, `trace_test.go`
- Modify: `internal/core/execution/pipeline/step.go` (`StepCommand`), `executor.go` (`FormatAction`, `FormatCondition`)
- Create: `internal/core/execution/pipeline/redact_test.go`
- Modify: `internal/cli/deploy/plan_test.go`, `internal/cli/deploy/deploy.go` (help text at :243)
- Modify: `internal/cli/lifecycle/reset_test.go` (`reset plan`, `reset step --dry-run`)
- Modify: `internal/core/workflow/deploy/print.go` (:10 comment), `print_test.go` (:458), `internal/core/workflow/reset/print_test.go`

- [ ] export `trace.Redact(s)`; document that it is the same redactor
      `trace.Command` and `emit` apply and that `LoadConfig` is its single
      installer
- [ ] `StepCommand`: redact `step.Cmd` and every string leaf of `step.With`
      BEFORE `builtin.Describe` / `--set` formatting, then `trace.Redact` the
      result; same leaf-first + final pattern in `FormatAction` and
      `FormatCondition`; grep every caller of the three and add the
      display-only/redacted contract comment on each
- [ ] reword the shell-plan comment (`print.go:10`), the cobra help
      (`deploy.go:243`, "script-friendly") and the wording of
      `print_test.go:458` (`TestPrintPlanShell_noUnresolvedAnnotation` — it
      asserts on annotations, does not execute the script; only its comment
      changes): the script is a redacted preview, not executable when a
      step references a secret
- [ ] test: `StepCommand` leaves the caller's `step.With` (nested map +
      slice) byte-identical — the deep-copy guard for `reset step` without
      `--dry-run`
- [ ] tests (pipeline, `redact_test.go`): fixture with a secret in `cmd`,
      in `with:` of a `type: command` step, in `with:` of a builtin
      `confirm` whose value holds `"`, `\` and a newline, in `check:` and in
      shell `when:`; after `ResetRedaction` + `RegisterRedaction`, each
      display string has `***` and not the plaintext; `UnresolvedTemplateRefs`
      on the display string of a secret containing `${x}` reports nothing;
      a 3-rune secret is NOT redacted (pins `MinRedactRunes`);
      `ResetRedaction` in `t.Cleanup`, no `t.Parallel`
- [ ] tests (cli + workflow printers): `deploy plan` table / `--format
      shell` / `--output json` (incl. the `unresolved` field) and
      `reset plan` / `reset step --dry-run` on a project with a marker and a
      usable identity (`DWE_AGE_KEY`) → no plaintext, `***` present; each
      test `ResetRedaction`s before and in cleanup and uses a plaintext
      unique to the package (the deploy package already registers
      `s3cr3t-value` via `menu_test.go`); existing secret-free assertions
      unchanged
- [ ] run `go test ./internal/shared/trace/... ./internal/core/execution/pipeline/... ./internal/core/workflow/deploy/... ./internal/core/workflow/reset/... ./internal/cli/deploy/... ./internal/cli/lifecycle/...` — must pass before task 5

### Task 5: `validate secrets` emits OK rows on a healthy project

**Files:**
- Modify: `internal/core/validate/secrets/secrets.go`, `secrets_test.go`
- Modify: `internal/cli/validate/validate_test.go` (+ `testdata/` golden if the scope has one)

- [ ] `recipientValidator`: OK row when the recipient is set, valid, and
      `diags` is empty
- [ ] `unresolvedValidator.Run` restructured in the six-step order from
      Solution Overview (inventory first, early nil on empty, source from
      `SecretsState.IdentitySource` or the kept `LoadIdentity` source for
      `.age`-only projects, OK row only when no diagnostic was produced,
      new counter message — `inventoryPhrase` untouched)
- [ ] tests: `TestRecipientValidator_validSetupIsSilent`,
      `TestUnresolvedValidator_decryptedIsSilent` and
      `TestUnresolvedValidator_ageSourceDecryptableIsSilent` become
      `…EmitsOK` (severity, target, file, message incl. the source word);
      marker-only healthy project → OK row (was unreachable before the
      restructure); no-recipient project → zero rows;
      `TestRecipientValidator_corruptMarker` → no recipient OK row; mixed
      state (valid recipient, one unreadable marker) → recipient OK row +
      `secrets.unresolved:<reason>` error row, no unresolved OK row; CLI:
      `internal/cli/validate/validate_test.go:1416-1422` and `:1437-1441`
      updated for the leading recipient OK row; healthy fixture → text shows
      the ✓ rows and `validation result: 2 checks`, JSON has
      `summary.ok == 2`, scopes `secrets/secrets.recipient` (empty
      `message`) and `secrets/secrets.unresolved`, no identity path or key
      material; preflight and the pre-wizard gate on the same project
      (buffered, non-TTY) print nothing extra
- [ ] run `go test ./internal/core/validate/... ./internal/cli/validate/... ./internal/core/execution/preflight/...` — must pass before task 6

### Task 6: Documentation and changelog

**Files:**
- Modify: `docs/reference/config/secrets.md`, `docs/i18n/ru/reference/config/secrets.md`
- Modify: `docs/reference/config/validate.md`, `docs/i18n/ru/reference/config/validate.md`
- Modify: `docs/reference/config/deploy/index.md`, `docs/i18n/ru/reference/config/deploy/index.md` (resolve the exact page describing `deploy plan` output before starting; both halves change together for the translation freshness gate)
- Modify: `docs/internals/packages.md`, `AGENTS.md`, `CHANGELOG.md`

- [ ] `secrets.md`: "Subcommands" — `set`/`init`/`rekey` change only the
      touched lines, list the refused shapes (multi-line scalar, wrapped
      quoted scalar, flow collection, null parent) and the fix, and the
      `secrets_write_unsupported` code in the error-code list; "Diagnostic
      redaction" → rename to "Redaction" and state: `-v` traces AND every
      plan / dry-run surface are redacted; `--format shell` is therefore not
      executable for secret-bearing steps; values under 4 runes are never
      redacted; redaction is not an access boundary — `dwe vars`,
      `secrets get` print plaintext by design (R10.4); "Validation and
      preflight" — the healthy output now shows two ✓ rows; ru mirror
- [ ] `validate.md` (+ ru): secrets row mentions the OK rows
- [ ] `deploy/index.md` (+ ru): one paragraph — plan output is redacted, the
      shell format is a preview
- [ ] `packages.md`: `internal/core/project/local/` — two writers, when to
      use which, the `!!merge` tag-clearing, the Splicer contracts
      (rune-column → byte offset, properties skipped, `candidate == Value`
      detector, raw-line mapping end, verify-before-write, refused shapes);
      Core — Execution (`pipeline/`) — `StepCommand`/`FormatAction`/
      `FormatCondition` are display-only, redact leaves before formatting,
      never feed execution; `shared/trace` — `Redact` export;
      Core — Validation — secrets OK rows and the six-step order
- [ ] `AGENTS.md` "Encrypted secrets" bullet: **net-zero byte delta** —
      tighten an existing clause to pay for one sentence (splice writer for
      secrets, redaction inside the display functions); `wc -c AGENTS.md`
      ≤ 40960, line ≤ 600 runes; run `go test ./internal/cli/docs/ -run TestAgentsMd`
      in this task
- [ ] `CHANGELOG.md` `## [Unreleased]`: format-preserving secrets writes,
      `!!merge` fix, plan redaction (incl. shell format), `validate secrets`
      OK rows
- [ ] `make build`, `go test ./internal/core/docs/... ./internal/cli/docs/...` — must pass before task 7

### Task 7: Verify acceptance criteria

- [ ] `make test`, `make lint`
- [ ] scratch project: `secrets set vars.x.y v` on a hand-annotated
      `defaults.yml` (2-space, blank lines, comments, quoted keys, anchor +
      `<<:`) → `git diff --stat` shows `1 insertion(+), 1 deletion(-)`;
      `secrets init` on an annotated `workspace.yml` → only the appended
      block; `rekey` → one changed line per marker + the recipient line
- [ ] `dwe deploy plan`, `deploy plan --format shell`, `deploy plan --output json`,
      `reset plan`, `reset step <addr> --dry-run` with a step
      `echo 'token is ${vars.telegram.bot_token}'` and a builtin `confirm`
      whose message embeds the token → `***`, no registered secret ≥ 4
      runes anywhere in the output; the same step under `dwe run -v` →
      `***` (unchanged)
- [ ] `dwe validate secrets` on the healthy scratch project → two ✓ rows,
      `validation result: 2 checks`; `--output json` → `summary.ok: 2`; on a
      project without secrets → output identical to the pre-change binary
- [ ] `../ficbird` (759-line annotated `defaults.yml`, one marker, one
      `.age`): `secrets set vars.telegram.bot_token <same value>` on a clean
      tree → one-line diff; `deploy plan` → no token; `validate secrets` → ✓
      rows; revert the ficbird change afterwards, commit nothing there

### Task 8: [Final] Update documentation

- [ ] re-read the touched docs against the shipped strings (copy from goldens)
- [ ] record any ➕ deviations in `packages.md`
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

**Manual verification**
- The `../ficbird` pass in Task 7 is the sign-off; paste the `git diff --stat`
  line and the redacted `deploy plan` excerpt into the PR description.

**Known limitation to state in the PR**
- Blank lines and comments are preserved because the Splicer never
  re-encodes the document; any future "structural" edit through the node
  writer (`vars set`, toggles, wizard) still loses blank lines. Migrating
  those writers to the Splicer is a separate decision, deliberately not
  taken here.
