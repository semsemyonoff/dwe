> Translated from: reference/config/deploy/steps.md @ 24b2e37e6f1b

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

Оба применения гарантируют, что условия вычисляются переносимо в разных CI-системах, рантаймах контейнеров и пользовательских shell-ах, независимо от настройки `config.ShellBin` в проекте. Полную документацию по билтину `cmd: shell` см. в [validate.yml](../validate.md#shell).

**Рабочий каталог.** И тело шага `type: shell`, и билтин `cmd: shell` выполняются с рабочим каталогом, установленным в **корень проекта** — ту же базу используют условия `when:` и билтин `file_exists`. Поэтому относительный путь в теле шага указывает на тот же файл, что и относительный путь в `check:`, который его охраняет, независимо от подкаталога, из которого вызван `dwe`.

**Таймаут.** Билтин `cmd: shell` принимает опциональный `timeout:` (по умолчанию `10s`). `timeout: "0"` означает **без ограничения**, как и в соглашении о собственном `timeout:` шага — именно это позволяет выведенному [`check: auto`](conditions.md#check-auto-инверсия-when) сохранить неограниченную позицию `when:`, который он инвертирует. Отрицательная длительность (`"-5s"`) отклоняется — ровно как и `timeout:` уровня шага; `0` — единственное написание «без ограничения». У тел шагов `type: shell` собственного встроенного таймаута нет; ограничивайте их полем `timeout:` уровня шага.

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

Выполняет внутреннюю Go-функцию движка. Билтины работают in-process и имеют доступ ко всему конфигу. Тот же реестр доступен из декларативных команд через [`type: builtin` в `commands/`](../commands/types.md#тип-builtin) — пайплайны и команды используют общий набор билтинов.

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

### Билтины-предикаты как тело шага (семантика утверждения)

Большинство билтинов — действия (они что-то делают). Некоторые — **предикаты**, отвечающие на вопрос «да/нет» о состоянии мира (`file_exists`, `executable_in_path`, `tcp_reachable`, `http_check`, `containers_running`, `env_keys_present`, `config_keys_present` и билтин `shell`). Предикат может использоваться как тело шага, где ведёт себя как **утверждение**:

- Проверка проходит → шаг успешен.
- Проверка не проходит → шаг **проваливается** с собственным сообщением предиката, останавливая пайплайн.

```yaml
- name: assert-seed-present
  type: builtin
  cmd: file_exists
  with:
    path: .dwe/seed.sql
```

Шаги-утверждения **всегда перезапускаются** — гейт деплоя «уже актуально» и per-step пропуск по action-hash никогда не пропускают шаг-предикат (та же обработка, что и у шагов `check:`), потому что у утверждения нет осмысленного кешированного результата. Гейт `when:` по-прежнему применяется: шаг-предикат, чей `when:` вычисляется в false, пропускается без утверждения.

Полный список и обоснование см. в [преамбуле про предикаты как тело](builtins.md#билтины-предикаты-как-тело-шага-семантика-утверждения) в справочнике билтинов.
