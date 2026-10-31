# Проверки 0.2.0-dev — первые три этапа

Стенд: macOS arm64, Go 1.26.6, CGO SQLite v1.14.52; YAML v3.0.1. Это верификация локального generic Go workflow, не готовность корпоративного пилота.

| Проверка | Результат |
|---|---|
| `go test -race -timeout 120s ./...` | PASS: 26 test functions в 9 пакетах, все 12 пакетов собраны |
| `go vet ./...`, `go mod tidy -diff`, `go mod verify` | PASS; зависимости закреплены и проверены |
| Сборка CLI/daemon | PASS: Go 1.26.6, версия SEAI 0.2.0-dev |
| JSON Schema syntax и ответы API в smoke | PASS: read/list/symbol/text/refs/task/diff/job responses проверены по схемам |
| Python installer tests | PASS: 5 сценариев сохранности/конфликтов/idempotency/layout |
| Skills quick_validate | PASS: 5 обновлённых skills |
| CLI → Unix daemon smoke | PASS: поиск/чтение/refs/direct tests, spec → plan → immutable patch → diff/export, cancel/resume/restart, freshness, исходный checkout не меняется |

Unit/component scenarios: корректность local typed refs и possible interface/unresolved function-value calls; исключение Test-prefixed функций из обычных файлов из direct related tests; исторические source bytes и stale guards; cursor scope; requirement hash/readiness и behavior digest; human approval gate и отсутствие approval в general tool dispatcher; revision/epoch/idempotency, включая общий namespace index/task; повторный build того же generation без invalidation задач; before/after hashes, path/scope/mode/binary guards; binary Git patch roundtrip, create-only/delete-only diffs; restart кандидата/плана/решений; additive migration с 001 на 002 без потери проекта и checksum drift rejection.

Smoke использует синтетический **refactor** с сохранением поведения и не выдаёт его за утверждённое изменение бизнес-правила. Проверка approval в application tests — test driver dedicated route, а не доказательство личности человека. Реальный интерактивный workflow на корпоративной версии GigaCode и OS permission enforcement не проверены. Product runner отсутствует: exported artifact всегда NOT_REVIEW_READY; внешняя проверка применимости Git patch не является registered check run SEAI.

Один повтор итогового smoke потребовался из-за timeout automatic permission review, без запуска процесса в отклонённой попытке; последующий разрешённый локальный прогон выполнен без сети. Исходные SQL 001 и исторические свидетельства ниже сохранены.

---

## Исторические проверки обновления Go 1.26.6

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

## SEAI-only skills

30 октября 2026: пять SKILL.md прошли quick_validate.py skill-creator (frontmatter/имена/описания/scaffold). Пять component tests установщика PASS: сохранность пользовательской памяти и ignore-файла, идемпотентность, отсутствие частичной установки при конфликте, сохранность отредактированной managed-политики, Git root/runtime guards, VS Code layout и отказ на symlink destination. Проверка модели в реальном GigaCode не выполнялась; policy не является техническим ограничением native tools. Go backend не менялся.
