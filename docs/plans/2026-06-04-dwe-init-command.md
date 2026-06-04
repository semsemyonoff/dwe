# dwe init command

## Overview

Add a new top-level `dwe init` command that scaffolds a fresh DWE project from
nothing. It writes the required project config (with rich commented placeholders),
a set of optional override files shipped as inert commented references, agent docs,
and the standard meta files (`.gitignore`, `.editorconfig`, `.dwe/config`).

Two modes:

- **Interactive (default)** — a `huh` form asks for the project **name** (required),
  **prefix** (default `dwe`), and **branding** (title / tagline / accent → `styles.yml`).
  Branding is skippable and falls back to a generic header.
- **Non-interactive** — driven entirely by flags (or directory defaults). Selected
  automatically when stdin/stdout is not a TTY, or when `--yes` / `--output json` is set.

The command runs **outside** an existing project (it creates one), is idempotent
(fill-gaps, never overwrite unless `--force`), and reports a structured
`{target, created, skipped, symlink_fallback}` result in JSON mode.

Problem it solves: today there is no bootstrap path — a new DWE project must be
hand-assembled file by file. `dwe init` gives a one-command, opinionated-but-inert
starting point that loads and `dwe validate`s clean on first run.

## Context (from discovery)

- **Project is** a Go cobra CLI (`dwe`, Dev Workspace Engine); two-layer `internal/`
  architecture: `cli/` (cobra) → `core/` (domain) → `shared/` (leaf infra).
- **Command wiring**: `internal/cli/root.go` — group const `groupConfiguration` (`root.go:51`);
  commands added via `root.AddCommand(cmdX.NewCmd(group, flags))` (`root.go:90-92`);
  project-free allowlist `allowedWithoutProject(cmd)` (`root.go:293`). `init` must be added there.
- **Interactive infra exists**: `internal/core/ui/ask` (`Run(ctx, title, []Field, RunOptions) (Result, error)`,
  `Field`/`Option`/`Result`/`FieldKind`), TTY gate `widgets.IsInteractiveFn(os.Stdin)`,
  theme `styles.Theme()`, abort sentinel `huh.ErrUserAborted`. `huh/v2` already in `go.mod`.
