> Translated from: guides/write-snapshot-workflows.md @ a5a1f22c2cb2

# Авторство снапшот-воркфлоу

Вы уже достаточно часто пользовались `dwe snapshot create` и `restore`, чтобы знать потребительскую сторону (см. [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md)). Теперь вы хотите написать `workspace/snapshot.yml`, который ими рулит — решить, что захватывается, что восстанавливается, что переживает restore и что подчищается при удалении.

Это руководство проходит по файлу от минимально полезной формы до production-ручек, к которым тянешься, когда коллеги начинают шарить снапшоты.

## Разделы

- [Минимальный воркфлоу](#минимальный-воркфлоу)
- [Шаблонный неймспейс `${snapshot.*}`](#шаблонный-неймспейс-snapshot)
- [Воркфлоу переиспользуют ваши пользовательские команды](#воркфлоу-переиспользуют-ваши-пользовательские-команды)
- [Варианты — альтернативные списки шагов](#варианты--альтернативные-списки-шагов)
- [`require_matching_config` и `config_hash`](#require_matching_config-и-config_hash)
- [Политика `services_mismatch`](#политика-services_mismatch)
- [`local_yml.preserve_keys` — сохранить машинно-локальные оверрайды](#local_ymlpreserve_keys--сохранить-машинно-локальные-оверрайды)
- [`pack.exclude` — держать эфемерное вне tarball-ов](#packexclude--держать-эфемерное-вне-tarball-ов)
- [`rollback_target` — откат одной командой](#rollback_target--откат-одной-командой)
- [`remove:` — подчистка внешних ресурсов](#remove--подчистка-внешних-ресурсов)

## Минимальный воркфлоу

Однострочного `snapshot.yml` достаточно, чтобы захватить и восстановить базу:

```yaml
# workspace/snapshot.yml
create:
  description: Capture the main DB
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }

restore:
  description: Restore the main DB
  steps:
    - command: db.restore
      with: { in: ${snapshot.path}/db/main.sql.gz }
```

`db.dump` и `db.restore` — это *ваши собственные* пользовательские команды; DWE не отгружает database-бэкенд (см. [author-project-commands.md](author-project-commands.md), если вы их ещё не написали). Подсистема снапшотов просто оркестрирует: берёт project-локи, создаёт `./snapshots/<имя>/`, запускает ваши `create`-шаги с разрешённым `${snapshot.path}` и затем пишет манифест.

Справочник: [`../reference/config/snapshot.md`](../reference/config/snapshot.md).

## Шаблонный неймспейс `${snapshot.*}`

`${snapshot.path}` — это абсолютный путь к `./snapshots/<имя>/`; именно туда ваш `create`-воркфлоу пишет артефакты и откуда `restore`-воркфлоу их читает. Воркфлоу ожидается, что пишут под этот путь; симлинки, положенные внутрь каталога снапшота, отвергаются на пост-create-сканировании.

Полный неймспейс:

| Переменная | `create` | `restore` / `remove` |
|---|---|---|
| `${snapshot.name}` | ✓ | ✓ |
| `${snapshot.path}` | ✓ | ✓ |
| `${snapshot.description}` | ✓ | ✓ |
| `${snapshot.variant}` | ✓ | ✓ |
| `${snapshot.created_at}` | ошибка (ещё не существует) | ✓ |

Вне снапшот-воркфлоу `${snapshot.*}` — это compile-time ошибка: scope-гейт проверяется до рендера шаблона. Случайно прочесть `${snapshot.path}` из обычной команды `db.dump`, вызванной вне снапшота, нельзя; та же `db.dump` подхватывает `${snapshot.path}`, только когда её вызвали *через* блок `with:` снапшот-воркфлоу.

## Воркфлоу переиспользуют ваши пользовательские команды

Шаги снапшот-воркфлоу — та же форма `WorkflowStep`, что и у пользовательских команд `type: workflow`: `command:`, `with:`, `when:`, `confirm:`, `parallel:`, `continue_on_error:`. Никакого специального синтаксиса для снапшотов и никакой отдельной модели исполнения: каждая фича, которую вы встроили в пользовательские команды (params, валидация, уведомления), переносится.

Это значит, что паттерн мульти-стора снапшота — это просто *больше шагов, вызывающих больше команд*:

```yaml
create:
  description: Capture full env
  steps:
    - command: db.dump
      with: { out: ${snapshot.path}/db/main.sql.gz }
    - command: opensearch.snapshot
      with: { out: ${snapshot.path}/search/index.tar }
    - command: redis.dump
      with: { out: ${snapshot.path}/redis/dump.rdb }
```

`restore:` повторяет ту же форму, вызывая соответствующие `*.restore` пользовательских команд. Условное восстановление — «только если файл есть» — это предикат `when:` на шаге:

```yaml
restore:
  steps:
    - command: opensearch.restore
      when: file-exists ${snapshot.path}/search/index.tar
      with: { in: ${snapshot.path}/search/index.tar }
```

Полный справочник workflow-шага — в [`../reference/config/commands/types.md`](../reference/config/commands/types.md#type-workflow).

## Варианты — альтернативные списки шагов

Workflow-блок может объявить именованные альтернативные списки шагов под `variants:`. Используйте их, когда «захватить всё» и «захватить только базу» должны сосуществовать без копипасты родителя:

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
    with-search:
      description: Capture DB + search
      steps:
        - command: db.dump
          with: { out: ${snapshot.path}/db/main.sql.gz }
        - command: opensearch.snapshot
          with: { out: ${snapshot.path}/search/index.tar }
```

Выбор варианта на create:

```shell
dwe snapshot create wip-x                # дефолтный блок
dwe snapshot create wip-x --using=db-only
```

Выбранный вариант фиксируется в манифесте снапшота, так что `dwe snapshot restore wip-x` автоматически берёт `restore.variants.db-only`, если он есть. Если соответствующего restore-варианта нет, restore фолбэкается на дефолтный блок `restore:` — полезный дефолт, когда асимметричный случай — это «захватить меньше, восстановить тем же способом».

Имена вариантов должны матчить `[a-z0-9][a-z0-9._-]{0,30}`. Запрос несуществующего варианта на `create` падает до любой мутации файловой системы, так что нельзя создать наполовину сломанный снапшот.

## `require_matching_config` и `config_hash`

Каждый снапшот фиксирует `config_hash` проекта на момент создания (дайджест над разрешённым deploy-конфигом). На restore DWE сравнивает его с хешем текущего deploy-стейта. По умолчанию несовпадение — мягкое предупреждение в stderr.

Поднимите строгий бит, когда восстановление против несовпадающего конфига было бы активно небезопасно — например, если ваш `db.restore`-воркфлоу опирается на конкретную версию схемы:

```yaml
require_matching_config: true
```

При строгом матче restore прерывается (exit 1) при расходящихся хешах. Пустой `config_hash` манифеста — снапшот создан до того, как прошёл хоть один деплой — трактуется как совпадение и никогда не блокирует.

Несовпадение `config_hash` на практике означает одно из:

- снапшот сделан на другой ветке с другим содержимым `workspace/deploy.yml` или `service.yml`,
- снапшот коллеги сделан против другой версии проектного конфига,
- проект был перебазирован вперёд/назад через коммит, меняющий deploy-конфиг.

Пара `require_matching_config: true` с `dwe snapshot inspect <name>` (показывает зафиксированный хеш) помогает разобраться с расхождением.

## Политика `services_mismatch`

Снапшоты фиксируют эффективный набор сервисов (имя + флаг `enabled`, отсортированные по имени) на момент создания. На restore этот набор диффится против текущего эффективного набора проекта. Блок политики решает, что делать, когда диф непустой:

```yaml
services_mismatch:
  policy: warn          # warn (по умолчанию) | block | ignore
```

| Политика | Поведение |
|---|---|
| `warn` (по умолчанию) | Restore продолжается. Диф рендерится в confirm-промте; с `-y` идёт в stderr, restore продолжается. |
| `block` | Любой непустой диф прерывает до того, как тронуть `workspace/local.yml` (exit 1). |
| `ignore` | Диф подавляется; restore продолжается молча. |

Диф группируется в три бакета — `только в снапшоте`, `только локально`, `enabled расходится` — и та же группировка появляется в `dwe snapshot inspect` и валидаторе `snapshot.<name>.services_diff`. Берите `block`, когда потребитель снапшота — non-interactive (CI / скрипт) и молчаливое продолжение поверх несовпадения приведёт к downstream-падению, которое сложно диагностировать.

## `local_yml.preserve_keys` — сохранить машинно-локальные оверрайды

`workspace/local.yml` — часть снапшота. В основном это то, что нужно — восстановление снапшота должно восстановить локальные тоглы, которые у коллеги были на момент создания. Но машинно-локальные значения (порты, переназначенные, потому что `5432` локально занят; хосты, указывающие на приватный DNS) ездить не должны.

`local_yml.preserve_keys` — это список dot-путей, чьи **текущие локальные значения** переживают restore:

```yaml
local_yml:
  preserve_keys:
    - services.main.ports
    - services.db.ports
    - host.shell
```

- Dot-пути адресуют вложенные ключи маппингов. Array-индекс-сегменты (`services[0].ports`) не поддерживаются.
- Пути, отсутствующие с обеих сторон, — молчаливые no-op.
- Порядок и YAML-комментарии на нетронутых узлах сохраняются там, где их удерживает `yaml.v3`; flow/block-стиль и отступы могут нормализоваться при маршалинге.

На create перечисленные пути вырезаются из захваченного `local.yml`. На restore захваченный `local.yml` мерджится поверх текущей копии с preserved-ключами, вшитыми обратно из вашего живого файла. Когда снапшот не несёт `local.yml` вовсе, но у вас локально есть preserved-значения, записывается минимальный `local.yml`, содержащий только preserved-ключи.

Частые кандидаты: что-нибудь под `services.<name>.ports`, что-нибудь под `services.<name>.hosts`, пути к локальным dev-тулам, варьирующиеся по ОС.

## `pack.exclude` — держать эфемерное вне tarball-ов

`dwe snapshot pack <name>` производит один `./snapshots/<name>.tar.gz`. Если ваш `create`-воркфлоу роняет временные файлы, scratch-дампы или что-то, что вы не хотите отгружать коллеге — исключите через doublestar-globs:

```yaml
pack:
  exclude:
    - "**/*.tmp"
    - ".cache/**"
    - "**/*.log"
```

Паттерны вычисляются относительно директории снапшота. CLI-флаг `dwe snapshot pack --exclude=<glob>` **дополняет** этот список, не заменяет — так что exclude в snapshot.yml — это baseline, который можно расширять per invocation, а не дефолт, который можно перебить.

Архив контент-адресуем на unpack против `manifest.yml`. Исключённые файлы не появляются в манифесте и не входят в integrity-чек, так что исключение эфемерного вывода безопасно.

## `rollback_target` — откат одной командой

Если один снапшот — каноническое «безопасное» состояние (обычно `baseline`, снятый сразу после чистого деплоя), объявите его как rollback-цель:

```yaml
rollback_target: baseline
```

Тогда `dwe snapshot rollback` — это шорткат для `dwe snapshot restore baseline`. Команда явно падает, если целевого снапшота нет, а валидатор `snapshot.rollback_target_exists` даёт warning на `dwe validate snapshot`, когда `rollback_target` задан, но именованного снапшота на диске нет.

`baseline` — это просто конвенция; работает любое существующее имя снапшота. Конвенция окупается тем, что каждое руководство, runbook и привычка может ссылаться на «откат» однозначно. См. [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md#откат-одной-командой-через-rollback_target) о потребительском фрейминге.

## `remove:` — подчистка внешних ресурсов

`dwe snapshot remove <name>` удаляет `./snapshots/<name>/` с диска. Если снапшот также соответствует внешнему состоянию — объекты в S3-бакете, строки в metadata-таблице, тэг в registry — объявите воркфлоу `remove:`, который запускается до удаления каталога:

```yaml
remove:
  description: Drop external artifacts for this snapshot
  steps:
    - command: s3.remove
      with: { prefix: snapshots/${snapshot.name}/ }
    - command: registry.untag
      when: file-exists ${snapshot.path}/registry-tag
      with: { tag: "${snapshot.name}" }
```

`remove:` опционален. Без него `dwe snapshot remove` просто делает `os.RemoveAll(snapshotDir)` и сбрасывает current-указатель, если он ссылался на этот снапшот. С ним воркфлоу запускается сначала с видимостью `${snapshot.*}` в `restore`-скоупе (так что `${snapshot.created_at}` доступен), затем директория удаляется. Падение воркфлоу прерывает удаление — директория остаётся на месте, чтобы вы могли разобраться и перезапустить.

Пара `remove:` с `pack.exclude` подходит, когда вы отгружаете артефакты, *на которые ссылаются* внешние системы: локальный файл — это маркер, который воркфлоу читает, чтобы решить, что подчищать удалённо.

## См. также

- [switching-tasks-with-snapshots.md](switching-tasks-with-snapshots.md) — потребительская сторона: когда создать, восстановить, откатить, упаковать
- [`../reference/config/snapshot.md`](../reference/config/snapshot.md) — полный справочник `snapshot.yml`
- [`../reference/config/commands/types.md`](../reference/config/commands/types.md#type-workflow) — форма workflow-шага, переиспользуемая блоками снапшота
- [author-project-commands.md](author-project-commands.md) — авторство команд `db.dump` / `db.restore`, которые вызывают снапшот-воркфлоу
