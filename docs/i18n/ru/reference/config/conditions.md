> Translated from: reference/config/conditions.md @ 89a0f0e6c5e2

# Условия и действия

Типизированные условия (`when:`) и типизированные действия (`check:` / тела шагов) в пайплайнах.

## Содержание

- [Обзор](#обзор)
- [Типизированные условия (`when:`)](#типизированные-условия-when)
  - [`type: builtin` — предикаты](#type-builtin--предикаты)
  - [`type: shell` — shell-команды](#type-shell--shell-команды)
  - [`type: template` — Go-шаблоны](#type-template--go-шаблоны)
- [Типизированные действия (`check:` и тела шагов)](#типизированные-действия-check-и-тела-шагов)
- [`check: auto` — инверсия `when:`](#check-auto--инверсия-when)
- [Два регистра `type: builtin`](#два-регистра-type-builtin)
- [Условия workflow'ов (строковые, отдельная система)](#условия-workflowов-строковые-отдельная-система)
- [Связанная документация](#связанная-документация)

## Обзор

**Условия** (`when:`) — это **предусловия**, вычисляемые до запуска фазы или шага. Они возвращают булево значение: true = продолжать, false = пропустить.

**Действия** — это **полезные нагрузки**, выполняющие код. Когда они используются как `check:` (post-action), их успех/падение определяет, прошёл шаг или упал.

Система пайплайнов использует **типизированные** формы для обоих — поле `type:` диспетчеризует в разные исполнители. Шаги workflow'ов (внутри определений команд) используют отдельную строковую форму `when:`, документированную в [commands/](commands/index.md).

```
Шаги пайплайна (типизированные):
  when: { type: builtin|shell|template, cmd: ..., expr: ... }
  check: { type: shell|dwe|command|builtin, cmd: ..., with: ... }
  check: auto                                  # логическая инверсия when:

Шаги workflow'а (строковые — отдельные, здесь не покрываются):
  when: "dir-empty path" | "{{ ... }}" | "cmd: ..."
  command: <id>
```

## Типизированные условия (`when:`)

`when:` на фазе или шаге пайплайна — это **типизированное условие** с тремя формами. Вычисляется до запуска фазы/шага; falsy-результат его пропускает.

### `type: builtin` — предикаты

Builtin-условия проверяют состояние файловой системы через **регистр предикатов**. Предикаты отличаются от engine-билтинов (вроде `service_configs_copy`) — они находятся в отдельном неймспейсе и не могут использоваться в действиях `check:`.

```yaml
when:
  type: builtin
  cmd: "dir-empty services/main/src"
```

**Доступные предикаты** (path относительно корня проекта):

| Предикат | True, когда |
|----------|-------------|
| `dir-exists <path>` | path — существующая директория |
| `dir-missing <path>` | path отсутствует или не директория |
| `dir-empty <path>` | path отсутствует или не содержит записей |
| `dir-not-empty <path>` | path — директория с минимум одной записью |
| `file-exists <path>` | path — существующий обычный файл |
| `file-missing <path>` | path отсутствует или не обычный файл |
| `generated-missing <svc> <field>` | значение `<field>` отсутствует в хранилище сгенерированных значений (`.dwe/generated.yml`) или файл хранилища отсутствует |

В отличие от остальных предикатов, `generated-missing` принимает **два** под-аргумента — имя сервиса и имя сгенерированного поля — а не путь. Он читает долговременное per-service хранилище `.dwe/generated.yml` и используется, чтобы ограничить шаг генерации секрета сервиса так, чтобы он выполнялся только при первом деплое (когда ещё не было собрано ни одного значения). Описание декларации `generated:` см. в [services/fields.md](services/fields.md), а поток harvest/replay — в [render/config.md](../render/config.md).

**Переносимость:** эвалюатор предикатов использует жёстко зафиксированный `sh -c` (а не настроенный shell-бинарь проекта), чтобы обеспечить POSIX-переносимость и согласованность независимо от выбора shell в проекте.

### `type: shell` — shell-команды

Shell-условия выполняют команду и проверяют её exit-код: exit 0 = true, не-ноль = false.

```yaml
when:
  type: shell
  cmd: "test -f services/main/src/vendor/autoload.php"
```

Применима полная shell-семантика: пайпы, редиректы, операторы и т.д. Как и предикаты, shell-условия используют жёстко зафиксированный `sh -c` для переносимости.

### `type: template` — Go-шаблоны

Template-условия вычисляются на **этапе планирования** с использованием синтаксиса Go `text/template`. Они не поддерживают `check:` в том же шаге (никаких сайд-эффектов до выполнения).

```yaml
when:
  type: template
  expr: "{{ .Services.second.Enabled }}"
```

Template-условия предназначены исключительно для проверок идемпотентности, известных на этапе планирования:

```yaml
- name: setup
  when:
    type: template
    expr: "{{ not .Services.database.Enabled }}"
  steps: []
```

Render-контекст включает полную разрешённую конфигурацию проекта, поэтому можно дотянуться до любого значения конфигурации. Синтаксис template-выражений и справочник по хелперам см. в [Шаблонах](../templates.md).

## Типизированные действия (`check:` и тела шагов)

Действия — это **исполняемые полезные нагрузки** — та же форма `type: shell|dwe|command|builtin`, что и в телах шагов. Когда они используются как post-action `check:`, успех/падение действия определяет успех/падение шага.

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

Действия поддерживают четыре типа исполнителей:

| Тип | Исполнитель | Пример |
|-----|-------------|--------|
| `shell` | `sh -c` | `type: shell, cmd: "test -f file.txt"` |
| `dwe` | DWE CLI | `type: dwe, cmd: "docker up"` |
| `command` | Регистр команд | `type: command, cmd: "services.main.migrate"` |
| `builtin` | Engine-билтин | `type: builtin, cmd: "service_configs_check"` |

Полный справочник действий и семантику падений `check:` под `continue_on_error` см. в [deploy/conditions.md](deploy/conditions.md).

## `check: auto` — инверсия `when:`

Кроме формы-маппинга `check:` принимает один скаляр: `auto`. Он разворачивается в логическую инверсию собственного `when:` шага — для частого случая, когда «надо ли запускать» и «не сделано ли уже» — это один и тот же предикат, прочитанный в противоположных направлениях.

```yaml
- name: clone-source
  type: shell
  cmd: "git clone ${vars.source.repo} services/backend/src"
  when:
    type: shell
    cmd: "[ ! -e services/backend/src/.git ]"
  check: auto          # ≡ check: {type: builtin, cmd: shell, with: {cmd: "! ( [ ! -e … ] )"}}
```

**Работает только с `when: {type: shell}`.** Две другие формы — ошибки времени загрузки:

- **`type: builtin`** — два неймспейса `type: builtin` ниже **не пересекаются**. `dir-empty` — это предикат, и в регистре действий, из которого черпает `check:`, у него нет пары, поэтому нет действия, способного выразить «НЕ `dir-empty foo`». (Инверсия подстановкой «парного противоположного» предиката тоже была бы неверна на краевых случаях: `dir-empty` и `dir-not-empty` не дополняют друг друга для отсутствующего каталога.)
- **`type: template`** — template-условия вычисляются на этапе плана, и ложное условие полностью удаляет шаг. Значит любой шаг, доживший до исполнения, имел `when == true`, его инверсия всегда ложна, и выведенный check всегда падал бы.

`check: auto` без `when:` тоже отвергается — инвертировать нечего. Инверсия — это логическое отрицание отрендеренной команды (`! (\n<cmd>\n)`), а не текстовая правка над ней.

Детали разрешения (shell, рабочий каталог, таймаут) и последствия для журнала/хеша конфигурации см. в [deploy/conditions.md](deploy/conditions.md#check-auto-инверсия-when).

## Два регистра `type: builtin`

Система пайплайнов содержит **два отдельных неймспейса `type: builtin`**, различаемых по позиции в YAML:

1. **Предикаты** — используются в `when: type: builtin`. Проверки состояния файловой системы, например, `dir-empty`, `file-exists`.
2. **Engine-билтины** — используются в телах шагов и в `check: type: builtin`. Исполняемые действия, например, `service_configs_copy`, `service_configs_check`, `message`.

Пример различия:

```yaml
phases:
  - name: setup
    when:                         # when: использует регистр ПРЕДИКАТОВ
      type: builtin
      cmd: "dir-empty src"
    steps:
      - name: copy
        type: builtin             # тело шага использует регистр ENGINE-БИЛТИНОВ
        cmd: service_configs_copy
        with:
          service: main
      - name: verify
        check:                     # check: использует регистр ENGINE-БИЛТИНОВ
          type: builtin
          cmd: service_configs_check
          with:
            service: main
```

`dir-empty` не engine-билтин (недоступен как тело шага или check). `service_configs_copy` не предикат (недоступен в `when:`).

## Условия workflow'ов (строковые, отдельная система)

Шаги workflow'ов используют отдельный строковый мини-язык условий. Полная грамматика документирована в [commands/](commands/index.md); эта секция лишь даёт общее представление для контекста.

```yaml
# Workflow (строковая, отдельная система)
steps:
  - command: services.main.migrate
    when: "file-missing services/main/src/vendor/autoload.php"

  - confirm: "Proceed?"
    when: "{{ if .Params.confirm }}1{{ else }}0{{ end }}"

  - command: cleanup
    when: "cmd: test -d /tmp/workdir"
```

Условия workflow'ов классифицируются по начальному префиксу (`{{ ... }}` → template, `cmd: ...` → shell-команда, иначе → предикат). Полную грамматику workflow'ов см. в [commands/](commands/index.md).

## Связанная документация

- [deploy](deploy/index.md) — синтаксис `when:` и `check:` пайплайна с примерами
- [lifecycle.md](lifecycle.md) — lifecycle-пайплайны (та же step/condition-грамматика, что и у deploy)
- [commands/](commands/index.md) — определения команд (отдельная система; workflow'ы сохраняют строковый `when:`)
- [Шаблоны](../templates.md) — синтаксис Go-шаблонов, sprout-хелперы, render-контексты
