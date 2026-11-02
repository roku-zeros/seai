# Проверки 0.4.2-dev — MCP, стоимость чтения и async workflow

2 ноября 2026, macOS arm64, Go 1.26.6. Полный `go test -race -p 1 -timeout 180s ./...`: PASS (54 test functions, 20 packages, 15 с tests). `go vet ./...`: PASS. После финального изменения stored diff и MCP input schema дополнительно прошёл целевой race-набор. Python tests: 17 PASS. Все пять обновлённых skills прошли quick_validate.

MCP smoke на собранных бинарниках: PASS, stdio → Unix RPC → daemon → индекс/задача. Discovery без daemon около 0,2s на этом стенде; input schemas валидируются Draft202012. Проверены bundled contracts, project root attestation, pinned read после изменения исходника без live scan, один text без base64 duplication, запрет human approval, async create/idempotent replay, run timing, stored task/diff и неизменность product checkout. Это не измерение пользовательского проекта и не проверка фактического GigaCode.

Новые регрессии: invalid spec/evidence отклоняются даже при заведомо недостаточном freshness budget (до scan); stored task имеет UNKNOWN и не даёт readiness; async failure не создаёт задачу; runtime budget bounds/reset; UTF-8 boundaries, binary fallback и чтение файла 19333 bytes одной страницей; read-only MCP не допускает hidden mutation params или candidate calls, не принимает другой root.

Полные CLI и business smoke с JSON Schema validation: PASS после повторного прогона, включая 18 CLI проверок (backup/restore/staged upgrade) и 9 исполненных business fixtures. При промежуточном прогоне найдены hardcoded version в upgrade fixture (исправлено чтением manifest бинарника) и нехватка локального диска; кэши этой задачи очищены, промежуточные failures не считаются PASS.

Не проверены: пользовательский двухчасовой сценарий, конкретный GigaCode/trust UX, реальный Qwen endpoint и продуктовые business/check profiles. Полный отказ от shell подтверждений относится только к инструментам подключённого доверенного MCP; оставшиеся CLI/операторские действия зависят от клиентской политики. Skills не обеспечивают технический sandbox остальных tools.

---

# Исторические проверки 0.4.1-dev — большой repo, symlinks и timeouts

2 ноября 2026, macOS arm64, Go 1.26.6. Root race suite: PASS, 48 test functions, все 17 пакетов собираются, 13 имеют tests. `go vet ./...`: PASS. Оба CLI/Unix smoke с JSON Schema validation: PASS, включая прежние checks/backup/restore/upgrade и 9 бизнес-сценариев. Python tests: 15 PASS (11 installer + 4 async-wait). Два изменённых skills прошли quick_validate.

Regression coverage:

- 2629 tracked files: Observe ≈0,312s и 6 Git subprocesses на два полных прохода; durable working Capture ≈29,37s с первичным CAS fsync (6 subprocesses), warm ref Capture ≈0,864s (3). Проверяется постоянное число команд, а не хрупкий фиксированный latency threshold. Это синтетический filesystem стенд, не замер пользовательского проекта.
- Observe не создаёт CAS и даёт тот же snapshot ID, что durable Capture. Изменение bytes той же длины с восстановленным mtime обнаруживается.
- Tracked agent metadata symlinks не блокируют working/ref capture и не изменяются. Неинтересный untracked notes.txt symlink отфильтровывается до чтения; source.go symlink отклоняется.
- Freshness deadline возвращает FRESHNESS_TIMEOUT, task freshness UNKNOWN и NOT_REVIEW_READY. Async workspace check не меняет generation/epoch, новый scan обнаруживает изменения.
- Живой Unix HTTP server с медленным запросом: timeout не называется отсутствием daemon; следующий project health request обслуживается. Cancellation и отсутствующий socket различаются.
- Исторический snapshot с обычным AGENTS.md проходит CheckFiles при новой capture policy; старые traversal/secret-path guards сохранены.
- Async smoke wait принимает SUCCEEDED после 60s при большем budget; ошибка ожидания содержит run ID; FAILED/CANCELLED/ABORTED/NEEDS_INPUT не становятся PASS. Fake clock не замедляет tests.

Реальный повтор на пользовательском стенде после обновления ещё нужен. Скриншоты — evidence симптомов; их команды удаления/перенастройки/остановки процессов не исполнялись. Полная business/Qwen/GigaCode/Docker приёмка не следует из устранения performance bottleneck.

---

## Исторические проверки 0.4.0-dev — Go business index, impact и бизнес-прогоны

2 ноября 2026, macOS arm64, Go 1.26.6. Полная race suite и адресные повторы новых сценариев прошли: 41 test functions в корневом модуле; 17 пакетов собираются, 12 имеют собственные tests. Дополнительно fixtures/business имеет два Go tests и исполняемый JSON probe.

| Проверка | Результат |
|---|---|
| `go test -race -p 1 -timeout 180s ./...` и адресные повторы business/reviewer/DAG | PASS |
| `go vet ./...`, business fixture race tests/vet | PASS |
| `go mod tidy -diff`, `go mod verify` | PASS: diff пуст, all modules verified |
| CLI/daemon build | PASS: 0.4.0-dev, Go 1.26.6 |
| Installer Python tests / skills quick_validate | PASS: 11 installer tests, все 5 skills |
| Прежний CLI/Unix smoke с JSON Schema validation | PASS: structural workflow, patch/checks, restart, backup/restore и staged upgrade совместимы |
| Новый business CLI smoke с JSON Schema validation | PASS: typed Go registrations, duplicate IDs по context, flow/constants, shared impact обоих продуктов, strict candidate analysis, 9 реальных baseline/candidate fixtures, category/trace coverage, generation pinning и restart |

