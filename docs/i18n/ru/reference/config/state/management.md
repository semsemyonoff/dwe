> Translated from: reference/config/state/management.md @ 73faa49bba3f

# Управление состоянием

Флаги команд, подкоманды управления, значения по умолчанию в неинтерактивном режиме и проработанные примеры для файла состояния деплоя.

## Флаги команд

### `--force`

Игнорировать файл состояния деплоя и выполнить все шаги заново с нуля.

```bash
dwe deploy run --force
```

Полезно, когда:
- Вы хотите гарантировать свежий деплой независимо от предыдущего состояния
- Файл состояния повреждён, и вы не можете его восстановить
- Вам нужно повторно выполнить шаги, которые были пропущены из-за неизменных хешей

При использовании `--force` файл состояния очищается до запуска пайплайна (все шаги считаются отсутствующими при принятии решений о пропуске).

### `--resume`

Продолжить с последнего шага, завершившегося с ошибкой или частично развёрнутого.

```bash
dwe deploy run --resume
```

Используйте это после неудачного деплоя, чтобы продолжить с того места, где он остановился, вместо повторного выполнения уже завершённых шагов.

В неинтерактивном режиме (нет TTY, нет флага `-y`/`--non-interactive`):
- Если последний запуск завершился ошибкой / частично, вы должны использовать `--resume` или `--force`, чтобы продолжить
- Без флага команда завершается с ошибкой (защитное поведение для CI)

В интерактивном режиме (TTY без `-y`/`--non-interactive`):
- Если последний запуск завершился ошибкой / частично, вам предлагается выбрать: продолжить, перезапустить все шаги (состояние игнорируется — `when:` по-прежнему применяется) или отменить

### `-y` / `--non-interactive`

Подавить все интерактивные приглашения.

```bash
dwe deploy run -y
dwe deploy run --non-interactive
```

Используйте в CI/CD пайплайнах, чтобы деплой не зависал в ожидании пользовательского ввода.

**Поведение при `-y`:**

- Если проект уже развёрнут и хеши конфигурации совпадают: завершается успешно (no-op)
- Если проект уже развёрнут, но конфигурация изменилась: применяется дельта (перезапускаются только изменённые области)
- Если последний запуск завершился ошибкой / частично: завершается с ошибкой (используйте `--force` или `--resume`, чтобы переопределить)

## Команды управления

### `dwe deploy state show`

Отобразить содержимое `.dwe/deploy/state.yml` в формате YAML.

```bash
dwe deploy state show
```

Показывает:
- Статус уровня проекта, хеш конфигурации и время последнего запуска
- Статус по каждому сервису, хеш конфигурации и время последнего запуска
- Результаты, action-хеши и длительности по каждой фазе и каждому шагу

Полезно для отладки того, почему шаг был пропущен, или для просмотра журнала после деплоя.

### `dwe deploy state clear`

Удалить файл состояния деплоя.

```bash
dwe deploy state clear
```

Эквивалентно `rm .dwe/deploy/state.yml`. В интерактивном режиме (TTY) запрашивает подтверждение. Используйте `-y`, чтобы пропустить подтверждение в CI.

```bash
dwe deploy state clear -y  # Non-interactive
```

После очистки следующий `dwe deploy run` считает все шаги отсутствующими и выполняет их заново.

### `dwe deploy state repair`

Пересобрать агрегаты статусов из записей по отдельным шагам.

```bash
dwe deploy state repair
```

Перевычисляет:
- Статус каждой фазы (из результатов шагов)
- Статус каждого сервиса (из результатов фаз)
- Статус проекта (из результатов по каждому сервису)

Сохраняет все данные уровня шагов (action-хеши, временные метки, длительности). Используйте это для исправления несогласованностей в статусах, которые могут возникнуть из-за ручных правок или непредвиденных сбоев.

## Значения по умолчанию в неинтерактивном режиме

В неинтерактивном режиме (нет TTY, `STDIN` подключён к pipe или закрыт):

