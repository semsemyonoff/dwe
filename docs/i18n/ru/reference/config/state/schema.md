> Translated from: reference/config/state/schema.md @ 4add20da2753

# Схема состояния

Справочник полей для `.dwe/deploy/state.yml`.

## Поля верхнего уровня

| Поле | Тип | Описание |
|-------|------|-------------|
| `schema_version` | string | Всегда `"1"`; зарезервировано для будущих изменений формата |
| `project` | object | Состояние уровня проекта (deployed_at, config_hash, status и т. д.) |
| `services` | map | Состояние по каждому сервису, индексируется именем папки сервиса (`workspace/services/<name>/`) |
| `pending` | object | Ожидающие операции, которые необходимо применить; присутствует только если `dwe services enable/disable` был запущен без `--apply`. Записывается атомарно командой переключения; очищается командами `dwe restart`, `dwe deploy run` или `dwe reset run` |

## Поля уровня проекта

| Поле | Тип | Описание |
|-------|------|-------------|
| `deployed_at` | ISO 8601 timestamp | Когда проект был последний раз полностью развёрнут |
| `config_hash` | sha256 hex | Отпечаток отслеживаемых сервисов + конфигурации деплоя верхнего уровня + конфигураций деплоя по каждому сервису. Правки включённых, но не отслеживаемых вариантов сервисов (например, `main-debug`) не меняют этот хеш. |
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed`, `in_progress` |
| `last_run` | object | Время и результат последней попытки деплоя (`status`, `started_at`, `finished_at`) |
| `phases` | map | Состояние по каждой фазе для фаз уровня проекта (не сервисных) |

## Поля сервиса

| Поле | Тип | Описание |
|-------|------|-------------|
| `status` | enum | `deployed`, `partial`, `failed`, `not_deployed` (сервис никогда не запускался или все шаги были пропущены) |
| `deployed_at` | ISO 8601 timestamp | Когда этот сервис был последний раз полностью развёрнут |
| `config_hash` | sha256 hex | Отпечаток `workspace/services/<name>/service.yml` + `workspace/services/<name>/deploy.yml` |
| `last_run` | object | Время и результат последней попытки деплоя для этого сервиса |
| `phases` | map | Состояние по каждой фазе этого сервиса |

## Поля фазы

| Поле | Тип | Описание |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `skipped` |
| `steps` | map | Состояние по каждому шагу, индексируется именем шага |

## Поля шага

| Поле | Тип | Описание |
|-------|------|-------------|
| `status` | enum | `ok`, `failed`, `in_progress` |
| `finished_at` | ISO 8601 timestamp | Когда этот шаг завершился (отсутствует, если in_progress) |
| `action_hash` | sha256 hex | Отпечаток полей шага `type`, `cmd` и параметров `with:` |
| `duration_ms` | integer | Длительность выполнения шага в миллисекундах |

## Состояние pending

Когда `dwe services enable` или `dwe services disable` запускается без `--apply`, команда переключения сразу записывает изменение в local.yml, но откладывает шаг применения. Поле `pending` в файле состояния отслеживает, что ещё нужно выполнить.

### Схема поля pending

| Поле | Тип | Описание |
|-------|------|-------------|
| `operations` | list | Упорядоченный список ожидающих операций |
| `config_hash` | sha256 hex | Хеш конфигурации на момент переключения; используется для обнаружения устаревших pending-записей |
| `created_at` | ISO 8601 timestamp | Когда была записана pending-запись |

### Схема pending-операции

| Поле | Тип | Описание |
|-------|------|-------------|
| `kind` | string | `restart` (всему стеку) или `deploy` (для конкретного сервиса) |
| `services` | list | Для типа `deploy`: имена сервисов, которые нужно развернуть. Пусто для `restart`. |

### Жизненный цикл состояния pending

| Событие | Эффект для `pending` |
|-------|---------------------|
| `dwe services enable/disable` (без `--apply`) | Записывает `pending.operations`; добавляет/сливает операции для contributor-ов restart или deploy |
| Успешный `dwe services enable/disable --apply` | Очищает принадлежащие contributor-у pending-операции через `ClearPendingOps` |
| Успешный `dwe restart` | Очищает операцию `restart`; операция deploy (если есть) сохраняется |
| Успешный `dwe deploy run` (по всему проекту) | Очищает операцию `deploy`; операция restart (если есть) сохраняется |
| Успешный `dwe deploy run --service <name>` | Удаляет `<name>` из списка сервисов операции `deploy`; если список пуст, удаляет операцию |
| Успешный `dwe reset run` (по всему проекту) | Очищает все pending-операции (полная очистка журнала) |
| Успешный `dwe reset run --service <name>` | Атомарно записывает `{kind: deploy, services: [<name>]}` одновременно с удалением состояния развёртывания сервиса |

### Баннер

`dwe status` (и его подкоманды `apps`, `tools`, `infra`, `deploy`) отображают предупреждающий баннер, когда `pending` не равно nil:

```
⚠ Pending: deploy required for: svc-a, svc-b
  Run: dwe deploy run
⚠ Pending: restart required
  Run: dwe restart
```

Баннер рендерится функцией `render.PendingBanner(p *journal.PendingApply)` из `internal/core/ui/render/`. Она проходит по `pending.operations` и выводит по одной строке на операцию. Возвращается пустая строка (баннер не выводится), когда `pending` равно nil.

## См. также

- [Хеширование и решения о пропуске](hashing.md) — как вычисляются `action_hash` и `config_hash` и как они управляют таблицей решений о пропуске
- [Управление](management.md) — команды просмотра, очистки и восстановления
- [Обзор](index.md) — назначение, расположение файла, файл блокировки