Component scenarios: typed struct/call/function adapters; отсутствие совпадения по одному имени у чужого типа/функции; dynamic ID/indirect handler diagnostics; config в generation identity; historical config и stale guards; plan evidence бизнес-регистрации; traversal limits; shared helper в отдельном файле затрагивает оба продукта. Candidate gate не принимает новую неизвестную регистрацию; удаление регистрации сохраняет прежний rule/context в regression obligations. Reviewer выбирает проверенный candidate generation и отвергает несовпавший checkpoint; его evidence может ссылаться на явно различённые baseline/candidate источники. Existing Qwen deterministic-stub DAG/resume tests сохранены.

Comparison tests проверяют required case categories, fixture digest при изменении coverage semantics, baseline drift, actual trace mismatch и missing coverage. CLI smoke выполняет refactor общего helper без изменения expected outcomes; он не обучает expectations по текущему stdout и не меняет основной source checkout.

Первый параллельный race build остановился при линковке из-за `no space left on device`. Удалён только выделенный build cache SEAI; последовательный повтор `-p 1` прошёл. Пользовательские файлы не удалялись.

Это проверка синтетических Go-проектов. Реальные AST-паттерны вашего продукта, GigaCode, Qwen endpoint и Docker по-прежнему требуют боевого прогона. Coverage partial: декларация/факт/trace тестового адаптера не равны полному runtime/branch coverage. Инструкция и ограничения — [BUSINESS.md](BUSINESS.md). Runtime schema остаётся 3; индекс structural-v4/business-v1 требует нового index build, старые поколения сохранены.

---

## Исторические проверки 0.3.0-dev — этапы 4–6

31 октября 2026, macOS arm64, официальный Go 1.26.6; CGO SQLite v1.14.52. Проверка локальной реализации и синтетического стенда, не приёмка продукта.

| Проверка | Результат |
|---|---|
| `go test -race -timeout 180s ./...` + адресные повторы после изменений | PASS: 36 test functions, 12 тестируемых пакетов; все 16 пакетов собираются |
| `go vet ./...`, fixture race tests/vet | PASS |
| `go mod tidy -diff`, `go mod verify` | PASS: diff пуст, all modules verified |
| CLI/daemon build Go 1.26.6 | PASS: 0.3.0-dev |
| Python installer tests | PASS: 11 сценариев |
| Skills quick_validate | PASS: все 5 skills |
| CLI/Unix smoke с JSON Schema validation | PASS: исследование/patch, реальные compile/tests/comparison, AC binding, durable logs, restart/stale/idempotency, backup/restore и staged upgrade |

Новые component scenarios: точные JSON comparisons больших чисел, missing/null, forbidden refactor delta и fixture binding; реальные timeout/cancel/process-group handling и output bounds; отсутствие terminal test evidence для неисполненных тестов; Qwen function-call roundtrip, model mismatch, HTTP retries и отсутствие токена в diagnostics; фиксированный role DAG и independent reviewer FAIL/PASS; resume без повторного patch; candidate tests проверяют границы 20/21/22. Provider — детерминированный localhost HTTP stub, без внешнего inference.

Release tests: pinned apply invalidates all tasks атомарно, конфликт не уничтожает candidate, resolution bound к промежуточному snapshot, approval сохраняется только для неизменившегося behavior, plan сбрасывается и requirements становятся not_checked. Merge tests покрывают независимые text edits, content/mode/delete conflicts. Cancellation/restart tests проверяют operation replay/conflict, mutation barrier, ABORTED и lease fencing позднего результата. Log access ограничен артефактами выбранного run; произвольный CAS digest и поддельный comparison index в stdout отклоняются.

Backup/upgrade smoke проверяет hash/integrity/foreign keys, восстановление CAS/indexes и переписанные paths, отказ overwrite/corrupt backup, lock живого daemon, offline compatibility smoke staged binary, отсутствие автоматической активации и неизменность старого runtime.db. Старые SQL migrations 001/002 не изменены; добавлена 003. Эти тесты не являются полной fault-injection/ACC/REL suite исходной спецификации.

Не проверены реальный Qwen endpoint (по решению пользователя конфигурация оставлена в README), корпоративный GigaCode, Docker backend/его resource limits и поведение модели на среднем/большом продукте. Local-approved runner не имеет OS sandbox; Qwen probe проверяет протокол, не качество модели. Pilot readiness остаётся false. Human approval/review проверены test driver через отдельный маршрут; это не доказательство личности и не аутентификация против того же OS user.

---

## Исторические проверки 0.2.0-dev — первые три этапа

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

## Область автоподключения GigaCode CLI

Проверены 11 Python tests установщика/диагностики: сохранение прежних пяти сценариев, отдельный parent workspace над Git-проектом, правила и skills в открытом корне, сохранность дочернего GIGACODE.md, idempotency, диагностика открытия неправильной папки, недопустимый workspace, различение CLI/VS Code layouts, проверка daemon root и CLI exit 3 для некорректного размещения. Проверяется установленный файловый контракт; фактическая загрузка памятью GigaCode и поведение модели без явного промпта не заявляются проверенными. Go runtime/протокол не менялись.
