# `ui` configuration

The optional `ui:` block in `devbox.yml` configures the interactive command browser used by `devbox commands run` and `devbox commands inspect` when invoked without an exact command ID.

The block is loaded with the same lenient loader as the rest of `devbox.yml`: an absent block and unknown keys are silently ignored at load time. The dedicated `ui` validator (run via `devbox validate`) surfaces unknown keys as warnings and invalid values (e.g. a negative depth) as errors.

## Schema

```yaml
ui:
  commands:
    default_expanded_depth: 3        # int, default 3
    auto_collapse_empty: true        # bool, default true
    show_type_badges: true           # bool, default true
```

### `ui.commands.default_expanded_depth`

Controls how many tree levels are expanded by default when the command browser opens. A value of `0` keeps everything collapsed; larger values expand deeper. Negative values are clamped to `0` by the accessor and rejected as an error by the validator.

### `ui.commands.auto_collapse_empty`

When `true` (default), fuzzy-filter sessions automatically collapse and dim subtrees with zero matches; the previous expansion state is restored on `Esc`.

### `ui.commands.show_type_badges`

When `true` (default), the right-hand command list shows a colour-coded type badge (`shell`, `script`, `workflow`, `service_exec`, `service_run`, `builtin`, `devbox`) next to each command ID. Set `false` to suppress badges on narrow or monochrome terminals.

## `*bool` semantics — omit vs `false`

`auto_collapse_empty` and `show_type_badges` use the same `*bool` pattern as `deploy.log` and `service.render.<kind>.enabled`: an absent key means "use the spec default" (which is `true` for both), while an explicit `false` is treated as a deliberate opt-out and honoured by the accessors. Plain `bool` would conflate these two states because an absent key and an explicit `false` both deserialize to the zero value.

Practical implication: to disable a default-true knob you must write the explicit `false`; deleting the key restores the default. This is the only knob in `devbox.yml` where the distinction is observable.

## Related

- [`devbox.md`](devbox.md) — top-level configuration overview
- [`commands.md`](commands.md) — user command definitions
- [`styles.md`](styles.md) — palette keys consumed by the type badges
