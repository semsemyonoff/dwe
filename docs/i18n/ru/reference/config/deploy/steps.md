> Translated from: reference/config/deploy/steps.md @ 43cdb6be1975

# Типы исполнения шагов

Каждый листовой шаг пайплайна декларирует `type:`, который выбирает способ исполнения его `cmd:`.

## Содержание

- [`type: shell`](#type-shell)
- [`cmd: shell` (билтин) vs `type: shell` (шаг)](#cmd-shell-билтин-vs-type-shell-шаг)
- [`type: dwe`](#type-dwe)
- [`type: command`](#type-command)
- [`type: builtin`](#type-builtin)

## `type: shell`

Выполняет shell-команду через `sh -c`. Полная семантика shell действует: подстановка переменных окружения, globbing, пайпы, перенаправления, операторы `&&`/`||` — всё работает как ожидается.

```yaml
- name: chmod-scripts
  type: shell
  cmd: chmod +x scripts/deploy.sh
```

## `cmd: shell` (билтин) vs `type: shell` (шаг)

Билтин `shell` (`cmd: shell`) **отличается** от типа исполнения шага (`type: shell`). Оба исполняют shell-команды, но с разными гарантиями переносимости:

**Тип шага `type: shell`** — использует настроенный для проекта shell (через `config.ShellBin`) для максимальной гибкости. Если проект задал кастомный shell-бинарь (например, `zsh` вместо `sh`), тела шагов используют этот shell.

```yaml
- name: run-with-project-shell
  type: shell
  cmd: some-zsh-specific-feature-here
```

**Билтин `cmd: shell`** — использует жёстко заданный POSIX-переносимый `sh -c` для максимальной предсказуемости. Применяется в двух контекстах:

1. **Как тело шага** (реже):

```yaml
- name: check-docker-login
  type: builtin
  cmd: shell
  with:
    cmd: docker info | grep -q ghcr.io
    timeout: 10s
```

2. **Как пред-/постусловие** (часто в deploy и validate):

```yaml
- name: copy-configs
  type: builtin
  cmd: service_configs_copy
  # ...
  when:
    type: shell
    cmd: "test -f templates/config.default"

  check:
    type: builtin
    cmd: shell
    with:
      cmd: "test -f services/main/configs/app.conf"
```

Оба применения гарантируют, что условия вычисляются переносимо в разных CI-системах, рантаймах контейнеров и пользовательских shell-ах, независимо от настройки `config.ShellBin` в проекте. Полную документацию по билтину `cmd: shell` см. в [validate.yml](../validate.md).

## `type: dwe`

Вызывает подкоманду CLI DWE. Путь к бинарю разрешается автоматически.

```yaml
- name: up
  type: dwe
  cmd: "docker up"

- name: info
  type: dwe
  cmd: "info"

- name: render-ide
  type: dwe
  cmd: "render ide main"
```

## `type: command`

Диспатчит декларативную команду по ID из реестра команд (`workspace/commands/`).

```yaml
- name: composer-install
  type: command
  cmd: services.main.composer-install

- name: db-create
  type: command
  cmd: services.main.db.create
  with:
    database: laravel_test
```

## `type: builtin`

Выполняет внутреннюю Go-функцию движка. Билтины работают in-process и имеют доступ ко всему конфигу. Тот же реестр доступен из декларативных команд через [`type: builtin` в `commands/`](../commands/types.md) — пайплайны и команды используют общий набор билтинов.

```yaml
- name: create-dirs
  type: builtin
  cmd: service_dirs_ensure
  with:
    service: main
    mode: skip

- name: success-msg
  type: builtin
  cmd: message
  with:
    level: success
    text: "Deploy completed"
```

Полный реестр и справочник параметров см. в [Доступные билтины](builtins.md).
