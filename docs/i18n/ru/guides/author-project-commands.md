> Translated from: guides/author-project-commands.md @ 3ba6ecad1ead

# Авторство проектных команд

README проекта обрастает командами, которые копируют и вставляют вручную: «запусти это, чтобы засидить базу», «запусти это, чтобы пересобрать фронт», «запусти это, когда очередь барахлит». Это руководство показывает, как заменить такие сниппеты на полноценные команды `dwe <id>`: вся команда сможет находить их через `dwe commands`, запускать через `dwe cmd <id>` и собирать из них более крупные пайплайны.

Полная схема — в [`../reference/config/commands/index.md`](../reference/config/commands/index.md); эта страница — про три типа, к которым вы будете обращаться чаще всего (`shell`, `service_exec`, `workflow`), и про директивы, делающие их безопасными в продакшене.

## Раскладка файлов и ID команд

Команды живут под `workspace/commands/`. Путь каталога становится ID команды, где сегменты разделены точками. Базовое имя файла — это листовой сегмент, а каждый ключ в `commands:` файла добавляет ещё одну точку.

```
workspace/commands/
├── db.yml                       # группа: db
└── db/
    └── seed.yml                 # группа: db.seed
```

Команда с ключом `default` внутри `workspace/commands/db/seed.yml` была бы `dwe cmd db.seed.default`. Чаще ядро действия кладут под описательный ключ:

```yaml
# workspace/commands/db/seed.yml
group:
  title: Database seeding

commands:
  run:                            # полный ID: db.seed.run
    type: shell
    description: Seed the database with development fixtures
    cmd: |
      "$DWE_BIN" shell app -c "php artisan db:seed --class=DevSeeder"
```

Запуск — `dwe cmd db.seed.run`, видна в `dwe commands list` или в интерактивном браузере по `dwe commands`.

Эмпирическое правило: основные команды группы держите в одном файле, названном по имени группы; разбивайте на поддиректорию только тогда, когда команд набирается достаточно для выделения логических подгрупп.

## `type: shell` — простейший случай

`type: shell` запускает команду на **хосте** через `sh -c`. Используйте для git-операций, host-side build-шагов или любого one-liner, которому не нужно быть внутри контейнера.

```yaml
commands:
  format:
    type: shell
    description: Run gofmt over the whole repo
    cmd: gofmt -w .
```

`cmd:` — это строка, которую отдают в `sh -c`, так что полная семантика шелла (пайпы, редиректы, env-расширение) работает. Если хотите обойти шелл целиком — используйте `argv:`:

```yaml
commands:
  commit-config:
    type: shell
    description: Commit a generated config file
    argv:
      - git
      - commit
      - -m
      - "chore: regen config"
      - generated.yml
```

`cmd:` и `argv:` взаимоисключающие.

### Контракт env у shell

Каждый подпроцесс `type: shell` наследует три экспортированные переменные, чтобы дотянуться до того же compose-проекта, которым управляет DWE, без повторного его обнаружения:

| Переменная | Значение |
|------------|----------|
| `DWE_BIN` | Абсолютный путь к запущенному бинарю `dwe` |
| `COMPOSE_PROJECT_NAME` | Активное имя compose-проекта |
| `COMPOSE_FILE` | Список активных оверлейных путей (абсолютных), склеенных двоеточием |

Используйте `"$DWE_BIN"` вместо `./bin/dwe` — это делает команды переносимыми между машинами:

```yaml
commands:
  warm-cache:
    type: shell
    cmd: |
      "$DWE_BIN" shell app -c "php artisan cache:warm"
```

`COMPOSE_PROJECT_NAME` и `COMPOSE_FILE` позволяют вызовам `docker compose ...` внутри `cmd:` подхватывать оверлеи DWE без флагов `-p` / `-f`.

## `type: service_exec` — запуск внутри контейнера

`type: service_exec` запускает команду внутри уже работающего контейнера через `docker compose exec`. Используйте для операций уровня приложения (artisan, manage.py, rails, mix), которые должны идти против живого контейнера.

```yaml
commands:
  db.create:
    type: service_exec
    description: Create a database in the db container
    service: db
    mode: exec-or-fail   # база — персистентное состояние, одноразовую создавать нельзя
    params:
      database:
        type: string
        required: true
        pattern: ^[a-zA-Z0-9_-]+$
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE IF NOT EXISTS `${param.database}`;'"
```

Ключевые поля:

