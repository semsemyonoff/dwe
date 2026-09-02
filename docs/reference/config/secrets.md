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
- [Subcommands](#subcommands)
  - [`dwe secrets init`](#dwe-secrets-init)
  - [`dwe secrets status`](#dwe-secrets-status)
  - [`dwe secrets set`](#dwe-secrets-set)
  - [`dwe secrets get`](#dwe-secrets-get)
  - [`dwe secrets encrypt` / `decrypt`](#dwe-secrets-encrypt--decrypt)
  - [`dwe secrets key export` / `import`](#dwe-secrets-key-export--import)
  - [`dwe secrets rekey`](#dwe-secrets-rekey)
- [Without a key: what still works](#without-a-key-what-still-works)
- [Output guards: no marker ever reaches a rendered file](#output-guards-no-marker-ever-reaches-a-rendered-file)
- [Validation and preflight](#validation-and-preflight)
- [Where plaintext goes](#where-plaintext-goes)
- [Diagnostic redaction](#diagnostic-redaction)
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

Blank lines and `#` comment lines in an identity file are skipped, so an `age`
CLI keyfile (which carries a `# public key:` header) can be used verbatim.

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
dwe secrets key import --file identity.txt
```

Then `${vars.telegram.token}` resolves to the plaintext everywhere, and
`dwe vars get vars.telegram.token` prints it.

## Subcommands

All writers take the project locks (`deploy.lock` → `snapshot.lock`) and run
**no preflight** — they are config edits, not stack mutations. `status`, `get`,
`decrypt` and `key export` are read-only.

### `dwe secrets init`

```
dwe secrets init
```

Generates an X25519 key pair, writes the identity to
`~/.config/dwe/keys/<recipient>.key`, and writes `secrets.recipient` into
`workspace.yml` through the comment-preserving node writer (comments, anchors
and a `<<:` merge key survive; the file keeps its mode).

The keyfile is written **first**: a recipient committed without a readable
identity would lock the project out of its own secrets. If the `workspace.yml`
write then fails, the keyfile is removed again so a re-run is not blocked by
the no-clobber guard.

Refuses when `secrets.recipient` is already set — replacing a live key pair is
[`rekey`](#dwe-secrets-rekey), which re-encrypts the existing values instead of
orphaning them.

### `dwe secrets status`

```
dwe secrets status
```

The report you run to find out why something is blocked. It never blocks
anything itself: **always exits 0**, even with no key and every value
unreadable.

It shows the configured recipient, the identity this machine holds (and, when
there is none, where the lookup looked), and two inventories:

- **Markers** — every `ENC[age:…]` scalar across the three layer files, as
  `layer` / `path` / `state`.
- **Encrypted files** — every `*.age` under `workspace/templates/config/**`.

Each entry is **actually decrypted**, so the report distinguishes the three
causes rather than lumping them together:

| State | Meaning |
|-------|---------|
| `decrypted` / `decryptable` | This machine can read it |
| `unresolved: no_identity` | No identity for this recipient is available here |
| `unresolved: wrong_identity` | An identity was found, but it does not open this value |
| `unresolved: corrupt` | The payload is damaged — not a key problem |

`corrupt` is detected without a key (marker shape, base64, age header), so a
keyless developer is never sent hunting for a key that would not have helped.

A half-rekeyed tree is reported **per value**: the configured identity is tried
first, then every other keyfile in the keys directory, so `status` says which
values still need the old key rather than failing wholesale. A `*.age`
candidate that fails the path discipline (a symlink, a device) is reported as
`not decryptable` with the refusal as its reason — never silently skipped.

Rows are sorted (layer order, then path), so the output is stable across runs
and diffable.

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
  is refused, leaving the file untouched.
- The staged document is validated as a config layer **before** it is
  persisted, so a `set` can never leave a layer unloadable.

The value is resolved *before* the project locks are taken — a hidden prompt
can sit open indefinitely, and holding the lock pair meanwhile would block
every other `dwe` command in the project. The recipient is then re-read **under
the lock**, so a value can never be encrypted to a recipient that a concurrent
`rekey` retired in between.

Writes preserve comments, key order and formatting; a missing
`workspace/defaults.yml` is created `0644` (tracked files keep a normal mode —
only `local.yml` is forced to `0600`).

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

`import` reads the identity from `--file` or stdin, **verifies that its
recipient matches the configured one** (`secrets_identity_mismatch`), and only
then writes the keyfile `0600`. It takes the project locks too: an import
racing a `rekey` would otherwise install the identity being retired. A TTY
stdin with no `--file` is refused rather than blocking on an invisible read.

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

1. **Read-only pass** — decrypt and validate everything into memory. A corrupt
   marker or an undecryptable `.age` aborts here, with **nothing written**.
2. **Write the new keyfile** — the first mutation. The old keyfile is kept.
3. **Re-encrypt** every `.age` file (atomically) and every layer file (through
   the comment-preserving node writer, so comments and anchors survive).
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
| `dwe run`, `dwe deploy`, `dwe reset`, the deploy wizard | **Blocked** by the `secrets.unresolved` preflight validator, naming the fix |
| `dwe render env`, `dwe render config` | **Fail** naming the value that would have been written |
| `dwe render ide` / `ai` / `git` | Work — they render against a sanitized config and emit the marker |

A missing key never renders a secret as `""`, and never writes a marker into an
output file.

An encrypted `project.name` / `project.prefix` is treated as **unset** by the
`dwe prompt` hot path (which has its own lenient parser and never loads the
full config) — an encrypted project name is nonsensical, and a marker in the
compose label filter would match no container.

## Output guards: no marker ever reaches a rendered file

Two renderers run with no preflight (`dwe render env`, `dwe render config`), and
`dwe run` renders `.env` *before* its preflight. They therefore enforce the
policy themselves:

- **`.env`** — every emitted value is checked: the system variables (including
  `PROJECT`, from `project.name`) and every `exports.env` rule. A marker is an
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

Unresolved markers are grouped **by reason**, one diagnostic per reason listing
the sorted paths. A keyless developer has every marker unresolved for one
cause; one row per marker would bury the single actionable fix.

The `.age` source scan mirrors what `render config` actually iterates, so a
disabled service or an unresolvable pack is invisible to the validator exactly
as it is at render time.

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
- **dwe's own diagnostic echoes** — `-v` / `--debug` traces and their `.dwe/logs`
  copies are redacted (see below);
- **`dwe vars` output** when the key is absent — `<encrypted>`, never the
  ciphertext.

A value passed to `dwe secrets set` **on the command line lands in shell
history** like any other argument. Use `--stdin` or the hidden prompt for
anything that matters.

## Diagnostic redaction

Every value the config loader decrypts is registered with the diagnostic trace
subsystem, so `-v` / `--debug` command echoes — and the `.dwe/logs` mirrors of
parallel pipeline steps — print `***` in place of the secret. The registration
lives in the loader because ~60 call sites load config and the root command hook
does not.

Two properties worth knowing:

- **Values shorter than 4 runes are not redacted.** Redacting `"1"` would shred
  every line of output.
- **Child-process output is not redacted** (an explicit non-goal). A command
  that prints its own configuration prints it.

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
| `init` | `{"recipient": "age1…", "keyfile": "/…/age1….key"}` |
| `status` | `{"recipient": "age1…", "identity": {"source": "keyfile\|env\|env-file\|", "keyfile": "…", "error": "…"}, "markers": [{"layer": "…", "path": "…", "state": "…", "reason": "…"}], "files": [{"file": "…", "state": "…", "reason": "…"}]}` |
| `set` | `{"path": "vars.…", "file": "workspace/defaults.yml"}` |
| `get` | `{"path": "vars.…", "value": "…"}` |
| `encrypt` / `decrypt` | `{"from": "…", "to": "…"}` |
| `key export` | `{"recipient": "age1…", "identity": "AGE-SECRET-KEY-1…"}` |
| `key import` | `{"recipient": "age1…", "keyfile": "/…/age1….key"}` |
| `rekey` | `{"old_recipient": "age1…", "recipient": "age1…", "keyfile": "…", "markers": N, "layers": ["…"], "files": ["…"]}` |

On `status`, `identity` is an object rather than a flat string: `source` is the
stable vocabulary a script branches on, `error` is the human sentence. An
identity load failure is **data** here, not an error — reporting "no identity,
and here is where it looked" is the command's whole job.

Typed error codes include `secrets_already_initialized`, `secrets_no_identity`,
`secrets_identity_mismatch`, `secrets_not_encrypted`, `secrets_path_invalid`,
`secrets_file_invalid`, `secrets_value_ambiguous`, `secrets_value_required`,
`secrets_output_exists`, `secrets_raw_stream`, `secrets_rekey_blocked`.

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
