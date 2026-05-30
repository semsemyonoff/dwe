> Translated from: reference/concepts/pipelines.md @ cddeb0a50edc

# Пайплайны

Модель выполнения phase → step → condition, которую разделяют `devbox deploy`, `devbox run`, `devbox stop` и `devbox reset`. Одна грамматика, один runner, один журнал.

## Содержание

- [Одна грамматика, три команды](#одна-грамматика-три-команды)
- [Фазы и шаги](#фазы-и-шаги)
- [Типы шагов](#типы-шагов)
- [Условия: `when:`, `check:`, `files_gate:`](#условия-when-check-files_gate)
- [Поток выполнения шага](#поток-выполнения-шага)
- [Параллельные группы шагов](#параллельные-группы-шагов)
- [Sub-step переопределения на целях workflow](#sub-step-переопределения-на-целях-workflow)
- [Что читать дальше](#что-читать-дальше)

## Одна грамматика, три команды

Пять пользовательских команд запускают пайплайн:

| Команда | Файл | Дефолт при отсутствии |
|---------|------|---------------------|
| `devbox deploy run` | `devbox/deploy.yml` + per-service `devbox/services/<svc>/deploy.yml` | Встроенный пайплайн `services → start → post-deploy`. |
| `devbox run` | `lifecycle.yml` → `run:` | Встроенная фаза `start`, вызывающая `docker up --wait`. |
| `devbox stop` | `lifecycle.yml` → `stop:` | Встроенная фаза `stop`, вызывающая `docker down`. |
| `devbox restart` | `lifecycle.yml` (обе секции) | Запускает `stop`, потом `run --no-update`. |
| `devbox reset run` | `devbox/reset.yml` (+ per-service `reset.yml`) | Встроенный пайплайн `pre → stop → cleanup`. |

Каждый файл объявляет одну и ту же форму — упорядоченный список фаз, каждая держит упорядоченный список шагов. Runner в `internal/core/execution/pipeline/` не знает, какая команда его вызвала. Поэтому per-service deploy-файлы, deploy-файл оркестратора, блоки `run:` / `stop:` в lifecycle и reset-файл принимают одну и ту же грамматику шагов.

Два расширения грамматики ограничены командой:

- **`deploy_services: true`** на фазе — это маркер оркестратора, инлайнящий per-service deploy-пайплайны. Допустимо только в `devbox/deploy.yml` — отвергается на загрузке в `lifecycle.yml` и `reset.yml`.
- **`after:`** на верхнем уровне per-service `deploy.yml` объявляет deploy-time порядок между сервисами. Отвергается в файле оркестратора, в `reset.yml` и в per-service `reset.yml`.

Всё остальное — `when:`, `check:`, `files_gate:`, `continue_on_error:`, `skip_confirm:`, `untracked:`, `parallel:` — общее.

## Фазы и шаги

Фаза группирует шаги, логически принадлежащие вместе: `pre`, `start`, `services`, `post-deploy`, `cleanup`. Фазы упорядочены; падение внутри фазы прерывает фазу и пропускает всё после неё (если только упавший шаг не выбрал `continue_on_error: true`).

Шаг — это листовая единица работы. Каждый шаг, реально выполняющийся, объявляет `type:` плюс payload `cmd:`. Счётчик шагов `[N/M]` в подвале репортёра перечисляет листовые шаги по всему пайплайну.

Три ручки регулируют, как фаза или шаг показываются:

- **`untracked: true`** на фазе или шаге исключает её/его из счётчика `[N/M]` и подавляет строки start/done. Падения всё равно всплывают. Полезно для info-выдач `post-deploy` и для учёта `_auto_reap_daemons`, который пайплайн stop добавляет автоматически.
- **`continue_on_error: true`** превращает падение из «прервать» в «доложить и идти дальше». Следующий шаг запускается как обычно. Используйте это на опциональных фазах pre/post-хуков; никогда на основных lifecycle-шагах, которые должны успешно завершаться.
- **`skip_confirm: true`** пробрасывает `--yes` в один шаг независимо от пайплайн-флага. ORится с пайплайн-флагом и (в параллельных группах) с групповым флагом — монотонно, никогда не снимается.

## Типы шагов

Поле `type:` выбирает runner:

| `type:` | Runner | Применение |
|---------|--------|----------|
| `shell` | `sh -c` через `config.ShellBin` | Inline shell с полной shell-семантикой (globs, pipes, redirection). |
| `devbox` | Рекурсивный вызов в бинарник devbox | Compose passthrough'и (`docker up`, `docker down`), render-команды, `info`, всё, доступное как devbox-подкоманда. |
| `command` | Декларативная команда из `devbox/commands/` | Workflow / service-exec / service-run / builtin / daemon / script / shell / devbox команды, объявленные в реестре. |
| `builtin` | Внутренняя Go-функция движка | In-process действие с доступом к смерженному конфигу. Тот же реестр, что используют user-команды. |

Два коротких имени выглядят похоже — они не одно и то же:

- **`type: shell` шаг** запускает ваш `cmd:` через настроенный shell проекта (`config.ShellBin`). Проект может закрепить `zsh`, и тело шага использует `zsh`.
- **`cmd: shell` builtin** запускает значение `with.cmd:` через жёстко закодированный `sh -c` для POSIX-портативности. Используется внутри `when:` предикатов и внутри `check:` действий, где портативность через CI важнее, чем паритет функций.

`when:` предикаты всегда используют жёстко закодированный `sh` для портативности. `check:` действия могут использовать любой: выбирайте `cmd: shell` (builtin) для портативных утверждений, выбирайте `type: shell` (step-shape действие), когда вам нужны функции shell проекта.

Полный справочник по типам: [`config/deploy/steps.md`](../config/deploy/steps.md). Каталог билтинов: [`config/deploy/builtins.md`](../config/deploy/builtins.md).

## Условия: `when:`, `check:`, `files_gate:`

Три директивы стробируют шаг:

- **`when:`** — предусловие. Вычисляется до запуска тела. Falsy → шаг пропущен (не упал). Типизировано как `builtin` (предикаты файловой системы вроде `dir-empty`), `shell` (жёстко закодированный `sh -c`) или `template` (Go-шаблон против смерженного `DevboxConfig`).
- **`check:`** — постусловие. Вычисляется после успешного завершения тела. Падение → шаг репортится как упавший. Та же `type:` форма, что у тела шага (`shell` / `devbox` / `command` / `builtin`), но его возвращаемое значение стробирует статус успеха шага, а не производит видимый пользователю вывод.
- **`files_gate:`** — гейт по file-spec. Ссылается на объявленный блок `files:` команды. `state: readable` запускает шаг, если только все пробуемые файлы существуют; `state: missing` запускает шаг, если только никаких не существует.

Две взаимодействия важны:

- `when:` и `files_gate:` ANDятся. Если `when:` ложно, гейт никогда не вычисляется.
- `files_gate:` асимметрично взаимодействует с журналом deploy-состояния. `state: missing` обходит journal-skip (producer-паттерн — перезапускается, когда артефакта нет). `state: readable` уважает journal-skip (consumer-паттерн — запускается один раз, потом журнал несёт нагрузку). Добавление или изменение `files_gate:` инвалидирует записанный хэш шага, так что следующий запуск переоценивает с нуля.

Каталог условий (предикаты, действия, шаблонные хелперы) живёт в [`config/conditions.md`](../config/conditions.md). Deploy-сторонняя семантика — в [`config/deploy/conditions.md`](../config/deploy/conditions.md).

## Поток выполнения шага

Как только фаза стартует и её фазовый `when:` проходит, каждый шаг внутри фазы проходит ту же последовательность:

```mermaid
flowchart TD
  START["Шаг начинается"] --> WHEN{"step-level when:<br/>(если объявлено)"}
  WHEN -- false --> SKIPW["Пометить Skipped<br/>журнал: skipped"]
  WHEN -- true --> GATE{"files_gate:<br/>(если объявлено)"}

  GATE -- "state: missing<br/>артефакт есть" --> SKIPG["Пометить Skipped<br/>журнал: skipped"]
  GATE -- "state: readable<br/>артефакт отсутствует" --> SKIPG
  GATE -- удовлетворено --> JOURNAL{"journal skip?<br/>(только deploy,<br/>совпадение хэша)"}
  GATE -- не объявлено --> JOURNAL

  JOURNAL -- skip --> SKIPJ["Пометить Done из журнала<br/>(check: всё равно перезапускается)"]
  JOURNAL -- run --> BODY["Запустить тело шага<br/>(type-specific runner)"]

  BODY -- ошибка --> FAIL{"continue_on_error?"}
  FAIL -- false --> ABORT["Пометить Failed<br/>прервать пайплайн"]
  FAIL -- true --> CONTERR["Пометить Failed<br/>продолжить со следующего"]

  BODY -- ok --> CHECK{"check:<br/>(если объявлено)"}
  CHECK -- не объявлено --> OK["Пометить Done<br/>журнал: ok + hash"]
  CHECK -- ok --> OK
  CHECK -- ошибка --> FAIL
```

Три вещи, которые стоит заметить на диаграмме:

- Решение о journal-skip принадлежит только `devbox deploy run`. `devbox run` / `stop` / `restart` и `devbox reset run` выполняют каждый достижимый шаг на каждом вызове — нет оптимизации «это уже запускалось» вне deploy.
- `check:` пропускается, когда `continue_on_error: true` и тело упало. Он также переоценивается, когда журнал иначе пропустил бы шаг — это то, что держит `check:` честным как утверждение идемпотентности.
- Шаг, пропущенный по журналу, всё равно занимает один слот в `[N/M]` и одну строку в живом репортёре; он просто немедленно оседает как `◎ Skipped (cached)`.

## Параллельные группы шагов

Шаг может объявить блок `parallel:` вместо листового тела. Runner порождает каждый sub-step в своей горутине через `errgroup.WithContext`, ограниченный семафором:

```yaml
steps:
  - name: db-dumps
    skip_confirm: true            # OR-мержится в каждый sub-step
    parallel:
      max_concurrent: 4           # дефолт min(NumCPU, len(steps))
      fail_fast: true             # дефолт true
      steps:                      # обязательно, >= 2
        - name: download-main
          type: command
          cmd: services.main.db.dump-download
        - name: download-stock
          type: command
          cmd: services.stock.db.dump-download
        - name: download-price
          type: command
          cmd: services.price.db.dump-download
```

Пять правил, вытекающих из дизайна:

- **Плоские группы.** Sub-step не может сам объявить блок `parallel:`. Если вам нужен DAG, разбейте его по фазам.
- **Group-level `when:`** вычисляется один раз до запуска sub-step'ов. Собственный `when:` каждого sub-step всё равно запускается внутри горутины.
- **`fail_fast: true`** отменяет соседей через родительский `context.Context`. `exec.CommandContext` шлёт SIGTERM, потом SIGKILL после 5-секундной `WaitDelay`. Никаких сирот `docker compose` или `sleep` после чистого прерывания.
- **Per-sub-step журналирование.** Журнал deploy записывает каждый sub-step под `(phase, sub-step.name)`. Перестановка или добавление sub-step'ов не инвалидирует соседей, потому что `journal.StepHash` вычисляется только из sub-step.
- **Никакого PTY в sub-step'ах.** Дача потомку PTY, когда stdin — пустой reader, ломает `docker compose exec/run`. Используйте последовательный шаг, когда нужна интерактивная консоль.

Репортёр заменяет подвал LiveLine на LiveBlock на время параллельной группы, с одной строкой на sub-step, пайплайн-индексом `[N/M]` на строку и frame-aware парсером, нормализующим `\r` progress-строки в один фрейм на видимую строку. Полный вывод идёт в `.devbox/logs/parallel/<pipeline>/<group>/<sub>.log`.

Полная схема, дефолты, правила валидации и семантика выполнения: [`config/deploy/examples.md → Parallel step groups`](../config/deploy/examples.md#parallel-step-groups).

## Sub-step переопределения на целях workflow

Шаг `type: command`, нацеленный на workflow, может прикрепить per-sub-step стробирование к этому workflow без модификации определения workflow:

```yaml
- name: db-dumps-deploy
  type: command
  cmd: services.main.db.dumps-deploy   # workflow с parallel-блоком
  skip_confirm: true
  sub_step_overrides:
    deploy-main:
      files_gate:
        state: readable
        command: services.main.db.dump-deploy
    deploy-stock:
      files_gate:
        state: readable
        command: services.main.db.dump-deploy
        with: { database: "${db.stock_database}" }
```

Workflow остаётся непрозрачным и переиспользуемым; решение о стробировании принадлежит шагу пайплайна, который его вызвал. Переопределения применяются только когда workflow вызван через породивший шаг; тот же workflow, вызванный ad-hoc (`devbox commands run …`) или как sub-step другого workflow, запускается как написано. В v1 переопределим только `files_gate`, и переопределения не могут нацеливаться на sub-step, чья команда сама является workflow.

Это канонический ответ на «я хочу per-element стробирование в workflow без форка workflow в N вариантов».

## Что читать дальше

- [Справочник `deploy.yml` / `reset.yml`](../config/deploy/index.md) — поля верхнего уровня, поля фаз, поля шагов, идемпотентность и взаимодействие с журналом состояния.
- [Типы выполнения шагов](../config/deploy/steps.md) — `shell`, `devbox`, `command`, `builtin`; `cmd: shell` builtin против `type: shell` step.
- [Каталог условий](../config/conditions.md) — каждый предикат и типизированное действие, доступные для `when:` / `check:` / `files_gate:`.
- [`lifecycle.yml`](../config/lifecycle.md) — пайплайны `run:` / `stop:`, проба `run.update`, конвенции hook-фаз.
- [Reset](../config/reset.md) — общий и сервисный reset, всегда-включённая базовая линия, lifecycle pending-состояния.
- [Состояние и блокировки](state-and-locks.md) — как журнал deploy записывает хэши и решает, что пропустить, и как `deploy.lock` / `snapshot.lock` сериализуют конкурентные запуски.