- **Default-config constructors** (sources for the inert mirrors' intent):
  `internal/core/workflow/deploy/defaults.go:DefaultDeployConfig`,
  `internal/core/workflow/lifecycle/defaults.go:DefaultRunConfig`/`DefaultStopConfig`,
  `internal/core/workflow/reset/defaults.go:DefaultResetConfig`.
- **Service loading**: `internal/core/project/config/services_loader.go` — empty/absent
  `workspace/services/` returns an empty map (not an error); each subdir requires a
  parseable `service.yml` with strict `KnownFields(true)` decode (so a fully-commented
  `service.yml` is invalid → the starter `app` service keeps `type`+`container` active).
- **Output/JSON**: `cmdctx.WriteData[T]` / `cmdctx.WriteError` / typed `cmdctx.Err`/`ErrWrap`;
  `cmdctx.RootFlags` carries `Output`, `Pretty`, `ProjectRoot()`.
- **Conventions to mirror**: repo `.editorconfig` (tabs for Go, 2-space YAML/JSON/sh);
  `AGENTS.md` canonical + `CLAUDE.md` symlink (this repo does exactly that);
  `dwe docs llms-txt` already emits an AI-agent index the generated `AGENTS.md` can point to.

## Development Approach

- **testing approach**: **Regular** (implement, then write tests in the same task).
- complete each task fully before the next; small, focused changes.
- **CRITICAL: every task includes new/updated tests** (success + error/edge cases), listed
  as separate checklist items.
- **CRITICAL: all tests pass before starting the next task.** Use `make test` (never bare
  `go test ./...` — embedded docs must sync first). For focused runs:
  `make embedded-docs` once, then `go test ./internal/cli/scaffold` /
  `go test ./internal/core/workflow/scaffold`.
- maintain backward compatibility (purely additive command).
- **update this plan file if scope changes during implementation.**

## Testing Strategy

- **unit tests**: required per task — template walker/renderer, atomic writer,
  `.gitignore` merge, symlink+fallback, flag→Options mapping, JSON output shape.
- **golden tests**: full scaffold into a temp dir, snapshot the file tree + contents
  (golden fixtures under `testdata/`). Use `config.DeployOrder`-style deterministic
  iteration — no map-order flakiness.
- **integration (load-bearing)**: scaffold → `config.LoadConfig` + `validate.*` → assert
  **zero errors** on a fresh project. Guards the active-minimal `service.yml` and the inert
  files against breaking load.
- **no e2e/UI harness** in this project — N/A.

## Progress Tracking

- mark completed items `[x]` immediately when done.
- add newly discovered tasks with ➕ prefix; blockers with ⚠️ prefix.
- keep the plan in sync with actual work.

## Solution Overview

Two new packages (note: `init` is an illegal Go package name → dir/package `scaffold`):

- `internal/cli/scaffold/` (package `scaffold`) — cobra `Use: "init"`. Owns flag parsing,
  interactive-vs-non-interactive selection, the `huh` form, and result reporting. **Only
  writer to stdout/stderr.**
- `internal/core/workflow/scaffold/` (package `scaffold`) — pure domain. `Scaffold(Options) (Result, error)`:
  computes the file plan, renders templates, writes atomically, returns
  `Result{Created, Skipped []string, SymlinkFallback bool, Target string}`. No cobra, no
  `io.Writer` (honors the "core returns data, cli writes" contract).

**Template storage**: `go:embed templates/**` inside the domain package, mirroring the
output tree. Walker rule:

- files ending `.tmpl` → rendered via `text/template` with **custom `[[ ]]` delimiters**
  (so they never clash with the literal `{{ .Project.Name }}` examples inside the inert
  `info.yml`/`docker.yml` references), then the `.tmpl` suffix stripped.
- every other file → copied **verbatim** (inert references, `.editorconfig`, `.dwe/config`).

**Why inert overrides**: DWE pipeline composition is full-replacement — an active
`deploy.yml` replaces the whole built-in pipeline, and a half-edit silently drops phases.
So `deploy/lifecycle/info/docker` ship fully commented; the built-in default stays active
until the user uncomments.

## Technical Details

**Generated tree** (✎ active/form-filled · ▢ active placeholder · ⊘ inert/commented):

```
<target>/
├─ workspace.yml        ✎ project.name + project.prefix (+ commented optional fields)
├─ compose.yaml         ▢ base compose; defaults.yml → compose.base: compose.yaml
├─ .gitignore           ✎ DWE runtime entries (append-merge if present)
├─ .editorconfig        ✎ only if absent; mirrors repo conventions
├─ AGENTS.md            ✎ brief DWE-project prompt; points to `dwe docs llms-txt`
├─ CLAUDE.md          → symlink to AGENTS.md (copy fallback if symlink fails)
├─ .dwe/
│  └─ config            ⊘ committed, all-commented user-config template (ralphex model)
└─ workspace/
   ├─ defaults.yml      ✎ starter service toggle + commented runtime/exports examples
   ├─ styles.yml        ✎ branding from the form + commented rest
   ├─ deploy.yml        ⊘ inert mirror of built-in deploy
   ├─ lifecycle.yml     ⊘ inert mirror of built-in lifecycle
   ├─ info.yml          ⊘ inert mirror of dashboard config
   ├─ docker.yml        ⊘ inert mirror of compose policy
   └─ services/app/
      └─ service.yml    ▢ type+container active, optional fields commented
```

**`Options`** (domain input):
`TargetDir string` · `Name string` · `Prefix string` · `Service string` (`""` = none) ·
`Branding struct{ Title, Tagline, Accent string }` · `Force bool`.

**Branding → real `styles.yml` schema** (`internal/core/project/config/styles.go`): there is
**no top-level `title:` field**. Render `Title → header.lines` (single-element list),
`Tagline → header.tagline`, `Accent → colors.accent`; leave `header.font` and the other
`colors.*` as commented placeholders. `LoadStylesConfig` uses lenient `yaml.Unmarshal`, so a
stray `title:` would silently vanish — render the nested shape, not a flat key.

**Command surface**:
```
dwe init [name] [flags]
  --name        project name      (default: [name] arg → cwd basename)
  --prefix      compose prefix     (default: "dwe")
  --brand-title / --tagline / --accent   styles.yml branding
  --service     starter service    (default: "app"; "" = none)
  --force       overwrite existing files
  -y, --yes     skip the form, take all defaults
  (--output json inherited)        → non-interactive, structured report
```

**Mode selection**: interactive iff `widgets.IsInteractiveFn(os.Stdin)` AND not `--yes`
AND `flags.Output != "json"`. Flags pre-fill the form's field defaults; the **entire form
is collected before any disk write**, so a mid-form `Ctrl-C` (`huh.ErrUserAborted`) leaves
the disk untouched and exits cleanly.

**Generated `.gitignore` entries** (paths verified against `internal/shared/lock/project.go`
— locks are nested under `deploy/`+`snapshots/`, already covered — and
`internal/shared/promptcache`):
```
# dwe — runtime data (managed by the CLI)
.dwe/deploy/
.dwe/snapshots/
.dwe/logs/
.dwe/prompt-cache.yml
# dwe — per-developer overrides
workspace/local.yml
workspace/docker.local.yml
# dwe — container data
volumes/
snapshots/
```
We ignore the `.dwe/` runtime **subdirs/files individually** (not `.dwe/` wholesale) precisely
so the committed `.dwe/config` template stays tracked. No `.dwe/*.lock` line — the real lock
files (`.dwe/deploy/deploy.lock`, `.dwe/snapshots/snapshot.lock`) are already covered by the
subdir entries.

**No preflight, no project locks** (there is no project yet); writes are atomic
(temp-file + rename). `init` runs even with no `workspace.yml`; if an **ancestor**
`workspace.yml` is detected, warn before creating a nested project.

## What Goes Where

- **Implementation Steps** (`[ ]`): all code, templates, tests, docs in this repo.
- **Post-Completion** (no checkboxes): manual cross-platform symlink check (Windows),
  real-world first-run UX walkthrough.

## Implementation Steps

### Task 1: Domain package skeleton + template walker/renderer

**Files:**
- Create: `internal/core/workflow/scaffold/scaffold.go` (package + `Options`, `Result`)
- Create: `internal/core/workflow/scaffold/templates.go` (`go:embed`, walker, renderer)
- Create: `internal/core/workflow/scaffold/templates_test.go`

- [x] define `Options` and `Result` structs in `scaffold.go`
- [x] add `//go:embed templates` FS and a `renderPlan(opts) (map[string][]byte, error)` that
      walks the FS: `.tmpl` → `text/template` with `Delims("[[", "]]")` (strip suffix), else verbatim
- [x] map embedded path → output path (e.g. `templates/dot-gitignore.tmpl` → `.gitignore`,
      `templates/dot-dwe/config` → `.dwe/config`) so embed can carry dotfiles —
      **`go:embed` excludes `.`/`_`-prefixed names; use the `dot-` rename, NOT `all:`**
      (`all:` would also pull in `_`-prefixed files and editor junk)
- [x] write tests: a `.tmpl` renders with `[[ .Name ]]`/`[[ .Prefix ]]` substitution
- [x] write tests: a verbatim file containing literal `{{ .Project.Name }}` is copied unchanged
- [x] write tests: unknown template key / parse error surfaces an error
- [x] run tests — must pass before Task 2

### Task 2: Atomic file writer with skip/force semantics

**Files:**
- Create: `internal/core/workflow/scaffold/writer.go`
- Create: `internal/core/workflow/scaffold/writer_test.go`

- [x] implement `writeFile(path string, data []byte, force bool) (written bool, err error)`:
      skip (return `false`) if the file exists and `!force`; else temp-file + `rename`, creating parent dirs
- [x] ensure parent directory creation (`os.MkdirAll`) and 0644/0755 perms
- [x] write tests: new file is written; existing file is skipped; `force` overwrites
- [x] write tests: nested path creates intermediate dirs; atomicity (no partial file on error)
- [x] run tests — must pass before Task 3

### Task 3: `.gitignore` append-merge

**Files:**
- Create: `internal/core/workflow/scaffold/gitignore.go`
- Create: `internal/core/workflow/scaffold/gitignore_test.go`

- [x] implement merge: if absent → create with the full DWE block; if present → append only
      the DWE lines not already present, under a `# dwe` marker comment
- [x] preserve the user's existing content and trailing-newline handling exactly
- [x] write tests: absent → created with full block; present-without-block → block appended
- [x] write tests: present-with-some-lines → only missing lines added; idempotent on re-run
- [x] run tests — must pass before Task 4

### Task 4: AGENTS.md ↔ CLAUDE.md symlink with copy fallback

**Files:**
- Create: `internal/core/workflow/scaffold/symlink.go`
- Create: `internal/core/workflow/scaffold/symlink_test.go`

- [ ] implement `linkClaudeMd(dir string) (fallback bool, err error)`: `os.Symlink("AGENTS.md", dir/CLAUDE.md)`;
      on error (or pre-existing target) write `CLAUDE.md` as a verbatim copy and return `fallback=true`
- [ ] make the symlink call injectable (func var) so the fallback path is testable
- [ ] write tests: POSIX symlink is created and resolves to `AGENTS.md`
- [ ] write tests: injected symlink failure → copy fallback written, `fallback=true`
- [ ] run tests — must pass before Task 5

### Task 5: Author the template content files

**Files:**
- Create: `internal/core/workflow/scaffold/templates/workspace.yml.tmpl`
- Create: `internal/core/workflow/scaffold/templates/compose.yaml`
- Create: `internal/core/workflow/scaffold/templates/dot-gitignore.tmpl` (DWE block source for Task 3)
- Create: `internal/core/workflow/scaffold/templates/dot-editorconfig`
- Create: `internal/core/workflow/scaffold/templates/AGENTS.md.tmpl`
- Create: `internal/core/workflow/scaffold/templates/dot-dwe/config`
- Create: `internal/core/workflow/scaffold/templates/workspace/defaults.yml.tmpl`
- Create: `internal/core/workflow/scaffold/templates/workspace/styles.yml.tmpl`
- Create: `internal/core/workflow/scaffold/templates/workspace/deploy.yml` (inert)
- Create: `internal/core/workflow/scaffold/templates/workspace/lifecycle.yml` (inert)
- Create: `internal/core/workflow/scaffold/templates/workspace/info.yml` (inert)
- Create: `internal/core/workflow/scaffold/templates/workspace/docker.yml` (inert)
- Create: `internal/core/workflow/scaffold/templates/workspace/services/app/service.yml.tmpl`
- Create: `internal/core/workflow/scaffold/templates_content_test.go`

- [ ] author active `.tmpl` files (`workspace.yml`, `defaults.yml`, `styles.yml`, `service.yml`,
      `AGENTS.md`, `dot-gitignore`) with `[[ ]]` placeholders + commented optional fields;
      `service.yml` keeps `type`+`container` active only
- [ ] `styles.yml.tmpl`: render the **nested** schema — `header.lines: [ [[ .Branding.Title ]] ]`,
      `header.tagline: [[ .Branding.Tagline ]]`, `colors.accent: [[ .Branding.Accent ]]`; comment
      out `header.font` + the other `colors.*` (no top-level `title:` key — it does not exist)
- [ ] author inert references (`deploy/lifecycle/info/docker`) fully commented, each with a
      header: "built-in default is ACTIVE; uncomment to override — REPLACES the whole pipeline.
      Authoritative default: `<Default*Config constructor>` / `docs/reference/config/<x>.md`"
      (the source pointer is the only drift guard — these files cannot be tested for divergence)
- [ ] author `dot-dwe/config` as an all-commented user-config template (ralphex model:
      full-line comments, every option documented with default)
- [ ] `AGENTS.md.tmpl`: brief project context + "run `dwe docs llms-txt` for the full index"
- [ ] write tests: every embedded template renders without error for representative `Options`
- [ ] write tests: every `.yml`/`.tmpl-rendered` output parses as YAML; inert files are
      100% comment/blank lines
- [ ] run tests — must pass before Task 6

### Task 6: Scaffold orchestration

**Files:**
- Modify: `internal/core/workflow/scaffold/scaffold.go`
- Create: `internal/core/workflow/scaffold/scaffold_test.go`
- Create: `internal/core/workflow/scaffold/testdata/` (golden fixtures)

- [ ] implement `Scaffold(opts Options) (Result, error)`: resolve target dir (mkdir for named
      target), render plan, write each file (skip/force), run `.gitignore` merge + symlink,
      omit the `app` service when `Service == ""`, accumulate `Created`/`Skipped`
- [ ] detect an ancestor `workspace.yml` and surface a warning signal in `Result`
      (CLI decides how to present); do not block
- [ ] write tests: full run into temp dir → golden tree + contents match `testdata/`
- [ ] write tests: idempotency (2nd run → all skipped, no diffs); `--force` overwrites;
      named-target creates `./<name>/`; `Service==""` omits the service folder
- [ ] run tests — must pass before Task 7

### Task 7: CLI command package

**Files:**
- Create: `internal/cli/scaffold/scaffold.go` (`NewCmd`, flags, mode, form, output)
- Create: `internal/cli/scaffold/scaffold_test.go`

- [ ] implement `NewCmd(groupID string, flags *cmdctx.RootFlags) *cobra.Command` with
      `Use: "init"`, positional `[name]`, and all flags (`--name/--prefix/--brand-title/--tagline/--accent/--service/--force/-y`)
- [ ] put the form behind an injectable func var (`runFormFn`, mirror
      `internal/cli/command/runbyid.go`) so the abort path is testable without driving real `huh`;
      keep flag→Options mapping + output shaping in plain functions outside the form seam
- [ ] mode selection: interactive iff `IsInteractiveFn(os.Stdin) && !yes && flags.Output != "json"`;
      run the form via `runFormFn` → `ask.Run` (name required w/ validation, prefix default `dwe`,
      skippable branding), flags pre-fill field defaults; assemble `Options`
- [ ] non-interactive: resolve name from `--name` → `[name]` → cwd basename; call `Scaffold`
- [ ] output: JSON → `cmdctx.WriteData` `{target, created, skipped, symlink_fallback, nested_warning}`;
      text → grouped created/skipped list + symlink-fallback note + next-steps footer; errors via `cmdctx.Err`/`ErrWrap`
- [ ] write tests: flag-driven non-interactive maps to the correct `Options`; JSON output shape;
      `huh.ErrUserAborted` from the injected `runFormFn` → clean exit, nothing written
- [ ] run tests — must pass before Task 8

### Task 8: Wire into the root command tree

**Files:**
- Modify: `internal/cli/root.go`

- [ ] add `root.AddCommand(cmdScaffold.NewCmd(groupConfiguration, flags))` in the configuration group
- [ ] add `path == "dwe init"` to `allowedWithoutProject(cmd)` — it matches on
      `cmd.CommandPath()` (`root.go:293-300`), so the bare string `"init"` would never match
      (mirror the existing `"dwe version"` / `"dwe prompt"` entries)
- [ ] write tests: `dwe init` runs outside a project (no `project_not_found`); `--help` succeeds;
      command is registered under the right group
- [ ] run tests — must pass before Task 9

### Task 9: Fresh-init validity integration test (load-bearing)

**Files:**
- Create: `internal/core/workflow/scaffold/validity_test.go`

- [ ] scaffold a project into a temp dir with default `Options`, then `config.LoadConfig`
      the result and assert no error
- [ ] run `validate/config.All()` (and `validate/templates.All()` if templates are scaffolded)
      over the loaded config and assert **zero error-severity diagnostics** — guards the
      active-minimal `service.yml` and inert files
- [ ] assert the rendered `styles.yml` round-trips through `LoadStylesConfig` with the form's
      title/tagline/accent preserved (catches the flat-`title:` regression from the review)
- [ ] write a variant with `Service == ""` (valid-but-empty) → loads clean (validate may
      emit a non-error "no services" notice only)
- [ ] run tests — must pass before Task 10

### Task 10: Verify acceptance criteria
- [ ] verify all Overview requirements implemented (two modes, idempotency, `--force`, JSON, symlink, inert overrides)
- [ ] verify edge cases: existing `.gitignore`/`.editorconfig`, nested-project warning, `--service ""`, non-TTY auto non-interactive
- [ ] run full suite: `make test`
- [ ] run `make lint`
- [ ] verify coverage of new packages meets project standard

### Task 11: [Final] Documentation
- [ ] add a `§ internal/cli/scaffold` note under "CLI (`internal/cli/`)" and a
      `§ workflow/scaffold` note under "Core — Workflow" in `docs/internals/packages.md`
      (capture: inert-override rationale, `[[ ]]` delimiter choice, no-preflight/no-locks, illegal `init` pkg name)
- [ ] add a `docs/guides/` page "Starting a new project with `dwe init`" (+ i18n stub if required)
- [ ] run `make build` so embedded docs are not stale; sanity-check `dwe init --help`
- [ ] update `AGENTS.md`/`CLAUDE.md` (repo root) only if a new cross-cutting pattern emerged
- [ ] move this plan to `docs/plans/completed/` (`mkdir -p docs/plans/completed`)

## Post-Completion
*Items requiring manual intervention or external systems — informational only*

**Manual verification:**
- Windows symlink behavior — confirm `CLAUDE.md` copy-fallback triggers cleanly without
  developer-mode privileges (CI is POSIX-only).
- First-run UX walkthrough — `dwe init` in an empty dir, then `dwe validate` / `dwe status`
  on the fresh project, confirming the inert files and starter service read well.
- Confirm the generated `.gitignore` runtime paths match the live `.dwe/` layout produced
  by an actual deploy (`shared/lock`, `workflow/deploy/journal`).
