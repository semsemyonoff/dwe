> Translated from: reference/config/services/examples.md @ c3023746774b

# Примеры сервисов и жизненный цикл переключения

Полное определение сервиса, семантика `on_enable` / `on_disable` / `notes` и типичные ловушки.

## Содержание

- [Полное определение сервиса](#полное-определение-сервиса)
- [Жизненный цикл переключения](#жизненный-цикл-переключения)
- [Типичные ловушки](#типичные-ловушки)

## Полное определение сервиса

```yaml
# workspace/services/main/service.yml
type: app
container: app-main
required: true
dir: ./services/main
dir_internal: /workspace
work_dir_internal: /workspace/src
icon: "📦"
info:
  title: "Main Application"
  paths:
    - name: "API Documentation"
      path: /api/docs
      icon: "📖"
ports:
  http: 80
hosts:
  web: app.localhost
configs:
  - .env
dirs:
  - logs
  - home
  - runtime
cli:
  mode: auto
  shell: bash
  user: www-data
  workdir: /workspace/src
render:
  ide:
    enabled: true
  ai:
    enabled: true
```

## Жизненный цикл переключения

Блоки `on_enable`, `on_disable` и `notes` управляют тем, что происходит при переключении сервиса через `dwe services enable/disable`.

### Схема `on_enable` и `on_disable`

```yaml
on_enable:
  requires: none | restart | deploy   # что запускать после записи local.yml
  before: [command-id]                # пользовательские команды, запускаемые до записи переключения
  after: [command-id]                 # пользовательские команды, запускаемые после записи переключения
on_disable:
  requires: none | restart            # deploy не разрешён на disable
  before: [command-id]
  after: [command-id]
```

| Поле | По умолчанию | Описание |
|-------|---------|-------------|
| `requires` | `restart` | Что должно произойти, чтобы изменение вступило в силу. `none` → только запись local.yml; `restart` → запустить `dwe restart`; `deploy` → запустить `dwe deploy run --service <name>`. `deploy` запрещён в `on_disable`. |
| `before` | — | ID пользовательских команд (из `workspace/commands/`) для запуска до записи переключения. Каждая должна быть `type: shell` или `type: script`. |
| `after` | — | ID пользовательских команд для запуска после записи переключения. Применяется то же ограничение типа. |

Хук-команды запускаются с `--yes` (неинтерактивно), stdout отбрасывается, stderr захватывается для сообщений об ошибках.

### Схема `notes`

```yaml
notes:
  enable: "Run migrations after enabling this service."
  disable: "Safe to disable while the stack is running."
```

Заметки показываются в выводе плана (`dwe services enable/disable --print-plan`), чтобы провести оператора через ручные последующие шаги.

### План переключения и `--apply`

`dwe services enable <name>` (без `--apply`) записывает `local.yml` и фиксирует ожидающую операцию в журнале состояния deploy. Ожидающая операция отображается в `dwe status`, пока не очищена. `--apply` выполняет план немедленно (запускает хуки, инициирует restart или deploy, как объявлено в `requires`).

## Типичные ловушки

- **Редактирование `dir` в потомке `extends`** — потомок, задающий `dir`, полностью заменяет `dir` родителя (не сливается). Это намеренно для сервисов, живущих в другом host-каталоге.
- **Абсолютные пути в `dirs`** — записи dirs должны быть относительными путями. Абсолютные пути или пути с `..` отклоняются `service_dirs_ensure` как проверка безопасности.
- **Отсутствие `container` в потомке** — `container` **не** наследуется через `extends:`. Потомок без явного `container` получает по умолчанию имя своей папки (тот же дефолт, что применяется к любому сервису). Объявляйте `container` явно, когда имя папки не подходит как имя контейнера.
- **Забытый `depends_on:` у потомка** — не наследуется. Потомок, нуждающийся в зависимости, должен объявить её явно. (`compose:` наследуется от родителя, когда потомок его опускает — см. [Правила разрешения наследования](extends.md).)
- **Блок `render:` под сервисом `tool` / `infra`** — блок `render:` только для app. Записи tool / infra, объявляющие его, не загружаются. Чтобы прикрепить шаблонный пак к не-app сервису, его нужно сначала переопределить как `app` (с обязательным `dir:`).
- **Существующий не-симлинк по пути управляемого симлинка** — если `CLAUDE.md` (или другой путь `symlinks[].link`) уже существует как обычный файл, `dwe render ai` отказывается его перезаписывать и завершается с ошибкой: `refuse to overwrite non-symlink file at <path>; remove it or disable via render.ai.enabled: false`. Удалите файл первым или установите `render.ai.enabled: false` для этого сервиса.
