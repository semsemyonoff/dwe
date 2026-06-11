> Translated from: reference/config/snapshot.md @ b82d30fabf87

# snapshot.yml

Декларативные workflow'ы снапшотов: захват состояния dwe-проекта (базы данных, индексы, локальный конфиг DWE, состояние деплоя) в именованную директорию под `./snapshots/<name>/` и восстановление или откат к нему.

## Содержание

- [Назначение](#назначение)
- [Разобранный пример: переключение между задачами (UC-3)](#разобранный-пример-переключение-между-задачами-uc-3)
- [Расположение файла](#расположение-файла)
- [Поля верхнего уровня](#поля-верхнего-уровня)
- [Workflow-блоки: `create` / `restore` / `remove`](#workflow-блоки-create--restore--remove)
- [Варианты](#варианты)
- [`services_mismatch`](#services_mismatch)
- [`local_yml.preserve_keys`](#local_ymlpreserve_keys)
- [`pack`](#pack)
- [`unpack`](#unpack)
- [Шаблонный неймспейс: `${snapshot.*}`](#шаблонный-неймспейс-snapshot)
- [Содержимое манифеста](#содержимое-манифеста)
- [Раскладка на файловой системе](#раскладка-на-файловой-системе)
- [Семантика lifecycle и безопасности](#семантика-lifecycle-и-безопасности)
- [Взаимодействие с блокировками](#взаимодействие-с-блокировками)
- [Exit-коды](#exit-коды)
- [Домен validate](#домен-validate)
- [Связанные команды](#связанные-команды)

## Назначение

Снапшот захватывает заведомо рабочее состояние изменяемых проектных данных — обычно баз данных, поисковых индексов, метаданных service-веток и workspace-файлов, фиксирующих локальную конфигурацию разработчика — в самодостаточную директорию. Restore — мягкая операция: запускается workflow `restore`, и workspace-файлы возвращаются на место. Он **не** вызывает `reset`, не пересоздаёт контейнеры и не переприменяет шаги деплоя.

Рассчитан на сценарий: *«я на фиче, прилетает хотфикс — сохраняю, переключаюсь на чистую БД, чиню, возвращаюсь к фиче»*.

Ядро ничего не знает о конкретных хранилищах данных. Пользователь сам определяет workflow'ы `create` / `restore`, которые вызывают существующие пользовательские команды (`db.dump`, `opensearch.snapshot` и т. д.).

## Разобранный пример: переключение между задачами (UC-3)

```yaml
# workspace/snapshot.yml
rollback_target: baseline

create:
  description: Capture current DB and search index
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }

restore:
  description: Restore DB and search index from snapshot
  steps:
    - command: db.restore
      when: file-exists ${snapshot.path}/db/main.sql.gz
      with: { in: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.restore
      when: file-exists ${snapshot.path}/search/index.tar
      with: { in: ${snapshot.path}/search/index.tar }
```

Один день из жизни:

```sh
dwe snapshot create feature-x-wip -d "WIP on feature X"
# прилетает хотфикс; восстанавливаем чистый baseline
dwe snapshot restore baseline
# ... делаем хотфикс, пушим, мерджим ...
dwe snapshot restore feature-x-wip          # обратно к WIP
dwe snapshot rollback                       # быстро: восстановить rollback_target
```

## Расположение файла

`workspace/snapshot.yml` в корне проекта. Файл опционален — подкоманды только для чтения (`list`, `current`, `inspect`, `unpack`) работают без него. Изменяющие подкоманды (`create`, `restore`, `rollback`, `remove`, `pack`) падают с ошибкой, если он отсутствует или нужный workflow-блок не задан.

## Поля верхнего уровня

| Поле | Тип | По умолчанию | Назначение |
|---|---|---|---|
| `dir` | string | `./snapshots` | Где лежат директории снапшотов и тарболы. Разрешается относительно корня проекта. |
| `rollback_target` | string | — | Имя снапшота, используемого `dwe snapshot rollback`. Должно указывать на существующий снапшот. |
| `require_matching_config` | bool | `false` | Когда `true`, `restore` прерывается (exit 1), если `project.config_hash` снапшота отличается от текущего состояния деплоя. Если `config_hash` снапшота пуст (деплой ещё не запускался), это трактуется как совпадение — никогда не блокируется. |
| `services_mismatch` | block | — | Политика расхождения набора сервисов между манифестом и текущим конфигом (см. [`services_mismatch`](#services_mismatch)). |
| `local_yml` | block | — | Политика переопределения ключей `workspace/local.yml` (см. [`local_yml.preserve_keys`](#local_ymlpreserve_keys)). |
| `pack` | block | — | Политика упаковки (см. [`pack`](#pack)). |
| `create` | workflow | — | Workflow захвата (см. [Workflow-блоки](#workflow-блоки-create--restore--remove)). |
| `restore` | workflow | — | Workflow восстановления. |
| `remove` | workflow | — | Workflow очистки, запускаемый `dwe snapshot remove` до удаления директории. |

Загрузчик использует строгое декодирование (`KnownFields(true)`): неизвестные ключи верхнего уровня — хард-ошибки.

## Workflow-блоки: `create` / `restore` / `remove`

Каждый блок имеет:

| Поле | Тип | Назначение |
|---|---|---|
| `description` | string | Свободное описание, показываемое `inspect` и `list`. |
| `steps` | `[]WorkflowStep` | Список шагов — та же форма, что и блок `workflow:` в декларативной команде. Синтаксис шага см. в [commands/types.md](commands/types.md#type-workflow). |
| `variants` | `map[string]Workflow` | Именованные альтернативные списки шагов (см. [Варианты](#варианты)). |

Форма `steps:` — это существующий тип `model.WorkflowStep`. Workflow'ы снапшотов — это workflow'ы пользовательских команд, выполняемые в рантайме из другого исходного файла; поддерживается каждая форма и фича шага (`command:`, `with:`, `when:`, `confirm:`, `parallel:`, `continue_on_error:`).

Restore — это **drop + restore**: никакого префиксирования БД, никакой подмены имени. Ваша пользовательская команда `db.restore` обычно дропает целевую БД и перезагружает её из `${snapshot.path}/db/main.sql.gz`.

`baseline` — это просто обычное имя снапшота. Никакой зарезервированной семантики нет.

## Варианты

Вариант — это именованный альтернативный список шагов внутри workflow-блока. Полезно, когда «захватить всё» и «захватить только БД» должны сосуществовать.

```yaml
create:
  description: Capture full env
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }
  variants:
    db-only:
      description: Capture DB only
      steps:
        - command: db.dump
          with: { out: ${snapshot.path}/db/main.sql.gz }
```

Выбор:

- `dwe snapshot create x` → блок по умолчанию.
- `dwe snapshot create x --using=db-only` → `create.variants.db-only`.
- `dwe snapshot restore x` → использует `restore.variants[<manifest.variant>]`, если задан; откатывается к дефолтному блоку `restore`, если вариант отсутствует на стороне restore.
- Отсутствующий вариант на **create** падает с ошибкой до любых мутаций файловой системы.

Имена вариантов должны соответствовать `[a-z0-9][a-z0-9._-]{0,30}`. Вариант, выбранный на create, записывается в манифест, чтобы restore автоматически подобрал соответствующий блок.

## `services_mismatch`

Управляет тем, что делает `restore`, когда набор сервисов, записанный в снапшоте, расходится с эффективным набором сервисов текущего проекта. Манифест снапшота записывает каждый эффективный сервис (имя + флаг enabled, отсортированный по имени) на момент create; restore сравнивает это с `cfg.Services` и применяет настроенную политику.

```yaml
services_mismatch:
  policy: warn          # warn (default) | block | ignore
```

| Политика | Поведение |
|---|---|
| `warn` (по умолчанию, в том числе когда блок опущен) | Restore продолжается. Любая непустая разница рендерится в промпте подтверждения; с `-y` предупреждение пишется в stderr, и restore продолжается. |
| `block` | Любая непустая разница прерывает работу до любых side-эффектов на `workspace/local.yml` (exit 1, типизированный `ServicesMismatchError`). |
| `ignore` | Разница полностью подавляется; restore продолжается молча. |

Разница группируется в три корзины, каждая рендерится одной формулировкой в промпте restore, `snapshot inspect` и валидаторе `snapshot.<name>.services_diff`:

| Группа | Значение |
|---|---|
| `only in snapshot` | Сервис назван в манифесте, но отсутствует в текущем проекте (вероятны runtime-падения в шагах workflow restore, нацеленных на него). |
| `only local` | Сервис есть в текущем проекте, но отсутствует в манифесте (записи deploy-state для него останутся на диске после restore — безвредно, но легко проглядеть). |
| `enabled differs` | Одно и то же имя по обе стороны, но флаг `enabled` инвертировался. |

Неизвестные значения `policy` отвергаются на загрузке с понятной ошибкой `"unknown policy"`, перечисляющей разрешённое множество.

## `local_yml.preserve_keys`

`workspace/local.yml` обычно содержит машинно-специфичные оверрайды (порты, имена хостов, пути), которые не должны путешествовать со снапшотом. `preserve_keys` перечисляет dot-пути, чьи **текущие** значения переживают restore, даже когда снапшот привозит свой `local.yml`.

```yaml
local_yml:
  preserve_keys:
    - services.main.ports
    - services.db.ports
    - vars.host.shell
```

- Dot-пути адресуют вложенные ключи маппинга; сегменты с индексом массива (`services[0].ports`) не поддерживаются — `local.yml` представляет собой maps-of-maps.
- Пути, отсутствующие с обеих сторон, — молчаливые no-op'ы; структурные конфликты (например, промежуточный сегмент, не являющийся маппингом, или несовместимые kind'ы по пути между снапшотом и текущим состоянием) всплывают как понятные ошибки.
- Хелперы работают с `*yaml.Node`, чтобы сохранить порядок ключей и комментарии, привязанные к узлам, там, где `yaml.v3` их удерживает. `yaml.v3` нормализует отступы и flow/block-стиль при маршалинге, так что байт-точное форматирование не сохраняется — только семантическое содержимое + порядок ключей + комментарии на нетронутых узлах.
- Применяется кэп 1 MiB на payload `local.yml` и на create, и на restore, чтобы обезвредить YAML alias-explosion в недоверенном содержимом архива.

**Create**: `captureDWEFiles` читает `workspace/local.yml`, вызывает `stripPreservedKeys`, чтобы удалить перечисленные dot-пути, и пишет результат в `<snap>/workspace/local.yml`. Если каждый ключ верхнего уровня был сохранён (получился пустой маппинг), файл всё равно записывается, чтобы семантика restore оставалась однозначной.

**Restore**: пограничные случаи:

| В снапшоте есть `local.yml` | В текущем есть `local.yml` | Поведение |
|---|---|---|
| да | да | merge: оверлей снапшота + сохранённые ключи, вшитые из текущего |
| да | нет | пишем `local.yml` снапшота как есть (preserve_keys — no-op) |
| нет | да (с сохраняемыми значениями) | пишем минимальный `local.yml`, содержащий только сохранённые ключи, извлечённые из текущего |
| нет | нет | no-op |

`deploy-state.yml` всегда перезаписывается при restore — merge не делается. Orphan-записи для сервисов, которых уже нет локально, безопасны (deploy игнорирует их на следующем запуске).

## `pack`

```yaml
pack:
  exclude:
    - "**/*.tmp"
    - ".cache/**"
```

`pack.exclude` — список doublestar-глобов, вычисляемых относительно директории снапшота. CLI-флаги `--exclude` **дополняют** этот список (не заменяют).

`dwe snapshot pack <name>` производит один `./snapshots/<name>.tar.gz`. `.sha256`-сайдкар не пишется — один файл на снапшот. In-memory sha256 архива показывается в сообщении об успехе для ad-hoc сверки, но `unpack` его не требует. Транспортные битфлипы ловятся CRC32 gzip'а и структурной валидностью tar; in-archive подделка отдельных артефактов ловится manifest-driven верификацией `unpack` (см. ниже).

## `unpack`

`dwe snapshot unpack <tar-path> [--as=<name>] [--no-verify] [-y]` извлекает архив в `./snapshots/<final-name>/`, используя существующий distrust-safe-контракт извлечения (`filepath.IsLocal`, без симлинков, кэп 50 GiB, кэп 100 000 записей) в соседнюю staging-директорию `./snapshots/.unpack-<random>/`, затем атомарным rename'ом ставит её на место.

После извлечения (и до rename'а в финальную позицию) staging-дерево верифицируется против `manifest.yml`:

| Группа | Строка в stderr | Триггерит confirm? |
|---|---|---|
| `Missing` (в манифесте, нет на диске) | `warning: artifact %q listed in manifest is missing from archive` | да (сгруппированно) |
| `HashMismatch` (есть и там, и там, sha256 различается) | `warning: artifact %q sha256 mismatch (manifest=%s, actual=%s)` | да (сгруппированно) |
| `Extra` (на диске, нет в манифесте) | `info: archive contains %q not listed in manifest` | нет |

`Missing` и `HashMismatch` делят один сгруппированный промпт `continue? [y/N]` в конце (по умолчанию `no`). Отказ триггерит очистку staging и возвращает `UnpackVerifyDeclinedError`; финальная директория не тронута, потому что верификация срабатывает до шага «отодвинуть старую aside». `Extra` — только информация.

Флаги:

- `--no-verify` — полностью пропустить верификацию артефактов. Обход объявляется в stderr (`warning: skipping artifact verification at user request (--no-verify)`), чтобы быть видимым в логах CI и постмортемах.
- `-y` — автоматически принять и промпт перезаписи (когда финальная директория уже существует), и промпт верификации; предупреждения всё равно печатаются в stderr.

Итоговая строка успеха гласит `(verified)`, `(verified with N warnings)` или `(verification skipped)` — это управляется `UnpackResult.Verification`. Верификатор валидирует путь каждого артефакта из `manifest.yml` через `filepath.IsLocal` + `pathsafe.ContainedRel` относительно корня staging **до** открытия любого файла, так что злонамеренно сконструированный манифест не может заставить верификатор читать пути за пределами staging-дерева.

> **Граница доверия**: верификация манифеста — это integrity-of-record, не authenticity. Она ловит случайные мутации, частичные обрезания и неаккуратную in-archive подделку. Она **не** ловит атакующего, который перепаковал архив с самосогласованным манифестом (он может просто переписать `manifest.yml` под новые артефакты). Это приемлемый компромисс для dev-инструмента — никакого GPG, никаких подписей, никакого криптографического provenance.

## Шаблонный неймспейс: `${snapshot.*}`

`${snapshot.*}` доступен только внутри workflow-блоков снапшота (и аргументов `with:`, передаваемых пользовательским командам, вызываемым из этих блоков). За их пределами — это compile-time ошибка.

| Переменная | Вне снапшота | Скоуп `create` | Скоуп `restore` / `remove` |
|---|---|---|---|
| `${snapshot.name}` | ошибка | ✓ | ✓ |
| `${snapshot.path}` | ошибка | ✓ | ✓ |
| `${snapshot.description}` | ошибка | ✓ | ✓ |
| `${snapshot.variant}` | ошибка | ✓ | ✓ |
| `${snapshot.created_at}` | ошибка | **ошибка** (ещё не существует) | ✓ |

`${snapshot.path}` — это абсолютный путь к `./snapshots/<name>/`. Ожидается, что workflow'ы пишут артефакты под ним. Симлинки, созданные внутри директории снапшота, отвергаются на стадии сканирования — workflow'ы должны производить обычные файлы.

Отсутствующие ключи внутри активного скоупа рендерятся как пустые строки, согласованно с `${param.*}`.

## Содержимое манифеста

Каждая директория снапшота несёт `manifest.yml`:

```yaml
name: feature-x-wip
created_at: 2026-05-24T11:02:00Z
description: WIP feature X
project:
  name: tbm-next
  config_hash: def67890          # empty if no deploy has run yet
  services:                      # effective service set at create time, sorted by name
    - { name: cdn,  enabled: false }
    - { name: db,   enabled: true }
    - { name: main, enabled: true }
dwe_version: 0.42.0
variant: ""
artifacts:
  - path: db/main.sql.gz
    size: 1287654321             # int64
    sha256: abc...
workspace_files:
  local_yml: workspace/local.yml
  deploy_state: workspace/deploy-state.yml
last_create:
  at: 2026-05-24T11:02:00Z
  status: ok                     # ok | failed | interrupted
  failed_step: ""
last_restore:
  at: 2026-05-24T15:42:00Z
  status: ok
  duration_ms: 12340
  failed_step: ""
```

## Раскладка на файловой системе

```
<project>/
  workspace/snapshot.yml
  snapshots/
    <name>/
      manifest.yml
      workspace/{local.yml, deploy-state.yml}
      <user artifacts>
    <name>.tar.gz
  .dwe/snapshots/
    current
    snapshot.lock
    .pre-restore-backup/{local.yml, deploy-state.yml}
    .unpack-<random>/             # transient unpack staging
```

- `./snapshots/` обычно **не** в gitignore, чтобы небольшие dev-фикстуры могли путешествовать через git; крупные артефакты следует добавлять в `.gitignore` пер-проектно.
- `.dwe/snapshots/` — в gitignore.
- `current` — небольшой текстовый файл с именем последнего созданного или восстановленного снапшота. Очищается, когда активный снапшот удаляют.

## Семантика lifecycle и безопасности

**Create**

- Захватывает проектные блокировки (см. [Взаимодействие с блокировками](#взаимодействие-с-блокировками)).
- Отказывается перезаписывать существующую директорию снапшота без `-y` в non-TTY контекстах (иначе — интерактивное подтверждение).
- Копирует `workspace/local.yml` и `.dwe/deploy/state.yml` в `<snap>/workspace/` до запуска workflow.
- Запускает выбранный workflow создания с `${snapshot.*}`, доступным в скоупе `create`.
- Сканирует получившуюся директорию (исключая `manifest.yml` и `workspace/`), стримя sha256 по файлам. Симлинки внутри директории снапшота отвергаются.
- Пишет `manifest.yml` атомарно (temp-файл в той же директории, `rename`).
- Атомарно обновляет указатель current.
- При падении workflow: оставляет директорию, пишет `last_create.status = "failed"` с `failed_step`, не трогает указатель current, выходит с кодом 1.
- При SIGINT: `last_create.status = "interrupted"`, exit 130.

**Restore**

- Захватывает проектные блокировки.
- Загружает и верифицирует манифест. Предупреждает, когда `project.config_hash` отличается от текущего состояния деплоя; блокирует (exit 1), когда `require_matching_config: true`. Пустой `config_hash` в манифесте трактуется как совпадение.
- Атомарно бэкапит текущие `workspace/local.yml` и `.dwe/deploy/state.yml` в `.dwe/snapshots/.pre-restore-backup/`. Предыдущий бэкап перезаписывается.
- Восстанавливает workspace-файлы из `<snap>/workspace/` поверх рабочих копий.
- Запускает выбранный workflow restore с `${snapshot.*}`, доступным в скоупе `restore` (все ключи, включая `created_at`).
- При успехе: атомарно обновляет указатель current и пишет `last_restore.status = "ok"` в манифест.
- При падении или SIGINT: не трогает указатель current; пишет `last_restore.status ∈ {failed, interrupted}` с `failed_step`; выводит подсказку про `.pre-restore-backup/` для ручного восстановления; выходит с кодом 1 (или 130 для SIGINT).

**Rollback** диспатчится в код-путь restore с `rollback_target`. Падает с понятной ошибкой, если целевой снапшот не существует.

**Remove**

- Захватывает проектные блокировки.
- Запускает workflow `remove:` (если определён) с шаблонной видимостью restore-скоупа.
- `os.RemoveAll(snapshotDir)`.
- Атомарно очищает указатель current, когда он указывал на этот снапшот.

**Live-вью**

`snapshot create`, `restore` и `remove` рендерят пошаговый live-статус (спиннер, иконка ✓ / ✗ / skip, прошедшее время) для top-level последовательных шагов выбранного workflow, в той же стилистике, что и `deploy run`. Параллельные группы внутри workflow сохраняют своё существующее построчное блочное отображение, вложенное под строку шага группы. Live-вью включается автоматически, когда stdout — это TTY; в non-TTY контекстах и когда передан `--no-live`, оно отключено, и workflow падает обратно к простому stdout-выводу. Mid-workflow шаги `confirm:` и промпты `confirmation:` уровня команды приостанавливают live-футер на время промпта и перерисовывают его после.

**Pack / unpack** используют тот же контракт блокировок, что и изменяющие команды снапшота — pack читает директорию снапшота под блокировкой, чтобы конкурентный `remove` / `create` не мог обрезать архив; unpack пишет под блокировкой, чтобы конкурентные операции не могли войти в гонку с rename'ом staging → final. Unpack обеспечивает строгий контракт безопасности архива: пути должны удовлетворять `filepath.IsLocal` и `ContainedRel` относительно корня staging, принимаются только обычные файлы и директории (никаких симлинков, hardlink'ов, devices, fifo, global headers), а экстрактор ограничивает общий размер байтов (50 GiB) и количество записей (100 000), чтобы обезвредить zip-бомбы. Извлечение стейджится в соседнюю temp-директорию под `./snapshots/`, затем при успехе атомарным rename'ом переезжает в финальное имя; любое падение удаляет staging-директорию, не трогая целевую. Если финальная директория уже существует, существующее дерево отодвигается в сторону как бэкап; staging-дерево переименовывается на место; при успехе бэкап удаляется, а при падении второго rename'а бэкап восстанавливается. После извлечения (и до шага rename-into-place) staging-дерево сверяется с `manifest.yml` — поведение warn + confirm см. в [`unpack`](#unpack).

## Взаимодействие с блокировками

Все команды, изменяющие проект, захватывают две блокировки в фиксированном порядке:

1. `<baseDir>/.dwe/deploy/deploy.lock`
2. `<baseDir>/.dwe/snapshots/snapshot.lock`

Освобождение — в обратном порядке. Общий хелпер `lock.AcquireProjectLocks(baseDir)` обеспечивает это и для изменяющих команд снапшота (`create`, `restore`, `rollback`, `remove`, `pack`, `unpack`), и для lifecycle-команд деплоя (`deploy`, `run`, `stop`, `restart`, `reset`).

Lifecycle-команды захватывают проектные блокировки **после** успешного прохода preflight — preflight может вызывать пользовательские `type: command` проверки и не должен запускаться под operation-локами. Мутирующие команды снапшота preflight не запускают и захватывают блокировки в начале своего `RunE`.

Когда любая из блокировок уже удерживается другим живым процессом, операция выходит с кодом 75 (`EX_TEMPFAIL`) с понятным сообщением `"<operation> in progress: pid N"`.

## Exit-коды

| Код | Когда |
|---|---|
| 0 | Успех |
| 1 | Падение workflow, повреждение манифеста, отказ архива, отсутствующий обязательный блок конфигурации, блок `require_matching_config` |
| 64 | Ошибка использования (плохое имя, отсутствующий аргумент, кривой YAML на CLI-поверхности) |
| 75 | Блокировка удерживается другим живым процессом |
| 130 | SIGINT во время долгого workflow |

## Домен validate

`dwe validate snapshot [<name>] [--verify]` выставляет статические проверки:

| Валидатор | Severity | Триггер |
|---|---|---|
| `snapshot.config_loadable` | error | `workspace/snapshot.yml` существует, но не парсится. Отсутствие файла — тишина. |
| `snapshot.create_defined` | info | Отсутствует блок `create:` — `dwe snapshot create` откажется запускаться. |
| `snapshot.restore_defined` | info | Отсутствует блок `restore:` — `restore` / `rollback` откажутся. |
| `snapshot.variant_pairing` | warn | `create.variants[X]` есть, но `restore.variants[X]` отсутствует, и дефолтного блока `restore` для отката нет. |
| `snapshot.rollback_target_exists` | warn | `rollback_target` задан, но снапшота с таким именем на диске не существует. |
| `snapshot.<name>.manifest_valid` | error | `manifest.yml` отсутствует или непарсимый. |
| `snapshot.<name>.artifacts_exist` | error | Любой артефакт, перечисленный в манифесте, отсутствует на диске. |
| `snapshot.<name>.checksums` | warn | С `--verify`: пересчитанный sha256 любого артефакта отличается от манифеста. |
| `snapshot.<name>.services_diff` | info | Набор сервисов, записанный в манифесте, отличается от эффективного набора сервисов текущего проекта. Hint цитирует отформатированную разницу. |
| `snapshot.<name>.last_create_failed` | info | `last_create.status ∈ {failed, interrupted}`. |
| `snapshot.template_scope` | error | `${snapshot.created_at}` использован в блоке `create:` (на момент create он ещё не существует). |

## Связанные команды

- `dwe snapshot create <name> [-d <desc>] [--using=<variant>] [-y] [--no-live]`
- `dwe snapshot list [--output json] [--pretty]`
- `dwe snapshot current [--output json] [--pretty]`
- `dwe snapshot inspect <name|tar-path> [--output json] [--pretty]`
- `dwe snapshot restore <name> [-y] [--no-live]`
- `dwe snapshot rollback [-y] [--no-live]`
- `dwe snapshot remove <name> [-y] [--no-live]`
- `dwe snapshot pack <name> [--out=<path>] [--exclude=<glob>...]`
- `dwe snapshot unpack <tar-path> [--as=<name>] [--no-verify] [-y]`
- `dwe validate snapshot [<name>] [--verify]`

См. [commands/types.md](commands/types.md#type-workflow) для формы `WorkflowStep`, переиспользуемой workflow'ами снапшотов, и [state/index.md](state/index.md) для журнала состояния деплоя, который снапшоты бэкапят рядом с `workspace/local.yml`.
