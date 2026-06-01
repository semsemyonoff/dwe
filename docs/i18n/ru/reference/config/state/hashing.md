> Translated from: reference/config/state/hashing.md @ e5639ee9d79e

# Хеширование и решения о пропуске

Как вычисляются `action_hash` и `config_hash`, когда они инвалидируют предыдущее состояние и как применяется таблица решений о пропуске.

Решения о пропуске определяются двумя видами хешей: `action_hash` (отпечаток шага) и `config_hash` (область конфигурации).

## action_hash

`action_hash` шага — это SHA-256 дайджест от:

```
sha256(type + "\x00" + cmd + "\x00" + canonical_json(with))
```

**Компоненты:**

- `type` — тип шага (`shell`, `dwe`, `command`, `builtin`)
- `cmd` — полезная нагрузка команды
- `with` — параметры шага, сериализованные как канонический JSON с ключами, отсортированными по алфавиту

**Ключевые свойства:**

- Если вы редактируете `type`, `cmd` или параметры `with:` шага, его хеш меняется → шаг выполняется при следующем деплое
- Изменения форматирования YAML, пробелов и комментариев НЕ меняют хеш (хеш вычисляется из разобранных Go-структур, а не из сырых YAML-байтов)
- Порядок ключей в `with:` не имеет значения (ключи сортируются при канонизации)
- Если `with:` отсутствует или равно nil, оно хешируется как пустой объект

**Примеры:**

```yaml
# Step 1: creates a database
- name: create-db
  type: command
  cmd: app.db.create
  with:
    host: localhost
    port: 3306

# On the next run, this step will skip (same hash)
# unless you edit type, cmd, or with parameters

# If you change with key order, hash stays the same:
- name: create-db
  type: command
  cmd: app.db.create
  with:
    port: 3306
    host: localhost  # reordered, hash unchanged

# If you change a parameter value, hash changes:
- name: create-db
  type: command
  cmd: app.db.create
  with:
    host: 127.0.0.1  # changed; step will re-run
    port: 3306
```

## config_hash для сервисов

`config_hash` сервиса покрывает две вещи:

```
sha256(canonical_json(services.<name>) + canonical_json(workspace/services/<name>/deploy.yml))
```

- Определение сервиса из `workspace/services/<name>/service.yml` (Enabled, Depends, Type, Dir и т. д.)
- Пайплайн деплоя для конкретного сервиса из `workspace/services/<name>/deploy.yml` (или пусто, если отсутствует)

Когда `config_hash` сервиса меняется (например, вы редактируете `workspace/services/main/service.yml` или `workspace/services/main/deploy.yml`), **все шаги во всех фазах этого сервиса считаются отсутствующими**. Они выполняются заново при следующем деплое независимо от их `action_hash`.

## config_hash для проекта

`config_hash` уровня проекта покрывает три вещи:

```
sha256(canonical_json(services[tracked_only]) + canonical_json(workspace/deploy.yml) + canonical_json(workspace/services/<tracked>/deploy.yml for all tracked services))
```

**«Отслеживаемый» означает:** сервис считается отслеживаемым тогда и только тогда, когда он появляется в разрешённом плане деплоя (то есть включён в `workspace/services/<name>/service.yml` И встроен в фазу с `deploy_services: true` в `workspace/deploy.yml`). Инструменты никогда не отслеживаются. Сервисы без `workspace/services/<name>/deploy.yml` всё равно отслеживаются, если они появляются в плане.

Когда `config_hash` проекта меняется (например, вы редактируете `workspace/deploy.yml` или добавляете сервис), **все шаги уровня проекта считаются отсутствующими** и выполняются заново при следующем деплое.

Примечание: правки включённых, но не отслеживаемых вариантов сервисов (например, сервис `main-debug`, расширяющий `main` без собственной конфигурации деплоя) НЕ меняют хеш проекта, поэтому они не инвалидируют журнал.

## Инвалидация хешей

Инвалидация происходит в **два уровня**:

1. **Валидация области сервиса** — прежде чем решить, следует ли пропустить шаг сервиса, проверяется: совпадает ли текущий `config_hash` сервиса с сохранённым? Если нет — предыдущее состояние шага считается отсутствующим.

2. **Валидация области проекта** — прежде чем решить, следует ли пропустить шаг уровня проекта, проверяется: совпадает ли текущий `config_hash` проекта с сохранённым? Если нет — предыдущее состояние шага считается отсутствующим.

Это гарантирует, что изменённая конфигурация сервиса не может привести к пропускам, даже если тела шагов не менялись.

## Таблица решений о пропуске

После того как валидация config-hash пройдена (или область не изменилась), предыдущий `StepState` шага оценивается по этой таблице:

| Прошлое состояние | Хеш совпадает | Есть `check:` | Решение |
|---|---|---|---|
| absent | — | — | **Run** |
| ok | yes | no | **Skip** |
| ok | yes | yes | **Run** (check re-validates) |
| ok | no | — | **Run** |
| failed / partial / in_progress | — | — | **Run** (resume) |

**Ключевая мысль:** шаги с действием `check:` **всегда выполняются**, даже если их хеш совпадает и предыдущий статус был ok. `check:` повторно проверяет, что предполагаемый эффект шага всё ещё присутствует (проверка идемпотентности). Это предотвращает ложные пропуски, когда внешнее состояние изменилось.

## См. также

- [Схема](schema.md) — где `action_hash` и `config_hash` хранятся в файле состояния
- [Управление](management.md) — флаги `--force` и `--resume`, которые переопределяют таблицу решений о пропуске
- [Обзор](index.md) — расположение файла, файл блокировки