- **`service:`** — имя compose-сервиса.
- **`mode:`** — что делать, если контейнер не запущен:
  - `exec-or-run` (по умолчанию) — фолбэк на свежий `docker compose run --rm`-контейнер с предупреждением об эфемерном запуске.
  - `exec-or-fail` — отказ с действенной ошибкой и подсказкой `dwe docker up <svc>`.
  - `exec` — голый `docker compose exec`; docker эмитит свою ошибку, если контейнер лёг.
  - `run` — всегда поднимать свежий контейнер.

  Не пишите `mode:` вовсе, если инструмент честно работает и как one-off (composer install на свежем чек-ауте и т.п.). Объявляйте `mode: exec-or-fail` для инструментов, зависящих от постоянного состояния контейнера (БД, app-серверы), — тех, которым нельзя молча поднять контейнер за вашей спиной.
- **`user:`** — `current` запускает от UID:GID хоста (для команд, пишущих в bind-mount); `root` — для привилегированных контейнерных операций; либо литерал `name` / `1000` / `1000:1000`. Опустите — наследуется `cli.user` сервиса.
- **`workdir_from:`** — dot-путь в собранный конфиг (например, `services.main.work_dir_internal`). Предпочтительнее жёсткого `workdir:`, потому что это позволяет оверрайдам из `local.yml` доходить до команд.

Для one-off инструментов, которым всегда нужен свежий контейнер (artisan tinker, php -a, irb), берите `type: service_run` — те же поля, но всегда `docker compose run --rm`.

