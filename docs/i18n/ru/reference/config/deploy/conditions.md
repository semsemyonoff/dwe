> Translated from: reference/config/deploy/conditions.md @ f49d1ee3614f

# Условия и проверки

`when:`, `check:` и `files_gate:` — три директивы, которые гейтят шаги и фазы пайплайна. Они используют типизированную форму условия/действия — полный каталог предикатов и действий см. в [Условия и действия](../conditions.md); эта страница описывает семантику интеграции с пайплайном деплоя.

## Содержание

- [`when:` (предусловие)](#when-предусловие)
- [`check:` (постусловие)](#check-постусловие)
- [`files_gate:` (предусловие по файлам)](#files_gate-предусловие-по-файлам)

## `when:` (предусловие)

`when:` — **предусловие**, вычисляемое до запуска фазы или шага. Ложный результат пропускает фазу/шаг без выполнения. Это **типизированное условие** в трёх формах:

**Билтин-предикаты** — проверяют состояние файловой системы, используя реестр предикатов:

```yaml
when:
  type: builtin
  cmd: "dir-empty services/main/src"
```

Доступные предикаты: `dir-exists`, `dir-missing`, `dir-empty`, `dir-not-empty`, `file-exists`, `file-missing`. Они отличаются от *билтинов движка* (`service_configs_copy` и т. д.), используемых в телах шагов и действиях `check:`; полное различие см. в [conditions.md](../conditions.md). Реестр предикатов использует жёстко заданный `sh -c` для POSIX-переносимости независимо от настроенного в проекте shell.

**Shell-команды** — выполняют shell-команду; код выхода 0 = true, ненулевой = false:

```yaml
when:
  type: shell
  cmd: "test -f services/main/src/vendor/autoload.php"
```

Shell-команды также используют жёстко заданный `sh -c` (а не `ShellBin`) для переносимости.

**Template-выражения** — синтаксис Go template, вычисляемый на этапе плана на объединённом `DevboxConfig`:

```yaml
when:
  type: template
  expr: "{{ .Services.second.Enabled }}"
```

Template-условия не поддерживают `check:` в том же шаге (нет побочных эффектов на этапе плана). Они предназначены только для проверок идемпотентности вида «пропустить эту фазу, если фича не включена», когда результат известен до выполнения. Полную поверхность шаблонов (helpers, sprout registries, `appURL`) см. в [Шаблоны](../../templates.md).

## `check:` (постусловие)

`check:` — **пост-действие**, вычисляемое после успеха шага. Это **типизированное действие** — та же форма `type:` / `cmd:` / `with:`, что и у тел шагов, но его успех/падение определяет, рапортуется ли шаг как успешный или упавший.

Используйте `check:`, чтобы утверждать, что шаг произвёл задуманный эффект — например, что миграция произвела определённый файл, что сервис стал доступен или что конфиги успешно скопированы.

**Пример: проверить, что конфиги развёрнуты**

```yaml
- name: copy-configs
  type: builtin
  cmd: service_configs_copy
  with:
    service: main
    mode: replace
  check:
    type: builtin
    cmd: service_configs_check
    with:
      service: main
```

**Пример: проверить, что shell-команда производит ожидаемый вывод**

```yaml
- name: run-migration
  type: command
  cmd: services.main.migrate
  check:
    type: shell
    cmd: "test -f services/main/src/migrations/.done"
```

**Поведение `continue_on_error` совместно с `check:`:**

- Когда тело шага падает и установлено `continue_on_error: true`, `check:` **не** вычисляется. Шаг рапортуется как упавший, а пайплайн продолжается.
- Когда тело шага успешно, но `check:` падает, шаг рапортуется как упавший. Если `continue_on_error: true`, пайплайн продолжается; иначе прерывается.

## `files_gate:` (предусловие по файлам)

`files_gate:` пробирует **существование или отсутствие** файлов перед запуском шага. В отличие от `when:` (общего предиката) или `check:` (валидирующего после успеха), `files_gate:` решает, запускать ли, на основе **того же блока `files:`, объявленного в определении команды**, делая файловую спецификацию команды единственным источником истины.

**Кейс:** пропустить шаг деплоя, если артефакт уже существует, или запустить его только при наличии предзагруженного кеша. Пример: «снять дамп БД только если файла дампа ещё нет» (продьюсер с `state: missing`) или «загрузить кеш только если он предзагружен» (консьюмер с `state: readable`).

**Короткая форма:**

```yaml
- name: db-dump
  type: command
  cmd: services.main.db.dump
  files_gate: readable              # runs iff dump file exists
```

**Длинная форма:**

```yaml
- name: db-load
  type: command
  cmd: services.main.db.load
  files_gate:
    state: missing                  # required: runs iff dump file does NOT exist
    command: services.main.db.dump  # optional: target command (default: step.cmd)
    require: all                    # optional: which files to probe (default: required)
    with:                           # optional: params for file resolution (default: step.with)
      database: test_db
```

**Справочник полей:**

| Поле | Тип | По умолчанию | Описание |
|------|-----|--------------|----------|
| `state` | `readable` \| `missing` | (обязательно) | `readable`: запускается тогда и только тогда, когда **все** выбранные файлы разрешаются (файл существует). `missing`: запускается, когда **ни один** не разрешается. |
| `command` | string | `step.cmd` | ID целевой команды, чей блок `files:` пробится. Если не указан, используется собственный `cmd` шага (самопроба). |
| `require` | string \| list | `required` | Какие файлы участвуют в пробе: `required` (файлы с `required: true` или `read_write`), `all` (все читаемые файлы) или явный список `[id1, id2]`. |
| `with` | mapping | `step.with` | Переопределения параметров для шаблонов разрешения файлов. Сливаются с `step.with` для целевой команды. |

**Семантика:**

- **Нет ошибок файлов → пропуск**, не падение. Если `state: readable` и ни один файл не совпал, шаг пропускается (не падает). Конфигурационные ошибки (плохой шаблон, плохой glob, отсутствующие параметры) приводят к ошибке и падению шага.
- **И-объединён с `when:`** — оба должны быть выполнены, чтобы шаг запустился. Если `when:` ложен, гейт никогда не вычисляется (короткое замыкание). Если `when:` истинен, но гейт не удовлетворён, шаг пропускается.
- **Взаимодействие с пропуском по журналу (асимметричное по `state:`)** — взаимодействие гейта с оптимизацией пропуска «уже задеплоено» зависит от `state:`:
  - `state: missing` (паттерн продьюсера) **обходит пропуск по журналу**. Гейт сам решает, запускать ли шаг, на каждом деплое. Шаг-продьюсер с `state: missing` перезапускается после удаления его артефакта между деплоями, потому что источником истины является состояние файловой системы — а не журнал.
  - `state: readable` (паттерн консьюмера) **уважает пропуск по журналу**. Журнал учитывается первым; если он зафиксировал успешный запуск, шаг пропускается без проверки гейта. Гейт фактически срабатывает только на первом запуске, после чего нагрузку несёт журнал. Это сохраняет идемпотентность деструктивных консьюмеров (например, drop + restore) по умолчанию. Чтобы принудительно переоценивать на каждом запуске, добавьте явную директиву `check:` — тот же рычаг, что и у любого другого шага.
  - Шаги без гейта пропускаются по журналу как раньше.

  Добавление или изменение директивы `files_gate:` инвалидирует записанный хеш шага, поэтому следующий запуск переоценит с нуля независимо от `state:`.
- **Область пробы** — участвуют только файлы с `access: read` или `access: read_write`. Файлы с `access: write` отклоняются на этапе валидации плана, если перечислены в спецификации `require:` гейта.

**Пример «до и после»:**

*Без `files_gate:` — дублированная логика glob+regex:*

```yaml
# Deploy step: hard-coded shell condition duplicating the command's file logic
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  when:
    type: shell
    cmd: "test -f services/main/.backups/dump_*.sql.gz"  # duplicated glob logic
```

*С `files_gate:` — единый источник истины:*

```yaml
# Deploy step: references the command's canonical file spec
- name: dump-download
  type: command
  cmd: services.main.db.dump-download
  files_gate: readable                # probes the dump_*.sql.gz from command definition
```

Определение команды один раз:

```yaml
# devbox/commands/services/main/db.yml
commands:
  dump-download:
    type: shell
    files:
      dump:
        access: read
        candidates:
          - glob: "services/main/.backups/dump_*.sql.gz"
            sort: modtime_desc
        required: true
```
