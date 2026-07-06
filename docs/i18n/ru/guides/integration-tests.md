> Translated from: guides/integration-tests.md @ 8d2ee019b30a

# Написание интеграционных тестов

`dwe test` запускает deploy-пайплайн вашего проекта — и любые проверки или команды, которые вы добавите — внутри свежей одноразовой копии проекта. Ничто из происходящего здесь не затрагивает окружение, в котором вы работаете. Это руководство по **процессу авторства**; пофайловая схема живёт в [`../reference/config/tests.md`](../reference/config/tests.md).

## Единственная предпосылка: провести порты через vars

Прежде чем писать сценарий, проверьте, как ваши сервисы отдают host-порты наружу. `dwe test` должен выдать каждой изолированной копии свои собственные порты, чтобы она могла работать *параллельно* с вашим рабочим окружением, и переписать он может только те порты, которые уже проведены через `vars:` — например, `ports:` сервиса, читающий `${vars.app.http_port}`, или compose-файл, подставляющий тот же var. Захардкоженный буквальный host-порт (`8080:8080` без единого var) переназначить нельзя, и собственная preflight-проверка копии откажется стартовать с ошибкой конфликта портов.

Если в вашем проекте так ещё не сделано, сначала перенесите нужный порт(ы) в `vars:` — механику см. в [vars](../reference/config/vars.md). Это разовое изменение на порт, а не на сценарий.

## Ваш первый сценарий

Создайте `workspace/tests/smoke.yml`:

```yaml
description: "Clean deploy comes up healthy"

env:
  vars:
    app.http_port: auto

steps:
  - name: "app answers"
    type: builtin
    cmd: http_check
    with:
      url: "http://localhost:${vars.app.http_port}/health"
      status: 200
```

Запустите:

```shell
dwe test run smoke
```

Это копирует ваш проект в изолированное дерево, генерирует свежий `local.yml` с выделенным портом, выполняет `dwe validate`, затем реальный `dwe deploy run` внутри копии, проверяет endpoint и полностью сносит всё после себя. Сценарий вообще без `steps:` уже полезен как тест — "деплой с этими параметрами проходит успешно".

## Тестирование варианта вашего стека

`env:` описывает, чем окружение этого сценария отличается от значений по умолчанию — переключатели сервисов и переопределения vars:

```yaml
description: "Deploy with redis disabled — cache falls back to in-memory"

env:
  services:
    disable: [redis]
  vars:
    app.http_port: auto

steps:
  - name: "app still answers without redis"
    type: builtin
    cmd: http_check
    with:
      url: "http://localhost:${vars.app.http_port}/health"
      status: 200
```

Каждый файл сценария — это одно изолированное окружение — пишите по одному на каждый значимый вариант (выключенный сервис, переключённый feature-флаг var, другая раскладка портов), а не пытайтесь параметризовать единственный файл.

## Проверка не только "порт отвечает"

Шаги используют ровно ту же схему, что и `workspace/deploy.yml`: `type: shell`, `type: dwe`, `type: command`, `type: builtin`, с условиями `when:`. Любой билтин-предикат — `file_exists`, `tcp_reachable`, `containers_running`, `env_keys_present`, `http_check` — работает напрямую как тело шага и ведёт себя как проверка pass/fail, а не только внутри `check:`:

```yaml
steps:
  - name: "containers are up"
    type: builtin
    cmd: containers_running
    with: { services: [app, db] }

  - name: "app answers"
    type: builtin
    cmd: http_check
    with: { url: "http://localhost:${vars.app.http_port}/health", status: 200, contains: "ok" }
```

`${...}` в `with:`/`cmd:` шага резолвится по конфигу копии до выполнения шага, а пути вида `file_exists` резолвятся относительно корня копии — проверки всегда смотрят на одноразовое окружение, а не на ваше рабочее дерево.

## Тестирование проектной команды через `type: command`

Второй сценарий использования из спецификации — "создать дамп БД, проверить, что файл появился" — это просто шаг `type: command`, вызывающий обычную пользовательскую команду, за которым следует проверка:

```yaml
steps:
  - name: "create dump"
    type: command
    cmd: db:dump

  - name: "dump file exists"
    type: builtin
    cmd: file_exists
    with: { path: "dumps/db-latest.sql.gz" }
```

Шаги `type: command` могут вызывать **`private`-команды**, так что вы можете держать тест-специфичные команды (например, команду, засеивающую фикстуры, или дампящую в файл с фиксированным именем вместо снабжённого timestamp'ом) вне повседневного листинга `dwe commands`:

```yaml
# workspace/commands/testing.yml
group: testing
commands:
  - id: db:dump
    private: true
    type: shell
    cmd: "docker compose exec -T db pg_dump -U app app > dumps/db-latest.sql.gz"
```

(`hide`-команды пайплайны пропускают полностью, поэтому здесь они не работают — используйте `private` для тест-специфичных команд, которые всё же должны быть запускаемы из сценария.)

## Отладка проваленного сценария через `--keep`

Когда сценарий проваливается, а живого вывода недостаточно, перезапустите с `--keep`:

```shell
dwe test run --keep smoke
```

Teardown пропускается; `dwe test run` печатает имя compose-проекта и путь копии, чтобы вы могли зайти внутрь (`cd`), осмотреть контейнеры через `docker compose -p <project> ps` или открыть шелл внутри сервиса. Манифест тоже остаётся на диске, поэтому повторный `dwe test run smoke` откажется стартовать, пока вы не уберёте всё вручную — это намеренно, чтобы сохранённое отладочное окружение никогда нельзя было тихо удалить из-под вас.

## Запуск всего набора

```shell
dwe test run                 # каждый сценарий под workspace/tests/*.yml, отсортированные
dwe test run smoke db-dump   # только эти два, по имени
dwe test list                # имена сценариев + описания
dwe test run --timeout 5m    # переопределить собственный timeout: каждого сценария
```

Коды выхода делают это CI-дружелюбным из коробки: `0` — всё прошло, `1` — хотя бы один сценарий провалился, `2` — сценарий не удалось даже подготовить (плохое имя, захваченный lock, ошибка файла сценария). `--output json` даёт машиночитаемый отчёт о том же результате.

## Смежные ссылки

- [`../reference/config/tests.md`](../reference/config/tests.md) — полная схема сценария, модель изоляции, структура `.dwe/tests/`, порядок teardown, коды выхода, документированные ограничения.
- [`../reference/config/deploy/builtins.md`](../reference/config/deploy/builtins.md) — каждый билтин, доступный `steps:`, включая `http_check` и семантику предиката-как-проверки.
- [`author-project-commands.md`](author-project-commands.md) — авторство шагов `type: command`, которые может вызывать сценарий, включая `private`-команды.
- [`preflight-checks.md`](preflight-checks.md) — проверка `ports_free`, которая обеспечивает предпосылку проведённых через vars портов.
