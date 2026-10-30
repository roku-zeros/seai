# Проверки инкремента 0.1.0-dev

Дата: 30 октября 2026 года. Стенд: macOS arm64, Go 1.22.1, development SQLite driver с CGO. Внешние Qwen/GigaCode/репозиторий стратегий не использовались.

| Проверка | Результат |
|---|---|
| `go test -race -timeout 90s ./...` | PASS: 12 тестов в 5 пакетах |
| `go vet ./...` | PASS |
| Сборка обоих бинарников с `-trimpath -buildvcs=false` | PASS |
| `python3 scripts/smoke.py --bin <compiled-binaries>` | PASS |

Smoke проверил startup, CLI/Unix JSON-RPC, полный индекс synthetic fixture, exact search, сохранность evidence после изменения live-файла, restart daemon, idempotent retry и неизменность HEAD/live-правок пользователя. В sandbox ОС создание Unix socket было запрещено; успешный smoke выполнен с разрешённым запуском вне sandbox, без внешней сети. Runtime и fixture smoke были временными и удалены после проверки.

Это проверки начального инженерного среза. Они не заменяют полный набор ACC/REL, реальную типизацию продукта, provider smoke, проверку корпоративной изоляции, пилотные сценарии, performance benchmark и backup/restore.