| Состояние проекта | Поведение |
|---|---|
| не развёрнут | запускает пайплайн |
| развёрнут, хеш конфигурации совпадает, шагов check нет | завершается с кодом 0, no-op |
| развёрнут, хеш конфигурации совпадает, есть шаги check | запускает пайплайн; пропускает неизменные шаги, перезапускает шаги check |
| развёрнут, хеш конфигурации разошёлся | запускает пайплайн; перезапускает изменённые области, пропускает неизменные |
| last_run failed/partial | завершается с кодом 1, требует `--resume` или `--force` |

## Примеры

### Пример: полный деплой, затем пропуск при повторном запуске

```bash
$ dwe deploy run
✓ Phase setup
  ✓ create-dirs
  ✓ install
✓ Phase init
  ✓ db-create
  ✓ migrate
✓ Phase finalize
  ✓ render-ide

# State file recorded: all steps ok, hashes match

$ dwe deploy run
✓ all steps already deployed, skipped

# (Or if there were check steps:)
$ dwe deploy run
✓ Phase setup
  · create-dirs  (skipped by state)
  · install      (skipped by state)
✓ Phase init
  · db-create    (skipped by state)
  · migrate      (skipped by state)
✓ Phase finalize
  ◎ render-ide   (check re-validated)
```

### Пример: редактирование шага, перезапуск при следующем деплое

```yaml
# workspace/deploy/main.yml
- name: install
  type: command
  cmd: app.install  # was "app.install"
  # (hash was abc123)
```

Меняем команду:

```yaml
- name: install
  type: command
  cmd: app.install-prod  # changed
  # (hash is now def456)
```

```bash
$ dwe deploy run
✓ Phase setup
  · create-dirs  (skipped by state)
  ✓ install      (re-run: hash changed abc123 → def456)
✓ Phase init
  ✓ db-create
  ✓ migrate
```

Шаг install выполняется заново, потому что его хеш изменился. Шаги с неизменными хешами пропускаются.

### Пример: редактирование конфигурации сервиса, инвалидация области сервиса

```yaml
# workspace/services/main/service.yml
enabled: true
type: app
dir: services/main
depends_on:
  - db
```

Редактируем сервис main:

```yaml
# workspace/services/main/service.yml
enabled: true
type: app
dir: services/main
depends_on:
  - db
  - cache  # added dependency
```

`config_hash` сервиса меняется, поэтому все шаги `main` перезапускаются:

```bash
$ dwe deploy run
✓ Phase setup (main)
  ✓ create-dirs    (re-run: service config_hash changed)
  ✓ install        (re-run: service config_hash changed)
✓ Phase init (main)
  ✓ db-create      (re-run: service config_hash changed)
  ✓ migrate        (re-run: service config_hash changed)
```

### Пример: принудительный перезапуск всех шагов

```bash
dwe deploy run --force
```

Очищает файл состояния и выполняет все шаги заново с нуля, даже если они все успешно завершились ранее.

> **Примечание:** `--force` лишь игнорирует состояние деплоя. Условия `when:` уровня фазы и шага по-прежнему
> вычисляются при каждом запуске. Например, `when: dir-empty services/main/src` всё равно пропустит шаг install
> после того, как директория была заполнена предыдущим успешным запуском. Чтобы стереть директории сервисов,
> Docker-тома и другие артефакты так, чтобы следующий деплой был действительно чистым, используйте `dwe reset run && dwe deploy run`.

### Пример: восстановление после сбоя в середине деплоя

```bash
$ dwe deploy run
✓ Phase setup
✓ Phase init
✗ Phase finalize
  ✗ render-ide  (failed)

# Process crashed or was killed. State file recorded the failure.

$ dwe deploy run  # (in interactive mode)
# Prompted: "Failed deploy detected: Resume / Re-run all steps / Cancel"
# Choose: Resume

✓ Phase setup
  · create-dirs (skipped)
  · install     (skipped)
✓ Phase init
  · db-create   (skipped)
  · migrate     (skipped)
✓ Phase finalize
  ✓ render-ide  (re-run from where it failed)
```

Или в неинтерактивном режиме:

```bash
dwe deploy run --resume
```

## См. также

- [Обзор](index.md) — назначение, расположение файла, файл блокировки
- [Схема](schema.md) — справочник полей
- [Хеширование и решения о пропуске](hashing.md) — как определяются пропуски
