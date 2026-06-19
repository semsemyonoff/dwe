> Translated from: reference/templates.md @ af37f3b26145

# Шаблоны

Go-шаблоны (с библиотекой функций [go-sprout](https://docs.atom.codes/sprout/)) вычисляются в нескольких точках DWE: элементах info-дашборда, декларативных командах, условиях `when:` пайплайнов, билтине `message` и render-паках IDE / AI / git / config. Эта страница — единый справочник по движку шаблонов, доступным хелперам и соглашениям, общим для всех мест. Обратите внимание: **config**-render-пак отличается от остальных видов рендера — он использует подложку-сокращение `${...}` (lenient — отсутствующее → `""`), а не строгий синтаксис `{{ ... }}` пакетов ide/ai/git.

## Содержание

- [Где вычисляются шаблоны](#где-вычисляются-шаблоны)
- [Два синтаксиса: shorthand и полные шаблоны](#два-синтаксиса-shorthand-и-полные-шаблоны)
  - [Квотинг шаблонов внутри YAML](#квотинг-шаблонов-внутри-yaml)
- [Render-контекст по местам использования](#render-контекст-по-местам-использования)
- [Встроенные функции `text/template`](#встроенные-функции-texttemplate)
- [Доменный хелпер: appURL](#доменный-хелпер-appurl)
- [Регистры Sprout](#регистры-sprout)
- [Резолверы scope команд](#резолверы-scope-команд)
- [Строгий рендер (render-паки)](#строгий-рендер-render-паки)
- [Распространённые паттерны](#распространённые-паттерны)
- [Соглашения и подводные камни](#соглашения-и-подводные-камни)
- [Дополнительное чтение](#дополнительное-чтение)

## Где вычисляются шаблоны

| Место | Синтаксис | Контекст | Заметки |
|-------|-----------|----------|---------|
| `info.yml` — `text`, `value`, `when` | `{{ ... }}` | Разрешённая конфигурация проекта | См. [info.md](config/info.md) |
| `workspace/commands/` — `cmd`, `argv`, `workdir`, `compose_args`, `env`, `messages.*`, `confirmation_text`, `files.*.path`/`candidates`, workflow-шаги `steps[].with[<key>]` / `steps[].when` | `${...}` и `{{ ... }}` | Контекст команды (`.Raw` + `.Params` + `.Context` + `.Files` + `.Host`) | См. [commands/](config/commands/index.md) |
| `deploy.yml` / `lifecycle.yml` / `reset.yml` — `when: type: template, expr:` | `{{ ... }}` | Разрешённая конфигурация проекта | Вычисляется на этапе планирования. См. [deploy](config/deploy/index.md) |
| Билтин `message` — `text:` | `{{ ... }}` | Разрешённая конфигурация проекта | См. [билтин message](config/deploy/builtins.md#message) |
| `docker.yml` — `project_name` | Только `${...}` | Разрешённая конфигурация проекта (lookup'ы по `.Raw`) | Только dot-path lookups (без `{{ }}`-логики). См. [docker.md](config/docker.md) |
| `workspace/templates/git/<pack>/**/*.tmpl` | `{{ ... }}` | Контекст render-пака (`.Project`, `.Service`, `.Resolved`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Строгий режим. См. [render/git.md](render/git.md) |
| `workspace/templates/ide/<pack>/**/*.tmpl` | `{{ ... }}` | Контекст render-пака (`.Project`, `.Service`, `.Resolved`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Строгий режим. См. [render/ide.md](render/ide.md) |
| `workspace/templates/ai/<pack>/**/*.tmpl` | `{{ ... }}` | Контекст render-пака (`.Project`, `.Service`, `.Resolved`, `.ServiceCfg`, `.Runtime`, `.Services`, `.Cfg`) | Строгий режим. См. [render/ai.md](render/ai.md) |
| `workspace/templates/config/<pack>/**` | `${...}` | Разрешённая конфигурация проекта (`.Raw`) + курируемый поднабор `${services.<name>...}` + `${generated.<name>}` | Lenient (отсутствующее → `""`). См. [render/config.md](render/config.md) |
| `params.*.default_from`, `context.*.from` | — | — | Только plain dot-paths (без template-выражений). |

## Два синтаксиса: shorthand и полные шаблоны

Существуют два слоя интерполяции; оба вычисляются одним движком.

**`${...}` — shorthand lookups.** Компактный, без логики. Используется в определениях команд и в `project_name` из `docker.yml`. Компилятор переписывает каждое `${...}` в эквивалентное выражение `{{ ... }}` на этапе разбора.

**`{{ ... }}` — полный Go `text/template`.** Условия, циклы, пайплайны, helper-функции. Доступно везде, где вычисляются шаблоны.

```yaml
# Микс в одной строке (место команды)
path: "${param.dump_dir}/${param.database}{{ if .Params.dump_date }}_{{ now | date \"2006-01-02\" }}{{ end }}.sql.gz"
```

Практическое правило: используйте `${...}` для простых lookup'ов; переходите к `{{ ... }}` всякий раз, когда нужно условие, сравнение, default, преобразование строки или пайплайн.

### Неймспейсы `${...}`

`${...}` разрешается через неймспейсы; первый сегмент маршрутизируется к конкретному источнику данных:

| Выражение | Резолвится как |
|-----------|----------------|
| `${vars.db.user}` | Dot-path в смерженном dwe-конфиге (`Raw`) |
| `${param.<name>}` | Разрешённое значение параметра |
| `${context.<name>}` | Разрешённое значение контекста |
| `${files.<id>.path}` | Абсолютный путь разрешённого файла-артефакта |
| `${host.uid}` / `${host.gid}` | Эффективные UID/GID (1000:1000 на macOS, реальные значения на Linux) |
| `${generated.<name>}` | Значение по сервису, собранное в `.dwe/generated.yml` (только config-render-паки; отсутствующее → `""`). См. [render/config.md](render/config.md) |

Всё, что не совпадает с известным неймспейсом (`${foo}`, `${a.b.c}`), трактуется как dot-path lookup в `Raw`. **Для пользовательских значений конфига предпочитайте `${vars.*}`** — они живут под блоком `vars:` в YAML; строгий корень отвергает свободные ключи верхнего уровня, поэтому `vars:` — их единственный дом. Литерал `$$` пропускается без изменений.

### Квотинг шаблонов внутри YAML

Строка между `{{ }}` одинаковая в любой форме YAML — меняется только обёртка:

```yaml
# скаляр в двойных кавычках: внутренний " нужно экранировать как \"
path: ".dwe/logs/{{ now | date \"2006-01-02\" }}.log"

# скаляр в одинарных кавычках: экранирование не требуется (рекомендуется для шаблонов)
path: '.dwe/logs/{{ now | date "2006-01-02" }}.log'

# литеральный блок-скаляр: экранирование не требуется
cmd: |
  echo "{{ now | date "2006-01-02" }}"
```

Предпочитайте скаляры в одинарных кавычках (`'...'`) для однострочных шаблонов, тело которых содержит `"`. Двойные кавычки (`"..."`) оставляйте для строк, которым нужны YAML escape-последовательности `\n`/`\t`. `\|`, который встречается в таблицах на этой странице, — это экранирование markdown-ячеек для отрендеренных доков; в YAML всегда используется обычный `|` внутри `{{ }}`.

## Render-контекст по местам использования

Данные, доступные шаблону, зависят от места. Доступ к полям — через точечный синтаксис (`.Project.Name`).

**Команды:**

| Путь | Содержимое |
|------|------------|
| `.Raw` | Смерженные `workspace.yml` + `defaults.yml` + `local.yml` как вложенная карта |
| `.Params` | Разрешённые значения параметров (map по имени параметра) |
| `.Context` | Разрешённые значения контекста (map по имени контекста) |
| `.Files` | Разрешённые файлы-артефакты (map по file id; у каждого есть поле `.Path`) |
| `.Host.UID` / `.Host.GID` | Строки UID/GID хоста |

**Info, пайплайны, билтин `message`:** разрешённая конфигурация проекта — адресуется тем же точечным синтаксисом, что и `.Cfg` render-пака ниже (например, `.Project.Name`, `((index .Services "main").Port "http")`, `(index .Services "catalog").Enabled`).

**Render-паки (git / ide / ai, строгие):**

| Переменная | Источник |
|------------|----------|
| `.Project` | блок `project:` из `workspace.yml` |
| `.Service` | каноническая идентичность конфига — корень цепочки `extends:` рендерящегося сервиса (равно `.Resolved`, когда цепочки extends нет) |
| `.Resolved` | идентичность рендера — ключ карты сервиса, который фактически рендерится (победитель политики коллизий) |
| `.ServiceCfg` | эффективная конфигурация сервиса после разрешения `extends` |
| `.Runtime` | смерженный блок `runtime` (`.Runtime.UseHTTPS`, `.Runtime.SPX.Path`). Порты / хосты на сервис находятся в каждой записи сервиса (см. `.Services` ниже). |
| `.Services` | сервисы по имени. Используйте `(index .Services "<name>")` для выборки; хелперы записи `.Port "<port-name>"` / `.Host "<host-name>"` / `.PortScheme "<port-name>"` (возвращает `""`, если переопределения нет) / `.EffectiveScheme "<port-name>" .Runtime.UseHTTPS` (возвращает `"http"` / `"https"` после прохода по цепочке per-port → сервис → runtime). Подмножества по типу — через `.AppServices` / `.ToolServices` / `.InfraServices`. |
| `.Cfg` | объединённая конфигурация проекта (продвинутое). `.Cfg.Raw` — это дерево конфига после слияния (`services.*` подставляется из per-service файлов `service.yml`). Точечный синтаксис (`.Cfg.Raw.git.project_prefix`) работает только для identifier-safe ключей; используйте `{{ index .Cfg.Raw "my-key" }}` для ключей с дефисами, точками, ведущими цифрами и т.д. Для типовых случаев предпочитайте выделенные поля выше. |

IDE- и AI-паки рендерятся в отслеживаемые файлы проекта. Избегайте использования developer-local или секретных ключей через `.Cfg.Raw` в этих шаблонах — значения из `local.yml` дадут разные диффы у разных разработчиков. Git-хуки рендерятся в `.git/hooks/` (gitignored) и под это ограничение не попадают.

## Встроенные функции `text/template`

Стандартная библиотека предоставляет следующие функции из коробки. Полный справочник: [pkg.go.dev/text/template#hdr-Functions](https://pkg.go.dev/text/template#hdr-Functions).

| Функция | Применение |
|---------|------------|
| `eq`, `ne`, `lt`, `le`, `gt`, `ge` | Сравнение |
| `and`, `or`, `not` | Булева логика |
| `len` | Длина строки / слайса / map |
| `index` | Индексация map / слайса |
| `printf` | Форматированные строки (Go format verbs) |
| `print`, `println` | Конкатенация |
| `html`, `js`, `urlquery` | Экранирование |

Управляющие конструкции: `{{ if }}`, `{{ range }}`, `{{ with }}`, `{{ define }}` / `{{ template }}`.

```yaml
# вывести флаг только если bool-параметр true
argv:
  - "{{ if .Params.fresh }}--fresh{{ end }}"

# вложенный if / else if / else
env:
  LOG_LEVEL: |-
    {{ if eq .Params.profile "prod" }}error
    {{ else if eq .Params.profile "stage" }}warn
    {{ else }}debug{{ end }}

# range с индексом
env:
  TAGS: "{{ range $i, $t := .Params.tags }}{{ if $i }},{{ end }}{{ $t }}{{ end }}"

# with / default
cmd: "mariadb -u${vars.db.user}{{ with .Params.database }} -D{{ . }}{{ end }}"
env:
  REGION: '{{ or .Params.region "us-east-1" }}'
```

### Обрезка пробельных символов

`{{- ... -}}` срезает окружающие пробельные символы. Полезно, когда многострочный `{{ if }}`-блок должен рендериться в один shell-аргумент:

```yaml
cmd: |-
  echo "{{- if .Params.verbose -}}verbose{{- else -}}quiet{{- end -}}"
```

## Доменный хелпер: appURL

Единственный хелпер, специфичный для проекта. Строит URL из хоста, порта, HTTPS-флага и опционального path. Порт опускается, если совпадает с дефолтом схемы (80 для http, 443 для https).

Сигнатура: `appURL host port useHTTPS [path]`

```yaml
# Хостнейм приложения + порт приложения (проксируется через main-сервис)
value: '{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}'
# → "http://laravel.localhost" или "https://laravel.localhost"

# Хостнейм инструмента + порт реверс-прокси main (инструмент маршрутизируется через main-приложение, а не через свой прямой порт)
value: '{{ appURL ((index .Services "adminer").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS "/login" }}'
# → "http://adminer.localhost/login"
```

## Регистры Sprout

Следующие регистры из [go-sprout](https://docs.atom.codes/sprout/registries/) доступны везде, где вычисляются шаблоны.

| Регистр | Примеры | Описание |
|---------|---------|----------|
| `std` | `default`, `ternary`, `empty`, `coalesce` | Дефолты, условия, проверки на пустоту |
| `strings` | `hasSuffix`, `hasPrefix`, `toLower`, `toUpper`, `trim`, `replace`, `split` | Манипуляции со строками |
| `numeric` | `add`, `sub`, `mul`, `div`, `max`, `min` | Числовые операции |
| `slices` | `first`, `last`, `slice`, `join`, `reverse`, `uniq` | Операции над списками/массивами |
| `maps` | `keys`, `values`, `has`, `pick`, `omit` | Операции над map'ами/объектами |
| `regexp` | `regexMatch`, `regexReplaceAll`, `regexSplit` | Сопоставление по регулярным выражениям |
| `conversion` | `toInt`, `toFloat64`, `toString`, `toBool` | Преобразование типов |
| `time` | `now`, `date`, `dateInZone`, `duration` | Операции с датой/временем |
| `filesystem` | `pathBase`, `pathDir`, `pathExt`, `pathClean`, `osBase`, `osDir` | Манипуляции с путями |
| `semver` | `semver`, `semverCompare` | Операции над семантическими версиями |

**Герметичность по построению.** Набор хелперов собран без единой функции, которая обращалась бы к окружению, файловой системе, сети или random/crypto-источникам. Sprout-функции `shuffle` (math/rand, засеянный из crypto) и `hello` (debug-заглушка) намеренно удалены.

Полную документацию по каждой функции см. в [справочнике регистров sprout](https://docs.atom.codes/sprout/registries/).

## Резолверы scope команд

Четыре дополнительных хелпера доступны **только** внутри шаблонов `workspace/commands/`. Они принимают сырые map'ы и проходят по dot-path'ам, возвращая `""` для отсутствующего ключа (без template-ошибки).

| Хелпер | Сигнатура | Применение |
|--------|-----------|------------|
| `resolve` | `resolve .Raw "vars.db.host"` | Dot-path lookup в смерженном конфиге. Эквивалентно `${vars.db.host}`. |
| `resolveMap` | `resolveMap .Params "name"` | Lookup ключа в плоской `map[string]any`. Эквивалентно `${param.name}` / `${context.name}`. |
| `resolveFile` | `resolveFile .Files "id" "path"` | Lookup подключа в разрешённом файле-артефакте. Эквивалентно `${files.id.path}`. |
| `resolveGenerated` | `resolveGenerated .Generated "app_key"` | Per-service значение, собранное (harvested) на проходе config-рендера. Эквивалентно `${generated.app_key}`. |

Они существуют, чтобы shorthand `${...}` мог разворачиваться в переносимую Go-template форму и чтобы авторы могли дотянуться до сырого конфига, когда точечный стиль `.Raw.<x>.<y>` неудобен (ключи с точками, числовые ключи и т.д.).

## Строгий рендер (render-паки)

`render ide`, `render ai` и `render git` парсят шаблоны с семантикой `{{.Option "missingkey=error"}}`: опечатка вроде `{{.Servic.Name}}` прерывает рендер всего пака целиком, а не пишет `<no value>` на диск. Защищайте действительно опциональные поля через `{{if ...}}`:

```gotemplate
{{if .ServiceCfg.CLI.Workdir}}WORKDIR={{.ServiceCfg.CLI.Workdir}}{{end}}
```

Другие места (info, commands, условия пайплайнов, `message`) используют мягкий рендер — отсутствующий ключ резолвится в `<no value>` или пустую строку, никогда не в ошибку.


## Распространённые паттерны

| Задача | Сниппет |
|--------|---------|
| Текущая дата | `{{ now \| date "2006-01-02" }}` |
| Текущие дата и время | `{{ now \| date "2006-01-02_15-04-05" }}` |
| Базовое имя пути | `{{ .Params.script_path \| pathBase }}` |
| Директория пути | `{{ .Params.script_path \| pathDir }}` |
| Default / fallback | `{{ .Value \| default "N/A" }}` или `{{ or .Params.region "us-east-1" }}` |
| Условное значение | `{{ if eq .State "ready" }}Ready{{ else }}Not ready{{ end }}` |
| Блок с защитой от пустоты | `{{ with .Params.database }} -D{{ . }}{{ end }}` |
| Объединить список | `{{ join "," .Params.tags }}` |
| Lookup сырого конфига | `{{ resolve .Raw "vars.db.host" }}` (только команды) |
| Сборка URL | `{{ appURL ((index .Services "main").Host "web") ((index .Services "main").Port "http") .Runtime.UseHTTPS }}` |

## Соглашения и подводные камни

- **Предпочитайте `path*` вместо `os*` для путей в контейнерах.** `pathBase` / `pathDir` используют семантику прямого слэша; `osBase` / `osDir` следуют разделителю хостовой ОС. Пути в контейнерах должны быть предсказуемыми при рендере на macOS-хостах — придерживайтесь вариантов `path*`, если только вам не нужно специфичное для ОС поведение.

- **`date` — это фильтр, не конструктор.** Он принимает строку-формат и `time.Time`, а не наоборот:
  - `{{ now | date "2006-01-02" }}` ✓
  - `{{ date "2006-01-02" }}` ✗ (нет значения времени)

  Строка-формат использует референсное время Go `Mon Jan 2 15:04:05 MST 2006` — см. [шпаргалку по форматированию даты/времени в Go](https://yourbasic.org/golang/format-parse-string-time-date-example/).

- **Truthiness `when:`.** Отрендеренное значение `when:` считается truthy, если только оно не равно `""`, `"false"` или `"0"` (после trim). Сравнения, возвращающие Go-`bool`, рендерятся как `"true"`/`"false"`; сравнения, возвращающие integer-like значение (например, длины), рендерятся как десятичные строки.

- **Никаких env, FS, сети или случайности.** Шаблоны вычисляются в герметичном FuncMap по построению. Если шаблону нужно состояние проекта, выставьте его через разрешённую конфигурацию проекта (info / пайплайны) или через декларацию `context.<name>: from: <dot.path>` (команды).

- **Смешивать `${...}` и `{{ ... }}` нормально.** Они используют один контекст и рендерятся за один проход — `${...}` переписывается в template-вызовы до парсинга.

## Дополнительное чтение

- [Доки пакета `text/template`](https://pkg.go.dev/text/template) — полный справочник по языку Go-шаблонов
- [Action-синтаксис](https://pkg.go.dev/text/template#hdr-Actions) — `{{ if }}`, `{{ range }}`, `{{ with }}`
- [Встроенные функции](https://pkg.go.dev/text/template#hdr-Functions) — `eq`, `printf`, `index` и т.д.
- [Пайплайны и курсор `.`](https://pkg.go.dev/text/template#hdr-Pipelines)
- [Регистры Sprout](https://docs.atom.codes/sprout/registries/) — документация по каждой функции
- [Шпаргалка по форматированию даты/времени в Go](https://yourbasic.org/golang/format-parse-string-time-date-example/) — референсный layout `2006-01-02 15:04:05` и распространённые строки-форматы
