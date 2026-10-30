# SEAI — начало реализации на Go

Рабочий фундамент по [спецификации v1](docs/specification-v1.md). Версия `0.1.0-dev`; это первый инженерный инкремент, не готовый пилот.

Реализованы два бинарника: CLI `seai` и единственный writer `seaid`. CLI обращается к daemon через Unix socket и JSON-RPC 2.0 с handshake `seai.api/v1`. Daemon сохраняет задания в SQLite (WAL, foreign keys, synchronous FULL), снимает исходники Git в SHA-256 CAS, строит отдельный индекс AST/types через `go/packages` и транзакционно публикует готовое поколение вместе с событием и результатом задания. Поиск точных идентификаторов и подстрок возвращает evidence из сохранённых байтов, а не из текущего checkout.

Рабочее дерево, HEAD и Git index пользователя не изменяются. Поддержаны явный ref и working tree с изменёнными/удалёнными tracked-файлами и разрешёнными untracked Go/testdata/go.mod/go.sum. Захват working tree выполняется дважды с сопоставлением manifests, до трёх попыток. Это проверка стабильности чтения, не блокировка внешнего редактора.

## Запуск через GigaCode

Для самостоятельной установки и первого прогона агентом есть [пошаговая инструкция](docs/AGENT_QUICKSTART.md): окружение и локальный Go, сборка, проверки, отдельный demo Git-репозиторий, daemon/PID/логи, поиск, вопросы и пробная правка. Открой этот checkout в GigaCode и отправь:

```text
@docs/AGENT_QUICKSTART.md Выполни установку и демонстрацию по шагам 1–4, затем покажи отчёт. Правило пока не меняй.
```

Для GigaCode CLI добавлены Markdown prompt shortcuts `/seai:setup` и `/seai:ask Как работает Eligible?` в `.gigacode/commands/seai/`. Их обнаружение зависит от версии клиента и ещё не проверено на реальной установке GigaCode; прямое прикрепление инструкции остаётся основным способом. Специальный программный адаптер и MCP пока отсутствуют.

## Требования и сборка

Целевая версия — Go 1.26.6 (оба go.mod, все cmd/internal пакеты и тестовые репозитории). Нужны Git, Go 1.26.6+, CGO и C compiler для текущего SQLite-драйвера. Windows пока не поддерживается: используются Unix socket и flock.

