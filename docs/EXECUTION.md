# SEAI 0.3: проверки, Qwen, релизы и обслуживание

Это продолжение этапов 1–3 из [WORKFLOW.md](WORKFLOW.md). API остаётся `seai.api/v1`; runtime получает additive migration 003. Go — 1.26.6. Реальный Qwen endpoint, GigaCode и Docker в этой поставке не проверены; по умолчанию provider и checks не настроены. Синтетические тесты не подтверждают готовность продуктового пилота.

## Регистрация проверок

Оператор передаёт `seaid --repo REPO --data DATA --project PROJECT --config /absolute/runtime.json`. Конфигурация читается при запуске; после её изменения нужен перезапуск. Начальный файл — `config/runtime.example.json`, контракт — `schemas/runtime-v1.schema.json`. Модель не может регистрировать команды или менять config через RPC. Зарегистрированная команда — argv без добавления shell из RPC; программа из проекта исполняется только по этому профилю.

Профиль задаёт id, kind (`compile`, `tests`, `comparison`), program, args, isolation, timeout_seconds (1–900), max_output_bytes (1 KiB–1 MiB на каждый поток), memory_mb и cpus. Режимы:

- `docker`: локально доступные Docker и образ `image@sha256:...`; автоматическое скачивание отключено. Нет сети, writable root, capabilities и host credentials; ограничены CPU/RAM/PIDs. Рабочая копия writable, контейнер удаляется после завершения/отмены. Образ должен содержать toolchain и нужные зависимости. Этот backend требует проверки на вашей машине.
- `local-approved`: абсолютный путь к executable и `acknowledge_local_execution:true`. Команды выполняются на временной копии, но **не изолированы ОС**: исполняемый код может обращаться к доступным пользователю файлам и сети. CPU/RAM в этом режиме не ограничиваются; действуют timeout, process-group cancellation и предел логов. Для доверенного кода и явного решения оператора.

Go-команды работают offline с CGO=0, без go.work, toolchain downloads и изменения module graph. Зависимости должны быть доступны заранее. Код, которому нужен CGO, требует другого зарегистрированного профиля/образа. Исполнение не получает токен Qwen из окружения daemon.

```sh
seai --data DATA checks profiles
seai --data DATA checks run TASK --profile compile
seai --data DATA task verify TASK
seai --data DATA run status RUN_ID
seai --data DATA run logs RUN_ID --profile tests
seai --data DATA run artifact RUN_ID --digest LOG_OR_MARKER_DIGEST
seai --data DATA run cancel RUN_ID
```

Длительные команды возвращают `run_id`, который опрашивается через `run status`. Отмена не удаляет уже сохранённые candidate/checkpoint. Пока run активен, новые mutation/index jobs блокируются. Повтор с тем же operation_id и точным payload возвращает прежнее принятие; новый payload с прежним ID отклоняется. После рестарта незавершённый run становится ABORTED и требует явного нового запуска.

Compile — реальный exit status. Tests требуют `go test -json -count=1` и непустой список `test_names`: каждый тест должен реально начать выполнение и завершиться PASS; пустой, skipped или cached-only прогон не считается доказательством. Compile/test checks выполняются на candidate.

Comparison запускает каждую fixture дважды на baseline и дважды на candidate с одинаковым stdin. stdout должен быть одним JSON-значением. Порядок массивов значим; отсутствующее поле, null и zero различаются; числа сравниваются без float64-округления. Нестабильный результат даёт NON_REPRODUCIBLE. Candidate сверяется с `expected`, а baseline→candidate delta — с точными JSON pointers в `allowed_paths`. У refactor любые изменения вывода запрещены. Полные stdout/stderr находятся в CAS; comparison stdout log содержит перечень digest логов всех запусков.

Чтобы comparison покрывал критерий приёмки, в ChangeSpec criterion должны совпадать `id`, `assertion_profile` и **`fixture_digest` из `checks profiles`**. Изменение fixtures/expected/allowed_paths требует нового digest в spec и, для change, нового подтверждения behavior. Просто совпавшее имя теста не покрывает критерий. Для обычного export нужны compile + tests + comparison, все проверки плана, покрытие всех AC и независимый review текущего набора результатов. Отсутствие настройки оставляет NOT_REVIEW_READY.

При смене spec/plan/candidate/config/epoch старые доказательства остаются историей. Новый check set требует нового review. Исторический/stale/cancelled task не становится REVIEW_READY. CHECKS_PASSED и REVIEW_READY — вычисляемые gates; state задачи отдельно сохраняет состояние редактирования. Автоматического COMPLETED/изменения исходного checkout нет.

Без Qwen независимый человек может после изучения diff/check reports выполнить:

```sh
seai --data DATA task review TASK --actor reviewer-name
seai --data DATA task status TASK
seai --data DATA task export TASK --output /new/result.patch
```

