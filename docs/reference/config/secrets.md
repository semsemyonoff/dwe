# `dwe secrets` — encrypted values committed to the repository

`dwe secrets` stores a value the whole team shares — a bot token, a
service-account JSON — **encrypted in git**. Markers are decrypted in memory at
config load time and never written back into a tracked file as plaintext.

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

Decryption happens once, in the config loader, so `${vars.*}`, `exports.env`,
`dwe vars`, the deployment hash and every other consumer of the merged config
see plaintext.

The format is [age](https://age-encryption.org). A marker payload is a
base64-wrapped age file, so the `age` CLI opens it directly — see
[`age` CLI interoperability](#age-cli-interoperability).

## Two shapes: scalars and whole files

**Scalar values.** Any string in any config layer (`workspace.yml`,
`workspace/defaults.yml`, `workspace/local.yml`) may be an `ENC[age:…]` marker:

```yaml
# workspace/defaults.yml — tracked by git
vars:
  telegram:
    token: ENC[age:YWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUx…]
```

The grammar is strict: `ENC[age:<base64>]`, the **whole scalar or nothing**. A
string that merely *contains* `ENC[` is ordinary data and is left alone.

The decrypt pass is path-agnostic, but `dwe secrets set` writes only under
`vars.` — the one free-form sandbox in the strict-root schema. See
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

The `to:` path is **never** derived from the source. See
[`render config`](../render/config.md).

## The `secrets:` block

```yaml
# workspace.yml — layer 1 only
secrets:
  recipient: age1qyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqszqgpqyqs3fgh2p
```

| Field | Type | Description |
|-------|------|-------------|
| `secrets.recipient` | string | The project's public age recipient (`age1…`). Committed. |

`secrets:` is legal in **`workspace.yml` only** — declaring it in
`workspace/defaults.yml` or `workspace/local.yml` is a hard load error naming
the file, since a per-developer recipient would split the team into groups that
cannot read each other's secrets. A malformed `recipient`, or a `secrets:` value
that is not a mapping, is a load error too.

## Keys: where the identity lives

`dwe` uses the **first source that is present**:

| Order | Source | Value |
|-------|--------|-------|
| 1 | `DWE_AGE_KEY` | The identity text itself (`AGE-SECRET-KEY-1…`) |
| 2 | `DWE_AGE_KEY_FILE` | Path to a file holding the identity text |
| 3 | `~/.config/dwe/keys/<recipient>.key` | The per-project keyfile, `0600` |

The first present source **must** match the configured recipient. A mismatch is
`wrong_identity` naming the source; there is **no fall-through** to the next
source.

The keys directory is created `0700` (an existing looser directory is tightened)
and each keyfile is written `0600` with `O_CREATE|O_EXCL` — DWE never overwrites
an identity file.

An identity is read as the **first `AGE-SECRET-KEY-1…` token on a line that is
not a `#` comment**, so an `age` keyfile with its `# public key:` header works
verbatim, and a live key below a commented-out old one resolves to the live key.
A later token is ignored, not an error. When *every* token sits inside a comment
— a paste that joined header and key onto one line — the whole text is scanned
instead, unless a non-comment line carries a malformed `AGE-SECRET-KEY-1…`, which
reports as invalid rather than wrong.

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

`${vars.telegram.token}` then resolves to the plaintext everywhere, and
`dwe vars get vars.telegram.token` prints it.

## New developer / new machine

A clone carries the public recipient; the private identity does not. Until it is
on this machine every encrypted value reads `<encrypted>` and every lifecycle
command stops at `secrets.unresolved`.

Put it in one of the three places in the
[lookup order](#keys-where-the-identity-lives) — the first present source must
match the recipient.

### The interactive import

On a terminal, with no `--file` and nothing piped, `import` asks for the key:

```
$ dwe secrets key import
Private identity for age1qyqs…
Paste the AGE-SECRET-KEY-… line, or the whole keyfile; the typed characters are not echoed.
> ••••••••••••

identity for age1qyqs… stored at /home/dev/.config/dwe/keys/age1qyqs….key
2 encrypted value(s) and 1 .age file(s) are now readable
```

The field is hidden and accepts the whole keyfile, comment header included, even
as a single joined line. Validation runs **in the form**: a key that does not
parse, or one belonging to another project, is reported without closing the
prompt. `Esc` cancels with `secrets_import_cancelled` and no keyfile.

The second line is the same scan [`dwe secrets status`](#dwe-secrets-status)
renders, so the import reports what it opened.

### The offer inside `dwe deploy`, `dwe run` and `dwe restart`

When a project has encrypted material, no usable identity and a human at a
terminal, these entry points offer to take the key:

```
Enter the private identity now?
This project has encrypted values that need the age identity for age1qyqs…, and
this machine does not have it. run 'dwe secrets key import' to store the identity
at /home/dev/.config/dwe/keys/age1qyqs….key, or set DWE_AGE_KEY / DWE_AGE_KEY_FILE
  [ Enter key ]   [ Abort ]
```

`Enter key` opens the same hidden prompt and the command **continues in the same
invocation** — the offer runs on the raw layers before the config is loaded, so
the config is read once and already decrypted. `Abort` (or `Esc`) ends the
command with the fix instruction and nothing written: the typed
`secrets_no_identity` in the `dwe deploy` menu, the same sentence in `dwe run` /
`dwe restart`.

- **`dwe restart` offers before it stops anything**, so declining leaves the
  stack running. `dwe restart <service>` goes straight to `docker restart` and
  runs neither the offer nor preflight.
- **The `dwe deploy` offer sits at the menu's entry**, covering `Plan` as well as
  `Run` and the wizard.
- **Non-interactive entry points keep the hard error**: `dwe deploy run`,
  `dwe reset`, `dwe render env` and `dwe render config`.

### What CI and scripts see

The offer never opens when there is nobody to answer: stdin is not a terminal,
`--yes` (defined by `dwe run` and `dwe restart` only), `--output json`, or
`DWE_NONINTERACTIVE=1`. Each keeps the existing failure — the
`secrets.unresolved` preflight wall — so a pipeline's output and exit code do not
change. `key import` is unchanged too: a piped identity is read as before, and at
a terminal `--output json` and `DWE_NONINTERACTIVE=1` refuse with
`secrets_identity_source_required` instead of prompting.

### A broken source is reported, never prompted

Because the first present source wins with no fall-through, a `DWE_AGE_KEY` that
is set but does not hold this project's identity **cannot be fixed by importing a
key** — the new keyfile would not be consulted. The gate names the source
instead:

```
$DWE_AGE_KEY is set but does not hold the identity for age1qyqs…;
unset it or fix it — a keyfile is not consulted while it is set
```

An existing keyfile holding another key is refused the same way, pointing at
[`dwe secrets key remove`](#dwe-secrets-key-remove). Both refusals fire in every
mode, interactive or not.

## Subcommands

Writers take the project locks (`deploy.lock` → `snapshot.lock`) and run **no
preflight** — they are config edits, not stack mutations. `status`, `get` and
`key export` write nothing. `decrypt` takes no locks but does write a plaintext
file. `key list` and `key remove` operate on the machine-wide keys directory, run
outside a project, and take the locks only when a project is resolved.

**`init`, `set` and `rekey` edit a layer file by replacing single lines.** They
do not re-encode the document, so indentation, blank lines, comments, anchors,
`<<:` merge keys, quoting style and line endings survive byte-for-byte: one
`dwe secrets set` into a large annotated `defaults.yml` is a one-line diff. A new
key is inserted — at the end of the file for a new top-level block, otherwise
after the nearest existing mapping.

A handful of shapes cannot be edited in place and are **refused with the file
untouched** (`secrets_write_unsupported`), naming the path and the fix:

| Refused shape | Fix |
|---------------|-----|
| A literal or folded block scalar, or a plain/quoted scalar wrapped over several lines | Write the value on one line |
| A target inside a flow collection (`{a: 1}`, `[x, y]`) | Write it as a block collection |
| A parent that is `null` (`vars:` with nothing under it), a sequence, or a non-mapping scalar | Materialize the parent as a block mapping |
| A parent reached through a YAML alias (`vars: *common`), or a key that may be inherited through a `<<:` merge key | Write the key explicitly in that mapping |

The spliced bytes are re-parsed and the value read back at its path before
anything is persisted; a mismatch is refused too.

### `dwe secrets init`

```
dwe secrets init
```

Generates an X25519 key pair, writes the identity to
`~/.config/dwe/keys/<recipient>.key`, and splices `secrets.recipient` into
`workspace.yml`. The keyfile is written **first** — a committed recipient with no
readable identity would lock the project out of its own secrets; if the
`workspace.yml` write then fails, the keyfile is removed so a re-run is not
blocked by the no-clobber guard.

Refuses when `secrets.recipient` is already set, branching on whether an identity
for the project is available here and reporting which in the `identity` detail:

- `identity: "available"` — the values are recoverable, so replacing the key pair
  is [`rekey`](#dwe-secrets-rekey). The keys directory is consulted as well as the
  [lookup order](#keys-where-the-identity-lives), so a `DWE_AGE_KEY` exported for
  another project does not misreport a healthy project as the case below.
- `identity: "missing"` — nothing here opens the project, and `rekey` **cannot
  run at all** (it must read every value before rewriting one). The refusal
  offers [`key import`](#dwe-secrets-key-export--import) first, and
  `init --replace-recipient` as the recovery.

#### `dwe secrets init --replace-recipient`

```
dwe secrets init --replace-recipient [--yes]
```

The exit from a **lost identity**. It mints a new key pair over the old
`secrets.recipient`, and that is all: every existing `ENC[age:…]` marker and
`*.age` source stays in place and becomes permanently unreadable. Those values
come back only by being re-entered.

The orphans are left on purpose — they are the record of *which* secrets have to
be re-entered. `dwe secrets set <path>` overwrites each in place,
`dwe secrets status` is the remaining to-do list, and `secrets.unresolved` keeps
the lifecycle commands stopped until it is empty. The final report names every
orphaned value.

**It refuses while anything is still readable here**
(`secrets_identity_available`, with a `readable` count) — that is `rekey`'s case.
To discard those anyway, save what you need with `dwe secrets get`, drop the
identities that open them with
[`dwe secrets key remove <recipient> --force`](#dwe-secrets-key-remove), and run
it again.

A confirmation naming the number of values at stake is required; `--yes` skips
it, and a mode with no way to ask refuses with `secrets_confirmation_required`.
The confirmation runs **before** the project locks are taken, so an open prompt
does not stall other `dwe` commands; `secrets.recipient` is re-read once the
locks are held, and a concurrent change refuses with
`secrets_recipient_changed` without writing. The old keyfile is never touched —
`key remove` deletes a keyfile.

On a project with no `secrets.recipient` the flag is refused
(`secrets_no_recipient`).

### `dwe secrets status`

```
dwe secrets status
```

The report you run to find out why something is blocked. **No encrypted value can
make it fail** — with no key and every value unreadable it still exits 0. (A
config that does not load at all is still an error.)

It shows the configured recipient, the identity this machine holds (and, when
there is none, where the lookup looked), and two inventories: every `ENC[age:…]`
scalar across the three layer files as `layer` / `path` / `state`, and every
`*.age` under `workspace/templates/config/**`.

Each entry is **actually decrypted**, so the report separates the causes:

| State | Meaning |
|-------|---------|
| `decrypted` / `decryptable` | The configured identity reads it |
| `decrypted: stale_key` / `decryptable: stale_key` | Readable here, but only with an *older* keyfile |
| `unresolved: no_identity` | No identity for this recipient is available here |
| `unresolved: wrong_identity` | An identity was found, but it does not open this value |
| `unresolved: invalid_identity` | A source was set but holds no age key — e.g. a truncated `DWE_AGE_KEY`. Repair that source; a keyfile is not consulted while an env source is set |
| `unresolved: corrupt` | The payload is damaged — not a key problem |

`corrupt` is detected without a key (marker shape, base64, age header), so a
keyless developer is not sent hunting for a key that would not have helped.

A half-rekeyed tree is reported **per value**: the configured identity is tried
first, then every other keyfile in the keys directory. Those are the `stale_key`
rows, rendered amber — the config loader tries the *configured* identity alone,
so such a value is still `wrong_identity` at load time and `secrets.unresolved`
still blocks the lifecycle commands until `rekey` finishes. A `*.age` candidate
that fails the path discipline (a symlink, a device) is `not decryptable` with
the refusal as its reason, never silently skipped.

Rows are sorted (layer order, then path), so output is stable and diffable.

#### Shadowed markers

A marker a **higher layer overrides with a plaintext value** decrypts perfectly
well and is still not what the project reads. Such a row renders amber and
carries the override on its state cell:

```
decrypted (shadowed by workspace/local.yml)
```

An *unresolved* marker can be shadowed too. With the plaintext covering for it, a
lost identity then surfaces nowhere in everyday use except
[`dwe render config`](../render/index.md).

In `--output json` a shadowed row carries two extra fields:

| Field | Meaning |
|-------|---------|
| `shadowed_by` | The layer file supplying the plaintext that wins the merge |
| `shadow_match: identical` | The override holds the **same** value — almost always a copy left behind when the value was encrypted |
| `shadow_match: different` | A different value — a deliberate local override |
| `shadow_match: unknown` | The marker could not be decrypted here, so the two were not compared |

A marker overridden by **another marker** is not reported: the winning value is
still encrypted at rest. `dwe validate secrets` reports the same finding as a
warning — see [`secrets.shadowed`](validate.md).

The **Identity** line reports the lookup honestly — a source that was consulted
and rejected never reads as a missing key:

| Header | Meaning |
|--------|---------|
| `keyfile (…)` / `$DWE_AGE_KEY` / `$DWE_AGE_KEY_FILE` | The identity loaded from that source |
| `none (looked at …)` | No identity anywhere; the line names every place the lookup looked |
| `invalid (…)` | A source was set but holds no age key — the line names the source to repair |
| `wrong recipient (…)` | A readable identity for **another** recipient; the line names both |

Whenever the identity did not load the report closes with the fix instruction —
the same sentence `dwe validate` prints. In `--output json` the same facts are
structured under `identity` (see [JSON output](#json-output)). Every string is
DWE-authored: an `age` parse error echoes the input, which for a broken identity
source is private-key bytes, so it is never printed.

### `dwe secrets set`

```
dwe secrets set <vars.path> [value] [--file defaults|workspace] [--stdin]
```

Encrypts a value to the project's recipient and writes it as a marker into a
**committed** layer file. Needs only the recipient.

The value comes from the positional argument (which **lands in the shell
history**), from `--stdin` (the whole of stdin, with exactly one trailing newline
and a preceding `\r` trimmed), or from a **hidden prompt** on a terminal.
Passing both an argument and `--stdin` is `secrets_value_ambiguous`.

- **The `vars.` prefix is required**, unlike `dwe vars set`.
- **No coercion.** The value is stored as a string, so a secret that looks like a
  number (`"123"`) stays what you typed. Contrast
  [`dwe vars set`](vars.md#dwe-vars-set), which parses the argument as a YAML
  scalar.
- **`--file`** targets `workspace/defaults.yml` (default) or `workspace.yml`.
  `--file local` is refused with a pointer to `dwe vars set` —
  `workspace/local.yml` is gitignored personal state, where encryption buys
  nothing.
- Descending through an existing **non-mapping** node (`vars.db.host.port` where
  `host` is a scalar) is refused as an unsupported shape, like the other
  [refused shapes](#subcommands).
- The staged document is validated as a config layer **before** it is persisted,
  so a `set` cannot leave a layer unloadable.

The value is resolved *before* the project locks are taken, since a hidden prompt
can sit open indefinitely; the recipient is then re-read **under the lock**, so a
value cannot be encrypted to a recipient a concurrent `rekey` retired.

Writes change only the target line. A missing `workspace/defaults.yml` is created
`0644` — only `local.yml` is forced to `0600`.

### `dwe secrets get`

```
dwe secrets get <vars.path>
```

Decrypts the marker at a path and prints the plaintext. Needs the identity.

`get` reads the layers **as written**, so it reports the secret itself rather
than the merged value: when a marker is shadowed by a plaintext override,
`dwe secrets get` prints the secret and [`dwe vars get`](vars.md#dwe-vars-get)
prints the override that wins at runtime.

A path holding no marker is `secrets_not_encrypted`.

### `dwe secrets encrypt` / `decrypt`

```
dwe secrets encrypt <file>     [--out PATH|-] [--force]
dwe secrets decrypt <file.age> [--out PATH|-] [--force]
```

Whole-file helpers for config-pack sources. `encrypt` writes `<file>.age` beside
the input; `decrypt` strips the `.age` suffix (an input that does not end in
`.age` needs an explicit `--out`). Both refuse to overwrite without `--force`.
`encrypt` needs only the recipient; `decrypt` needs the identity.

The flag is `--out`, not `-o`: the root command owns `-o` for `--output`.
`--out -` streams to stdout and **rejects** `--output json`
(`secrets_raw_stream`).

An absolute path outside the project is legal, but a **symlink or non-regular
file is refused wherever it lives**, as is an output equal to the input.

A decrypted output is `0600`, and an existing one is **tightened** to `0600`. An
overwritten *ciphertext* file keeps its repository mode.

### `dwe secrets key export` / `import`

```
dwe secrets key export
dwe secrets key import [--file|-f PATH]
```

The identity is never in git — it travels through a password manager or another
out-of-band channel.

`export` prints `AGE-SECRET-KEY-1…` to stdout, and warns on stderr (text mode
only) when stdout is a terminal.

`import` reads the identity from `--file`, from piped stdin, or from a **hidden
prompt** on a terminal with neither. Whatever the source, it **verifies the
recipient matches the configured one** (`secrets_identity_mismatch`) and only
then writes the keyfile `0600`. It takes the project locks, so an import cannot
race a `rekey` into installing the identity being retired.

Paste either the `AGE-SECRET-KEY-1…` line or the whole keyfile. The prompt
validates in place — a key that does not parse, or one belonging to another
project, is reported without closing the form. `Esc` cancels with
`secrets_import_cancelled`. Because the write is `O_EXCL`, an already-installed
identity is reported **before** the form opens.

The prompt never opens without a terminal: a piped identity is read from stdin,
an empty stdin is `secrets_identity_source_required`, and at a terminal
`--output json` and `DWE_NONINTERACTIVE=1` refuse with the same code.

A successful import ends with what the key opened:

```
identity for age1… stored at ~/.config/dwe/keys/age1….key
2 encrypted value(s) and 1 .age file(s) are now readable
```

Only values the *configured* identity opens are counted. If the scan cannot run
at all, the import still **succeeds** and the second line becomes
`the readability report could not be built: <reason>`; JSON then omits
`markers_readable` / `files_readable` in favour of `report_error`.

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

| State | Meaning |
|-------|---------|
| `ok` | The file holds the age identity its name claims |
| `unreadable` | The file could not be read (permissions, a directory, a dangling link) |
| `unparsable` | The file holds no age identity |
| `misnamed` | It parses, but belongs to another recipient than the file name says — the row shows the **parsed** recipient |

The keys directory is **machine-wide, not per project**, so nothing is ever
pruned automatically: a key here may belong to any other project. The only
relation `list` states is the row this project uses, marked `current project`.
Outside a project no row is marked and the command still runs. An empty or absent
directory prints `No identities in <dir>.` and exits 0.

The states are a fixed vocabulary — no I/O or parse error text reaches the
output, because both echo file content. For an `unreadable` or `unparsable` file
the RECIPIENT column shows the file name's stem, never anything read out of the
file.

### `dwe secrets key remove`

```
dwe secrets key remove <recipient> [--force] [--yes|-y]
```

Deletes `~/.config/dwe/keys/<recipient>.key`. **The argument names the file**, so
a `misnamed` file is removed under its own name; aiming at the recipient
`key list` shows for it is `secrets_key_not_found`, as is a file that is not
there.

Removing the file that HOLDS the current project's identity is refused
(`secrets_key_in_use`) unless `--force`. The guard reads the file, not its name,
so it covers a `misnamed` file carrying this project's key. A file that opens
nothing — no age identity, another project's key, or a dangling symlink — is
removed without `--force`.

A file whose bytes cannot be **read** is refused (`secrets_key_unreadable`) until
`--force`: deleting a file needs no read permission, so waving it through would
unlink key material nobody ruled out as live.

Otherwise the removal is confirmed interactively. Where it cannot ask — no
terminal, `--output json`, `DWE_NONINTERACTIVE=1` — it is
`secrets_confirmation_required`, and `--yes` is the way through; declining prints
`kept <path>` and exits 0. The project locks are held around the delete when a
project is resolved.

### `dwe secrets rekey`

```
dwe secrets rekey
```

Mints a new key pair and re-encrypts **every** committed secret — every marker in
the layer files and every `*.age` pack source — to it. Run it when the identity
may have leaked, or when someone who held it should no longer read the project's
secrets.

This machine must be able to read every existing secret. The configured identity
is tried first, then every other keyfile in the keys directory, so a half-rekeyed
tree finishes cleanly.

The order is a **recoverable sequence, not a transaction**:

1. **Read-only pass** — decrypt and validate everything into memory, then
   rehearse every layer-file edit on a throwaway copy. A corrupt marker, an
   undecryptable `.age`, or a marker whose YAML shape cannot be rewritten in
   place aborts here with **nothing written**.
2. **Write the new keyfile** — the first mutation. The old keyfile is kept.
3. **Re-encrypt** every `.age` file (atomically) and every layer file (one
   spliced line per marker).
4. **Update `secrets.recipient` last.**

A crash mid-way leaves both identities on disk and a mixed tree; re-running
`rekey` converges. Failures after step 2 say so, and the JSON envelope
distinguishes them from the read-only refusals (which carry `written: false`).

Remove the old keyfile once every developer has imported the new identity.

## Without a key: what still works

A project whose secrets cannot be decrypted here **still loads**. Markers stay
literal in the config, and:

| Surface | Behaviour |
|---------|-----------|
| `dwe status`, `dwe docs`, `dwe validate`, `dwe prompt`, `dwe commands` | Work normally |
| `dwe vars list` / `get` / `inspect`, the TUI browser | Show `<encrypted>` — never the ciphertext |
| `dwe secrets status` | Reports every value and its reason; exits 0 |
| `dwe run`, `dwe restart`, `dwe deploy`, `dwe reset`, `dwe stop`, the deploy wizard | **Blocked** by the `secrets.unresolved` preflight validator. At a terminal, `dwe run` / `dwe restart` and the `dwe deploy` menu first [offer to take the identity](#the-offer-inside-dwe-deploy-dwe-run-and-dwe-restart) |
| `dwe render env`, `dwe render config` | **Fail** naming the value that would have been written |
| `dwe render ide` / `ai` / `git` | Work — they render against a sanitized config and emit the marker |

A missing key never renders a secret as `""`, and never writes a marker into an
output file.

`dwe stop` is blocked because it runs the `lifecycle.yml` stop hooks, which are
ordinary user commands and may reference `${vars.*}`; it shares the `stop`
preflight stage with `dwe reset`. Pass `--skip-preflight` to tear a stack down on
a machine with no key.

An encrypted `project.name` / `project.prefix` is treated as **unset** by the
`dwe prompt` hot path, which has its own lenient parser and never loads the full
config. A marker in the compose label filter would match no container.

## Output guards: no marker ever reaches a rendered file

Two renderers run with no preflight (`dwe render env`, `dwe render config`), and
`dwe run` renders `.env` *before* its preflight, so they enforce the policy
themselves:

- **`.env`** — every emitted value is checked: the system variables (`PROJECT`
  and `COMPOSE_PROJECT_NAME`) and every `exports.env` rule. A marker is an error
  naming the variable and its source path. This fires from all four `.env` write
  sites: `dwe render env`, the compose auto-regeneration before `dwe docker up` /
  `run` / `exec` / `restart` / `build`, `dwe services enable` / `disable`, and the
  render `dwe run` performs before its own preflight. The root `.env` is also
  `chmod`ed to `0600`.
- **Config packs** — after the `${...}` render, an output that still contains a
  marker is refused, naming the entry's `to:` path.
- **ide / ai / git packs** — their outputs are usually **tracked by git**, so
  those three renderers load a **sanitized** config assembled over the raw layers
  with no decrypt pass. Every field a template can reach (`.Raw`, `.Vars`,
  `.Project`, `.Runtime`, `.Services`, `.ServiceCfg`) carries the marker where the
  real config carries plaintext, so a template that reads a secret emits
  ciphertext — already committed, harmless.

`.age`-sourced pack outputs are written `0600` and explicitly `chmod`ed. Other
pack outputs keep `0644`, so a scalar secret substituted into a rendered `.env`
lands in the gitignored hub dir at the pack's usual mode. The container reads a
`0600` file fine, because it runs as the host UID/GID that `exports.env`
publishes.

## Validation and preflight

Three validators in the `secrets` domain (`dwe validate secrets`):

| Validator | Kind | Fires when |
|-----------|------|------------|
| `secrets.recipient` | content | Markers or `.age` sources exist but `secrets.recipient` is missing or is not a valid `age1…`; or a marker payload is damaged (`corrupt`) |
| `secrets.unresolved` | readiness | Any value in the merged config is unresolved, or a resolvable config pack's `.age` source fails to decrypt with the loaded identity |
| `secrets.shadowed` | effectiveness (**warning**) | A higher layer overrides a marker with a plaintext value, so the encrypted value is never read |

`secrets.recipient` raw-loads the layers itself when the config failed to load,
so a scoped `dwe validate secrets` still diagnoses a malformed recipient.

`secrets.unresolved` is the **second exception** to the preflight rule that only
`env.*` and `checks.*` run there (the first is `config.validate`): it is a
readiness question. It is cherry-picked into `preflight.Run` and into the deploy
wizard's gate, so `dwe run` / `deploy` / `reset` and the menu stop with the same
named fix. At an interactive `dwe run` / `dwe restart` / `dwe deploy` the
[key offer](#the-offer-inside-dwe-deploy-dwe-run-and-dwe-restart) runs first, so
the validator sees only what the offer never covered; a *declined* offer never
reaches it.

Unresolved markers are grouped **by reason**, one diagnostic per reason listing
the sorted paths — a keyless developer has every marker unresolved for one cause,
and one row per marker would bury the single actionable fix. The `.age` source
scan mirrors what `render config` iterates, so a disabled service or an
unresolvable pack is invisible to the validator exactly as at render time.

**A shadowed marker is a warning, not an error.** Overriding a shared secret
locally is legitimate, so it never blocks and never runs in preflight. Findings
are grouped by overriding file and by whether the override holds the same value
as the marker or a different one. See
[Shadowed markers](#shadowed-markers) for what `dwe secrets status` shows.

**A healthy project says so.** Each validator emits an `✓` row when it finds
nothing wrong — `validation result: 3 checks` rather than `validation skipped (no
files found)`. Each row appears only when that validator had something to check:
the recipient row needs a valid `secrets.recipient`, the unresolved row an
inventory to read (`1 encrypted value(s) and 0 config-pack source(s) readable via
keyfile`), the shadowed row any markers at all (`1 encrypted value(s), none
shadowed by a plaintext override`). A project with no `secrets:` block stays
silent. The identity is named by source word (`env` / `env-file` / `keyfile`),
never by path. Preflight and the deploy wizard's gate filter `✓` rows.

## Where plaintext goes

Decrypted values exist, by design, in:

- **process memory** — the merged config;
- **the project-root `.env`** — gitignored, `0600`;
- **config-pack outputs in the service hub dir** — gitignored via `/services/`;
- **`.dwe/generated.yml`** — `0600`, gitignored, when a `generated:` harvest
  reads a rendered hub file containing a decrypted value;
- **the container**, which reads the hub dir;
- **child-process output** the user chose to run.

They are kept **out of**:

- **git-tracked files** — ide / ai / git pack outputs render against a sanitized
  config (see [output guards](#output-guards-no-marker-ever-reaches-a-rendered-file));
- **dwe's own command echoes** — `-v` / `--debug` traces, their `.dwe/logs`
  copies and every plan / dry-run surface (see below);
- **`dwe vars` output** when the key is absent — `<encrypted>`, never the
  ciphertext.

A value passed to `dwe secrets set` **on the command line lands in shell
history**. Use `--stdin` or the hidden prompt for anything that matters.

## Redaction

Every value the config loader decrypts is registered with the trace subsystem,
which prints `***` in place of it. Redaction covers:

- **Diagnostic echoes** — `-v` / `--debug` command echoes, and the `.dwe/logs`
  mirrors of parallel pipeline steps.
- **Live-run skip reasons** — the `Skipped: <step> (when: …)` line and its
  parallel-group equivalent. The reason is display-only and never persisted to
  `.dwe/deploy/state.yml`, so the deployment hash is unaffected.
- **Plan and dry-run surfaces** — `dwe deploy plan` (table, `--format shell` and
  `--output json`, including the `unresolved` field), `dwe reset plan` and
  `dwe reset step --dry-run`. Redaction happens in the display functions that
  build those lines, before the value is quoted or embedded into a `--set k=v`
  argument.

Because `--format shell` is redacted, **it is a preview of what will run, not a
script to execute**. What actually executes is never redacted.

- **Values shorter than 4 runes are not redacted** — redacting `"1"` would shred
  every line of output. `dwe secrets set` stores such a value but warns on stderr.
- **Child-process output is not redacted** (an explicit non-goal).
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
reaches the host `dwe`, which holds the identity, and returns plaintext — the
same exposure the container already has through its rendered `.env` and config
files. `dwe render config` is likewise reachable and decrypts host-side.

## `age` CLI interoperability

Nothing here is a private format. A marker payload is base64 of a binary age file
with one X25519 stanza:

```bash
# Open a marker with the age CLI:
dwe secrets get vars.telegram.token                       # the dwe way
echo 'YWdlLWVuY3J5…' | base64 -d | age -d -i ~/.config/dwe/keys/age1….key

# Open a pack source:
age -d -i ~/.config/dwe/keys/age1….key creds.json.age
```

The keyfile is an ordinary age identity file, so `age`, `age-keygen -y` and every
other age tool work on it.

## JSON output

Every subcommand routes through `--output json` (with `--pretty`) and keeps
stdout clean; typed errors serialize to a `{"error":{…}}` envelope on stderr.

| Command | Shape |
|---------|-------|
| `init` | `{"recipient": "age1…", "keyfile": "/…/age1….key"}` — `--replace-recipient` adds `old_recipient` plus `orphaned_markers` / `orphaned_files`, carrying the same row shapes `status` reports |
| `status` | `{"recipient": "age1…", "identity": {"source": "keyfile\|env\|env-file\|", "keyfile": "…", "reason": "…", "error": "…", "hint": "…"}, "markers": [{"layer": "…", "path": "…", "state": "…", "reason": "…", "shadowed_by": "…", "shadow_match": "identical\|different\|unknown"}], "files": [{"file": "…", "state": "…", "reason": "…", "detail": "…"}]}` — `shadowed_by` / `shadow_match` appear only on a shadowed marker; a file row's `reason` stays inside the fixed vocabulary (`no_identity` / `wrong_identity` / `invalid_identity` / `corrupt` / `stale_key` / `unreadable`), with `detail` carrying the free-form cause behind `unreadable` |
| `set` | `{"path": "vars.…", "file": "workspace/defaults.yml"}` |
| `get` | `{"path": "vars.…", "value": "…"}` |
| `encrypt` / `decrypt` | `{"from": "…", "to": "…"}` |
| `key export` | `{"recipient": "age1…", "identity": "AGE-SECRET-KEY-1…"}` |
| `key import` | `{"recipient": "age1…", "keyfile": "/…/age1….key", "markers_readable": N, "files_readable": N}` — the counters are replaced by `report_error` when the surface could not be scanned |
| `key list` | `{"keys": [{"recipient": "age1…", "file": "/…/age1….key", "state": "ok\|unreadable\|unparsable\|misnamed", "current": true}]}` |
| `key remove` | `{"recipient": "age1…", "keyfile": "/…/age1….key", "removed": true}` |
| `rekey` | `{"old_recipient": "age1…", "recipient": "age1…", "keyfile": "…", "markers": N, "layers": ["…"], "files": ["…"]}` |

`key list` always emits `keys` as an array — `[]` when the directory is empty or
absent, never `null`. `key remove` always reports `removed: true`: a refusal is a
typed error envelope, not a payload saying nothing happened.

On `status`, `identity` is an object rather than a flat string: `source` is the
stable vocabulary a script branches on (the **consulted** source, filled on
failure too), `reason` the stable reason word, `error` the human sentence and
`hint` the fix. An identity load failure is **data** here, not an error.

Every failure carries a typed `secrets_*` code. The ones that describe a state
you act on, rather than an I/O failure:

`secrets_already_initialized` (whose `identity` detail is `available` or
`missing`), `secrets_identity_available` (`init --replace-recipient` while values
are still readable, with a `readable` count), `secrets_recipient_changed`,
`secrets_no_recipient`, `secrets_no_identity`, `secrets_identity_mismatch`,
`secrets_identity_invalid`, `secrets_identity_source_required`,
`secrets_not_encrypted`, `secrets_path_invalid`, `secrets_file_invalid`,
`secrets_value_ambiguous`, `secrets_value_required`, `secrets_input_missing`,
`secrets_output_required`, `secrets_output_invalid`, `secrets_output_exists`,
`secrets_raw_stream`, `secrets_rekey_blocked`, `secrets_import_cancelled`,
`secrets_write_unsupported` (a [refused shape](#subcommands) — the file is
untouched), and, for the keys directory, `secrets_key_in_use`,
`secrets_key_unreadable`, `secrets_key_not_found`,
`secrets_confirmation_required` and `secrets_recipient_invalid`.

The rest are per-operation I/O failures in the `secrets_<operation>_failed`
family — `secrets_encrypt_failed`, `secrets_decrypt_failed`,
`secrets_write_failed`, `secrets_keygen_failed`, `secrets_keyfile_write_failed`,
`secrets_recipient_write_failed`, `secrets_scan_failed`, `secrets_rekey_failed`,
`secrets_key_list_failed`, `secrets_key_remove_failed`,
`secrets_output_write_failed`, `secrets_value_read_failed`,
`secrets_input_read_failed`, `secrets_identity_read_failed`.

## Non-goals

- **No `${secret.*}` namespace.** A secret is a `vars.*` value that happens to be
  encrypted at rest; a second namespace would fork every consumer.
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
