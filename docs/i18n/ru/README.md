> Translated from: README.md @ a568f0456775

# DWE

*Языки: 🇬🇧 [English](../../../README.md) · 🇷🇺 **Русский***

CLI в виде одного бинарника для декларативного запуска, настройки и обслуживания контейнеризованных локальных окружений разработки.

## Зачем DWE

- Одно описательное дерево (`workspace.yml` + `workspace/`) управляет всем проектом — сервисами, пайплайнами, командами, информационной панелью, шаблонами, переводами.
- Команды deploy, run, stop, restart, reset и snapshot работают на одном движке пайплайнов, поэтому поведение согласовано между операциями.
- Оркестрация контейнеров строится поверх обычных файлов Docker Compose, которыми уже владеет проект — без синтетической генерации compose, без скрытой привязки.
- Оверрайды для разработчика лежат в отслеживаемом `defaults.yml` плюс gitignored `local.yml`; один и тот же проект чисто поднимается на любой рабочей станции.
- Встроенные документация и i18n позволяют `dwe docs` и переведённым интерфейсам работать без сети и внешних ресурсов.

## Установка

DWE поставляется как один статический Go-бинарник. Выбирайте удобный канал.

### Через Homebrew

```sh
brew install semsemyonoff/tap/dwe
```

Устанавливает бинарник и completion для bash, zsh, fish по стандартным путям Homebrew. Работает на macOS (Intel + Apple Silicon) и Linux (linuxbrew).

### Из релизов