`task approve`, `task review` и `task requirements` требуют точного подтверждения через `/dev/tty`. GigaCode не выполняет эти команды за человека. Это локальное разделение workflow, не аутентификация против другого процесса с тем же OS user.

## Синтетический пример без Qwen

Используйте отдельный Git-репозиторий, скопировав туда `fixtures/strategy`, и выполните git init/add/commit. У fixtures есть `cmd/probe`: stdin `{"age":18}`, stdout с eligible/reason. Для него генерируется config:

```sh
python3 scripts/configure_fixture_runtime.py \
  --output /absolute/new-runtime.json --go /absolute/go/bin/go \
  --approve-local-execution
```

Запустите daemon с этим config, выполните `index build --wait`, установите skills в открытый GigaCode workspace и попросите: «Через SEAI исследуй Eligible и подготовь refactor с сохранением поведения. Включи compile/tests/comparison и привяжи AC1 к eligibility-v1 и fixture_digest из checks profiles. Применяй изменения только в candidate, затем task verify; human review оставь мне». Генератор предназначен только для fixture, чьи AC имеют ID `AC1`; для реального проекта оператор определяет свои проверки и ожидаемые outputs. Изменение порога потребует новых fixtures, tests и человеческого согласования.

## Настройка Qwen пользователем

Qwen оставлен **не настроенным**, согласно вашему решению. В runtime JSON добавьте:

```json
"provider": {
  "endpoint": "https://YOUR-ENDPOINT/v1",
  "model": "YOUR-QWEN-MODEL-ID",
  "api_key_env": "SEAI_QWEN_API_KEY",
  "allow_loopback_http": false,
  "context_tokens": 32768,
  "max_output_tokens": 4096,
  "timeout_seconds": 120,
  "max_calls": 30,
  "max_task_tokens": 200000
}
```

Endpoint — base URL без `/chat/completions`: адаптер добавляет этот путь сам. Имя model должно совпадать с response.model endpoint. API — OpenAI-compatible Chat Completions с function tool calls, без streaming. Укажите лимиты по реальной модели; SEAI использует консервативный byte-based estimate, который может остановить запрос раньше реального token limit. Токен задайте в `SEAI_QWEN_API_KEY` в окружении запуска seaid; **не записывайте токен в config, README, spec или Git**. Если авторизация не нужна, api_key_env может быть пустым. HTTP разрешён только для loopback и при allow_loopback_http:true. Redirects запрещены.

После перезапуска seaid:

```sh
seai --data DATA provider status
seai --data DATA provider probe
seai --data DATA run status RUN_ID
seai --data DATA task run TASK
seai --data DATA agents list RUN_ID
```

Probe проверяет полный function-call roundtrip и точный ответ. Только успешный probe включает provider_ready на текущий сеанс. Он подтверждает протокол endpoint, а не качество модели. `multi_agent_ready`/`pilot_ready` остаются false до проверки реального продукта. HTTP retries ограничены тремя попытками на Chat call; тела ошибок и токены не попадают в сообщения об ошибках. Usage, если endpoint её не прислал, не объявляется измеренной.

Worker DAG фиксирован: rule researcher и impact researcher с отдельными контекстами → test designer и implementer → deterministic patch integrator → registered checks → независимый reviewer. До двух исследователей одновременно; остальные этапы последовательны. Reviewer получает spec/diff/реальные check reports без диалога implementer. Model tools разрешают только SEAI reads, без shell, approval, release или DB access. Typed graph остаётся baseline graph; чтение изменённого кода идёт по candidate snapshot. Structured results, evidence IDs, attempts, leases/heartbeat и bounded context/call budgets сохраняются. Это фиксированный pipeline, не произвольный распределённый scheduler и не автоматическое разбиение больших задач.

Если run остановился после сохранения candidate, возобновите:

```sh
seai --data DATA task run TASK --resume-from FAILED_RUN_ID
```

Resume допускается только для неизменившегося checkpoint (task/config/baseline/plan/candidate/epoch). После успешной интеграции патч повторно не применяется: checks/reviewer запускаются заново. Если сбой произошёл до durable checkpoint, сохранённый candidate доступен для изучения, но несовпадающий resume отклоняется. Тестовый Qwen stub проверяет протокол и этот путь; реальный inference остаётся вашей настройкой и отдельной проверкой.

## Обновление исходного релиза

```sh
seai --data DATA release plan --ref origin/main --release-id product-v2
seai --data DATA run status RUN_ID
seai --data DATA release inspect PLAN_ID
seai --data DATA release apply PLAN_ID
seai --data DATA run status APPLY_RUN_ID
```

