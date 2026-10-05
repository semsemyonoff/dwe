# Root agent document starter

This workspace-owned starter renders root `AGENTS.md`, copies
`workspace-notes.txt` verbatim, and links `CLAUDE.md` to `AGENTS.md`. Take it from
the DWE tag matching your installed `dwe --version`, then maintain your copy.

```sh
cp -R <dwe-checkout>/examples/workspace-packs/root-agents/workspace/. workspace/
```

Add `root-agents` to `render.workspace` in `workspace.yml`. Before running
`dwe render workspace`, move the existing scaffold's root `AGENTS.md` content into
`workspace/templates/workspace/root-agents/AGENTS.md.tmpl`: rendering overwrites
the existing regular file. The template lists `.Services` and project commands.
Adapt it to the workspace's rules; `workspace-notes.txt` demonstrates verbatim
copying even when a file contains `{{PLAN_FILE}}`.

The `CLAUDE.md` symlink stays current with `AGENTS.md`; a copied fallback goes
stale on the next render. If this workspace reads `AGENTS.md` natively and does
not allow `CLAUDE.md`, remove the `symlinks` entry before rendering. An existing
regular `CLAUDE.md` must be removed or migrated first: the symlink writer refuses
to replace it. Commit the pack and rendered files together.