Установите Go 1.26.6 из [официальных загрузок](https://go.dev/dl/#go1.26.6) и убедитесь, что его `bin` находится первым в `PATH`. Индексатор использует `go` из `PATH`; при версии ниже 1.26.6 daemon возвращает `UNSUPPORTED_TOOLCHAIN`. Автоматическое скачивание toolchain в процессе индексации отключено.

```sh
go version # для воспроизведения проверок: go1.26.6
go mod download
make build
make test
make vet
make smoke
```

Зависимости закреплены в go.mod/go.sum. Для сборки без сети нужны заранее заполненные module cache и toolchain; offline bundle ещё не подготовлен. В проверочном окружении использовались `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local` и отдельный доступный для записи `GOCACHE`.

## Запуск

Нужен существующий Git-репозиторий с хотя бы одним commit и корневым go.mod. Runtime размещается **за пределами репозитория**, в локальном каталоге владельца с правами 0700. Выбирайте короткий путь: Unix socket ограничен длиной пути.

В первом терминале:

```sh
./build/seaid --repo /absolute/path/strategies --data /tmp/seai-demo --project strategies-pilot
```

Во втором:

```sh
./build/seai --data /tmp/seai-demo --json doctor
./build/seai --data /tmp/seai-demo project status
./build/seai --data /tmp/seai-demo index build --ref HEAD --wait
./build/seai --data /tmp/seai-demo search Eligible
# Без --ref захватывается working tree.
./build/seai --data /tmp/seai-demo index build --wait
```

`--data` и `--json` — глобальные флаги, ставятся перед командой. Сейчас весь вывод CLI — JSON; текстовый formatter не реализован. Daemon запускается явно, без скрытого фонового старта и без прямого доступа CLI к SQLite. Проект регистрируется при первом запуске daemon; YAML `project init --config` пока отсутствует.

Без `--wait` index build сразу возвращает job_id. Статус: `seai --data /tmp/seai-demo job status <job_id>`. Ctrl+C при ожидании прерывает клиент, серверное задание продолжает работу (предел 10 минут). SIGTERM daemon перестаёт принимать запросы и ожидает активный индексатор. После аварии QUEUED/RUNNING задания получают ABORTED; повторять их нужно новым operation_id. Успешное задание остаётся успешным после перезапуска.

Для точного повтора передавайте **те же** operation_id, ref и epoch:

```sh
./build/seai --data /tmp/seai-demo index build --ref HEAD --operation-id demo-001 --epoch 0 --wait
```

При смене payload возвращается IDEMPOTENCY_CONFLICT. Устаревший epoch возвращает REVISION_CONFLICT. Если epoch не задан, CLI получает текущий; для повтора завершённой операции укажите исходный epoch явно. Только одно индексирование выполняется одновременно. Повторная публикация того же поколения новым заданием повышает epoch; release baseline пока не моделируется.

## Границы текущего инкремента

- Индексирует определения функций/методов, типов, констант и переменных; `go/packages` выполняет загрузку AST/types с test variants. Typed references, CALLS, FTS, graph expansion, предметные связи и отдельные signatures пока отсутствуют.
- Coverage всегда `partial`. Ошибки загрузки/типизации и ограничения возвращаются в поиске. Частичный структурный индекс допустим для исследования, готовность change workflow из него не следует.
- Поиск читает текущее **опубликованное поколение**. Автоматического freshness detector пока нет: последующие live-изменения не обновляют индекс. Результат содержит snapshot/generation IDs. Для новых исходников явно пересоберите индекс.
- `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOWORK=off`, `CGO_ENABLED=0`, `GOPACKAGESDRIVER=off`. Go environment наследуется по allowlist; GOFLAGS фиксирован. Это профиль загрузки исходников, не изоляция исполнения тестов ОС.
- Symlink/submodule, go.work и любые local replace возвращают UNSUPPORTED_SOURCE. Внешние modules не индексируются; недоступные зависимости становятся diagnostics. Вложенные Go modules не имеют отдельного traversal.
- Исключаются `.git/**`, `.seai/**`, `vendor/**`, файлы `.env` и `*.key`. Настраиваемый YAML allowlist и отдельный staged/unstaged provenance ещё не реализованы. Не используйте реальные секреты в исследуемых репозиториях; production ACL/retention/GC остаются открытыми P0/P1.
- Полное перестроение вместо incremental. Старые поколения удерживаются без GC. Crash staging/orphan objects автоматически не удаляются. Индекс не содержит model output и не запускает go generate или тесты.
- Отсутствуют ChangeSpec/approval, tasks/DAG/workers, Qwen, patch/check runner, release reconciliation/rebase, backup/upgrade/restore, MCP и программный GigaCode-адаптер (доступны только prompt shortcuts). `doctor` честно возвращает `pilot_ready=false` и причины.

Неподдержанные RPC-команды завершаются METHOD_NOT_FOUND; нет имитации выполненных задач. [Текущий статус и план](docs/IMPLEMENTATION.md), [паспорт P0](docs/P0.md), [контракты](schemas/).

## Проверка

Unit/component tests проверяют канонические identities без потери целочисленной точности, CAS corruption, path traversal, symlink, dirty/ref snapshots, idempotency, optimistic epoch, rollback публикации, checksums миграции и recovery заданий. Интеграционный тест строит индекс синтетического Go-репозитория и проверяет поиск после live-правки и открытия runtime заново. `make smoke` дополнительно запускает реальные бинарники, Unix RPC и restart daemon.

Зависимости обновлены для Go 1.26.6: `golang.org/x/tools v0.52.0`, `golang.org/x/mod v0.42.0`, `golang.org/x/sync v0.24.0`, `github.com/mattn/go-sqlite3 v1.14.52`. После обновления SEAI выполните `index build --wait`: версия анализатора повышена до `structural-v2`, новое задание создаст отдельное поколение. Старый runtime schema v1 сохраняется; SQL-миграция не изменена.

Технические основания: [go/packages](https://pkg.go.dev/golang.org/x/tools/go/packages) и [SQLite WAL](https://www.sqlite.org/wal.html). Версии для корпоративной поставки требуют отдельной проверки, текущие pins выбраны по совместимости с Go 1.26.6 и закреплены в go.mod/go.sum.