Нужный ref должен уже существовать локально. Plan фиксирует SHA, source snapshot, extractor, epoch, changed paths и affected tasks. SEAI не делает fetch/checkout/stash. Apply строит новое поколение и одной транзакцией меняет pointer/epoch/active release и выставляет reconciliation_required всем задачам. Старые индексы, checks и candidates сохраняются. Если checkout отличается от активного release snapshot, обычные live-команды сообщат STALE; оператор самостоятельно синхронизирует checkout с выбранным релизом. Explicit snapshot reads и rebase используют сохранённые снимки.

```sh
seai --data DATA task rebase TASK
seai --data DATA run status REBASE_RUN_ID
```

Three-way merge: old baseline / candidate / new release, включая add/delete/mode/binary conflicts. При конфликтах исходная задача сохраняется; результат содержит `conflicts`, `resolution_base_snapshot_id` и CAS digest conflict markers. Можно прочитать промежуточный snapshot через `read --snapshot`, а conflict markers — через `run artifact REBASE_RUN_ID --digest MARKER_DIGEST`. Передайте resolution Patch, привязанный к этому snapshot и текущему spec; он должен покрыть все конфликтующие пути:

```sh
seai --data DATA task rebase TASK --resolution /absolute/resolution.json
```

Если baseline включал незакоммиченные изменения, обязательно явно выбрать `--overlay-policy task-only` (перенос только baseline→candidate) либо `carry` (clean baseline commit→candidate, включая унаследованные изменения). Политика и результат сохраняются в provenance/history; автоматического выбора ours/theirs нет. Успех меняет baseline/candidate/epoch, очищает plan, инвалидирует check gates и помечает requirements как not_checked. Неизменившийся behavior approval сохраняется, но требования после смены релиза должен подтвердить человек:

```sh
seai --data DATA task requirements TASK --actor requirements-owner
```

Далее нужен актуальный plan, повторные checks/review. `release rollback --release-id product-v1` переключает на ранее зарегистрированный release с **новым epoch** и тем же reconciliation barrier; не откатывает всю базу. Чтобы иметь rollback к исходному релизу, сначала зарегистрируйте его через release plan/apply. Завершённые продуктовые задачи/внешние requirements trackers пока отдельно не моделируются.

## Backup, restore и обновление самого SEAI

```sh
seai --data DATA backup create
seai --data DATA run status RUN_ID
# backup_path из результата; manifest.json содержит SHA-256 всех файлов
seai --data /absolute/NEW_DATA backup restore --from /absolute/BACKUP_PATH
```

Backup создаёт согласованный SQLite VACUUM INTO snapshot и копии CAS/indexes. Sockets, locks, config, токен Qwen, временные staging directories и source checkout не копируются. Backup хранится внутри DATA/backups; для защиты от потери диска скопируйте готовый каталог на отдельное хранилище. Restore работает offline в **новый несуществующий каталог**, проверяет paths/symlinks/hashes/integrity/FK, мигрирует runtime 2→3 и переписывает абсолютные index paths. Старые незавершённые jobs/runs становятся ABORTED. Backup и CAS могут содержать код/требования: храните их приватно. Контрольные суммы обнаруживают порчу; это не подпись доверенного поставщика.

Для обновления бинарников оператор сначала собирает или получает одобренные локальные seai/seaid. Bundle manifest:

```json
{
  "schema_version": "seai.upgrade/v1",
  "product_version": "0.3.0-dev",
  "api_version": "seai.api/v1",
  "runtime_schema": 3,
  "seai": "/absolute/new/seai",
  "seai_digest": "SHA256_OF_SEAI",
  "seaid": "/absolute/new/seaid",
  "seaid_digest": "SHA256_OF_SEAID"
}
```

Остановите daemon, затем:

```sh
seai --data OLD_DATA upgrade prepare \
  --bundle /absolute/bundle.json --output /absolute/NEW_INSTALLATION
```

Команда требует эксклюзивную daemon.lock, проверяет hashes/version/API/schema, создаёт согласованный backup, восстанавливает/мигрирует данные в NEW_INSTALLATION/data, копирует бинарники и запускает новый seaid --check-data. Поддержан только bundle текущего `0.3.0-dev` и runtime 2/3; произвольные будущие миграции не обещаются. Бинарники bundle — исполняемый код оператора: checksum устанавливает идентичность, а не доверие. Старые данные/бинарники не заменяются; **автоматической активации нет**. Ошибка удаляет новую подготовленную установку.

После успеха вручную запустите NEW_INSTALLATION/seaid с NEW_INSTALLATION/data, прежними repo/project и вашим config. Обновите пути установки skills/session. До начала новой работы rollback — остановить новый daemon и запустить прежний с OLD_DATA. После новой работы сначала сохраните новый backup: простой возврат к старому runtime потеряет новую историю. Downgrade schema 3 старым бинарником не поддерживается. `seaid --version` печатает manifest, `seaid --data DATA --check-data` offline мигрирует и проверяет SQLite под lock.
