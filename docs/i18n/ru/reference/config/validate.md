> Translated from: reference/config/validate.md @ 0c4bfb9059f0

# validate.yml

Проверки готовности проекта.

## Содержание

- [Назначение](#назначение)
- [Домены валидации](#домены-валидации)
- [Структура](#структура)
- [Поля верхнего уровня](#поля-верхнего-уровня)
- [Поля записи check](#поля-записи-check)
- [Стадии](#стадии)
- [Доступные билтины](#доступные-билтины)
  - [`shell`](#shell)
  - [`file_exists`](#file_exists)
  - [`executable_in_path`](#executable_in_path)
  - [`env_keys_present`](#env_keys_present)
  - [`tcp_reachable`](#tcp_reachable)
- [Проверки `type: command`](#проверки-type-command)
- [Проверки должны быть идемпотентной инспекцией](#проверки-должны-быть-идемпотентной-инспекцией)
- [Разобранные примеры](#разобранные-примеры)
- [CLI-флаги](#cli-флаги)
- [Диагностический вывод](#диагностический-вывод)
- [Внешние линтеры](#внешние-линтеры)
- [Связанные команды](#связанные-команды)

## Назначение

`devbox/validate.yml` объявляет проверки готовности уровня проекта. CLI потребляет их из двух точек входа:

- `devbox validate` — запускает каждую проверку (плюс YAML-shape валидаторы в доменах `config`, `templates` и `commands`, плюс окружающие probe'ы в домене `env`) и репортит диагностику.
- Хук preflight на `devbox deploy run`, `devbox run`, `devbox stop` и `devbox restart` — запускает подмножество проверок, связанных с соответствующей стадией, до любого side-эффекта на Docker, git или файловую систему.

Цель — поднять наверх проблемы, которые пользователь может починить («вы не залогинены в ghcr.io», «DATABASE_URL пуст в `.env`», «VPN лёг») ДО того, как шаги деплоя упадут на середине с криптическими ошибками.

## Домены валидации

Команда validate запускает три домена в дополнение к существующим YAML-shape валидаторам:

| Домен | Источник | Настраивается? |
|--------|--------|---------------|
| `env.*` | Хардкод на Go (`internal/core/validate/env/`) | Нет — семь фиксированных probe'ов |
| `checks.*` | Записи `devbox/validate.yml` | Да — декларативно |
| `linters.*` | Встроенные адаптеры (shellcheck, hadolint) + блок `linters:` в `devbox/validate.yml` | Да — декларативно |
| `snapshot.*` | Директории снапшотов на диске + `devbox/snapshot.yml` | Нет — фиксированные валидаторы на каждое имя снапшота |

Probe'ы `env.*` это: `env.docker_bin`, `env.docker_daemon`, `env.docker_compose`, `env.git_bin`, `env.shell_bin`, `env.project_perms`, `env.ports_free`. Они запускаются на каждом вызове `devbox validate` и на каждом preflight (независимо от стадии — у env нет понятия стадии), с одним исключением: `env.ports_free` сам пропускает себя на стадии `stop`, поскольку конфликты портов нерелевантны при сворачивании проекта.

`env.ports_free` читает каждый хост-порт, объявленный под `services.<name>.ports` (только enabled-сервисы), и проверяет, можно ли каждый забиндить. Он один раз опрашивает `docker ps --format=json`, чтобы узнать, какие контейнеры держат какие порты сейчас: контейнеры с лейблом `com.docker.compose.project=<наш проект>` трактуются как «наши» (compose переиспользует их на `up`); контейнеры из любого другого compose-проекта триггерят диагностику конфликта, называющую чужой контейнер и проект; для портов, не удерживаемых ни одним контейнером, probe откатывается к `net.Listen`, чтобы детектировать не-Docker процессы. Недоступность Docker проходит молча — `env.docker_daemon` покрывает этот случай.

Валидаторы `checks.*` синтезируются по одному на каждую запись `validate.yml`. Каждый в рантайме диспатчится либо во встроенную процедуру инспекции, либо в локдаун-пользовательскую команду.

## Структура

```yaml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    severity: error
    hint: |
      Run `docker login ghcr.io` with a personal access token.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/repo:latest >/dev/null

  - id: project-deps
    description: Project dependency check script passes
    stages: [run, deploy]
    type: command
    cmd: deps.check
```

Файл опционален. При его отсутствии запускаются только `env.*` и существующие YAML-shape валидаторы.

## Поля верхнего уровня

| Поле | Тип | Обязательное | Описание |
|-------|------|----------|-------------|
| `checks` | list | да | Записи проверок (см. ниже). Может быть пустым. |

Неизвестные поля верхнего уровня отвергаются на загрузке (строгое декодирование).

## Поля записи check

| Поле | Тип | Обязательное | Описание |
|-------|------|----------|-------------|
| `id` | string | да | Уникальный идентификатор. Становится `Target` в диагностике. |
| `description` | string | да | Человекочитаемое summary, показываемое в таблице диагностики. |
| `stages` | list of strings | да | Стадии, триггерящие эту проверку (см. [Стадии](#стадии)). |
| `type` | string | да | Одно из `builtin` или `command`. Неизвестные значения отвергаются. |
| `cmd` | string | да | Имя билтина (для `type: builtin`) или ID пользовательской команды (для `type: command`). |
| `severity` | string | нет | Одно из `error` (по умолчанию), `warning`, `info`. Неизвестные значения отвергаются. |
| `hint` | string | нет | Подсказка по устранению, включаемая в диагностику. Держите кратко; длинные подсказки разбивайте через `\n`. |
| `with` | map | нет | Параметры, передаваемые билтину или пользовательской команде. |

Правила схемы, проверяемые на загрузке:

- `id` должен быть уникальным среди записей.
- `stages` должен быть непустым.
- `type` должен быть `builtin` или `command`.
- `severity` должен быть `error` / `warning` / `info`, если задан.
- Валидность формы `with:` (обязательные ключи, типы) проверяется методом `Validate` целевого билтина — падения всплывают как диагностики `checks.<id>` в рантайме, не как load-ошибки.

## Стадии

Проверка запускается всякий раз, когда её список `stages` содержит стадию, запрошенную вызывающим. CLI определяет четыре зарезервированные стадии с встроенными хуками:

| Стадия | Триггеры |
|-------|--------------|
| `deploy` | `devbox deploy run`, `devbox validate --stage deploy` |
| `run` | `devbox run`, `devbox restart` (нога run), `devbox validate --stage run` |
| `stop` | `devbox stop`, `devbox restart` (нога stop), `devbox validate --stage stop` |
| `command` | `devbox validate --stage command` (зарезервировано на будущее; автоматического хука нет) |

`devbox validate` без `--stage` запускает каждую проверку независимо от стадии.

Неизвестные стадии принимаются (открытое перечисление), но производят **предупреждение** на загрузке, чтобы пользователи ловили опечатки рано:

- `stage "deplooy" is not a known preflight stage` (с предложением, если близко по расстоянию Левенштейна)
- Особые заметки: `restart` композитный (использует обе стадии stop и run, отдельного preflight нет); `reset` использует только стадию stop

Неизвестные стадии всё ещё можно вызвать явно через `devbox validate --stage <name>`, если нужно (например, для кастомных workflow'ов валидации).

## Доступные билтины

Все пять билтинов живут в `internal/core/execution/builtin/` и применимы и как записи `type: builtin` проверок, и как тела шагов деплоя / блоки экшенов `check:`.

### `shell`

Запускает shell-команду через хардкоженный `sh -c` (по соглашению с предикатом `when:` деплоя). Exit 0 = pass. Этот билтин использует POSIX-портативный `sh -c` независимо от сконфигурированного в проекте шелла, обеспечивая идентичный запуск проверок во всех окружениях.

| Ключ | Тип | Обязательное | По умолчанию | Описание |
|-----|------|----------|---------|-------------|
| `cmd` | string | да | — | Тело shell-команды. |
| `timeout` | duration | нет | `10s` | Максимальное время выполнения. |

Сообщение об ошибке при ненулевом exit: `exit status N: <последняя строка stderr>`.

См. [deploy: `cmd: shell` vs `type: shell`](deploy/steps.md#cmd-shell-builtin-vs-type-shell-step) для различия между этим билтином и типом выполнения шага `type: shell`.

### `file_exists`

Проверяет, что файл присутствует на диске.

| Ключ | Тип | Обязательное | Описание |
|-----|------|----------|-------------|
| `path` | string | да | Путь относительно корня проекта. |

### `executable_in_path`

Проверяет, что бинарь резолвится через `exec.LookPath`.

| Ключ | Тип | Обязательное | Описание |
|-----|------|----------|-------------|
| `name` | string | да | Имя исполняемого файла (без сегментов пути). |

### `env_keys_present`

Проверяет, что один или несколько ключей существуют с непустыми значениями в файле в стиле `.env`. Парсинг следует соглашениям `.env`: пустые строки и full-line `#`-комментарии пропускаются; обрамляющие кавычки `"..."` / `'...'` снимаются; `KEY=`, `KEY=""` и `KEY=''` все считаются пустыми.

| Ключ | Тип | Обязательное | Описание |
|-----|------|----------|-------------|
| `file` | string | да | Путь к файлу в стиле `.env`, относительно корня проекта. |
| `keys` | list of strings | да | Ключи, которые должны присутствовать И быть непустыми. |

Сообщение об ошибке: `missing or empty keys: A, B, C`.

### `tcp_reachable`

Пытается сделать TCP-dial до `host:port`.

| Ключ | Тип | Обязательное | По умолчанию | Описание |
|-----|------|----------|---------|-------------|
| `host` | string | да | — | Имя хоста или IP. |
| `port` | int | да | — | Порт в диапазоне 1–65535. |
| `timeout` | duration | нет | `3s` | Таймаут dial'а. |

## Проверки `type: command`

Запись проверки с `type: command` диспатчится в декларативную пользовательскую команду из `devbox/commands/`. Блок `with:` пробрасывается как payload `params:` пользовательской команды — ровно как `devbox commands <id> --set k=v`.

Ограничения, проверяемые на загрузке:

- `type:` целевой команды ДОЛЖЕН быть `shell` или `script`. Цели workflow, service_exec, service_run, devbox и builtin-as-command отвергаются с: `checks may only invoke user commands of type shell or script (got: <type>)`.
- Неизвестный ID команды отвергается с: `unknown command: <id>`.

Выполнение залочено:

- `SkipConfirm = true` — промпты подтверждения обходятся.
- `NonInteractive = true` — пути UI с промптами шорт-серкатятся.
- `SkipNotify = true` — десктоп-уведомления подавляются.
- `stdout` отбрасывается; `stderr` захватывается, и его хвост включается в сообщение диагностики, если проверка падает.

## Проверки должны быть идемпотентной инспекцией

Проверка отвечает на «готов ли мир?» — не «подготовь мир». По соглашению, каждая проверка ДОЛЖНА быть идемпотентной, без побочных эффектов и быстрой.

**CLI этого НЕ обеспечивает.** Read-only песочницы для подпроцессов нет; проверка `type: command`, чьё тело — `rm -rf /tmp/work`, выполнит ровно это в preflight.

Что CLI ОБЕСПЕЧИВАЕТ на проверках `type: command`:

- Неинтерактивное выполнение (без промптов, без подтверждений).
- Уведомления подавлены.
- stdout отброшен, stderr захвачен.

Компромисс: CLI держит мостик минимальным, чтобы пользовательские shell/script-команды можно было переиспользовать и для шагов деплоя, и для проверок готовности, не вводя новый ограниченный режим выполнения. Мутирующая проверка — это острый край для автора: громко документируйте это в `description:`, если ваша проверка обязана мутировать, и предпочитайте чистую инспекцию (`shell: docker pull ... --quiet`, `shell: test -f path`) везде, где возможно.

## Разобранные примеры

**1. Логин в реестр контейнеров (встроенный shell):**

```yaml
checks:
  - id: ghcr-login
    description: Authenticated against ghcr.io
    stages: [deploy]
    severity: error
    hint: |
      Run `docker login ghcr.io` with a GitHub PAT.
    type: builtin
    cmd: shell
    with:
      cmd: docker pull ghcr.io/owner/private-image:latest --quiet
      timeout: 30s
```

**2. Локальный дамп БД присутствует (file_exists):**

```yaml
  - id: db-dump-present
    description: Seed dump exists for first-run import
    stages: [deploy]
    severity: warning
    hint: Download from s3://team-dumps/latest.sql and place at .devbox/seed.sql
    type: builtin
    cmd: file_exists
    with:
      path: .devbox/seed.sql
```

**3. Необходимые секреты сконфигурированы (env_keys_present):**

```yaml
  - id: app-secrets
    description: Required app secrets configured in .env
    stages: [run, deploy]
    severity: error
    hint: |
      Copy .env.example to .env and fill in:
        DATABASE_URL, REDIS_URL, JWT_SECRET
    type: builtin
    cmd: env_keys_present
    with:
      file: .env
      keys: [DATABASE_URL, REDIS_URL, JWT_SECRET]
```

**4. Корпоративный VPN доступен (tcp_reachable):**

```yaml
  - id: corporate-vpn
    description: Internal git mirror is reachable (VPN up?)
    stages: [deploy, run]
    severity: error
    hint: Connect to the corporate VPN and retry.
    type: builtin
    cmd: tcp_reachable
    with:
      host: git.internal.example.com
      port: 22
      timeout: 2s
```

**5. Скрипт проверки зависимостей проекта (type: command):**

```yaml
  - id: project-deps
    description: Required CLIs installed (./scripts/check-deps.sh)
    stages: [run]
    type: command
    cmd: deps.check
```

Где `devbox/commands/deps.yml` объявляет:

```yaml
group: deps
commands:
  check:
    type: shell
    description: Verify required CLIs
    cmd: |
      set -e
      command -v node
      command -v pnpm
      command -v psql
```

**6. Только compose-плагин v2 (executable_in_path):**

```yaml
  - id: jq-installed
    description: jq is available for compose introspection helpers
    stages: [deploy]
    severity: warning
    type: builtin
    cmd: executable_in_path
    with:
      name: jq
```

## CLI-флаги

- `devbox validate` — запускает `config.*`, `templates.*`, `commands.*`, `env.*` и все `checks.*`. Опциональный позиционный scope сужает запуск (например, `devbox validate env`, `devbox validate checks ghcr-login`).
- `devbox validate --stage <name>` — локальный флаг команды `validate`. Фильтрует `checks.*` по стадии. `env.*` и другие домены не затрагиваются (у них нет стадий).
- `devbox validate --strict` — трактовать предупреждения как ошибки (exit 1).
- `devbox validate --quiet` — скрыть строки ok / info.
- `--skip-preflight` — локальный флаг на `deploy run`, `run`, `stop` и `restart`. Когда задан, preflight печатает `preflight skipped (--skip-preflight)` в stderr и НЕ запускает валидаторов. Флаг — это настоящий байпас: проверки `type: command` вызывают произвольные пользовательские скрипты, поэтому CLI не запускает их под флагом, который пользователь назвал «skip».

## Диагностический вывод

Диагностики разделяют модель рендеринга и severity, используемую остальным `devbox validate`:

- `Severity`: из `entry.severity` (по умолчанию `error`).
- `Domain`: `checks` (или `env` для хардкод-probe'ов).
- `Target`: `id` записи.
- `File`: `devbox/validate.yml` (записи) или пусто (env-probe'ы).
- `Line`: номер строки (1-based) первого ключа записи (записи).
- `Message`: строка ошибки билтина / команды.
- `Hint`: из `entry.hint`.

Preflight пишет ту же таблицу диагностики в stderr перед падением с exit-кодом 1. Используйте `\n` в подсказках, чтобы разбить длинный текст устранения на строки — таблица Lipgloss уважает переводы строк.

## Внешние линтеры

Домен `linters.*` запускает хорошо известные внешние линтеры (shellcheck, hadolint) и произвольные адаптеры `type: generic` как часть `devbox validate`. Линтеры **не** запускаются в preflight — preflight отвечает на «можем ли мы запуститься?», а не «чист ли код?».

### Раскладка проводки

```yaml
linters:
  shellcheck:
    enabled: true
    bin: shellcheck
    paths: [devbox/scripts, scripts]
    extensions: [.sh, .bash]
    flags: [--severity=warning]
    severity: warning
  hadolint:
    paths: ["."]
    filenames: [Dockerfile]
    extensions: [.dockerfile]
  yamllint:
    type: generic
    bin: yamllint
    paths: ["."]
    extensions: [.yml, .yaml]
    flags: [-s]
```

Ключ маппинга — это ID адаптера. Неизвестные поля отвергаются на загрузке (строгое декодирование).

### Поля записи

| Поле | Тип | Обязательное | Описание |
|-------|------|----------|-------------|
| `type` | string | нет | `builtin` (по умолчанию) или `generic`. |
| `enabled` | bool | нет | Опущено → автоопределение (true, если `bin` есть в PATH). `false` → молчаливый пропуск. |
| `bin` | string | нет | По умолчанию — дефолт адаптера (например, `shellcheck`). **Должно быть голым именем команды** — без сепараторов пути. Абсолютные или относительные пути отвергаются на загрузке. |
| `paths` | list of strings | нет | По умолчанию — дефолт адаптера. Каждая запись должна быть относительной, непустой и не содержать `..`. `"."` разрешён (равенство корню, используется hadolint). |
| `extensions` | list of strings | нет | По умолчанию — дефолт адаптера. Каждая запись должна начинаться с `.` (например, `.sh`, не `sh`). |
| `filenames` | list of strings | нет | Литеральные basename'ы, матчащиеся рядом с расширениями (например, `Dockerfile`). Сепараторы пути не разрешены. |
| `flags` | list of strings | нет | Дописываются после встроенных флагов адаптера. Встроенные адаптеры резервируют флаги output-формата (`--format`, `-f`) — передача их в любой argv-форме (`--format=gcc`, `-f tty`, `-fgcc`) отвергается на загрузке. |
| `severity` | string | нет | Одно из `error`, `warning`, `info`. Кэпит находки адаптера сверху (например, `severity: warning` понижает находки адаптера уровня `error` до `warning`). `ok` **не** разрешено — используйте `enabled: false` для отключения. Операционные диагностики (timeout, truncation, parse failure, missing-path) никогда не клэмпятся, так что пользователь не может случайно заглушить сигналы рантайм-падений. |

### Встроенные адаптеры

| ID | Bin по умолчанию | Paths по умолчанию | Extensions по умолчанию | Filenames по умолчанию | Зарезервированные флаги |
|----|-------------|---------------|--------------------|--------------------|----------------|
| `shellcheck` | `shellcheck` | `devbox/scripts`, `scripts` | `.sh`, `.bash` | — | `--format`, `-f` |
| `hadolint` | `hadolint` | `.` | `.dockerfile` | `Dockerfile` | `--format`, `-f` |

### `type: generic`

Generic-адаптер запускает `bin <flags> <files...>` и конвертирует ненулевой exit в одну диагностику severity error с объединённым stdout+stderr в качестве сообщения (обрезано до ~2 KB, чтобы таблица оставалась читаемой). У него нет зарезервированных флагов — пользователь владеет всей флаговой поверхностью — и нет построчного парсинга. Используйте его для линтеров, чей формат вывода мы не парсим нативно.

### Правила автоопределения

1. Для каждого известного встроенного адаптера, если в `linters:` нет записи → синтезировать запись с дефолтами (на адаптер, не all-or-nothing).
2. Блок присутствует, `enabled` опущен → `true`.
3. `enabled: false` → молчаливый пропуск (без диагностики).
4. Дефолтный `bin:` отсутствует в PATH → молчаливый пропуск («мы попробовали автоопределить; делать нечего»).
5. Явный `bin:` сконфигурирован, но отсутствует в PATH → одна Warning-диагностика (проблема конфига, не кода).
6. Раскрытие путей не дало файлов → молчаливый пропуск.

### Пользовательские оверрайды бинаря

Можно переопределить путь бинаря для любого линтера через свой user-level конфигурационный файл (`~/.config/devbox/config`). Полезно, когда у вас кастомные установки, замены (например, `podman` вместо `docker`) или бинари вне дефолтного PATH.

Добавьте строку в свой user config:

```
binary_shellcheck=/custom/path/to/shellcheck
binary_hadolint=/opt/hadolint
```

Формат — `binary_<linter-id>=<path>`. Пути абсолютные или относительные к текущей директории. Если путь не существует или не исполняем, `devbox validate` эмитит error-диагностику в домене `linters`.

**Заметка:** Эти оверрайды консультируются **только** во время `devbox validate`. Lifecycle-команды (deploy, run, stop и т. д.) не используют бинари линтеров, так что сломанные оверрайды не затрагивают обычную работу.

### Scope

Запустить все линтеры или сузить до одного через подкоманду `linters`:

```
devbox validate                       # all domains (including linters)
devbox validate linters               # all linters
devbox validate linters shellcheck    # only shellcheck
```

Неизвестные ID линтеров производят пустой результат (не хард-ошибку — зеркалит поведение `checks`).

### Пер-линтерные пределы

- **Timeout**: 5 минут на линтер (`DefaultLinterTimeout`). Превышение → Error-диагностика; частичный вывод не парсится.
- **Кэп вывода**: 50 MB объединённого stdout+stderr на линтер (`MaxLinterOutputBytes`). Излишек сбрасывается, и эмитится Warning-диагностика; парсер всё равно запускается на захваченном префиксе.
- **Конкурентность**: линтеры запускаются параллельно, ограничено `runtime.NumCPU()` (`MaxLinterConcurrency`). Падение одного линтера (panic, timeout, ошибка парсера) никогда не отменяет соседей.

### Обход файлов

- Записи `paths:` рекурсивно обходятся под корнем проекта.
- Явные пути к файлам (записи, резолвящиеся в обычный файл) обходят фильтры extensions/filenames.
- Файл матчится, если его расширение в `extensions:` ИЛИ его basename в `filenames:`.
- Симлинки пропускаются (защита от выходов за пределы корня проекта).
- `.git/` всегда пропускается. Сужение специфичного для адаптера шума (например, `node_modules`, `vendor`) оставлено пользователю через `paths:`.
- Отсутствующие **дефолтные** пути (например, `devbox/scripts` shellcheck'а в проекте, где их нет) молча отбрасываются. Отсутствующие **пользовательские** пути (записи, явно написанные пользователем) производят Warning.

### Модель доверия

`bin:` ограничен голым именем команды, резолвимым через `PATH` в рантайме; абсолютные и относительные пути запрещены на загрузке. Обоснование: `validate.yml` едет с репозиторием; злонамеренный конфиг с `bin: ./scripts/evil.sh` не должен молча выполнять произвольный код на `devbox validate`. Пользователи, кому действительно нужен кастомный путь бинаря, устанавливают его в `PATH` (или оборачивают).

## Связанные команды

- `devbox validate` — полный прогон валидации (все домены).
- `devbox validate env` — только env-probe'ы.
- `devbox validate checks [id]` — декларативные проверки (опциональный id сужает до одной).
- `devbox validate linters [id]` — внешние линтеры (опциональный id сужает до одного).
- `devbox deploy run` / `run` / `stop` / `restart` — автоматически вызывают preflight (см. `--skip-preflight`). Линтеры в preflight **не** запускаются.
