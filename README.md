# SEAI — начало реализации на Go

Рабочий фундамент по [спецификации v1](docs/specification-v1.md). Версия `0.2.0-dev`: исследование → контракт задачи → изолированный candidate. Это ещё не готовый пилот.

Реализованы два бинарника: CLI `seai` и единственный writer `seaid`. CLI обращается к daemon через Unix socket и JSON-RPC 2.0 с handshake `seai.api/v1`. Daemon сохраняет задания в SQLite (WAL, foreign keys, synchronous FULL), снимает исходники Git в SHA-256 CAS, строит отдельный индекс AST/types через `go/packages` и транзакционно публикует готовое поколение вместе с событием и результатом задания. Поиск точных идентификаторов и подстрок возвращает evidence из сохранённых байтов, а не из текущего checkout.

Рабочее дерево, HEAD и Git index пользователя не изменяются. Поддержаны явный ref и working tree с изменёнными/удалёнными tracked-файлами и разрешёнными untracked Go/testdata/go.mod/go.sum. Захват working tree выполняется дважды с сопоставлением manifests, до трёх попыток. Это проверка стабильности чтения, не блокировка внешнего редактора.

## Запуск через GigaCode

Для работы с кодом только через SEAI добавлен [набор из пяти skills и установщик](docs/GIGACODE_SKILLS.md): connect, research, plan, change, review. `GIGACODE.md` задаёт постоянную политику; `.gigacode/skills/` содержит процессы для разных задач. Прямое чтение, поиск, правки и product tests другими инструментами запрещены этой инструкцией; если capability отсутствует, агент сообщает об ограничении. Skills не являются техническим sandbox.

В отдельный целевой Git-проект набор устанавливается командой из checkout SEAI:

```sh
python3 scripts/install_gigacode_skills.py \
  --project /absolute/path/strategies \
  --seai /absolute/path/seai/build/seai \
  --data /absolute/path/seai-runtime
```

После установки перезапусти GigaCode CLI, проверь `/skills` и `/memory`. Можно написать: «Изучай код только через SEAI и объясни функцию Eligible». Для первоначальной установки самого инструмента есть [отдельный runbook](docs/AGENT_QUICKSTART.md). `/seai:setup` и `/seai:ask` в checkout SEAI теперь подключают установленный daemon и исследуют код в том же режиме.

Через SEAI доступны file/symbol reads, literal text search, typed refs/static calls, связанные тесты, ChangeSpec/plan, интерактивное подтверждение человеком, candidate patches и diff/export. [Новые команды и ограничения](docs/WORKFLOW.md). Product checks ещё отсутствуют, поэтому export только диагностический с NOT_REVIEW_READY. GigaCode на конкретной машине и native MCP пока не проверены/не реализованы.

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

При смене payload возвращается IDEMPOTENCY_CONFLICT. Устаревший epoch возвращает REVISION_CONFLICT. Если epoch не задан, CLI получает текущий; для повтора завершённой операции укажите исходный epoch явно. Только одно индексирование выполняется одновременно. Повторная публикация того же поколения не повышает epoch; новое generation повышает его. Release baseline пока не моделируется.

## Границы текущего инкремента

Реализованы paged reads из CAS, определения Go AST/types, локальные typed references, static/possible/unresolved calls и direct related tests. Текстовый поиск literal по строкам/chunks; FTS5/reranker и полный dynamic graph не реализованы. Coverage остаётся partial, конкретные type/load diagnostics видны; baseline с type errors блокирует patch.

Текущие source reads проверяют live freshness. Для исторического generation/snapshot требуется явный ID. При неизменном generation повторный index build не повышает epoch; новый generation делает старые задачи stale. Устаревшие task baselines нужно будет переносить через release reconciliation, пока оно не реализовано.

ChangeSpec/plan/candidate/решения и successful operation results сохраняются в runtime migration 002. Patch не изменяет основной checkout и требует scope/preconditions/ревизии/approval для behavior changes. Проверки и независимый review пока не выполняются: всегда NOT_REVIEW_READY, экспорт требует --allow-incomplete. [Подробный протокол](docs/WORKFLOW.md).

Single-module offline profile: GOPROXY=off, GOSUMDB=off, GOTOOLCHAIN=local, GOWORK=off, CGO_ENABLED=0, GOPACKAGESDRIVER=off. Symlink/submodule, go.work и local replace неподдержаны. Generated code не запускается. Исключены .git/.seai/vendor/.env/*.key по snapshot policy; retention/GC/ACL/квоты и staged provenance не завершены.

Qwen/workers, product runner, domain extractors, incremental index, MCP/broker, release rebase и backup/restore остаются следующими этапами. `pilot_ready=false`. Skills — инструкции, не sandbox. Интерактивное CLI approval не является аутентификацией против другого процесса того же OS user.

## Проверка

Unit/component tests проверяют канонические identities без потери целочисленной точности, CAS corruption, path traversal, symlink, dirty/ref snapshots, idempotency, optimistic epoch, rollback публикации, checksums миграции и recovery заданий. Интеграционный тест строит индекс синтетического Go-репозитория и проверяет поиск после live-правки и открытия runtime заново. `make smoke` дополнительно запускает реальные бинарники, Unix RPC и restart daemon.

Зависимости обновлены для Go 1.26.6: `golang.org/x/tools v0.52.0`, `golang.org/x/mod v0.42.0`, `golang.org/x/sync v0.24.0`, `github.com/mattn/go-sqlite3 v1.14.52`. После обновления остановите старый seaid, пересоберите бинарники, запустите новый daemon с прежними --repo/--data/--project, затем выполните `index build --wait`: версия анализатора повышена до `structural-v3`, новое задание создаст отдельное поколение. Runtime получает additive migration 002; применённая SQL migration 001 не изменена.

Технические основания: [go/packages](https://pkg.go.dev/golang.org/x/tools/go/packages) и [SQLite WAL](https://www.sqlite.org/wal.html). Версии для корпоративной поставки требуют отдельной проверки, текущие pins выбраны по совместимости с Go 1.26.6 и закреплены в go.mod/go.sum.
