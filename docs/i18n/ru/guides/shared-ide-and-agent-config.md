> Translated from: guides/shared-ide-and-agent-config.md @ 0b09e49f2aeb

# Общий конфиг IDE и AI-агентов

Сделайте так, чтобы у каждого разработчика в команде были одинаковые настройки VS Code, одинаковые `AGENTS.md` / `CLAUDE.md` и одинаковые git-хуки — без того, чтобы кто-то правил эти файлы руками. Три рендер-подкоманды DWE (`dwe render ide`, `dwe render ai`, `dwe render git`) рулят всем этим из template-паков, лежащих в репо.

Это руководство ведёт от нуля до работающего общего конфига с местом под per-developer твики. Полная схема и edge-cases — в [справочнике render](../reference/render/index.md).

## Разделы

- [Раскладка template-пака](#раскладка-template-пака)
- [Файл `manifest.yml`](#файл-manifestyml)
- [Разрешение пака: какой пак использует сервис?](#разрешение-пака-какой-пак-использует-сервис)
- [Политики коллизий (deepest vs shallowest)](#политики-коллизий-deepest-vs-shallowest)
- [Dry run — рендерим всё](#dry-run--рендерим-всё)
- [Личные оверрайды через `<pack>.local/`](#личные-оверрайды-через-packlocal)
- [Что трекается, а что в gitignore](#что-трекается-а-что-в-gitignore)

## Раскладка template-пака

Все три рендера читают паки из `workspace/templates/<kind>/<pack>/`, где `<kind>` — это `ide`, `ai` или `git`:

```
workspace/templates/
  ide/
    default/                # неявный fallback-пак
      manifest.yml
      .vscode/settings.json.tmpl
      .devcontainer/devcontainer.json.tmpl
    main-debug/             # пак с именем сервиса (или пин через render.ide.template)
      manifest.yml
      .vscode/launch.json.tmpl
      .vscode/settings.json.tmpl
  ai/
    default/
      manifest.yml
      AGENTS.md.tmpl
  git/
    default/
      manifest.yml
      pre-commit.tmpl
```

Каждый пак — это каталог; рендер никогда не следует за симлинкнутыми паками. Template-файлы заканчиваются на `.tmpl` и используют [синтаксис Go text/template](../reference/templates.md).

Выходы оседают в hub-директории каждого включённого сервиса:

| Вид | Назначение выхода |
|------|------------------|
| `ide` | `<svc.Dir>/<rel>` — например, `services/main/.vscode/settings.json` |
| `ai`  | `<svc.Dir>/<rel>` — например, `services/main/AGENTS.md` |
| `git` | `<svc.Dir>/src/.git/hooks/<basename>` — chmod `0755` на каждом прогоне |

По умолчанию рендерятся только сервисы `type: app` (для всех трёх видов — кроме `ai`, который по умолчанию `true` тоже только для apps). Сервисы других типов опт-инятся через `services.<name>.render.<kind>.enabled: true`.

## Файл `manifest.yml`

В корне каждого пака должен быть `manifest.yml`. Это единственный источник правды о том, что рендерится — рендер никогда не обходит пак сам по себе.

```yaml
render:
  - from: .vscode/settings.json.tmpl
    to:   .vscode/settings.json
  - from: .devcontainer/devcontainer.json.tmpl
    to:   .devcontainer/devcontainer.json

symlinks:                              # только ide / ai — git отвергает симлинки
  - link: CLAUDE.md
    to:   AGENTS.md
```

Per-kind ограничения:

| Вид | Форма `to` | Блок `symlinks` |
|------|------------|------------------|
| `ide` | любой путь внутри hub | разрешено |
| `ai`  | любой путь внутри hub | разрешено; каждый `to` должен ссылаться на запись `render` |
| `git` | **только basename** (без слешей) | отвергается |

Неизвестные ключи в `manifest.yml` — это hard error (строгий YAML-декод). Файл внутри пака, не указанный в `render:`, молча игнорируется — добавьте его в манифест, чтобы включить.

## Разрешение пака: какой пак использует сервис?

Сервис может явно зафиксировать пак:

```yaml
# workspace/services/api/service.yml
render:
  ide:
    template: corporate-vscode    # использовать workspace/templates/ide/corporate-vscode/
```

Если `render.<kind>.template` задан, такой пак обязан существовать — опечатки это hard error, никогда не молчаливый фолбэк на `default/`. Это защищает вас от того, что `templete: corporete-vscode` случайно отрендерит то, что окажется в `default/`.

Когда `render.<kind>.template` не задан, резолвер обходит неявную цепочку — побеждает первое попадание:

1. `workspace/templates/<kind>/<имя-сервиса>/`
2. каждый предок в цепочке `extends:` сервиса, по очереди
3. `workspace/templates/<kind>/default/`

Это согласовано с тем, как работает `extends:` в сервисах: ребёнок типа `main-debug extends: main` обычно наследует родительский IDE-пак, а не падает сразу в `default/`.

## Политики коллизий (deepest vs shallowest)

Когда два сервиса разделяют один и тот же `dir:` (классический случай — ребёнок `extends:` родителя, и оба указывают на `services/main`), для любого вида рендера выигрывает только один — но какой именно зависит от вида:

| Вид | Политика коллизий | Почему |
|------|-------------------|--------|
| `ide` | **побеждает самый глубокий** | IDE-конфиги про per-variant поведение (другой дебаггер, другой launch-профиль). Самый специализированный вариант владеет рендеренными файлами. |
| `git` | **побеждает самый глубокий** | То же обоснование — хуки часто варьируются per-variant. |
| `ai`  | **побеждает самый верхний** (предок в цепочке `extends`) | `AGENTS.md` описывает каноническую идентичность hub. Варианты её разделяют; описание владеет родитель. |

Ничья на одной глубине разрешается лексикографически по имени сервиса. Проигравшие сервисы дают warning с именем победителя и оспариваемого каталога — это ваш намёк, что два сервиса случайно столкнулись.

`dwe render ide main` (с явным аргументом) тоже уважает политику коллизий: если `main-debug` — самый глубокий вариант, делящий `services/main`, рендерит он, и info-строка анонсирует подмену.

## Dry run — рендерим всё

После правки `manifest.yml` или любого `.tmpl` запустите рендеры вручную, чтобы увидеть выход:

```sh
dwe render ide       # рендерим IDE-файлы для каждого пригодного сервиса
dwe render ai        # рендерим AGENTS.md (и симлинки)
dwe render git       # пишем исполняемые git-хуки (mode 0755)
```

Каждая подкоманда принимает опциональный аргумент `[сервис]`, чтобы сузить скоуп:

```sh
dwe render ide api
dwe render ai api
dwe render git api
```

Если хотите увидеть, что будет, без записи — нацельте сначала на один сервис, изучите результат, потом коммитьте. Отдельного `--dry-run` нет — команды перезаписывают существующие обычные файлы в назначении без промта, но отказываются перезаписывать симлинк (получите явную ошибку и путь).

В повседневной работе deploy-пайплайны прогоняют это автоматически — звать руками обычно нужно только при авторстве или дебаге пака.

## Личные оверрайды через `<pack>.local/`

Хотите личные настройки редактора без коммита в командный пак? Положите сиблинг shadow-пак:

```
workspace/templates/ide/
  default/                       # tracked, командный
    manifest.yml
    .vscode/settings.json.tmpl
  default.local/                 # gitignored, личный
    .vscode/settings.json.tmpl   # подменяет тот, что выше
```

Когда рендер читает `default/.vscode/settings.json.tmpl`, он сначала проверяет `default.local/.vscode/settings.json.tmpl`. Если ваш локальный файл есть — он используется как источник, и рендер печатает:

```
using local override: workspace/templates/ide/default.local/.vscode/settings.json.tmpl
```

Ключевые правила:

- Shadow-пак — это **подмена входа**, не перенаправление выхода. Отрендеренный файл всё равно ложится в то же место (`services/main/.vscode/settings.json`).
- Shadow-пак должен содержать **только те файлы, которые вы переопределяете**. Свой `manifest.yml` ему не нужен — манифест канонического пака всё равно рулит тем, что рендерится.
- Добавьте `workspace/templates/*/*.local/` (или более широкое `*.local/`) в `.gitignore`.

Это совпадает с более широкой конвенцией DWE про локальные оверрайды:

| Канонический (tracked) | Локальный сиблинг (gitignored) |
|------------------------|--------------------------------|
| `workspace/workspace.yml` | `workspace/local.yml` |
| `workspace/docker.yml` | `workspace/docker.local.yml` |
| `workspace/templates/<kind>/<pack>/` | `workspace/templates/<kind>/<pack>.local/` |

### Оговорка для оверрайдов IDE/AI

Для `git` отрендеренный выход ложится в `.git/hooks/`, который никогда не трекается — оверрайды полностью приватны без трения.

Для `ide` и `ai` отрендеренный выход обычно — это трекаемый файл (`.vscode/settings.json`, `AGENTS.md`). Личный оверрайд, дающий другой файл, значит, что повторный `dwe render ide` создаст вам diff в трекаемом файле. Не коммитьте такие diff-ы — `git stash`, `git checkout -- <path>` или личный pre-commit guard — все три способа годятся, чтобы держать их локально.

## Что трекается, а что в gitignore

| Путь | Трекается? | Заметка |
|------|-----------|---------|
| `workspace/templates/<kind>/<pack>/` | да | Командный пак — источник правды. |
| `workspace/templates/<kind>/<pack>.local/` | **нет** | Личные оверрайды. Игнорируйте паттерн `.local/`. |
| `services/<name>/.vscode/settings.json` (и подобные IDE-выходы) | обычно да | Отрендеренный выход; коммитьте, чтобы у коллег сразу был тот же конфиг редактора без вызова `dwe render ide`. |
| `services/<name>/AGENTS.md`, `services/<name>/CLAUDE.md` | обычно да | Отрендеренный выход; то же обоснование. |
| `services/<name>/src/.git/hooks/<name>` | **никогда** | Лежит внутри `.git/`, который git сам игнорирует. |

Типичный проект коммитит отрендеренные IDE- и AI-выходы, чтобы свежий клон сразу имел рабочие конфиги, потом перезапускает `dwe render ide` / `dwe render ai` при каждом изменении пака или `service.yml`. Git-хуки — исключение: они лежат внутри `.git/` и должны рендериться заново после каждого клона.

## См. также

- [индекс справочника render](../reference/render/index.md) — полная схема, защиты путей, edge-cases
- [`render ide`](../reference/render/ide.md) — детали IDE и шаблонные переменные
- [`render ai`](../reference/render/ai.md) — рендеринг `AGENTS.md` и семантика симлинков
- [`render git`](../reference/render/git.md) — рендеринг git-хуков и оговорки про worktree
- [add-a-service](add-a-service.md) — добавление сервиса, участвующего в IDE/AI/git-рендере
