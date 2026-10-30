# Проверки обновления Go 1.26.6

Дата: 30 октября 2026 года. Стенд: macOS arm64, официальный Go 1.26.6, CGO_ENABLED=1. Системный Go не заменялся; для сборки и запуска использовался отдельный проверенный toolchain в PATH.

| Проверка | Результат |
|---|---|
| `go test -race -timeout 120s ./...` | PASS: все 9 пакетов собраны, 13 тестов в 6 пакетах (включая 5 subtests проверки версии) |
| `go vet ./...` | PASS |
| `go -C fixtures/strategy test -race ./...` | PASS |
| `go -C fixtures/strategy vet ./...` | PASS |
| `go mod verify` | PASS: all modules verified |
| `go mod tidy -diff` | PASS: нет изменений |
| Сборка CLI/daemon и `go version -m` | PASS: оба бинарника go1.26.6, зависимости соответствуют go.mod |
| Сквозной smoke через Unix socket | PASS: startup, CLI/RPC, immutable evidence, restart, idempotency, сохранность пользовательского исходника |

Дополнительно проверены AST/types generic type alias в модуле с `go 1.26.6`, совпадение toolchain индексатора с тестовым toolchain и отказ при Go ниже 1.26.6. Версия анализатора повышена до `structural-v2`; миграция SQL не менялась. Зависимости: go-sqlite3 v1.14.52, x/tools v0.52.0, x/mod v0.42.0, x/sync v0.24.0.

Toolchain: `go1.26.6.darwin-arm64.tar.gz`; SHA-256: `2dc95ce4675829f2df0e86b28bcef3283635902062a5f0580ca659bf570f3204`, проверен по [официальному каталогу Go](https://go.dev/dl/?mode=json&include=all). Smoke generation: `d9b6f98851e310e904b15b91d582aa7d9a8b77539042c16641a4a0b8336bb4c7`.

Smoke выполнен с разрешением на локальный Unix socket вне sandbox, без сети. Реальный Qwen/GigaCode, целевой Linux-стенд и полный ACC/REL набор остаются за границами этих проверок.

---

## Исторические проверки первого инкремента (Go 1.22.1)

Дата: 30 октября 2026 года. Стенд: macOS arm64, Go 1.22.1, development SQLite driver с CGO. Внешние Qwen/GigaCode/репозиторий стратегий не использовались.

| Проверка | Результат |
|---|---|
| `go test -race -timeout 90s ./...` | PASS: 12 тестов в 5 пакетах |
| `go vet ./...` | PASS |
| Сборка обоих бинарников с `-trimpath -buildvcs=false` | PASS |
| `python3 scripts/smoke.py --bin <compiled-binaries>` | PASS |

Smoke проверил startup, CLI/Unix JSON-RPC, полный индекс synthetic fixture, exact search, сохранность evidence после изменения live-файла, restart daemon, idempotent retry и неизменность HEAD/live-правок пользователя. В sandbox ОС создание Unix socket было запрещено; успешный smoke выполнен с разрешённым запуском вне sandbox, без внешней сети. Runtime и fixture smoke были временными и удалены после проверки.

Это проверки начального инженерного среза. Они не заменяют полный набор ACC/REL, реальную типизацию продукта, provider smoke, проверку корпоративной изоляции, пилотные сценарии, performance benchmark и backup/restore.

## Проверка инструкции для GigaCode

30 октября 2026: проверен shell-синтаксис всех 9 блоков runbook и синтаксис вложенных Python snippets. Выполнены подготовка отдельного demo Git-репозитория по командам инструкции, startup Unix daemon, doctor/status, индексация, поиск Eligible/MinimumAge/TestEligibleBoundary, изменение demo порога на 21 с тестами 20/21/22 и повторная индексация working tree. Все шаги PASS; snapshot/generation IDs изменились. Данные и daemon проверки удалены/остановлены.

Prompt shortcuts проверены на наличие file injection, description и args placeholder по документированному формату. Реальная загрузка shortcuts и поведение модели в установленном GigaCode не проверялись. Установка Go проверяется предыдущим отчётом; новый сетевой download-блок инструкции проверен синтаксически, повторно архив не скачивался.
