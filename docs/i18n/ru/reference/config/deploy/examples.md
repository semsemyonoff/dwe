> Translated from: reference/config/deploy/examples.md @ 9b73243f0382

# Примеры и паттерны

Разобранные примеры пайплайнов оркестратора и отдельных сервисов, поля упорядочивания `after:`, параллельных групп шагов и переопределений под-шагов на воркфлоу-целях. В конце — типичные ловушки.

## Содержание

- [Пример: пайплайн оркестратора](#пример-пайплайн-оркестратора)
- [Пример: пайплайн отдельного сервиса](#пример-пайплайн-отдельного-сервиса)
- [Пример: пайплайн infra-сервиса с `after:`](#пример-пайплайн-infra-сервиса-с-after)
- [Параллельные группы шагов](#параллельные-группы-шагов)
- [Прицеливание в под-шаги воркфлоу через переопределения](#прицеливание-в-под-шаги-воркфлоу-через-переопределения)
- [Типичные ловушки](#типичные-ловушки)

## Пример: пайплайн оркестратора

```yaml
# workspace/deploy.yml
phases:
  - name: services
    deploy_services: true
    description: Deploy all enabled services

  - name: start
    description: Start containers
    steps:
      - name: up
        type: dwe
        cmd: "docker up"
      - name: wait-healthy
        type: builtin
        cmd: docker_wait_healthy

  - name: post-deploy
    description: Post-deploy summary
    untracked: true
    steps:
      - name: info
        type: dwe
        cmd: "info"
      - name: success
        type: builtin
        cmd: message
        with:
          level: success
          text: Deploy completed successfully
```

## Пример: пайплайн отдельного сервиса

```yaml
# workspace/services/main/deploy.yml
phases:
  - name: setup
    description: Create dirs and install
    when:
      type: builtin
      cmd: "dir-empty services/main/src"
    steps:
      - name: create-dirs
        type: builtin
        cmd: service_dirs_ensure
        with:
          service: main
      - name: install
        type: command
        cmd: app.install
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

  - name: init
    description: Initialize application
    steps:
      - name: db-create
        type: command
        cmd: services.main.db.create
      - name: composer-install
        type: command
        cmd: services.main.composer-install
      - name: migrate
        type: command
        cmd: services.main.migrate

  - name: finalize
    description: Generate IDE config
    steps:
      - name: render-ide
        type: dwe
        cmd: "render ide main"
```

## Пример: пайплайн infra-сервиса с `after:`

Сервис любого типа может иметь пайплайн деплоя. Infra-сервис вроде MinIO может декларировать начальное создание бакетов и использовать `after:`, чтобы развернуться после приложения, чьи секреты он подготавливает:

```yaml
# workspace/services/minio/deploy.yml
after:
  - main  # deploy after the main app service

phases:
  - name: init
    description: Create MinIO buckets
    when:
      type: shell
      cmd: "mc alias ls local 2>/dev/null | grep -q local"
    steps:
      - name: create-bucket
        type: shell
        cmd: mc mb --ignore-existing local/uploads
```

## Параллельные группы шагов

Шаг может декларировать блок `parallel:` вместо листового тела (`type` + `cmd`). Оркестратор запускает внутренние под-шаги конкурентно через `errgroup` + семафор, ждёт их всех и агрегирует результаты. Та же модель применяется в `lifecycle.yml` и `reset.yml`.

```yaml
phases:
  - name: init
    steps:
      - name: db-dumps
        when: ...                  # optional; evaluated once before the group
        skip_confirm: true         # optional; OR-merged into every sub-step
        parallel:
          max_concurrent: 4        # optional; default = min(NumCPU, len(steps))
          fail_fast: true          # optional; default true
          steps:                   # required, >= 2
            - name: download-main
              type: command
              cmd: services.main.db.dump-download
              files_gate: { ... }
            - name: download-stock
              type: command
              cmd: services.stock.db.dump-download
            - name: download-price
              type: command
              cmd: services.price.db.dump-download
```

### Схема

Ключи уровня группы, допустимые на шаге с `parallel:`:

| Поле | Тип | Заметки |
|------|-----|---------|
| `name` | string | Обязательно. Имя группы (используется в выводе плана и заголовках репортера). |
| `description` | string | Опционально. |
| `when` | condition | Вычисляется **один раз** перед запуском под-шагов. При false вся группа пропускается, и каждый под-шаг рапортуется в журнале как пропущенный. |
| `skip_confirm` | bool | Объединяется по ИЛИ с каждым под-шагом на этапе разрешения. Собственный `skip_confirm: false` под-шага не может снять унаследованный true (монотонно). |
| `parallel.max_concurrent` | int | По умолчанию `min(runtime.NumCPU(), len(steps))`; ограничивается `len(steps)` при превышении. |
| `parallel.fail_fast` | bool (tristate) | По умолчанию `true`. При true первое падение под-шага отменяет соседей через `context`. При false все под-шаги дорабатывают до конца, а ошибки объединяются. |
| `parallel.steps` | []DeployStep | Обязательно, >= 2 записей. |

Листовые директивы **отклоняются** на групповом шаге: `type`, `cmd`, `with`, `check`, `files_gate`, `continue_on_error`. YAML-загрузчик возвращает ошибку строгого декодирования с именем нарушающего поля. Неизвестные поля внутри самого `parallel:` (например, опечатка `max_concurent`) тоже отклоняются.

### Значения по умолчанию и валидация

| Правило | Диагностика |
|---------|-------------|
| Вложенный `parallel:` (у под-шага свой `parallel:`) | ошибка — в v1 только плоские группы |
| `parallel.steps` < 2 | ошибка — используйте листовой шаг, если запись одна |
| У под-шага нет `name` | ошибка |
| Дубликаты имён под-шагов внутри фазы (группы + листовые шаги) | ошибка — журнал ключит записи по `(phase, name)` |
| Интерактивный промпт в под-шаге без `skip_confirm: true` (под-шага или унаследованного от группы) | ошибка — покрывает `confirmation: true` в целевой команде, `builtin: confirm` и воркфлоу, рекурсивно содержащие confirm-шаг |
| Под-шаг `service_run` с compose-аргументами, аллоцирующими TTY | предупреждение — TTY никогда не выделяется в параллельном режиме |

Валидация выполняется на `dwe validate` и на этапе разрешения плана; любой путь ловит misconfigurations до выполнения.

### Семантика исполнения

- **Отмена**: `errgroup.WithContext` пробрасывает родительский `context.Context` через каждый раннер (`HostRunner`, `DWERunner`, `ServiceExecRunner`, `ServiceRunRunner`, `ScriptRunner`, `WorkflowRunner`) и каждый билтин. Дочерние процессы запускаются с `exec.CommandContext` и привязаны к `cmd.Cancel = SIGTERM` + `cmd.WaitDelay = 5s` — при отмене ребёнок сначала получает SIGTERM (graceful shutdown), затем SIGKILL после задержки. Go-сторонний билтин `docker_wait_healthy` прерывает свой polling-цикл на `ctx.Done()` в течение одного тика.
- **SIGINT**: `RunWithOptions` устанавливает `signal.NotifyContext(SIGINT, SIGTERM)` на родительский контекст один раз на запуск пайплайна. Пользовательский Ctrl-C отменяет контекст, что распространяется на дочерние процессы каждого активного под-шага. Никаких осиротевших процессов `docker compose` / `sleep` после чистого завершения.
- **`fail_fast: true`**: первый упавший под-шаг (не считая тех, у кого `continue_on_error: true`) отменяет группу; оставшиеся под-шаги наблюдают `ctx.Done()`, и их дочерние процессы убиваются. Ошибка группы — первое падение, обёрнутое адресом своего под-шага.
- **`fail_fast: false`**: все под-шаги дорабатывают до конца. Ошибки оборачиваются по каждому под-шагу (`parallel sub-step "phase/group/sub": <err>`) и комбинируются через `errors.Join`.
- **Под-шаговые `when` / `files_gate` / пропуск по журналу**: каждый под-шаг по-прежнему проходит тот же пайплайн `step-when → (files_gate ↔ journal-skip) → ExecAction → check`. Взаимодействие `files_gate ↔ journal-skip` асимметрично: `state: missing` обходит journal-skip; `state: readable` и шаги без гейта сначала консультируются с journal-skip (см. [`files_gate:`](conditions.md)). Групповой `when` вычисляется **один раз**; per-sub-step `when` **также** вычисляется внутри goroutine.
- **Журнал**: каждый под-шаг журналируется независимо под `(phase, sub-step.Name)`. Сама группа не журналируется. `journal.StepHash(step)` вычисляется только по под-шагу, поэтому переупорядочивание или добавление под-шагов не инвалидирует соседей.

### Репортер и логирование

- **Live view (TTY)**: репортер ведёт sticky-футер `LiveLine` (`bubbles/v2/spinner` + пайплайновый секундомер `[<elapsed>]` + текст `[N/M] <step>`) для всего пайплайна и переключается на `LiveBlock`, пока активна параллельная группа. Каждая строка блока показывает `<spinner-or-final-glyph> [<sub-elapsed>] [<pipelineIdx>/<pipelineTotal>] <sub-name>`, при этом построчный спиннер заменяется ✓/✗/◎ (в зелёном/красном/жёлтом) при завершении под-шага. Пайплайн-индекс `[N/M]` в строках блока позволяет параллельным под-шагам вписаться в окружающий счётчик шагов, а не начинать с `[1/3]`. Live view рендерится через bubbles `Model.View()` плюс приватный `time.Ticker` — `tea.NewProgram` НЕ используется (поэтому терминал остаётся в cooked mode, Ctrl+C всё ещё поднимает SIGINT через VINTR, и не выводятся capability-запросы или kitty-keyboard последовательности).
- **Маршрутизация sequential vs parallel**:
  - Последовательные тела шагов приостанавливают LiveLine через `Reporter.SuspendForExec`, и дочерний процесс пишет в host-терминал напрямую (с PTY, когда stdout — TTY). Цвета, позиционирование курсора и интерактивный UX работают как в обычном shell; tee, обёрнутый `logSanitizer`, фиксирует ANSI-очищенную копию в on-disk лог. `ResumeAfterExec` перерисовывает футер после выхода ребёнка.
  - Параллельные под-шаги НЕ выделяют PTY — выдача ребёнку PTY при пустом stdin приводит к падению `docker compose exec/run` с «cannot attach stdin to a TTY-enabled container». Вывод под-шагов течёт через `ansiOnlyStripper` → `lineTee` → `Reporter.StepOutput`, чтобы строка блока показывала последний `\n`-фрейм; host-терминал принадлежит LiveBlock.
- **Frame-aware парсер**: `\r`-aware `lineTee` парсит поток каждого параллельного под-шага в коллбеки `(frame, final)`; на живой строке показывается только последний фрейм, а `\r`-фреймы нормализуются до одного-кадра-на-строку в лог-файлах через `logSanitizer` (ANSI вырезается, `\r\n` сворачивается в один `\n`, одиночный `\r` — в `\n`).
- **Политика дампа буфера**: при завершении под-шага буферизованный полный вывод под-шага воспроизводится между разделителями `───── output ─────`, если под-шаг упал или если логирование в файл не включено. При успехе в TTY с включённым логированием дамп подавляется, а вместо него выводится строка `Full log: <path>`. Non-TTY режим всегда дампит (и дампы чистые благодаря frame-парсеру).
- **Лог-файлы под-шагов**: `.dwe/logs/parallel/<pipeline>/<group>/<sub>.log` фиксирует полный вывод под-шага (ANSI вырезается, `\r`→`\n`). Глобальный пайплайновый лог (`.dwe/logs/<pipeline>.log`) получает каждую статусную строку и каждую закоммиченную дочернюю строку ровно один раз.
- **Семантика EndBlock**: когда параллельная группа завершается, `LiveLine.EndBlock` СТИРАЕТ живой футер, сидевший под блоком (иначе он застыл бы в scrollback с спиннером посреди кадра рядом с текстом последнего стартовавшего под-шага), и рисует на его месте свежий однолинейный футер. Финализированные строки блока (✓/✗/◎ + замороженный elapsed) сохраняются в scrollback.
- **Передача промпта**: каждый huh-промпт `widgets.Run*` стреляет пакетными хуками, зарегистрированными `NewPlainReporter` (`widgets.SetHuhHooks(live.Pause, live.Resume)`), так что футер стирается перед рендером промпта и перерисовывается после его возврата. Последовательные тела шагов используют те же `live.Pause`/`live.Resume` через `Reporter.SuspendForExec`/`ResumeAfterExec`.
- **Паритет non-TTY**: когда `term.IsTerminal(os.Stdout.Fd())` ложен (CI, piped stdout), live view полностью отключён (без тикера, курсорных последовательностей, футера), но frame-aware парсер всё равно включён, поэтому CI-дампы не имеют `\r`-спама.

### Вывод плана

`dwe deploy plan` рендерит параллельные группы непрерывным диапазоном индексов и отступами в строках под-шагов:

```
[12-14/25] [parallel group: db-dumps (3 steps, max_concurrent=3, fail_fast=true)]
  [12/25]  download-main      command services.main.db.dump-download [files_gate]
  [13/25]  download-stock     command services.stock.db.dump-download
  [14/25]  download-price     command services.price.db.dump-download
```

### Ограничения (v1)

- Нет вложенного `parallel:` внутри `parallel:`.
- Нет интерактивных подтверждений в под-шагах (`confirmation: true`, `builtin: confirm`, workflow с `WorkflowStep.Confirm`). Задайте `skip_confirm: true` или переструктурируйте.
- Нет DAG / `depends_on` между под-шагами. Только плоские группы.
- Нет флага автопараллелизации — только явный opt-in в YAML.
- Нет PTY в под-шагах. `service_run` с compose-аргументами стиля `-it` упадёт на ребёнке с «cannot allocate tty» (всплывает как обычное падение под-шага, подчиняясь `continue_on_error` / `fail_fast`).

## Прицеливание в под-шаги воркфлоу через переопределения

Шаг пайплайна, чей `type: command` целится в воркфлоу, может прикрепить **per-sub-step оркестрационные директивы** к этому воркфлоу без модификации самого воркфлоу. Это сохраняет `WorkflowStep` минимальным (только `command:` / `with:` / `confirm:` / `when:` / `continue_on_error:` / `parallel:`) и оставляет решения о гейтинге на стороне пайплайна, где им место.

### Схема

```yaml
phases:
  - name: deploy-dumps
    steps:
      - name: db-dumps-deploy
        type: command
        cmd: services.main.db.dumps-deploy   # a workflow with a parallel block
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
              with: { database: "${vars.db.stock_database}" }
          deploy-price:
            files_gate:
              state: readable
              command: services.main.db.dump-deploy
              with: { database: "${vars.db.price_database}" }
```

Целевой воркфлоу остаётся непрозрачным и переиспользуемым:

```yaml
# workspace/commands/services/main/db.yml
commands:
  dumps-deploy:
    type: workflow
    description: Restore all dumps in parallel
    steps:
      - parallel:
          steps:
            - name: deploy-main
              command: services.main.db.dump-deploy
            - name: deploy-stock
              command: services.main.db.dump-deploy-stock
            - name: deploy-price
              command: services.main.db.dump-deploy-price
```

### Разрешение

- Поиск под-шага использует `WorkflowStep.name`, если задан, иначе ссылочный `command`. Имена должны быть однозначны внутри целевого воркфлоу, когда ключ переопределения на них ссылается; коллизии отклоняются на этапе плана с `sub_step_overrides[<key>] is ambiguous`.
- Каждый ключ переопределения должен совпадать с листовым под-шагом (top-level Command-шаг или Command-лист внутри блока `parallel:` воркфлоу). Под-шаги, чья команда сама является воркфлоу, не адресуемы в v1 — переопределение должно достать non-workflow под-шаг.
- `files_gate:` внутри переопределения валидируется против целевой команды **под-шага** по тем же правилам, что и шаговый `files_gate:` (state, спецификация require, покрытие требуемых параметров через with/default-from).
- Переопределения применяются только при вызове воркфлоу из исходного шага пайплайна. Тот же воркфлоу, вызванный ad-hoc (`dwe commands run …`) или как под-шаг другого воркфлоу, исполняется как написан. Переопределения НЕ распространяются через вложенные вызовы воркфлоу.

### Рантайм-семантика

При исполнении воркфлоу каждый листовой под-шаг сопоставляется с `sub_step_overrides[<step-name>]`:

- **гейт удовлетворён** → под-шаг исполняется как обычно.
- **гейт не удовлетворён** → под-шаг **пропускается**, рапортуется как `Skipped: <command> (files_gate: <state> [<offending-id>…])` в stderr и в строке live-блока. Пропуски не валят воркфлоу.
- **ошибка вычисления гейта** (неизвестная команда, отсутствующий блок files:, плохая спецификация require) → под-шаг **падает** с обёрнутой ошибкой; действуют стандартные `continue_on_error` / `fail_fast`.

Собственный `when:` воркфлоу на под-шаге вычисляется первым; гейт переопределения учитывается только если `when:` истинен.

### Когда использовать это, а когда — шаговый `files_gate:`

| Ситуация | Что использовать |
|----------|------------------|
| Единственный non-workflow листовой шаг, чей запуск зависит от файла | шаговый `files_gate:` |
| Воркфлоу, оркеструющий несколько похожих под-шагов, и вы хотите гейтить per-sub-step из пайплайна | `sub_step_overrides:` |
| Хотите, чтобы воркфлоу громко падал, когда обязательный вход отсутствует при ad-hoc вызове | оставьте переопределения выключенными — `files: required: true` нижележащей команды это обеспечит |

### Ограничения (v1)

- Внутри переопределения поддерживается только `files_gate`. Будущие версии могут расширить это до `when:` и `continue_on_error:` на уровне переопределения.
- Переопределения не могут целиться в под-шаг, чья команда сама является воркфлоу. Отрефакторите внутренний воркфлоу, чтобы выставить лист, или передвиньте переопределение на уровень глубже, передекларировав шаг пайплайна против этого внутреннего воркфлоу.
- Ключи переопределений должны ссылаться на имена под-шагов, существующих в непосредственном воркфлоу. Валидация выполняется на `dwe validate` и на этапе разрешения плана.

## Типичные ловушки

- **Отсутствие `with:` для параметров билтина** — билтины требуют `with:` для параметров; передача их как top-level полей шага не работает.
- **`deploy_services` в `reset.yml`** — отклоняется на этапе загрузки. Пайплайн сброса не итерируется по сервисам; если нужна per-service очистка, декларируйте её явно в фазах сброса.
- **Забыли `log: false` для шумных reset-запусков** — reset по умолчанию `log: false`, deploy по умолчанию `log: true`. Выставляйте поле явно, когда хотите поведение, отличное от дефолтного.
- **Использование `continue_on_error` для маскировки реальных падений в core-фазах** — это для хук-фаз (pre/post). Упавший `docker up` должен всегда прерывать пайплайн.
- **Путаница `when:` и `check:`** — `when:` вычисляется до запуска шага (предусловие); `check:` вычисляется после успеха (пост-действие). `when:` использует типизированную форму `type: builtin|shell|template` / `cmd:`; `check:` использует типизированную форму `type: shell|dwe|command|builtin`.
- **Дублирование file-probe логики в `when:` вместо `files_gate:`** — если шаг должен запускаться условно на основе существования файла, используйте `files_gate:` вместо жёстко закодированных glob-ов в shell-условии `when:`. Так правки в определении `files:` команды автоматически применяются к probe-логике шага — они остаются в синхроне. `files_gate:` ссылается на канонический файловый спек команды.