Полный справочник: [`../reference/config/commands/types.md#type-service_exec`](../reference/config/commands/types.md#type-service_exec).

## `type: workflow` — компонуем несколько команд

Workflow сшивает существующие команды в одну именованную последовательность. Используйте, когда трижды-четырежды подряд запускаете одни и те же команды или когда одно пользовательское действие («bootstrap», «reset-and-reseed») естественно распадается на несколько шагов.

```yaml
commands:
  bootstrap:
    type: workflow
    description: Full bootstrap — db, deps, migrate, seed
    steps:
      - command: db.start
      - command: db.create
        with:
          database: "${vars.db.database}"
      - command: composer-install
      - command: migrate
      - command: db.seed.run
        when: "{{ if .Params.seed }}1{{ else }}0{{ end }}"
        continue_on_error: true
    params:
      seed:
        type: bool
        default: true
```

Каждый шаг — это один из трёх вариантов (взаимоисключающие):

- **`command: <id>`** — вызов другой команды. `with:` переопределяет её params; `when:` пропускает шаг, когда выражение ложно; `continue_on_error: true` превращает падение в предупреждение вместо прерывания.
- **`confirm: <text>`** — спросить пользователя перед продолжением. Обходится через `--yes` и `DWE_NONINTERACTIVE=1`.
- **`parallel:`** — разветвить группу листовых command-шагов параллельно. Полезно для «запусти composer install в каждом app-сервисе».

### Выражения `when:`

`when:` шага сначала рендерится, затем классифицируется:

| Форма | Пример | Заметки |
|------|--------|---------|
| Boolean-литерал | `"true"`, `"1"`, `""` | Быстрый путь после рендера |
| Builtin-предикат | `file-missing services/main/vendor/autoload.php` | `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`; пути относительны корня проекта |
| Shell-команда | `cmd: test ! -d services/main/vendor` | Прогоняется через `sh -c`; exit 0 = true |

Предпочитайте builtin-предикаты, когда они подходят — они дешевле и не плодят шелл.

Полный справочник: [`../reference/config/commands/types.md#type-workflow`](../reference/config/commands/types.md#type-workflow).

## Params: типизированные входы

Каждый тип команды может объявить `params:` — типизированные входы, которые пользователь подаёт через `--set key=value` на CLI или которые передаёт workflow/pipeline-шаг через `with:`.

```yaml
commands:
  db.create:
    type: service_exec
    service: db
    params:
      database:
        type: string                # string (по умолчанию) | bool | int | path
        description: Database name to create
        required: true
        default: "app"              # литеральный фолбэк
        default_from: vars.db.database   # dot-путь в собранный конфиг (предпочтительнее)
        env: DB_NAME                # экспонирует значение как $DB_NAME
        pattern: ^[a-zA-Z0-9_-]+$   # якорный regex (только string/path)
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'CREATE DATABASE `${param.database}`;'"
```

Порядок разрешения, сверху вниз:

1. Значение от вызывающего (`--set database=foo` или `with: { database: foo }`).
2. `default_from` — dot-путь в собранный конфиг DWE. Пустой результат считается отсутствующим.
3. Литеральный `default:`.
4. Если всё ещё пусто и `required: true` — ошибка.

Правило `default_from` позволяет оверрайдам из `local.yml` доходить до команд без того, чтобы каждый разработчик переписывал литеральный default. Это тот же паттерн «конфиг побеждает, код даёт safety net», что и в других местах DWE.

Используйте разрешённое значение как `${param.<name>}` в `cmd:`, `argv:`, `env:`, `workdir:`, `confirmation_text:` и путях к файлам.

Чтобы показать params в интерактивном браузере как дружелюбную форму (dropdown-ы, multi-select, confirm-виджеты), объявите `widget:` и `options:` — см. [param widgets](../reference/config/commands/directives.md#param-widgets).

## Подтверждение и `--yes`

Любая команда может требовать подтверждение перед запуском:

```yaml
commands:
  db.drop:
    type: service_exec
    description: Drop a database (irreversible!)
    confirmation: true
    confirmation_text: "Drop database `${param.database}`?"
    service: db
    params:
      database:
        required: true
        default_from: vars.db.database
    env:
      MYSQL_PWD: "${vars.db.password}"
    cmd: "mariadb -u${vars.db.user} -e 'DROP DATABASE IF EXISTS `${param.database}`;'"
```

`confirmation_text:` поддерживает `${...}`-шаблонизатор, чтобы можно было показать значения, над которыми пользователь сейчас собирается выполнить действие. Промт обходится в трёх случаях:

- Пользователь передал `--yes` / `-y` на CLI.
- Команда выполняется как sub-step workflow под родителем, запущенным с `--yes`.
- Не-TTY stdin при `CI=1` (авто-подтверждается через plain Y/n фолбэк).

Для скриптов канонический идиом: `dwe cmd db.drop --set database=app --yes`.

## Уведомления

`notify: true` подключает команду к desktop-уведомлению по завершении (успех или провал). Уведомление срабатывает только когда команда — **верхнеуровневый** вызов; workflow и пайплайны не пробрасывают вложенный `notify:` пользователю.

```yaml
commands:
  db.import:
    type: shell
    notify: true
    cmd: |
      "$DWE_BIN" shell app -c "php artisan db:import ${param.dump}"
```

Используйте для долгих «запустил и переключился в другое окно» команд (большие импорты, полные bootstrap-ы, pack/unpack снапшота). Для команд короче секунды опускайте — десктоп-попапы для мгновенных операций лишь создают шум.

`notify: true` на `type: daemon` — это ошибка валидатора (у демонов нет события завершения), на sub-step внутри `parallel:` — info-диагностика (runtime его всё равно подавляет).

Полный справочник: [`../reference/config/notifications.md`](../reference/config/notifications.md).

## Видимость: `private:` и `hide:`

По умолчанию каждая команда показывается в `dwe commands list`, в интерактивном браузере и в tab-completion. Две директивы их скрывают:

- **`private: true`** — статическое намерение автора. Команда исключена из `commands list`, браузера и прямого вызова `dwe cmd <id>`, но остаётся вызываемой из workflow и пайплайнов. Используйте для «step»-команд, которые не должны запускаться напрямую.

  ```yaml
  commands:
    db.up:
      type: dwe
      private: true                  # используется только внутри workflow db.start
      cmd: "docker up db"
  ```

- **`hide:`** — runtime-условие. Тот же синтаксис выражения, что и у `when:` workflow-шага. Команда видна, когда выражение falsy, и исчезает, когда truthy. Типичное использование — привязать команду к включённости сервиса.

  ```yaml
  commands:
    db.engine.reset:
      type: shell
      hide: '{{ eq (index .services "db" "engine") "sqlite" }}'
      cmd: db reset --engine
  ```

  `hide:` на блоке `group:` скрывает всю группу и потомков.

`private:` подходит для «внутренней проводки» — атомарных шагов workflow, не предназначенных к показу. `hide:` подходит для «релевантно только в некоторых конфигурациях» — команды, имеющей смысл только при одном из движков сервиса или только при включённом опциональном сервисе.

## Перекрёстные ссылки

- [`../reference/config/commands/index.md`](../reference/config/commands/index.md) — полная структура файла и жизненный цикл выполнения.
- [`../reference/config/commands/types.md`](../reference/config/commands/types.md) — каждый тип команды (`shell`, `dwe`, `script`, `service_exec`, `service_run`, `workflow`, `builtin`, `daemon`) со всеми type-specific полями.
- [`../reference/config/commands/directives.md`](../reference/config/commands/directives.md) — каждая общая директива на одной странице.
- [`../reference/config/commands/templating.md`](../reference/config/commands/templating.md) — полная таблица резолвера `${...}` и `{{ ... }}`.
- [`background-daemons.md`](background-daemons.md) — форма `type: daemon` для долгоживущих фоновых процессов.