Скачайте подходящий архив или пакет с [GitHub releases](https://github.com/semsemyonoff/dwe/releases):

- `dwe_<version>_macos_{x86_64,arm64}.tar.gz` и `dwe_<version>_linux_{x86_64,arm64}.tar.gz` — распакуйте и положите `dwe` в `/usr/local/bin` (или в любую директорию из `$PATH`); внутри архива лежит папка `completions/` со скриптами для `dwe completion install`.
- `dwe_<version>_{amd64,arm64}.deb` — `sudo dpkg -i dwe_*.deb`; completion ставится автоматически в `/usr/share/{bash-completion,zsh/site-functions,fish/vendor_completions.d}`.
- `dwe-<version>-1.{x86_64,aarch64}.rpm` — `sudo rpm -i dwe-*.rpm`; пути для completion те же, что у deb.

Целостность файлов проверяется по опубликованному `checksums.txt`.

### Из исходников

```sh
git clone https://github.com/semsemyonoff/dwe.git
cd dwe
make build
```

`make build` запускает `go mod tidy`, синхронизирует `docs/` в `internal/core/docs/embedded/`, перегенерирует `internal/core/docs/content_hashes_gen.go`, компилирует `./cmd/dwe` и пишет `bin/dwe`. Бинарник самодостаточен: документация, переводы, дефолтные пайплайны и встроенные шаги вшиты внутрь. Положите на `$PATH`:

```sh
install -m 0755 bin/dwe /usr/local/bin/dwe
```

> `go install github.com/semsemyonoff/dwe/cmd/dwe@latest` намеренно не поддерживается: дерево встроенной документации (`internal/core/docs/embedded/`) генерируется build-time скриптом `scripts/sync-embedded-docs.sh` и не коммитится — сборка через `go install` приехала бы с пустыми docs.

### Shell completion (только для tar.gz)

Если бинарник получен из tar.gz, completion ставится отдельной командой:

```sh
dwe completion install            # автоопределение $SHELL
dwe completion install zsh        # или явно указать шелл
```

Поддерживаемые шеллы: bash, zsh, fish, powershell. Подробности и dry-run — `dwe completion install --help`.

### Runtime-зависимости

`docker` (с `docker compose`), `git` и POSIX-shell на хосте. Если они лежат в нестандартных местах, переопределите их пути в пользовательском конфиге `~/.config/dwe/config` через записи `binary_<name> = <path>` — см. [`docs/reference/config/userconfig.md`](../../reference/config/userconfig.md#переопределения-бинарей).

### Опционально: скил для AI-агентов

Репозиторий поставляет агентский скил в [`skills/dwe/`](../../../skills/dwe/SKILL.md) — тонкий навигатор, который объясняет Claude Code, Codex, Cursor, OpenCode и другим совместимым агентам, как обнаружить DWE-проект, какие команды `dwe` использовать для инспекции vs изменения и как смотреть всё остальное через встроенную подсистему `dwe docs`. Установка через CLI [vercel-labs/skills](https://github.com/vercel-labs/skills):

```sh
# установить в текущий проект (./<agent>/skills/)
npx skills add semsemyonoff/dwe --skill dwe

# или установить глобально для всех проектов
npx skills add semsemyonoff/dwe --skill dwe -g

# выбрать конкретного агента (claude-code, codex, cursor, opencode, ...)
npx skills add semsemyonoff/dwe --skill dwe -a claude-code
```

## Быстрый старт

Войдите в директорию проекта, содержащую `workspace.yml`, и запустите любую команду — DWE идёт вверх от рабочей директории, чтобы найти корень проекта.

Перед первым деплоем проверьте проект:

```sh
cd my-project
dwe validate
```

Соберите и запустите стек:

```sh
dwe deploy run    # идемпотентно: установить, настроить, мигрировать, поднять
dwe info          # отрендеренные URL, хосты, группы команд
```

Управляйте жизненным циклом:

```sh
dwe run           # before-хуки → docker up → after-хуки → готово
dwe stop          # before-stop → docker down → after-stop
dwe restart       # stop + run
dwe status        # сервисы, порты, хосты, git, env
```

Plan-only превью — только чтение:

```sh
dwe deploy plan   # разрешённое дерево фаз/шагов, без выполнения
dwe validate      # проверки готовности
```

Журнал деплоя живёт в `.dwe/deploy/state.yml`. Повторные запуски пропускают шаги, у которых `action_hash` и входы не изменились. Логи попадают в `.dwe/logs/`, когда выставлен `log: true`.

## Архитектура

DWE стоит между разработчиком и Dockerized-стеком: читает YAML-дерево проекта, рендерит небольшой объём сгенерированного состояния рядом с ним и дёргает `docker compose`, чтобы реально поднять контейнеры. Разработчик никогда не набирает `docker compose` руками.

```mermaid
flowchart LR
  Dev["Разработчик"] -->|dwe| CLI["dwe CLI"]
  Project["DWE конфиг проекта<br/>+ compose конфиг"] --> CLI
  CLI -->|docker compose| Engine["Docker engine"]
  Engine --> Containers["контейнеры"]
  Dev -.->|http / tcp| Containers
```

- DWE владеет моделью проекта, упорядоченным списком compose-файлов, рендером env, оркестрацией жизненного цикла, блокировками и журналом состояния под `.dwe/`.
- Docker владеет контейнерами, сетями, volume'ами, слоями образов и репортингом здоровья. Единственное рукопожатие — argv, который DWE передаёт в `docker compose`, и код возврата, который тот возвращает.
- Каждый вызов короткоживущий и stateless: нет демона DWE, нет загрузчика плагинов, нет сети на штатном пути. Удаление DWE оставляет compose-файлы под `compose/` валидным самостоятельным вводом для `docker compose`.

Полное описание: [`docs/reference/concepts/architecture.md`](../../reference/concepts/architecture.md).

## Раскладка проекта

Типичный проект держит декларативное дерево под `workspace/`, оверлеи Docker Compose под `compose/` и runtime-данные под `.dwe/` / `volumes/` / `snapshots/`.

```text
my-project/
├── workspace.yml              # идентичность проекта
├── workspace/                 # отслеживаемое дерево конфигурации
│   ├── defaults.yml        # версионированные дефолты
│   ├── local.yml           # оверрайды на разработчика (gitignored)
│   ├── services/<name>/    # папки сервисов (service.yml + опциональные пайплайны)
│   ├── commands/           # декларативные пользовательские команды
│   ├── templates/          # template-паки для `dwe render`
│   ├── deploy.yml          # верхнеуровневый оркестратор деплоя
│   ├── lifecycle.yml       # run / stop / restart
│   ├── reset.yml           # пайплайн reset
│   ├── info.yml            # информационная панель
│   ├── validate.yml        # проверки готовности
│   └── docker.yml          # список compose-файлов + топология
├── compose/                # отслеживаемые оверлеи Docker Compose
├── configs/                # отслеживаемые шаблоны конфигов сервисов
├── volumes/                # gitignored bind-mount цели
├── snapshots/              # gitignored хранилище снапшотов
└── .dwe/                # gitignored runtime-данные CLI (state, locks, logs)
```

Полное описание: [`docs/reference/concepts/project-layout.md`](../../reference/concepts/project-layout.md).

## Документация

Справочная документация живёт в `docs/reference/` и также встроена в бинарник. Просматривайте её офлайн через `dwe docs` (интерактивный TUI) или `dwe docs show <topic>` (обычный текст).

- [Концепции](../../reference/concepts/index.md) — высокоуровневая ориентация: начало работы, архитектура, раскладка проекта, интеграция с Docker, интеграция с Git, пайплайны, состояние и блокировки.
- [Конфигурация](../../reference/config/index.md) — справочник по полям для `workspace.yml`, сервисов, команд, пайплайнов deploy/reset/lifecycle, snapshot, info, validate, setup, styles, UI, state, i18n, нотификаций, docker.
- [Render-паки](../../reference/render/index.md) — `dwe render env / ide / ai / git` — схема манифеста, политики коллизий, локальные оверрайды.
- [Подсистема документации](../../reference/docs/index.md) — браузер `dwe docs`, неинтерактивные подкоманды, переводы, проверка свежести через хэш контента.
- [Шаблоны](../../reference/templates.md) — общий шаблонизатор: `{{ ... }}` против `${ ... }`, реестры sprout, контекст рендеринга по местам использования.
- [Справочник CLI](../../reference/cli/index.md) — автогенерируемое дерево команд (перегенерируется через `dwe docs generate`).

Полезные однострочники:

```sh
dwe docs                 # интерактивный браузер
dwe docs list            # перечислить все топики
dwe docs show <topic>    # отрендерить одну страницу
dwe docs search <term>   # поиск по всему дереву
dwe docs llms-txt        # компактный индекс проекта для AI-агентов
```

## Лицензия

Распространяется под лицензией [MIT](../../../LICENSE).
