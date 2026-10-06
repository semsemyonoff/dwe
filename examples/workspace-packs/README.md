# Workspace template pack starters

These examples are workspace-owned starters, not built-in DWE assets. Take them
from the DWE release tag matching your installed `dwe --version`; after copying,
maintain your own pack alongside the project's configuration.

- [ralphex](ralphex/README.md): multi-repository run scope and prompt insertions.
- [root-agents](root-agents/README.md): a root agent document and a verbatim file.

Install from a checkout of that tag, from the workspace root:

```sh
cp -R <dwe-checkout>/examples/workspace-packs/<name>/workspace/. workspace/
```

The trailing `/.` copies the contents on both BSD and GNU cp. Merge the example's
`workspace.yml.snippet` into `workspace.yml` when present; otherwise add its name
to `render.workspace`. Existing lists are replaced as a whole by later config
layers, so include every pack you want. Inspect destinations before rendering:
existing regular files are overwritten. Files removed from a manifest remain on disk.
