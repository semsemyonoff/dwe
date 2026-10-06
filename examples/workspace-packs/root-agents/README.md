# Root agent document starter

This workspace-owned starter renders root `AGENTS.md` and copies
`workspace-notes.txt` verbatim. Take it from
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

Claude Code reads `AGENTS.md` natively, so the pack renders no `CLAUDE.md`: with its
default setting, a `CLAUDE.md`, `.claude/CLAUDE.md` or `CLAUDE.local.md` in or above
the working directory makes it skip every `AGENTS.md`. Commit the pack and rendered
files together.
