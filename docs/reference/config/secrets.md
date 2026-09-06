# `dwe secrets` — encrypted values committed to the repository

`vars:` is the home for free-form project values, but it has no answer for a
value the whole team shares and that must not sit in the open in git: a test
Telegram bot token, a Google service-account JSON. `workspace/local.yml` is
gitignored, so such values get pasted around by hand and drift between machines.

`dwe secrets` closes that gap: values are **encrypted at rest and committed**,
decrypted in memory at config load time, and never written back into a tracked
file as plaintext.

## Contents

- [The model](#the-model)
- [Two shapes: scalars and whole files](#two-shapes-scalars-and-whole-files)
- [The `secrets:` block](#the-secrets-block)
- [Keys: where the identity lives](#keys-where-the-identity-lives)
- [Getting started](#getting-started)
- [New developer / new machine](#new-developer--new-machine)
- [Subcommands](#subcommands)
  - [`dwe secrets init`](#dwe-secrets-init)
  - [`dwe secrets status`](#dwe-secrets-status)
  - [`dwe secrets set`](#dwe-secrets-set)
  - [`dwe secrets get`](#dwe-secrets-get)
  - [`dwe secrets encrypt` / `decrypt`](#dwe-secrets-encrypt--decrypt)
  - [`dwe secrets key export` / `import`](#dwe-secrets-key-export--import)
  - [`dwe secrets key list`](#dwe-secrets-key-list)
  - [`dwe secrets key remove`](#dwe-secrets-key-remove)
  - [`dwe secrets rekey`](#dwe-secrets-rekey)
- [Without a key: what still works](#without-a-key-what-still-works)
- [Output guards: no marker ever reaches a rendered file](#output-guards-no-marker-ever-reaches-a-rendered-file)
- [Validation and preflight](#validation-and-preflight)
- [Where plaintext goes](#where-plaintext-goes)
- [Redaction](#redaction)
- [Container behavior](#container-behavior)
- [`age` CLI interoperability](#age-cli-interoperability)
- [JSON output](#json-output)
- [Non-goals](#non-goals)
- [Related references](#related-references)

## The model

One **X25519 age key pair per project**:

- The **public recipient** (`age1…`) is committed to `workspace.yml` under
  `secrets.recipient`. Encryption needs nothing else, so **anyone with the
  repository can add a secret**.
- The **private identity** (`AGE-SECRET-KEY-1…`) never enters git. It lives in
  `~/.config/dwe/keys/<recipient>.key`, or in `DWE_AGE_KEY` /
  `DWE_AGE_KEY_FILE` for CI. Only identity holders can **read** a secret.

Decryption happens once, in the config loader. `${vars.*}`, `exports.env`,
`dwe vars`, the deployment hash and every other consumer of the merged config
see plaintext and need no knowledge of encryption at all.

The format is [age](https://age-encryption.org) — a standard, audited file
format. A marker payload is a base64-wrapped age file, so the `age` CLI opens
it directly; see [`age` CLI interoperability](#age-cli-interoperability).

## Two shapes: scalars and whole files

**Scalar values.** Any string in any config layer (`workspace.yml`,
`workspace/defaults.yml`, `workspace/local.yml`) may be an `ENC[age:…]` marker:

```yaml
# workspace/defaults.yml — tracked by git
vars:
  telegram:
    token: ENC[age:YWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUx…]
```

The marker grammar is strict: `ENC[age:<base64>]`, the **whole scalar or
nothing**. A string that merely *contains* `ENC[` — a log line, a regex, a
comment — is ordinary data and is left alone.

The decrypt pass is path-agnostic (a marker works anywhere in the tree), but
`dwe secrets set` writes only under `vars.`: that is the one free-form sandbox
in the strict-root schema, and the only place the overlay validator lets an
arbitrary value live. See
[`workspace.md` → Strict root + the `vars:` sandbox](workspace.md#strict-root--the-vars-sandbox).

**Whole files.** A config-pack source whose `from:` ends in `.age` is a native
age-encrypted file. `render config` decrypts it before the usual `${...}`
render into the service hub dir:

```yaml
# workspace/templates/config/bot/manifest.yml
render:
  - from: google-credentials.json.age
    to: config/google-credentials.json
```

The `to:` path is **never** derived from the source — name the output whatever
the service expects. See [`render config`](../render/config.md).

## The `secrets:` block

```yaml
# workspace.yml — layer 1 only
secrets:
  recipient: age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p
```

| Field | Type | Description |
|-------|------|-------------|
| `secrets.recipient` | string | The project's public age recipient (`age1…`). Committed. |

`secrets:` is part of the strict root allowlist, but unlike every other
formalized block it is legal in **`workspace.yml` only**. Declaring it in
`workspace/defaults.yml` or `workspace/local.yml` is a hard load error naming
the file — a per-developer recipient would silently split the team into groups
that cannot read each other's secrets. (`compose.extra` is the existing
precedent for a key legal in exactly one layer.)

A malformed `recipient`, or a `secrets:` value that is not a mapping, is
likewise a load error naming the file. The same validation runs for
`dwe vars inspect`'s per-layer resolution, so the two loaders cannot disagree.

## Keys: where the identity lives

`dwe` looks for the private identity in this order and uses the **first source
that is present**:

| Order | Source | Value |
|-------|--------|-------|
| 1 | `DWE_AGE_KEY` | The identity text itself (`AGE-SECRET-KEY-1…`) |
| 2 | `DWE_AGE_KEY_FILE` | Path to a file holding the identity text |
| 3 | `~/.config/dwe/keys/<recipient>.key` | The per-project keyfile, `0600` |

The first present source **must** match the configured recipient. A mismatch is
`wrong_identity` naming the source — it does **not** fall through to the next
source, because silently ignoring an explicitly-set `DWE_AGE_KEY` is how a CI
job ends up reporting "no key" while the real problem is the wrong key.

The keys directory is created `0700` (an existing looser directory is tightened)
and each keyfile is written `0600` with `O_CREATE|O_EXCL` — DWE never
overwrites an identity file.

An identity is read as the **first `AGE-SECRET-KEY-1…` token on a line that is
not a `#` comment**, so an `age` CLI keyfile (which carries a `# public key:`
header) is used verbatim, and a file whose live key sits under a commented-out
old one resolves to the live key — the same one `age` itself would use. A later
token is ignored, not an error: a multi-identity `DWE_AGE_KEY_FILE` is a
documented `age` shape. When *every* token sits inside a comment — the shape a
paste produces when it joins a keyfile's header and key onto one line — the
whole text is scanned instead, so that paste still parses. That rescan is
skipped when a non-comment line carries an `AGE-SECRET-KEY-1…` that is too short
or malformed: reaching past a damaged live key for a commented-out old one would
report the file as the *wrong* identity instead of an invalid one.

## Getting started

```bash
# Once per project, on one machine:
dwe secrets init                                  # mint the key pair
git add workspace.yml && git commit               # commit the recipient

# Add a secret (needs only the committed recipient):
pbpaste | dwe secrets set vars.telegram.token --stdin
git add workspace/defaults.yml && git commit

# Onboard a teammate — share the identity out of band:
dwe secrets key export                            # → password manager
# …on the other machine:
dwe secrets key import                            # hidden prompt: paste it
dwe secrets key import --file identity.txt        # …or read it from a file
```

Then `${vars.telegram.token}` resolves to the plaintext everywhere, and
`dwe vars get vars.telegram.token` prints it.

## New developer / new machine

A clone carries the public recipient; the private identity does not travel with
it. Until the identity is on this machine every encrypted value reads
`<encrypted>` and every lifecycle command stops at `secrets.unresolved`.

There are exactly three places to put it —
[the lookup order above](#keys-where-the-identity-lives): `DWE_AGE_KEY`,
`DWE_AGE_KEY_FILE`, or the per-project keyfile that `dwe secrets key import`
writes. The **first present source must match the recipient**; there is no
fall-through to the next one.

### The interactive import

On a terminal, with no `--file` and nothing piped, `import` asks for the key
itself:

```
$ dwe secrets key import
Private identity for age1qyqs…
Paste the AGE-SECRET-KEY-… line, or the whole keyfile; the typed characters are not echoed.
> ••••••••••••

identity for age1qyqs… stored at /home/dev/.config/dwe/keys/age1qyqs….key
2 encrypted value(s) and 1 .age file(s) are now readable
```

The field is hidden, and the whole keyfile is accepted — comment header
included, even when the paste arrives as a single joined line. Validation runs
**in the form**: a key that does not parse, or one belonging to another project,
is reported without closing the prompt, so a mistyped paste is corrected rather
than restarted. `Esc` cancels with `secrets_import_cancelled` and no keyfile.

The second line is the point of the exercise: it is the same scan
[`dwe secrets status`](#dwe-secrets-status) renders, so the import ends by
telling you what it actually opened instead of leaving you to check.

### The offer inside `dwe deploy`, `dwe run` and `dwe restart`

Nobody has to know the import command by heart. When a project has encrypted
material, no usable identity, and a human at a terminal, the interactive entry
points offer to take the key right there:

```
Enter the private identity now?
This project has encrypted values that need the age identity for age1qyqs…, and
this machine does not have it. run 'dwe secrets key import' to store the identity
at /home/dev/.config/dwe/keys/age1qyqs….key, or set DWE_AGE_KEY / DWE_AGE_KEY_FILE
  [ Enter key ]   [ Abort ]
```

`Enter key` opens the same hidden prompt; the stored identity is verified by a
real lookup, and the command **continues in the same invocation** — the offer
runs on the raw layers *before* the config is loaded, so the config is read once
and already decrypted. `Abort` (or `Esc`) ends the command with the fix
instruction and nothing written — in the `dwe deploy` menu as the typed
`secrets_no_identity`, in `dwe run` / `dwe restart` as the same sentence.

Three details worth knowing:

- **`dwe restart` offers before it stops anything.** Declining leaves the stack
  running, rather than tearing it down and then failing to bring it back. This
  is the stack-level restart; `dwe restart <service>` goes straight to
  `docker restart` and runs neither the offer nor preflight.
- **The `dwe deploy` offer sits at the menu's entry**, so it covers `Plan` as
  well as `Run` and the wizard — a plan built without the key would print
  commands derived from `<encrypted>`.
- **Non-interactive entry points keep today's hard error**: `dwe deploy run`,
  `dwe reset`, `dwe render env` and `dwe render config` fail with the same
  message and hint as before. The gate is an offer where a human is already
  waiting, not a global hook.

### What CI and scripts see

The offer never opens when there is nobody to answer it: stdin is not a
terminal, `--yes` (which only `dwe run` and `dwe restart` define — `dwe deploy`
has no such flag), `--output json`, or `DWE_NONINTERACTIVE=1`. Each of those
keeps the existing failure — the `secrets.unresolved` preflight wall for the
lifecycle commands — so a pipeline's output and exit code do not change.
`key import` is unchanged too: a piped identity is read as before, and at a
terminal `--output json` and `DWE_NONINTERACTIVE=1` refuse with
`secrets_identity_source_required` instead of prompting.

### A broken source is reported, never prompted

Because the first present source wins with no fall-through, a `DWE_AGE_KEY` that
is set but does not hold this project's identity **cannot be fixed by importing
a key**: the freshly written keyfile would not be consulted at all. So instead
of opening a form whose result could not be used, the gate names the source:

```
$DWE_AGE_KEY is set but does not hold the identity for age1qyqs…;
unset it or fix it — a keyfile is not consulted while it is set
```

A keyfile that already exists but holds another key is refused the same way,
pointing at [`dwe secrets key remove`](#dwe-secrets-key-remove) — the write is
`O_EXCL` and would not replace it. Both refusals fire in **every** mode,
interactive or not: they are more precise than the generic "no identity"
message, and neither is a question.

## Subcommands

All writers take the project locks (`deploy.lock` → `snapshot.lock`) and run
**no preflight** — they are config edits, not stack mutations. `status`, `get`
and `key export` write nothing at all. `decrypt` takes no locks either — it
does not touch the config — but it does write a plaintext file (see below).

**`init`, `set` and `rekey` edit a layer file by replacing single lines.** They
do not re-encode the document, so indentation, blank lines, comments, anchors,
`<<:` merge keys, quoting style and line endings all survive byte-for-byte: one
`dwe secrets set` into a several-hundred-line annotated `defaults.yml` produces
a one-line diff. A new key is inserted rather than replaced — at the end of the
file for a new top-level block, otherwise after the end of the nearest existing
mapping.

The price is that a handful of shapes cannot be edited in place and are
**refused with the file untouched** (`secrets_write_unsupported`), naming the
path and the fix:

| Refused shape | Fix |
|---------------|-----|
| A literal or folded block scalar, or a plain/quoted scalar wrapped over several lines | Write the value on one line |
| A target inside a flow collection (`{a: 1}`, `[x, y]`) | Write it as a block collection |
| A parent that is `null` (`vars:` with nothing under it), a sequence, or a non-mapping scalar | Materialize the parent as a block mapping |
| A parent reached through a YAML alias (`vars: *common`), or a key that may be inherited through a `<<:` merge key | Write the key explicitly in that mapping |

The spliced bytes are re-parsed and the value is read back at its path before
anything is persisted; a mismatch is refused too, and nothing is written.

### `dwe secrets init`

```
dwe secrets init
```

Generates an X25519 key pair, writes the identity to
`~/.config/dwe/keys/<recipient>.key`, and splices `secrets.recipient` into
`workspace.yml` (a project with no `secrets:` block yet gets the block appended
at the end of the file; the rest of the file is untouched and keeps its mode).

The keyfile is written **first**: a recipient committed without a readable
identity would lock the project out of its own secrets. If the `workspace.yml`
write then fails, the keyfile is removed again so a re-run is not blocked by
the no-clobber guard.

Refuses when `secrets.recipient` is already set. The refusal names the fix that
actually works in the state it found, and reports which one in the
`identity` detail:

- `identity: "available"` — an identity for the project is on this machine, so
  the values are recoverable and replacing the key pair is
  [`rekey`](#dwe-secrets-rekey), which re-encrypts them instead of orphaning
  them. The keys directory is consulted as well as the
  [lookup order](#keys-where-the-identity-lives), so a `DWE_AGE_KEY` exported
  for another project — which stops the lookup at the first source and never
  reaches the keyfile — does not turn a healthy project into the case below.
- `identity: "missing"` — no identity here opens the project. `rekey` **cannot
  run at all** in this state: it has to read every value before it can rewrite
  one. The refusal offers [`key import`](#dwe-secrets-key-export--import) first
  (the identity may still exist on another machine), and
  `init --replace-recipient` as the recovery when it does not.

#### `dwe secrets init --replace-recipient`

```
dwe secrets init --replace-recipient [--yes]
```

The exit from a **lost identity**. It mints a new key pair and commits it over
the old `secrets.recipient`, and that is all it does: every existing
`ENC[age:…]` marker and every `*.age` source stays exactly where it is and
becomes permanently unreadable. Those values come back only by being re-entered
from their own plaintexts.

The orphans are left in place on purpose — they are the record of *which*
secrets have to be re-entered. `dwe secrets set <path>` overwrites each marker
in place as you work through them, `dwe secrets status` is the remaining to-do
list, and until it is empty the `secrets.unresolved` preflight check keeps the
lifecycle commands stopped. The report the command ends with names every
orphaned value, so the list is in hand before you go looking for it.

**It refuses while anything is still readable here** (`secrets_identity_available`,
with a `readable` count): that is `rekey`'s case, and those values are not lost
yet. To discard them anyway, save what you need with `dwe secrets get`, drop the
identities that open them with
[`dwe secrets key remove <recipient> --force`](#dwe-secrets-key-remove), and run
it again.

A confirmation naming the number of values at stake is required; `--yes` skips
it, and a mode with no way to ask refuses with `secrets_confirmation_required`
rather than orphaning the tree silently. The confirmation runs **before** the
project locks are taken — a prompt sitting open must not stall every other `dwe`
command — so `secrets.recipient` is re-read once they are held, and a value that
changed under the decision refuses with `secrets_recipient_changed` and writes
nothing. The old keyfile, if one is lying around, is never touched —
`key remove` is what deletes a keyfile.

On a project with no `secrets.recipient` the flag is refused
(`secrets_no_recipient`) instead of quietly behaving like a plain `init`.

### `dwe secrets status`

```
dwe secrets status
```

The report you run to find out why something is blocked. It never blocks
anything itself: **no encrypted value can make it fail** — with no key and every
value unreadable it still exits 0, reporting each one as a row. (A config that
does not load at all is still an error: there would be no inventory to report.)

It shows the configured recipient, the identity this machine holds (and, when
there is none, where the lookup looked), and two inventories:

- **Markers** — every `ENC[age:…]` scalar across the three layer files, as
  `layer` / `path` / `state`.
- **Encrypted files** — every `*.age` under `workspace/templates/config/**`.

Each entry is **actually decrypted**, so the report distinguishes the three
causes rather than lumping them together:

| State | Meaning |
|-------|---------|
| `decrypted` / `decryptable` | The configured identity reads it |
| `decrypted: stale_key` / `decryptable: stale_key` | Readable here, but only with an *older* keyfile — the configured identity does not open it |
| `unresolved: no_identity` | No identity for this recipient is available here |
| `unresolved: wrong_identity` | An identity was found, but it does not open this value |
| `unresolved: invalid_identity` | An identity source was set but holds no age key — e.g. a truncated `DWE_AGE_KEY`. Repair that source; a keyfile is not consulted while an env source is set |
| `unresolved: corrupt` | The payload is damaged — not a key problem |

`corrupt` is detected without a key (marker shape, base64, age header), so a
keyless developer is never sent hunting for a key that would not have helped.

#### Shadowed markers

A marker a **higher layer overrides with a plaintext value** decrypts perfectly
well and is still not what the project reads. Such a row carries the override on
its state cell and renders amber:

```
decrypted (shadowed by workspace/local.yml)
```

The qualifier is separate from the state, because the two answer different
questions — "can this machine open the value?" and "is the key pair what
actually shares it?" — and an *unresolved* marker can be shadowed too. That
pairing is the one worth naming: with the plaintext covering for it, a lost
identity shows up nowhere in everyday use except in
[`dwe render config`](../render/index.md), which has no plaintext layer to fall
back on.

In `--output json` a shadowed marker row carries two extra fields:

| Field | Meaning |
|-------|---------|
| `shadowed_by` | The layer file supplying the plaintext that wins the merge |
| `shadow_match: identical` | The override holds the **same** value as the marker — almost always a copy left behind when the value was encrypted |
| `shadow_match: different` | The override holds a different value — a deliberate local override |
| `shadow_match: unknown` | The marker could not be decrypted here, so the two were not compared |

A marker overridden by **another marker** is not reported: the value that wins is
still encrypted at rest. `dwe validate secrets` reports the same finding as a
warning — see [`secrets.shadowed`](validate.md).

A half-rekeyed tree is reported **per value**: the configured identity is tried
first, then every other keyfile in the keys directory, so `status` says which
values still need the old key rather than failing wholesale. Those rows are the
`stale_key` ones, and they render amber — the config loader tries the
*configured* identity alone, so such a value is still `wrong_identity` at load
time and `secrets.unresolved` still blocks the lifecycle commands until
`dwe secrets rekey` finishes. A `*.age` candidate that fails the path
discipline (a symlink, a device) is reported as `not decryptable` with the
refusal as its reason — never silently skipped.

Rows are sorted (layer order, then path), so the output is stable across runs
and diffable.

The **Identity** line reports the lookup honestly — a source that was consulted
and rejected never reads as a missing key:

| Header | Meaning |
|--------|---------|
| `keyfile (…)` / `$DWE_AGE_KEY` / `$DWE_AGE_KEY_FILE` | The identity loaded from that source |
| `none (looked at …)` | No identity anywhere; the line names every place the lookup looked |
| `invalid (…)` | A source was set but holds no age key — the line names the source to repair |
| `wrong recipient (…)` | A readable identity for **another** recipient; the line names both |

Whenever the identity did not load, the report closes with the fix instruction
(`run 'dwe secrets key import' to store the identity at …, or set DWE_AGE_KEY /
DWE_AGE_KEY_FILE`) — the same sentence `dwe validate` prints. In `--output
json` the same facts are structured: `identity.source` is the **consulted**
source even on failure, `identity.reason` carries the stable reason word
(`no_identity` / `invalid_identity` / `wrong_identity`), `identity.error` the
sentence, `identity.hint` the fix. Every one of them is DWE-authored: an `age`
parse error echoes the input characters, which for a broken identity source are
private-key bytes, so it is never printed.

### `dwe secrets set`

```
dwe secrets set <vars.path> [value] [--file defaults|workspace] [--stdin]
```

Encrypts a value to the project's recipient and writes it as a marker into a
**committed** layer file. Needs only the recipient — a developer without the
identity can still add a secret.

The value comes from one of three sources:

- the positional argument — **lands in the shell history** like any argument;
- `--stdin` — the whole of stdin, with exactly one trailing newline (and a
  preceding `\r`) trimmed, never more;
- neither, on a terminal — a **hidden prompt**.

Passing both an argument and `--stdin` is a typed refusal
(`secrets_value_ambiguous`) rather than letting one silently win.

Rules:

- **The `vars.` prefix is required**, not optional as it is in `dwe vars set`.
  Silently rewriting `project.name` into `vars.project.name` would accept a
  path the user meant literally, and `secrets set` has no completion to make
  the shorthand discoverable.
- **No coercion.** The value is always stored as a string, so a secret that
  looks like a number (`"123"`) stays what you typed. Contrast
  [`dwe vars set`](vars.md#dwe-vars-set), which parses the argument as a YAML
  scalar.
- **`--file`** targets `workspace/defaults.yml` (default) or `workspace.yml`.
  `--file local` is refused with a pointer to `dwe vars set`:
  `workspace/local.yml` is gitignored personal state, where encryption buys
  nothing.
- Descending through an existing mapping is normal; descending through an
  existing **non-mapping** node (`vars.db.host.port` where `host` is a scalar)
  is refused as an unsupported shape, leaving the file untouched — as are the
  other [refused shapes](#subcommands).
- The staged document is validated as a config layer **before** it is
  persisted, so a `set` can never leave a layer unloadable.

The value is resolved *before* the project locks are taken — a hidden prompt
can sit open indefinitely, and holding the lock pair meanwhile would block
every other `dwe` command in the project. The recipient is then re-read **under
the lock**, so a value can never be encrypted to a recipient that a concurrent
`rekey` retired in between.

Writes change only the target line — comments, key order, blank lines and
formatting are preserved byte-for-byte; a missing `workspace/defaults.yml` is
created `0644` (tracked files keep a normal mode — only `local.yml` is forced to
`0600`).

### `dwe secrets get`

```
dwe secrets get <vars.path>
```

Decrypts the marker at a path and prints the plaintext. Needs the identity.

`get` reads the layers **as written**, so it reports the secret itself rather
than the merged value: when a marker in `workspace/defaults.yml` is shadowed by
a plaintext override in `workspace/local.yml`, `dwe secrets get` still prints
the secret and [`dwe vars get`](vars.md#dwe-vars-get) prints the override that
actually wins at runtime.

A path that holds no marker is `secrets_not_encrypted` — use `dwe vars get` for
a plaintext value.

### `dwe secrets encrypt` / `decrypt`

```
dwe secrets encrypt <file>     [--out PATH|-] [--force]
dwe secrets decrypt <file.age> [--out PATH|-] [--force]
```

Whole-file helpers, for config-pack sources. `encrypt` writes `<file>.age`
beside the input; `decrypt` strips the `.age` suffix (an input that does not end
in `.age` needs an explicit `--out`). Both refuse to overwrite an existing
output without `--force`.

The flag is `--out`, not `-o`: the root command owns `-o` for `--output`.
`--out -` streams to stdout and **rejects** `--output json`
(`secrets_raw_stream`) — a byte stream and a JSON envelope cannot share stdout.

Path discipline applies to both ends: an absolute path outside the project is
legal (this is a file utility), but a **symlink or a non-regular file is
refused wherever it lives** — writing through a symlinked output is how a
"decrypt to a scratch file" turns into an overwrite of something else. Output
equal to input is refused too.

A decrypted output is `0600`, and an existing one is **tightened** to `0600` —
it is plaintext on disk now, so do not commit it. An overwritten *ciphertext*
file keeps whatever mode the repository gave it.

`encrypt` needs only the recipient; `decrypt` needs the identity.

### `dwe secrets key export` / `import`

```
dwe secrets key export
dwe secrets key import [--file|-f PATH]
```

The identity is never in git — it travels through a password manager or another
out-of-band channel.

`export` prints `AGE-SECRET-KEY-1…` to stdout. When stdout is a terminal it
also warns on stderr (text mode only), because the key is about to sit in the
scrollback.

`import` reads the identity from `--file`, from stdin when it is piped, or —
on a terminal with neither — from a **hidden prompt**. Whatever the source, it
**verifies that the recipient matches the configured one**
(`secrets_identity_mismatch`) and only then writes the keyfile `0600`. It takes
the project locks too: an import racing a `rekey` would otherwise install the
identity being retired.

Paste either the `AGE-SECRET-KEY-1…` line or the whole keyfile — the comment
header an `age` keyfile carries is ignored, including when a paste joins its
lines into one.

The prompt validates in place: a key that does not parse, or one belonging to
another project, is reported without closing the form, so a mistyped paste is
retried rather than restarted. `Esc` cancels with `secrets_import_cancelled`
and no keyfile. Because the write is `O_EXCL`, an already-installed identity is
reported **before** the form opens rather than after the key is typed.

The prompt never opens without a terminal — a piped identity
(`pbpaste | dwe secrets key import`, CI) is read from stdin as before, and an
empty stdin is `secrets_identity_source_required`. At a terminal, `--output
json` and `DWE_NONINTERACTIVE=1` refuse with the same code rather than
prompting.

A successful import ends with what the key opened:

```
identity for age1… stored at ~/.config/dwe/keys/age1….key
2 encrypted value(s) and 1 .age file(s) are now readable
```

The counts come from the same scan `dwe secrets status` renders, and only
values the *configured* identity opens are counted — a value left behind by an
interrupted `rekey` is still a to-do, so it is not.

If that scan cannot run at all (an unreadable `workspace/templates/config`), the
import still **succeeds** — the keyfile is written, and `O_EXCL` would refuse a
retry — but the second line becomes `the readability report could not be built:
<reason>`, and JSON omits `markers_readable` / `files_readable` in favour of
`report_error`. A zero count there would say the key opens nothing, which is not
what happened.

### `dwe secrets key list`

```
dwe secrets key list
```

Every identity installed on this machine, sorted by file name:

```
  Directory — /home/dev/.config/dwe/keys

╭───────────┬───────────────┬────────────────────╮
│RECIPIENT  │FILE           │STATE               │
├───────────┼───────────────┼────────────────────┤
│age1broken │age1broken.key │unparsable          │
│age1current│age1current.key│ok (current project)│
│age1locked │age1locked.key │unreadable          │
│age1other  │age1other.key  │ok                  │
│age1parsed │age1stale.key  │misnamed            │
╰───────────┴───────────────┴────────────────────╯
```

The keys directory is **machine-wide, not per project**, which is why nothing is
ever pruned automatically: a key here may belong to any other project on this
machine, so "unused" is not computable. The only relation `list` can state is
the one it knows — the row this project uses is marked `current project`.
Outside a project no row is marked, and the command still runs (it is in the
allowlist of commands that need no project). An empty or absent keys directory
prints `No identities in <dir>.` and exits 0.

| State | Meaning |
|-------|---------|
| `ok` | The file holds the age identity its name claims |
| `unreadable` | The file could not be read (permissions, a directory, a dangling link) |
| `unparsable` | The file holds no age identity |
| `misnamed` | It parses, but the identity belongs to another recipient than the file name says — the row shows the **parsed** recipient |

The states are a fixed vocabulary: no I/O or parse error text ever reaches the
output, because both echo file content. For an `unreadable` or `unparsable` file
the RECIPIENT column shows the file name's stem — the recipient is the file name
by construction — never anything read out of the file.

### `dwe secrets key remove`

```
dwe secrets key remove <recipient> [--force] [--yes|-y]
```

Deletes `~/.config/dwe/keys/<recipient>.key`. **The argument names the file**, so
a `misnamed` file is removed under its own name, never under the recipient
`key list` shows for it — aiming at that recipient is `secrets_key_not_found`.

Removing the file that HOLDS the identity the current project uses is refused
(`secrets_key_in_use`) unless `--force` is passed: those encrypted values become
unreadable here, and unless the key was exported it exists nowhere else. The
guard reads the file, not its name, so it covers a `misnamed` file carrying this
project's key too. A file that opens nothing — holding no age identity, holding
another project's key, or resolving to nothing at all (a dangling symlink) — is
removed without `--force`; that is what makes the "remove it and import the
right one" advice above work on a stale keyfile. A file that is not there is
`secrets_key_not_found`.

A file whose bytes cannot be **read** is the one case the guard cannot answer,
and it is refused (`secrets_key_unreadable`) until `--force`: deleting a file
needs no read permission on it, so waving it through would unlink key material
nobody ruled out as live.

Otherwise the removal is confirmed interactively. Where it cannot ask — no
terminal, `--output json`, `DWE_NONINTERACTIVE=1` — it is
`secrets_confirmation_required`, and `--yes` is the way through; declining is a
no-op that prints `kept <path>` and exits 0. The project locks are held around
the delete when a project is resolved, for the same reason `key import` holds
them: a removal racing a `rekey` would retire the key the rekey is installing.

### `dwe secrets rekey`

```
dwe secrets rekey
```

Mints a new key pair and re-encrypts **every** committed secret — every marker
in the layer files and every `*.age` pack source — to it. Run it when the
identity may have leaked, or when someone who held it should no longer be able
to read the project's secrets.

This machine must be able to read every existing secret. The configured
identity is tried first, then every other keyfile in the keys directory (so a
half-rekeyed tree finishes cleanly).

The order is a **recoverable sequence, not a transaction**:

1. **Read-only pass** — decrypt and validate everything into memory, then
   rehearse every layer-file edit on a throwaway copy. A corrupt marker, an
   undecryptable `.age`, or a marker whose YAML shape cannot be rewritten in
   place (a block scalar, a flow mapping) aborts here, with **nothing written**.
2. **Write the new keyfile** — the first mutation. The old keyfile is kept.
3. **Re-encrypt** every `.age` file (atomically) and every layer file (one
   spliced line per marker, so the diff is one line per re-encrypted value).
4. **Update `secrets.recipient` last.**

A crash mid-way leaves both identities on disk and a mixed tree; re-running
`rekey` — or just `dwe secrets status` — tries the configured identity first
and then every other keyfile, so the sequence converges. Failures after step 2
say so explicitly, and the JSON envelope distinguishes them from the read-only
refusals (which carry `written: false`).

Remove the old keyfile once every developer has imported the new identity.

## Without a key: what still works

A project whose secrets cannot be decrypted here **still loads**. Markers stay
literal in the config, and:

| Surface | Behaviour |
|---------|-----------|
| `dwe status`, `dwe docs`, `dwe validate`, `dwe prompt`, `dwe commands` | Work normally |
| `dwe vars list` / `get` / `inspect`, the TUI browser | Show `<encrypted>` — never the ciphertext |
| `dwe secrets status` | Reports every value and its reason; exits 0 |
| `dwe run`, `dwe restart`, `dwe deploy`, `dwe reset`, `dwe stop`, the deploy wizard | **Blocked** by the `secrets.unresolved` preflight validator, naming the fix. At a terminal, `dwe run` / `dwe restart` and the `dwe deploy` menu first [offer to take the identity](#the-offer-inside-dwe-deploy-dwe-run-and-dwe-restart) |
| `dwe render env`, `dwe render config` | **Fail** naming the value that would have been written |
| `dwe render ide` / `ai` / `git` | Work — they render against a sanitized config and emit the marker |

A missing key never renders a secret as `""`, and never writes a marker into an
output file.

`dwe stop` is on the blocked list because it runs the `lifecycle.yml` stop
hooks, which are ordinary user commands and may reference `${vars.*}`; it
shares the `stop` preflight stage with `dwe reset`. Pass `--skip-preflight` to
tear a stack down on a machine that has no key.

An encrypted `project.name` / `project.prefix` is treated as **unset** by the
`dwe prompt` hot path (which has its own lenient parser and never loads the
full config) — an encrypted project name is nonsensical, and a marker in the
compose label filter would match no container.

## Output guards: no marker ever reaches a rendered file

Two renderers run with no preflight (`dwe render env`, `dwe render config`), and
`dwe run` renders `.env` *before* its preflight. They therefore enforce the
policy themselves:

- **`.env`** — every emitted value is checked: the system variables (`PROJECT`,
  from `project.name`, and `COMPOSE_PROJECT_NAME`, from the compose project
  name) and every `exports.env` rule. A marker is an
  error naming the variable and its source path, and pointing at
  `dwe secrets status`. This fires from all four `.env` write sites:
  `dwe render env`, the compose auto-regeneration before `dwe docker up` / `run`
  / `exec` / `restart` / `build`, `dwe services enable` / `disable`, and the
  `.env` render `dwe run` performs before its own preflight.
  The root `.env` is also explicitly `chmod`ed to `0600`, so a pre-existing
  permissive file is tightened.
- **Config packs** — after the `${...}` render, an output that still contains a
  marker is refused, naming the entry's `to:` path. Because the render context
  is the raw merged config, this catches every substitution of an unresolved
  marker without tracking which one.
- **ide / ai / git packs** — their outputs are usually **tracked by git**, so
  they never see plaintext at all: those three renderers load a **sanitized**
  config assembled over the raw layers with no decrypt pass. Every field a
  template can reach (`.Raw`, `.Vars`, `.Project`, `.Runtime`, `.Services`,
  `.ServiceCfg`) carries the marker where the real config carries plaintext. A
  template that reads a secret emits ciphertext — already committed, harmless —
  never the plaintext. This is a structural guarantee, not a path allowlist.

`.age`-sourced pack outputs are written `0600` and explicitly `chmod`ed (a
pre-existing `0644` target is tightened). Other pack outputs keep `0644`, so a
scalar secret substituted into a rendered `.env` lands in the gitignored hub dir
at the pack's usual mode. The container reads a `0600` file fine, because it
runs as the host UID/GID that `exports.env` already publishes.

## Validation and preflight

Two validators in the `secrets` domain (`dwe validate secrets`):

| Validator | Kind | Fires when |
|-----------|------|------------|
| `secrets.recipient` | content | Markers or `.age` sources exist but `secrets.recipient` is missing or is not a valid `age1…`; or a marker payload is damaged (`corrupt`) |
| `secrets.unresolved` | readiness | Any value in the merged config is unresolved, or a resolvable config pack's `.age` source fails to decrypt with the loaded identity |

`secrets.recipient` raw-loads the layers itself when the config failed to load,
so a scoped `dwe validate secrets` still diagnoses a malformed recipient
instead of going blind.

`secrets.unresolved` is the **second exception** to the preflight rule that only
`env.*` and `checks.*` run there (the first is `config.validate`): it is a
readiness question, not a content one — "can this machine actually deploy right
now". It is cherry-picked into `preflight.Run` **and** into the deploy wizard's
pre-flight gate, so `dwe run` / `deploy` / `reset` and the menu all stop with
the same named fix.

It is still that wall. What changed is what a human meets *before* it: at an
interactive `dwe run` / `dwe restart` / `dwe deploy` the
[key offer](#the-offer-inside-dwe-deploy-dwe-run-and-dwe-restart) runs first, so
by the time preflight executes the identity is there and the validator has
nothing to report. Run without a terminal and it blocks exactly as described
here; *declining* the offer never reaches it at all — the command ends there
and then, with the same fix instruction.

Unresolved markers are grouped **by reason**, one diagnostic per reason listing
the sorted paths. A keyless developer has every marker unresolved for one
cause; one row per marker would bury the single actionable fix.

The `.age` source scan mirrors what `render config` actually iterates, so a
disabled service or an unresolvable pack is invisible to the validator exactly
as it is at render time.

**A shadowed marker is a warning, not an error.** `secrets.shadowed` reports
every marker a higher layer overrides with a plaintext value — the case where
every other row in this domain stays green about a value the project never
reads. Overriding a shared secret locally is legitimate, so it never blocks and
never runs in preflight; the findings are grouped by overriding file and by
whether the override holds the **same** value as the marker (a copy left behind
when the value was encrypted) or a different one (a deliberate override). See
[Shadowed markers](#shadowed-markers) for what `dwe secrets status` shows.

**A healthy project says so.** All three validators emit an `✓` row when they
find nothing wrong, so `dwe validate secrets` after onboarding reports
`validation result: 3 checks` instead of `validation skipped (no files found)`.
The `secrets.recipient` row carries no message — the target is the statement —
the `secrets.unresolved` row counts the inventory it just read
(`1 encrypted value(s) and 0 config-pack source(s) readable via keyfile`), and
the `secrets.shadowed` row states the thing the other two never checked
(`1 encrypted value(s), none shadowed by a plaintext override`).

The rows are tied to what each validator actually checked: the recipient row
needs only a valid `secrets.recipient` (a project that ran `dwe secrets init`
and has nothing encrypted yet still gets it), the unresolved row appears only
when there is an inventory to read *and* no diagnostic was produced, and the
shadowed row only when the project carries markers at all. A project with no
`secrets:` block and nothing encrypted stays silent, exactly as before. The identity is named by source word (`env` / `env-file` / `keyfile`) —
never by path, and never with key material. Preflight and the deploy wizard's
gate filter `✓` rows, so neither prints anything extra.

## Where plaintext goes

Decrypted values exist, by design, in:

- **process memory** — the merged config;
- **the project-root `.env`** — gitignored, `0600`, already the home of
  `exports.env`;
- **config-pack outputs in the service hub dir** — gitignored via `/services/`;
- **`.dwe/generated.yml`** — `0600`, gitignored, when a `generated:` harvest
  reads a rendered hub file that contains a decrypted value;
- **the container**, which reads the hub dir;
- **child-process output** the user chose to run.

They are kept **out of**:

- **git-tracked files** — ide / ai / git pack outputs render against a sanitized
  config (see [output guards](#output-guards-no-marker-ever-reaches-a-rendered-file));
- **dwe's own command echoes** — `-v` / `--debug` traces, their `.dwe/logs`
  copies and every plan / dry-run surface are redacted (see below);
- **`dwe vars` output** when the key is absent — `<encrypted>`, never the
  ciphertext.

A value passed to `dwe secrets set` **on the command line lands in shell
history** like any other argument. Use `--stdin` or the hidden prompt for
anything that matters.

## Redaction

Every value the config loader decrypts is registered with the trace subsystem,
which prints `***` in place of it. The registration lives in the loader because
~60 call sites load config and the root command hook does not.

Redaction covers three families of output:

- **Diagnostic echoes** — `-v` / `--debug` command echoes, and the `.dwe/logs`
  mirrors of parallel pipeline steps.
- **Live-run skip reasons** — the `Skipped: <step> (when: …)` line `dwe deploy
  run` and `dwe reset run` print at default verbosity, and its parallel-group
  equivalent. The reason is display-only: it is never persisted to
  `.dwe/deploy/state.yml`, so the deployment hash is unaffected.
- **Plan and dry-run surfaces** — `dwe deploy plan` (table, `--format shell` and
  `--output json`, including the `unresolved` field), `dwe reset plan` and
  `dwe reset step --dry-run`. Redaction is a property of the display functions
  that build those lines, so it applies before the value is quoted or embedded
  into a `--set k=v` argument, and any future print surface inherits it.

Because `--format shell` is redacted too, **it is a preview of what will run,
not a script to execute**: a step referencing a secret prints `***` where the
value goes. Redaction never touches what is actually executed — a real
`dwe reset step <addr>` runs with the real value.

Three properties worth knowing:

- **Values shorter than 4 runes are not redacted.** Redacting `"1"` would shred
  every line of output. `dwe secrets set` still stores such a value, but warns
  on stderr that it will not be redacted, so the limit is not something you
  discover from a pasted plan.
- **Child-process output is not redacted** (an explicit non-goal). A command
  that prints its own configuration prints it.
- **Redaction is not an access boundary.** `dwe vars get` and `dwe secrets get`
  print plaintext by design — see
  [`vars.md` → Output is not redacted](vars.md#output-is-not-redacted).

Redaction is a union that lives for the process, so concurrent pipelines and
`dwe test --parallel` scenarios all see one consistent set.

## Container behavior

`dwe secrets` is **not** in the container command allowlist. No container can
mint, rekey, export or import a key, or add a secret.

`DWE_AGE_KEY` and `DWE_AGE_KEY_FILE` are stripped from the client environment at
the shim **and** re-supplied from the daemon's own environment, so a container
cannot point the host `dwe` at a file of its choosing, while a host running with
an env-only identity keeps working over the bridge.

**Reads are deliberately not gated.** `dwe vars get` from a bridged container
already reaches the host `dwe`, which holds the identity, and returns plaintext
— the same exposure the container already has through its rendered `.env` and
config files. `dwe render config` is likewise reachable and decrypts host-side.
This is an accepted consequence, not an oversight: a container that can read its
own config can read its own credentials.

## `age` CLI interoperability

Nothing here is a private format. A marker payload is base64 of a binary age
file with one X25519 stanza:

```bash
# Open a marker with the age CLI:
dwe secrets get vars.telegram.token                       # the dwe way
echo 'YWdlLWVuY3J5…' | base64 -d | age -d -i ~/.config/dwe/keys/age1….key

# Open a pack source:
age -d -i ~/.config/dwe/keys/age1….key creds.json.age
```

The keyfile is an ordinary age identity file, so `age`, `age-keygen -y` and
every other age tool work on it. This escape hatch is a feature: a project's
secrets never depend on `dwe` being installed or working.

## JSON output

Every subcommand routes through `--output json` (with `--pretty`) and keeps
stdout clean; typed errors serialize to a `{"error":{…}}` envelope on stderr.

| Command | Shape |
|---------|-------|
| `init` | `{"recipient": "age1…", "keyfile": "/…/age1….key"}` — `--replace-recipient` adds `old_recipient` plus `orphaned_markers` / `orphaned_files`, which carry the same row shapes `status` reports |
| `status` | `{"recipient": "age1…", "identity": {"source": "keyfile\|env\|env-file\|", "keyfile": "…", "reason": "…", "error": "…", "hint": "…"}, "markers": [{"layer": "…", "path": "…", "state": "…", "reason": "…", "shadowed_by": "…", "shadow_match": "identical\|different\|unknown"}], "files": [{"file": "…", "state": "…", "reason": "…", "detail": "…"}]}` — `shadowed_by` / `shadow_match` appear only on a marker a higher layer overrides with plaintext; a file row's `reason` stays inside the fixed vocabulary (`no_identity` / `wrong_identity` / `invalid_identity` / `corrupt` / `stale_key` / `unreadable`); `detail` carries the free-form cause behind `unreadable` |
| `set` | `{"path": "vars.…", "file": "workspace/defaults.yml"}` |
| `get` | `{"path": "vars.…", "value": "…"}` |
| `encrypt` / `decrypt` | `{"from": "…", "to": "…"}` |
| `key export` | `{"recipient": "age1…", "identity": "AGE-SECRET-KEY-1…"}` |
| `key import` | `{"recipient": "age1…", "keyfile": "/…/age1….key", "markers_readable": N, "files_readable": N}` — the two counters are replaced by `report_error` when the surface could not be scanned |
| `key list` | `{"keys": [{"recipient": "age1…", "file": "/…/age1….key", "state": "ok\|unreadable\|unparsable\|misnamed", "current": true}]}` |
| `key remove` | `{"recipient": "age1…", "keyfile": "/…/age1….key", "removed": true}` |
| `rekey` | `{"old_recipient": "age1…", "recipient": "age1…", "keyfile": "…", "markers": N, "layers": ["…"], "files": ["…"]}` |

`key list` always emits `keys` as an array — `[]` when the directory is empty or
absent, never `null`. `key remove` always reports `removed: true`: a refusal is
a typed error envelope, not a payload saying nothing happened.

On `status`, `identity` is an object rather than a flat string: `source` is the
stable vocabulary a script branches on (the **consulted** source, filled on
failure too), `reason` the stable reason word, `error` the human sentence and
`hint` the fix. An identity load failure is **data** here, not an error —
reporting "no identity, and here is where it looked" is the command's whole job.

Typed error codes include `secrets_already_initialized` (whose `identity` detail
is `available` or `missing` — which fix the refusal named),
`secrets_identity_available` (`init --replace-recipient` while values are still
readable here, with a `readable` count), `secrets_recipient_changed` (the
recipient moved while `init` was deciding what to do — nothing was written),
`secrets_no_identity`,
`secrets_identity_mismatch`, `secrets_not_encrypted`, `secrets_path_invalid`,
`secrets_file_invalid`, `secrets_value_ambiguous`, `secrets_value_required`,
`secrets_output_exists`, `secrets_raw_stream`, `secrets_rekey_blocked`,
`secrets_import_cancelled` (the hidden `key import` prompt was cancelled),
`secrets_write_unsupported` (a [refused shape](#subcommands) — the file is
untouched; the hint names the path and what to change), and, for the keys
directory, `secrets_key_in_use` (the current project's identity, without
`--force`), `secrets_key_unreadable` (a keyfile whose bytes could not be read,
so the guard could not rule that out — also `--force`), `secrets_key_not_found`, `secrets_confirmation_required` (a removal
with no way to ask and no `--yes`), `secrets_recipient_invalid` (a malformed
`key remove` argument, refused before the filesystem is touched),
`secrets_key_list_failed` and `secrets_key_remove_failed`.

## Non-goals

Decided against, deliberately:

- **No `${secret.*}` namespace.** A secret is a `vars.*` value that happens to
  be encrypted at rest; a second namespace would fork every consumer.
- **No masking in `dwe vars`** for *decrypted* values — output is not an access
  boundary (see [`vars.md` → Output is not redacted](vars.md#output-is-not-redacted)).
  `<encrypted>` appears only when the value could not be decrypted.
- **No recipient lists.** One recipient per project; the scalar field can grow
  into a list later without breaking anything.
- **No `.age` support in ide / ai / git packs** — their outputs are tracked.
- **No `secrets unset`** — remove the key from the YAML file.
- **No container-side read gate** (see [container behavior](#container-behavior)).
- **No redaction of child-process output.**

## Related references

- [`workspace.yml` / `defaults.yml` / `local.yml`](workspace.md) — the 3-layer
  merged config, the strict root and the `vars:` sandbox
- [`dwe vars`](vars.md) — reading and editing `vars.*`, and how `<encrypted>`
  surfaces there
- [`render config`](../render/config.md) — config packs, `.age` sources, the hub dir
- [`render env`](../render/env.md) — `.env` generation and the marker guard
- [Templates](../templates.md) — what ide / ai / git templates can reach
- [`validate.yml`](validate.md) — validation domains and preflight
- [Project layout](../concepts/project-layout.md) — where the keys directory lives
