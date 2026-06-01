> Translated from: reference/config/index.md @ ea9533e26547

# Справочник конфигурации

Обзор всех конфигурационных файлов в системе DWE.

> Впервые сталкиваетесь с DWE? Начните с [Getting started](../concepts/getting-started.md), чтобы пройти сценарий от начала до конца, и возвращайтесь сюда за пофайловым справочником.

## Содержание

- [Инвентарь файлов](#инвентарь-файлов)
- [Топология загрузчиков](#топология-загрузчиков)
- [Слитые vs standalone](#слитые-vs-standalone)
- [Страницы](#страницы)
- [Связанные команды](#связанные-команды)

## Инвентарь файлов

| Файл | Трекается | Загрузчик | Назначение |
|------|-----------|-----------|------------|
| `workspace.yml` | да | layer 1 | Идентичность проекта и структура сервисов |
| `workspace/defaults.yml` | да | layer 2 | Версионированные дефолты: runtime, экспорты, тумблеры enabled сервисов |
| `workspace/local.yml` | нет (gitignored) | layer 3 | Per-user override'ы: состояние, тумблеры enabled сервисов |
| `workspace/services/<name>/service.yml` | да | standalone | Per-service декларация (dirs, cli, configs, порты) |
| `workspace/deploy.yml` | да | standalone | Deploy-пайплайн оркестратора (фазы + шаги) |
| `workspace/services/<name>/deploy.yml` | да | standalone | Per-service deploy-пайплайны |
| `workspace/reset.yml` | да | standalone | Reset-пайплайн |
| `workspace/lifecycle.yml` | да | standalone | Пайплайны run / stop (драйверы для `dwe run`/`stop`/`restart`) |
| `workspace/docker.yml` | да | standalone | Политика выполнения compose |
| `workspace/docker.local.yml` | нет (gitignored) | сливается в `docker.yml` | Локальные override'ы compose-политики |
| `workspace/styles.yml` | да | standalone | ASCII header, цветовая палитра, разделитель |
| `workspace/info.yml` | да | standalone | Секции info-дашборда |
| `workspace/commands/` | да | standalone | Декларативные определения команд (per-file группы) |
| `workspace/validate.yml` | да | standalone | Проверки готовности проекта (preflight + `dwe validate`) |
| `workspace/snapshot.yml` | да | standalone | Snapshot-workflow'ы: create / restore / remove (`dwe snapshot`) |
| `workspace/i18n/*.yml` | нет (ignored) | standalone | Переводы пользовательских команд и UI-строк (опционально; один файл на язык) |

## Runtime-артефакты

Директория `.dwe/` содержит управляемые DWE артефакты и **gitignored**:

- `.dwe/logs/` — логи пайплайнов (deploy, reset, lifecycle run/stop)
- `.dwe/deploy/deploy.lock` — lock-файл деплоя (только Unix; предотвращает параллельные деплои)
- `.dwe/deploy/state.yml` — журнал состояния деплоя (трекает deploy-статус и хэши сервисов)
- `.dwe/snapshots/snapshot.lock` — snapshot lock-файл (только Unix; сериализует snapshot-мутации и совместно захватывается lifecycle-командами деплоя)
- `.dwe/snapshots/current` — указатель текущего снапшота (последний созданный или восстановленный)
- `.dwe/snapshots/.pre-restore-backup/` — бэкап `workspace/local.yml` + `.dwe/deploy/state.yml`, снимаемый перед каждым restore; цель ручного восстановления при сбое restore

Добавьте `.dwe/` в `.gitignore` проекта, если ещё не добавлено.

## Топология загрузчиков

```mermaid
flowchart LR
  subgraph merged["Слияние 3 слоёв — DweConfig"]
    direction TB
    A[workspace.yml] --> B[workspace/defaults.yml] --> C[workspace/local.yml]
  end

  S["workspace/services/&lt;name&gt;/service.yml"] -. инжектится в Raw .-> merged

  merged --> R[(DweConfig.Raw<br/>+ типизированные структуры)]

  subgraph standalone["Standalone-загрузчики"]
    direction TB
    D[workspace/deploy.yml]
    DS["workspace/services/&lt;name&gt;/deploy.yml"]
    RS[workspace/reset.yml]
    L[workspace/lifecycle.yml]
    DK[workspace/docker.yml<br/>+ docker.local.yml]
    ST[workspace/styles.yml]
    IN[workspace/info.yml]
    CM[workspace/commands/]
  end

  R -. "dot-paths / шаблоны" .-> D
  R -. "dot-paths / шаблоны" .-> DS
  R -. "dot-paths / шаблоны" .-> RS
  R -. "dot-paths / шаблоны" .-> L
  R -. "$#123;...#125; project_name" .-> DK
  R -. "#123;#123;...#125;#125; выражения" .-> IN
  R -. "$#123;...#125; параметры команд" .-> CM
```

## Слитые vs standalone

**Слитые (3-слойный конфиг)**: `workspace.yml` → `workspace/defaults.yml` → `workspace/local.yml` глубоко сливаются на старте. Поздние слои выигрывают; map'ы сливаются рекурсивно. Результат — эффективный конфиг, используемый для генерации `.env`, резолва топологии и правил экспорта. Каждый `workspace/services/<name>/service.yml` грузится отдельно и затем инжектится в слитый raw-map, чтобы dot-пути вроде `services.main.container` резолвились.

**Standalone**: `workspace/services/<name>/service.yml`, `deploy.yml`, `workspace/services/<name>/deploy.yml`, `reset.yml`, `lifecycle.yml`, `docker.yml` (+ `docker.local.yml`), `styles.yml`, `info.yml` и `commands/*.yml` грузятся выделенными функциями в `internal/core/project/config/` и `internal/core/usercommands/`. Они не часть 3-слойного слияния, но большинство из них резолвят template-выражения против слитого конфига.

## Файлы, поддерживающие локальные override'ы

На данный момент только `docker.local.yml` поддерживает вариант `.local.yml` для per-developer кастомизации. Паттерн такой:

**Docker**: `workspace/docker.yml` (трекаемый, общий на весь проект) + `workspace/docker.local.yml` (gitignored, per-developer). Локальный файл сливается поверх базового, позволяя разработчикам кастомизировать политику выполнения compose — например, добавить дополнительные volume'ы, смонтировать локальные директории с исходниками или переопределить platform/args, не задевая коллег.

**Почему только docker?** Docker-настройки по своей природе персональные — они зависят от локального окружения разработчика (доступные бинарники, монтирование volume'ов, различия платформ). Другие конфиги вроде `lifecycle.yml`, `info.yml` и `styles.yml` общие на весь проект и не выигрывают от per-developer override'ов.

Подробнее о семантике и примерах `docker.local.yml` см. [docker.yml](docker.md#dockerlocalyml).

## Страницы

- [workspace / defaults / local](workspace.md) — 3-слойный слитый конфиг: порядок слияния, приоритет, резолв dot-path'ов, справочник полей
- [services/<name>/service.yml](services/index.md) — per-service декларации, extends, dirs, cli-конфиг
- [deploy.yml / reset.yml](deploy/index.md) — deploy- и reset-пайплайны, шаги, билтины, file-логирование, идемпотентный деплой
- [state.yml](state/index.md) — трекинг состояния деплоя, таблица skip-решений, хэширование, lock-файл, восстановление после крашей
- [lifecycle.yml](lifecycle.md) — пайплайны run/stop, проба обновлений, hook-фазы, гейт обязательных сервисов
- [Условия и действия](conditions.md) — типизированные условия для `when:`, типизированные действия для `check:` и тел шагов, различие predicate vs engine-builtin
- [docker.yml](docker.md) — политика выполнения Compose, имя проекта, env-триггеры
- [styles.yml](styles.md) — ASCII header, цветовая палитра, разделитель
- [info.yml](info.md) — секции info-дашборда, template-выражения
- [commands/](commands/index.md) — декларативные команды: типы, параметры, контекст, файлы, workflow'ы, шаблоны
- [validate.yml](validate.md) — проверки готовности проекта: env-пробы, декларативные проверки, билтины, стадии, preflight
- [snapshot.yml](snapshot.md) — snapshot-workflow'ы: блоки create/restore/remove, варианты, неймспейс `${snapshot.*}`, manifest, взаимодействие с lock, безопасность архивов
- [Локализация (i18n)](i18n.md) — переводы пользовательских команд и UI-строк: разрешение локали, формат файла, справочник ключей, валидация
- [Пользовательский конфиг](userconfig.md) — пользовательские настройки: расположение файла, синтаксис, переопределения бинарей, язык, тема mermaid
- [Уведомления](notifications.md) — user-level desktop-уведомления: расположение конфиг-файлов, ключи, gate-матрица, env-override'ы
- [UI](ui.md) — конфигурация интерактивного браузера команд: глубина, collapse, бэйджи, хоткеи, fallback-лестница
- [Шаблоны](../templates.md) — Go-шаблоны, shorthand `${...}`, sprout-хелперы (общие для info, commands, пайплайнов, render-паков)

## Связанные команды

- `dwe render env` — генерирует `.env` из правил экспорта слитого конфига
- `dwe render ide` — генерирует IDE-конфиги
- `dwe render ai` — генерирует hub-level AGENTS.md и симлинки CLAUDE.md
- `dwe render git` — генерирует shell-хуки git в `<svc.Dir>/src/.git/hooks/`
- `dwe info` — рендерит info-дашборд из `info.yml`
- `dwe deploy plan` — показывает разрешённый deploy-пайплайн
- `dwe compose files` — показывает список активных compose-файлов (диагностика)
- `dwe status apps` — показывает app-сервисы с health и deploy-статусом
- `dwe status tools` — показывает таблицу tool-сервисов (read-only)
- `dwe status infra` — показывает таблицу infra-сервисов (read-only)
