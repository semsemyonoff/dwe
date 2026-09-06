> Translated from: reference/config/reset.md @ eb8339e9a185

# Reset

Команда `dwe reset run` сносит проект или отдельный сервис и возвращает его в чистое состояние, после которого требуется повторный деплой.

## Reset всего проекта

```
dwe reset run [--yes] [--skip-preflight] [--clear-generated]
```

Выполняет `workspace/reset.yml`. Файл **опционален** — если он отсутствует, DWE использует встроенный reset-пайплайн по умолчанию и печатает одну info-строку в stderr: `Using built-in default reset pipeline (override with workspace/reset.yml).` Info-строка подавляется в режиме `--output json`.

**Reset-пайплайн по умолчанию** (срабатывает, если `workspace/reset.yml` отсутствует):

Фазы: `pre` (confirm-промпт: "This will stop containers, remove project volumes, and delete generated data.") → `stop` (`type: dwe`, `cmd: "docker down"`) → `cleanup` (удалить все volume'ы проекта, затем удалить директорию `services/`). Удаление volume'ов устойчиво: отдельный volume, который не удалось удалить (например, всё ещё используется), логируется и пропускается — он **не** прерывает reset; при этом по-настоящему сломанная конфигурация (имя проекта не резолвится или падает `docker volume ls`) остаётся фатальной — reset прерывается, а не чистит журнал, оставив volume'ы.

При успехе журнал состояния деплоя удаляется целиком, так что каждый сервис в `dwe status` показывается как не задеплоенный.

| Опция | Описание |
|-------|----------|
| `--yes` / `-y` | Пропустить confirm-промпты внутри reset-шагов |
| `--skip-preflight` | Обойти environment-пробы и проверки проекта перед запуском |
| `--clear-generated` | Также очистить store собранных сгенерированных значений (`.dwe/generated.yml`), чтобы секреты перегенерировались при следующем деплое (по умолчанию сохраняются) |

## Per-service reset

```
dwe reset run --service <name> [--yes] [--skip-preflight] [--clear-generated]
```

Сбрасывает отдельный сервис, не затрагивая остальной проект:

1. Запускает preflight-проверки stop-стадии (бинари docker, git; проверка доступности порта для stop-стадии пропускается).
2. Показывает интерактивную форму подтверждения, перечисляющую ровно то, что произойдёт (пропускается с `--yes`).
3. Если сервис сейчас **enabled**, запускает все пользовательские команды `on_disable.before`, объявленные в `workspace/services/<name>/service.yml` (вне project-лока).
4. Захватывает project-лок, затем выполняет один пайплайн, состоящий из:
   a. **Baseline (всегда):** остановить **и удалить** контейнер сервиса напрямую через `docker stop` + `docker rm -f` (в обход compose — работает независимо от того, включён сервис или выключен).
   b. **Baseline (условно):** удалить директорию сервиса, если сервис объявляет `dir:` в `service.yml` и директория существует на диске.
   c. **Пользовательский пайплайн (опционально):** фазы, объявленные в `workspace/services/<name>/reset.yml`, если он есть, добавляются после baseline.
5. Атомарно убирает задеплоенное состояние сервиса из журнала и пишет запись `PendingDeploy`.
6. Освобождает лок.

После per-service reset'а `dwe status` показывает баннер pending-deploy для сервиса. Запустите `dwe deploy run --service <name>`, чтобы пере-провизионировать его.

**Volume'ы автоматически не трогаются.** Если нужно сбросить Docker-volume'ы сервиса как часть reset'а, объявите `services/<name>/reset.yml` с шагом, вызывающим [`docker_remove_project_volumes`](deploy/builtins.md#docker_remove_project_volumes).

### Требования

- Сервис должен существовать в `workspace/services/<name>/`.
- **У сервиса должен быть `workspace/services/<name>/deploy.yml`** — per-service reset пишет в журнал запись `PendingDeploy`, поэтому сервис должен быть деплоимым. Если `deploy.yml` нет, используйте полный `dwe reset run`.

Обязательные сервисы (`required: true`) **разрешены** для per-service reset'а (`required` защищает от `services disable`, а не от reset'а).

| Опция | Описание |
|-------|----------|
| `--service <name>` | Сбросить только этот сервис |
| `--yes` / `-y` | Пропустить confirm-промпт |
| `--skip-preflight` | Обойти environment-пробы перед запуском |
| `--clear-generated` | Также очистить собранные сгенерированные значения этого сервиса (`.dwe/generated.yml`); форсирует перегенерацию при следующем деплое |

### Per-service `reset.yml`

`workspace/services/<name>/reset.yml` следует тому же формату, что и общий `workspace/reset.yml`. Он **опционален** и добавляется после всегда включённого baseline'а (stop+rm контейнера, опциональное удаление `dir:`). Если отсутствует, выполняются только baseline и обновление журнала.

```yaml
# workspace/services/postgres/reset.yml
phases:
  - name: wipe
    steps:
      - name: drop-volume
        type: builtin
        cmd: docker_remove_project_volumes
```

### Жизненный цикл pending-состояния

| Команда | Эффект на журнал |
|---------|------------------|
| `dwe reset run --service <name>` | Удаляет `state.services.<name>`, пишет `PendingDeploy` для `<name>` |
| `dwe deploy run --service <name>` | Очищает `PendingDeploy` для `<name>` при успехе |
| `dwe reset run` (полный проект) | Удаляет весь файл состояния (`journal.Remove`) |

## Связанные команды

- `dwe reset plan` — показать разрешённый пайплайн сброса
- `dwe reset run [--yes]` — выполнить его (см. [Reset всего проекта](#reset-всего-проекта))
- `dwe reset eject [--out PATH] [--force]` — выдать **встроенный дефолтный** пайплайн сброса в виде закомментированного, готового к правке `reset.yml`. Это константа, а не эффективный план проекта: ничего не рендерится, файлы `workspace/services/<name>/reset.yml` не встраиваются, фильтра `--service` нет (для разрешённого экземпляра используйте `dwe reset plan`). Без `--out` (или с `--out -`) документ уходит в stdout и ничего не записывается; с `--out PATH` он пишется в этот файл и **отказывается перезаписывать существующий без `--force`**. Выданный файл объявляет `log: false` явно — как и встроенный дефолт. Неявного пути по умолчанию нет: канонический адресат `workspace/reset.yml` передаётся явно, а активный файл **заменяет** встроенный пайплайн целиком.
- `dwe validate` — когда он репортит `reset.yml has no active content (all comments or empty) — built-in default pipeline is active` (или `declares no phases`), ответом на это служит `dwe reset eject`: файл на диске ни на что не влияет, поэтому выдайте поверх него встроенный пайплайн через `--force` и правьте уже его. Отказ самого `eject` называет те же два условия, так что обе команды одинаково понимают, какие файлы инертны. См. [validate.md](validate.md).
- `dwe deploy eject` — двойник со стороны деплоя; см. [deploy/index.md](deploy/index.md).

Команды `lifecycle eject` намеренно **нет**. Эффективный пайплайн `stop` всегда
несёт синтетическую фазу движка `_auto_reap_daemons`, а пользовательская фаза с
именем на `_` отвергается при загрузке — выданный `lifecycle.yml` был бы файлом,
который сам dwe отказывается загружать.
