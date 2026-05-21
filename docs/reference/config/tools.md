# devbox/tools.yml

Tool definitions live in `devbox/tools.yml`. The file is loaded standalone and is not part of the three-layer merge. The merged main config (`devbox.yml` + `defaults.yml` + `local.yml`) carries only `tools.<name>.enabled` overlays — see [`devbox.md`](devbox.md) for the overlay shape.

## Schema

```yaml
tools:
  adminer:
    container: adminer
    host: adminer.localhost
    port: 8080
    compose: compose/tools/adminer.yml
  mailpit:
    container: mailpit
    host: mail.localhost
    port: 8025
    compose: compose/tools/mailpit.yml
    status:
      - name: ENDPOINT
        value: "http://{{ .Tool.Host }}:{{ .Tool.Port }}"
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `container` | string | yes | Docker image or container name. |
| `host` | string | yes | Virtual hostname for the tool. |
| `port` | int | yes | Container port (non-zero). |
| `compose` | string | no | Relative path to a docker-compose overlay file. |
| `status` | list | no | Custom columns rendered in `devbox status tools`. Each entry has `name` (column header) and `value` (hermetic Go template). |

Tool keys must be identifier-safe (`^[A-Za-z_][A-Za-z0-9_]*$`) so they can be used with Go template dot syntax.

Strict-decode parsing: unknown fields under `tools.<name>` cause a config-load error. Missing `devbox/tools.yml` is fine — the tool set is just empty.
