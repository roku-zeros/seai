# SEAI: техническая спецификация реализации и пилота

**Версия:** 1.0 · **Дата:** 30 октября 2026 года  
**Статус:** проект для передачи в разработку после закрытия блокирующих вопросов этапа P0.  
**Основание:** `qwen_strategy_engine_spec.md`, редакция 2.0 от 30.10.2026.  
**Аудитория:** технический лидер, разработчики SEAI, пилотный разработчик стратегий, QA и владелец требований.

## 1. Назначение документа и принятые решения

Документ переводит исходное ТЗ в реализуемые модули, данные, протоколы, алгоритмы, задачи и проверяемые условия готовности. Это спецификация будущей системы. Примеры API, конфигураций и бизнес-сценариев ниже являются проектными контрактами, а не описанием уже существующих возможностей корпоративной среды. Исходный документ рассматривается как материал для анализа; его организационные указания не являются командами на выполнение работ в текущем окружении.

Ключевое уточнение: **пилот на одном разработчике означает одного пользователя инструмента**, а не отмену субагентного runtime. В пилоте остаются независимые контексты работников и хотя бы один сценарий с несколькими работниками. При последовательном inference они могут исполняться последовательно; требование ускорения проверяется только на подходящем стенде.

Вводятся два независимых процесса обновления:

1. **Обновление исходной системы стратегий:** установка релиза, смена checkout, обновление `master` или другого базового ref. Меняются снимки, граф, ссылки, планы и проверки задач.
2. **Обновление SEAI:** установка бинарника, миграции данных, изменение схем, экстракторов, промптов, модели и клиентского адаптера.

Под «актуализировать всё» понимается обновить производные данные и явно перечислить то, что нельзя подтвердить автоматически. Новые требования аналитика, неизвестные регистрации правил и конфликтующие патчи нельзя достоверно восстановить только из нового кода.

### 1.1. Архитектурные решения

| ID | Решение | Причина и следствие |
|---|---|---|
| ADR-01 | Локальное ядро на Go, CLI и один локальный daemon на проект | Единая логика; сохранение задач при закрытии GigaCode |
| ADR-02 | SQLite: отдельная БД runtime и отдельная БД каждого поколения индекса | Ошибка перестройки индекса не повреждает журнал задач |
| ADR-03 | Индекс неизменяемого снимка; новая версия публикуется указателем | Нет частично обновлённого графа в поиске |
| ADR-04 | Собственные workers с адаптером Qwen; GigaCode — клиент | Не предполагаем существование native subagent API |
| ADR-05 | Все изменения кода в выделенных рабочих копиях | Пилот не требует отдавать основное рабочее дерево агенту |
| ADR-06 | Сначала AST/types + exact/FTS + предметные экстракторы | Проверяемая база; embeddings не блокируют пилот |
| ADR-07 | Release reconciliation — обязательная функция пилота | Работа должна продолжаться после обновления master |
| ADR-08 | Производные результаты имеют fingerprint зависимостей | Решение о повторном использовании принимает код |
| ADR-09 | Исторические свидетельства сохраняются неизменными | Старые доказательства доступны, но не становятся текущими |
| ADR-10 | Один активный мутирующий workflow на проект в пилоте | Упрощение блокировок; параллельные workers работают внутри workflow |

### 1.2. Что сохраняется и что откладывается

Обязательны для пилота: самостоятельный ChangeSpec, Go-анализ, предметный поиск одного продуктового потока и его shared-зависимостей, CLI, GigaCode-адаптер, собственный runtime работников, изоляция патчей, проверки, журнал, resume, обновление базы после релиза и обновление SEAI с восстановлением.

После пилота: все продукты и NRT в полном объёме, SSA, embeddings/reranker, прямые Jira/Confluence API, автоматическая настройка concurrency, масштабирование на несколько пользователей. В пилоте требования импортируются из разрешённого файла с revision и digest. Для неохваченной предметной области работает структурный поиск, а предметная полнота явно ограничена.

Исходные требования о 30 задачах, закрытом наборе, сравнении с SRE, ускорении и токенах сохраняются для полной приёмки. Пилот не объявляется доказательством превосходства системы.

## 2. Сценарии пилота и границы ответственности

### 2.1. Сценарии пользователя

| ID | Сценарий | Завершающий результат |
|---|---|---|
| U-01 | Установить SEAI и подключить репозиторий | Capability report, конфигурация, первый готовый индекс |
| U-02 | Объяснить правило или параметр | Ответ с источниками, ограничениями анализа и снимком |
| U-03 | Изменить правило по утверждённому требованию | Изолированный diff, проверки, отчёт по критериям |
| U-04 | Остановить клиент/daemon и продолжить | Задача восстановлена без повторного патча |
| U-05 | Установить новый релиз стратегий | Новый индекс, перечень устаревших задач и результатов |
| U-06 | Продолжить незавершённое изменение на новом релизе | Новый candidate, разрешённые конфликты, повторные проверки |
| U-07 | Обновить SEAI | Совместимые данные и адаптер, сохранённая возможность отката |
| U-08 | Вручную поправить результат агента | Новый снимок candidate и повторная валидация изменённого результата |

### 2.2. Ответственность

- Владелец бизнес-требования определяет ожидаемое поведение и подтверждает существенные изменения контракта.
- Пилотный разработчик выбирает репозиторий/релиз, формулирует задачу, разрешает смысловые конфликты, проверяет итоговый diff и переносит его в обычный процесс разработки.
- SEAI собирает доказательства, исполняет ограниченный план, готовит кандидат и сообщает, какие проверки выполнены.
- Ответственный за поставку SEAI фиксирует совместимые версии, упаковывает offline-зависимости, проверяет обновление и восстановление.

Пилотный разработчик может совмещать роли технического владельца и оператора. Это не даёт модели права утверждать бизнес-правила за аналитика. Push, PR и deployment не входят в локальный workflow пилота.

### 2.3. Обычный путь работы

```text
seai doctor --json
seai project init --config pilot-project.yaml
seai index build --ref <проверенный-ref>
seai task create --spec .seai/changes/CHG-0042/spec.yaml
seai task plan CHG-0042
seai task run CHG-0042 --mode auto
seai task status CHG-0042
seai task diff CHG-0042
seai task verify CHG-0042
seai task report CHG-0042
seai task export CHG-0042 --format patch --output change.patch
```

Все команды ниже — интерфейс, который предстоит реализовать. Неизвестные версии GigaCode и Qwen не считаются поддержанными до P0.

## 3. Структура поставки и программные модули

```text
cmd/seai/                       # пользовательский CLI
cmd/seaid/                      # daemon и локальный RPC
internal/application/           # use cases и транзакционные границы
internal/domain/                # состояния, fingerprints, инварианты
internal/workspace/             # Git, снимки, dirty-state, worktrees
internal/indexer/               # AST/types, поколения, incremental
internal/extractors/            # регистрации, правила, параметры, маршруты
internal/retrieval/             # exact, FTS, graph expansion
internal/context/               # evidence и context packs
internal/changespec/            # загрузка, canonical hash, валидация
internal/orchestrator/          # DAG, leases, retries, бюджеты
internal/workers/               # роли и отдельные model sessions
internal/providers/qwen/        # provider protocol и capabilities
internal/patches/               # preconditions, staging, интеграция
internal/checks/                # runner, test selection, comparison
internal/reconcile/             # release update и task rebase
internal/storage/               # SQLite, CAS, migrations
internal/transports/            # local RPC, MCP, CLI formatting
internal/clientprofiles/        # проверенные профили GigaCode
schemas/                        # versioned JSON Schema
migrations/                     # ordered runtime migrations
adapters/gigacode/              # manifest и шаблоны команд
fixtures/                       # синтетические Go-репозитории
acceptance/                     # сквозные проверки
```

CLI и MCP вызывают application use cases. Они не имеют собственного scheduler или собственной логики актуальности. Изменение файла, запуск проверки и интеграция патча проходят через application слой; модель не получает прямой дескриптор БД или произвольный shell.

### 3.1. Границы интерфейсов ядра

```go
type SnapshotService interface {
    Capture(ctx context.Context, req CaptureRequest) (SnapshotManifest, error)
    Materialize(ctx context.Context, id SnapshotID) (ReadonlyWorkspace, error)
}
type IndexService interface {
    Build(ctx context.Context, req BuildRequest) (JobID, error)
    Publish(ctx context.Context, req PublishRequest) (GenerationID, error)
}
type RetrievalService interface {
    Search(ctx context.Context, req SearchRequest) (SearchResult, error)
    Context(ctx context.Context, req ContextRequest) (ContextPack, error)
}
type TaskService interface {
    Execute(ctx context.Context, cmd TaskCommand) (OperationResult, error)
}
type CheckRunner interface {
    Start(ctx context.Context, req CheckRequest) (RunID, error)
}
type ModelProvider interface {
    Capabilities(ctx context.Context) (Capabilities, error)
    Generate(ctx context.Context, req ModelRequest) (ModelResult, error)
}
```

DTO определяются схемами; идентификаторы — отдельные типы. `context.Context` передаёт отмену и deadline. `Generate` получает сообщения и декларации инструментов, но выполнение вызова инструмента выполняет SEAI после валидации. Provider adapter обязан нормализовать разные форматы endpoint в один внутренний формат.

### 3.2. Транспорт и процессная модель

Для пилота: локальный Unix domain socket на согласованной ОС; каталог и socket доступны только владельцу. Windows — отдельный профиль с named pipe после проверки. Сетевой HTTP listener по умолчанию не поднимается. Wire protocol: JSON-RPC 2.0, прикладная версия `seai.api/v1` в handshake.

MCP-процесс работает через согласованный transport клиента и проксирует запросы daemon. БД пишет только daemon. CLI может автоматически запустить daemon, но должен показать startup error и путь к диагностике. Прямой fallback, при котором второй процесс самостоятельно пишет ту же runtime DB, запрещён.

Длительные операции возвращают `job_id` или `run_id`, а не удерживают запрос до завершения. `events.list` имеет cursor, limit ≤ 200 и wait ≤ 30 секунд. Повторное подключение клиента не создаёт новую задачу.

## 4. Конфигурация и размещение данных

### 4.1. Конфигурация проекта

Пример содержит условные продукт и версии. В поставку входит заполненная конфигурация стенда, а не эти placeholders.

```yaml
schema_version: seai.project/v1
project_id: credit-strategies-pilot
repository:
  root: /approved/path/strategies
  baseline_ref: refs/heads/master
  release_policy: explicit_target
  network_fetch: manual
  include_untracked: ["**/*.go", "testdata/**"]
  exclude: ["vendor/**", ".git/**", ".seai/**", "**/.env", "**/*.key"]
build:
  go_toolchain: "<PINNED_GO_VERSION>"
  goos: linux
  goarch: amd64
  tags: []
  environment_profile: corporate-offline-v1
index:
  structural: true
  lexical: true
  embeddings: false
  domain_profile: pilot-product-v1
runtime:
  profile: pilot
  active_workflows: 1
  max_workers: 2
  max_model_calls: 1
  max_index_jobs: 1
  max_check_jobs: 1
  max_task_tokens: 120000
  max_task_wall_time_seconds: 3600
  max_node_attempts: 2
provider:
  profile: qwen-corporate-v1
  credential_ref: approved-secret-provider/qwen
checks:
  profile: pilot-go-v1
updates:
  auto_detect: true
  auto_fetch: false
  auto_rebase_tasks: false
  retain_previous_generations: 2
```

`max_workers` ограничивает активные задания, `max_model_calls` — одновременные запросы модели. Числа выше — начальные настройки для настройки на P0; они не являются оценкой возможностей стенда. Если inference поддерживает concurrency, лимит model calls повышается до 2 с замером. Ядро поддерживает значение 4 из исходного ТЗ, но пилот не обязан постоянно его использовать.

В конфигурации нельзя хранить секреты. Профиль среды задаёт разрешённые переменные, proxy/module cache и запрет неожиданных сетевых обращений. Fingerprint включает влияющие на сборку значения; credentials в hash не включаются, вместо них фиксируются policy/ACL epoch.

### 4.2. Каталоги и владельцы

В Git: `.seai/project.yaml`, версии схем, словарь, предметные исключения, ChangeSpec, подтверждённые fixtures/builders и при необходимости опубликованные отчёты. Автоматический локальный отчёт сначала записывается в runtime; копирование в Git — отдельная явная команда.

В локальном каталоге данных проекта:

```text
runtime.db
indexes/<generation_id>/index.db
objects/sha256/<prefix>/<digest>    # CAS: исходники, логи, patches, manifests
workspaces/<task>/<node>/<attempt>/
releases/<operation_id>/           # планы актуализации и итоги
backups/<backup_id>/
logs/                             # технические события без секретов
```

Runtime хранится на локальной файловой системе, вне индексируемого дерева. Размеры и срок хранения задаются квотой. GC не удаляет артефакты активных задач, rollback target, pinned generations и свидетельства, включённые в сохранённый отчёт. Удаление истории — отдельная обслуживающая операция с preview.

## 5. Снимки, версии и актуальность

### 5.1. Идентификаторы

Разделить сущности, которые в исходном ТЗ частично объединены одним `snapshot_id`:

| Поле | Что идентифицирует |
|---|---|
| `repository_id` | Явно зарегистрированный проект/репозиторий; смена remote не создаёт его молча |
| `source_snapshot_id` | Сохранённые байты исходников, базовый commit, конфигурацию сборки и окружение |
| `index_generation_id` | Source snapshot + версии анализаторов, предметных адаптеров и схемы индекса |
| `spec_revision/spec_digest` | Точный контракт задачи |
| `requirements_digest` | Набор версий импортированных требований и права на них |
| `candidate_snapshot_id` | Полный снимок изолированного результата изменения |
| `release_id` | Manifest установленного релиза, который указывает на commit и source snapshot |
| `environment_digest` | Toolchain, зависимости, разрешённые переменные и профиль runner |

`source_snapshot_id = SHA256(canonical manifest)`; manifest содержит repository ID, commit, отсортированные записи `path/mode/content_digest`, submodule commits при поддержке, build config, toolchain и dependency manifests. Исключены `.git`, runtime, отчёты и ChangeSpec; последние учитываются отдельными digest, иначе генерация отчёта делала бы код устаревшим.

Внешние `replace`, workspace modules и сгенерированные входы, реально участвующие в сборке, либо включаются как разрешённые immutable source roots, либо capability check блокирует полную верификацию. Нельзя считать два окружения одинаковыми только по `go.mod`.

Canonical encoding: UTF-8 JSON, ключи объектов сортируются, массив файлов сортируется по нормализованному repository-relative пути, числа — целые, hash — SHA-256 в lower hex. Нормализация пути не меняет байты файла и окончания строк. Регистр учитывается; коллизии путей для целевой ФС — ошибка. Symlink за пределы разрешённого root не разыменовывается. Исполняемый бит, удаление и переименование отражаются в manifest.

### 5.2. Захват dirty-state

1. Считать HEAD, build config и список включённых файлов; сохранить staged/unstaged metadata отдельно.
2. Прочитать байты файлов в CAS; использовать эти байты и для хеша, и для анализа.
3. Повторно проверить HEAD, список файлов и изменения относительно начала. При обнаружении гонки повторить захват не более двух раз; затем `WORKSPACE_BUSY`.
4. Материализовать снимок в собственной копии; анализ и проверки работают на ней. Не анализировать изменяемое основное дерево после вычисления hash.
5. Зафиксировать manifest в runtime. Unsaved IDE buffers не входят в пилот; это отображается в status и отчёте.

Tracked файлы не исключаются автоматически из снимка только потому, что совпали с новым ignore rule. Секретные/запрещённые файлы из tracked tree требуют явного исключения и диагностического ограничения анализа. Релевантные untracked включаются по allowlist. Внешний код и команды тестов получают только разрешённый профиль среды.

Основной index Git, stash и HEAD пользователя не меняются. Для dirty worktree создаётся приватный materialized baseline; при необходимости синтетический commit создаётся через временный Git index и отдельный ref SEAI. Сохраняется соответствие исходному HEAD и overlay. Никаких автоматических `stash`, `reset --hard`, `clean` или `pull`.

### 5.3. Жизненный цикл индекса

`BUILDING → VALIDATING → READY → RETIRED`; ошибочная сборка переходит в `FAILED`. `current_generation` хранится в runtime DB. Сначала полностью создаются и проверяются файлы индекса, затем одной транзакцией меняется указатель и добавляется событие. При падении до транзакции остаётся неподключённое поколение, после — готовое поколение. Читатель фиксирует generation на всё время запроса; старое поколение удерживается pin до завершения.

Неполная типизация допускает `READY` только с явным `coverage=partial`, перечнем пакетов с ошибками и запретом объявлять соответствующие связи полными. Пилотный change workflow требует успешной типизации затронутого scope. Индекс с нарушенной целостностью или отсутствующими исходными blobs не публикуется.

### 5.4. Свежесть артефактов

Каждый context pack, план, patch, check run и отчёт содержит `input_fingerprint` и список зависимостей. Состояния актуальности независимы от истории исполнения: `CURRENT`, `STALE`, `NEEDS_REVIEW`, `SUPERSEDED`.

| Изменение входа | Что обязательно инвалидируется |
|---|---|
| Файл исходника или build config | Зависимые graph facts, context packs; проверки другого candidate |
| Публичный интерфейс/shared package | Обратное замыкание зависимых пакетов и impact analysis |
| Requirement revision/acceptance criterion | Approval по изменённым условиям, план, тестовое соответствие, готовность отчёта |
| Предметный adapter или vocabulary | Предметные рёбра, impact/context/plans на их основе |
| Модель, tokenizer, prompt profile | Кеш model output; решения работников требуют revalidation при продолжении |
| Runner/toolchain/dependencies/fixture | Соответствующие проверки и сравнение версий |
| Только текстовое оформление отчёта | Перегенерация отчёта; проверки не инвалидируются |
| ACL epoch/отзыв доступа | Чтение, retrieval и кеши закрытых документов; ранее полученные данные не выдаются |

Для пилота после смены базового релиза все активные change tasks получают `reconciliation_required=true`, а их release readiness — `STALE`. Даже при совпадении файлов новый итоговый отчёт требует сверки с новым baseline и полной регрессии. Старый run сохраняет статус `PASSED` как исторический факт, но не подтверждает новый candidate.

Автоматический detector реагирует на изменения ref, checkout, конфигурации и файлов. Watcher — ускорение; перед мутацией, `run/resume/verify/export` обязательно повторно проверяются guards. Пилотный status показывает `active_baseline`, `observed_checkout` и `freshness_checked_at`. При обнаруженном расхождении новые мутации блокируются до reconcile. Для исторического explain допускается явный `--snapshot` с маркировкой historical.

## 6. Хранилище и модель данных

### 6.1. Общие требования

SQLite работает с включёнными foreign keys, WAL и `synchronous=FULL` для runtime; один writer сериализует короткие транзакции. Длительные subprocess/model calls не выполняются внутри транзакции. `busy_timeout`, checkpoint и квоты WAL настраиваются и проверяются на стенде. Index DB после публикации открывается read-only; её завершение и перенос в финальный каталог предшествуют смене указателя.

CAS: временный файл → запись → flush/fsync → проверка digest → rename в конечный путь на той же ФС. Ссылка в DB создаётся после durable publication объекта. Обратный порядок создавал бы ссылки на отсутствующие данные. Неиспользуемые объекты после падения удаляет отложенный GC.

### 6.2. Runtime DB: таблицы

| Таблица | Ключ и существенные поля | Инварианты |
|---|---|---|
| `projects` | id, root, config_digest, active_baseline_id, current_generation_id, epoch | Один активный baseline; epoch растёт при переключении |
| `source_snapshots` | id, repository_id, base_commit, manifest_digest, environment_digest | Immutable; manifest доступен в CAS |
| `index_generations` | id, snapshot_id, path, extractor_digest, state, coverage | Current ссылается только на READY |
| `release_baselines` | id, project_id, resolved_commit, manifest_digest, installed_at | Ref-name хранится отдельно от разрешённого commit |
| `tasks` | id, project_id, mode, spec_digest, state, revision, baseline_id, candidate_id, freshness | Optimistic revision для каждой команды |
| `nodes` | id, task_id, role, state, revision, input_digest, attempt, lease_epoch, lease_until, result_digest | Результат только для текущих revision/attempt/lease |
| `node_dependencies` | node_id, dependency_id | DAG проверяется до активации |
| `operations` | project_id, operation_id, method, payload_digest, state, result_digest | Unique(project_id, operation_id) |
| `events` | project_id, seq, task_id, type, payload_digest, occurred_at | Append-only; unique(project_id, seq) |
| `artifacts` | id, kind, digest, input_fingerprint, snapshot_id, created_by | Содержимое immutable |
| `artifact_dependencies` | artifact_id, input_kind, input_id, input_digest | Для invalidation и аудита |
| `task_artifacts` | task_id, artifact_id, role, freshness, superseded_by | Привязка актуальности отдельно от содержимого |
| `patches` | id, task_id, base_snapshot_id, manifest_digest, state, operation_id | Полные pre/post hashes всех затрагиваемых путей |
| `check_runs` | id, task_id, candidate_id, check_profile_digest, input_digest, status, exit_code, result_digest | PASSED только после подтверждённого завершения процесса |
| `decisions` | id, task_id, spec_digest, actor, decision, scope_digest, timestamp | Изменение утверждённого scope требует нового решения |
| `update_jobs` | id, kind, from_id, to_id, plan_digest, state, result_digest | Resume по фазе, без повторной публикации |
| `resource_leases` | resource_key, owner, epoch, expires_at | Fencing token для операций с файлами |
| `schema_migrations` | version, checksum, applied_at | Изменение применённого SQL запрещено |

IDs задач/операций/запусков — UUID/ULID, человекочитаемый `CHG-0042` — уникальный alias проекта. CAS IDs — digest, не случайный UUID. JSON payload хранится по версионированной схеме; отсутствие поля не равнозначно `null`.

### 6.3. Минимальные SQL-ограничения

Ниже обязательный каркас; полные migrations включают остальные таблицы из 6.2.

```sql
CREATE TABLE operations (
  project_id TEXT NOT NULL,
  operation_id TEXT NOT NULL,
  method TEXT NOT NULL,
  payload_digest TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN ('STARTED','SUCCEEDED','FAILED')),
  result_digest TEXT,
  PRIMARY KEY(project_id, operation_id)
);
CREATE TABLE tasks (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL REFERENCES projects(id),
  alias TEXT NOT NULL,
  mode TEXT NOT NULL CHECK(mode IN ('explain','change','refactor')),
  spec_digest TEXT NOT NULL,
  state TEXT NOT NULL,
  revision INTEGER NOT NULL CHECK(revision > 0),
  baseline_id TEXT NOT NULL REFERENCES release_baselines(id),
  candidate_id TEXT REFERENCES source_snapshots(id),
  freshness TEXT NOT NULL CHECK(freshness IN
    ('CURRENT','STALE','NEEDS_REVIEW','SUPERSEDED')),
  reconciliation_required INTEGER NOT NULL DEFAULT 0,
  UNIQUE(project_id, alias)
);
CREATE TABLE events (
  project_id TEXT NOT NULL REFERENCES projects(id),
  seq INTEGER NOT NULL,
  task_id TEXT REFERENCES tasks(id),
  type TEXT NOT NULL,
  payload_digest TEXT NOT NULL,
  occurred_at TEXT NOT NULL,
  PRIMARY KEY(project_id, seq)
);
CREATE INDEX tasks_project_state ON tasks(project_id, state);
```

Task update, увеличение revision, event и результат idempotent-команды коммитятся в одной транзакции. Обновление выполняется как `UPDATE ... WHERE id=? AND revision=?`; ноль строк означает `REVISION_CONFLICT`. Event log используется для аудита и восстановления незавершённых операций; это не требование реализовать полное event sourcing. Текущее состояние в нормализованных таблицах — источник runtime-state, ChangeSpec — источник поведения задачи.

### 6.4. Index DB

Таблицы: `files`, `packages`, `symbols`, `entities`, `edges`, `evidence`, `chunks`, `chunks_fts`, `extractor_diagnostics`, `package_dependencies`, `source_manifest`.

- `files(path PRIMARY KEY, blob_digest, mode, package_key)`.
- `symbols(symbol_id PRIMARY KEY, package_path, receiver, name, kind, signature, file_path, start_byte, end_byte)`.
- `entities(entity_id PRIMARY KEY, kind, domain_key, attributes_json)`.
- `edges(edge_id PRIMARY KEY, src_id, dst_id, kind, certainty, evidence_id, extractor_version)`.
- `evidence(evidence_id PRIMARY KEY, file_path, blob_digest, start_byte, end_byte, method, details_json)`.
- `chunks(chunk_id PRIMARY KEY, owner_id, text_digest, scope_json)` и FTS5 по нормализованному тексту и идентификаторам.

Индексы: `symbols(package_path,name,receiver)`, `entities(kind,domain_key)`, `edges(src_id,kind)`, `edges(dst_id,kind)`, `evidence(file_path,blob_digest)`. Duplicate Rule ID не схлопывается: ключ включает endpoint/product/registration context.

Symbol ID стабилен только в рамках поколения. Cross-generation matching использует qualified name, сигнатуру, enclosing symbol, Git diff и content similarity; результат — `exact_match`, `candidate_match`, `unmatched`. Переименование без однозначного доказательства не переносит подтверждение автоматически.

## 7. Индексация и предметные экстракторы

### 7.1. Полная индексация

1. Принять snapshot и build profile; проверить доступность modules/toolchain без неожиданной загрузки из интернета.
2. Загрузить пакеты через `go/packages` с файлами, imports, dependencies, syntax, types и types info; тестовые варианты учитывать отдельно.
3. Для больших репозиториев обрабатывать пакетные области с лимитами памяти, сохраняя согласованный manifest.
4. Построить определения, imports, typed references, selector references, методы и interface relations в поддержанном scope.
5. Построить прямые статические CALLS. Динамические вызовы отметить unresolved/possible; не выдавать их за точный граф.
6. Запустить предметные экстракторы и прикрепить evidence к каждой связи.
7. Создать AST chunks и lexical index; не обрезать тело теста отдельно от существенного setup/assertions.
8. Проверить целостность ссылок, coverage и воспроизводимость counts; опубликовать READY generation.

Built-in symbols и external modules представлены внешними узлами с package/version, если исходники не разрешены для индексации. Это не ошибка «отсутствующего локального файла». Generated Go учитывается, если присутствует в snapshot. `go generate` не запускается автоматически; утверждённая генерация — отдельный check/build profile.

### 7.2. Инкрементальный алгоритм

`changed_paths` вычисляется по manifests, включая добавления, удаления и режимы файлов. Для каждого пути определить старый и новый package. Минимальный rebuild set: изменённые пакеты и их обратные транзитивные import-зависимости, включая релевантные test variants.

Изменение `go.mod/go.sum/go.work`, build tags, GOOS/GOARCH, toolchain, разрешённых внешних roots или версии структурного анализатора запускает полную перестройку соответствующего build profile. Неопределённый package mapping также запускает полную перестройку.

Новое поколение собирается отдельно. Неизменённые пакеты можно копировать как immutable records с повторной привязкой generation. Все file-backed evidence должны ссылаться на blob из нового manifest. Старые удаления не переносятся. Для пилота допустимо строить полный индекс вместо небезопасного incremental path; это отдельно отражается в latency.

Acceptance: после серии add/delete/rename/interface/build-tag изменений normalized export incremental поколения равен full build того же снимка. Из сравнения исключаются только timestamps и случайные job IDs.

### 7.3. Контракт предметного экстрактора

```text
Input: package AST + types + immutable source bytes + extractor config
Output: entities[], edges[], diagnostics[], coverage
Every edge: exact/possible/unresolved + evidence + extractor_version
```

Экстрактор — детерминированный код, покрытый положительными и отрицательными fixtures. Запрещены молчаливые регулярные выражения без проверки AST/type identity для критичных связей.

| Extractor | Обязательная первая реализация | Условие деградации |
|---|---|---|
| `rule_registry` | Формы регистрации из выбранного продукта | Неизвестная форма → diagnostic с позицией |
| `routing` | SourceSystem/DopCode/ProductMarker для выбранного потока | Динамическая таблица → unresolved |
| `parameter` | Имена/ID, parse/default/read для pilot scope | Неизвестный wrapper → possible/unresolved |
| `shared_usage` | Импорты, alias и typed calls к общему модулю | Reflection → ограничение scope |
| `output_priority` | Утверждённые формы назначения и приоритетов | Вычисляемое значение → не придумывать literal |
| `requirement_link` | Явная связь из versioned YAML/ChangeSpec | Нет revision → документ unverified |

До реализации P0 фиксирует реальные AST patterns на коде. Точное число pipelines, названия пакетов и специфические формы `rules.Rules`/`ruler.Set` не зашиваются на основании отчёта. При обнаружении неподдержанной регистрации в области изменения change workflow получает блокирующий gap.

## 8. Поиск, анализ влияния и Context Pack

### 8.1. Контракт выдачи

Каждый запрос требует `source_snapshot_id` и `index_generation_id` либо явный alias `current`, который разрешается в конкретные IDs до начала выполнения. После разрешения alias не меняется в середине запроса.

Поиск: exact identifier → FTS → объединение → graph expansion → evidence resolution. В пилоте exact совпадения полного Rule ID/qualified symbol ранжируются первыми. Для равного приоритета применяются scope proximity, FTS score и стабильный tie-break по path/offset. Embeddings отключены. При расширении позже используется versioned ranking profile и отдельная оценка качества.

Бюджеты по умолчанию: depth ≤ 3, visited nodes ≤ 500, fan-out ≤ 50 на узел, выдача ≤ 50 результатов, snippet ≤ 8 KiB. Это настраиваемые лимиты; достижение любого лимита даёт `partial`, причину и способ продолжить. Shared consumers не отсекаются фильтром продукта. Для общего узла возвращаются сводка потребителей и cursor, а не произвольные первые несколько без предупреждения.

Cursor непрозрачен для клиента, связан с generation/query/filter/access scope/ranking version. Использование cursor с другим snapshot возвращает `CURSOR_SCOPE_MISMATCH`.

### 8.2. Impact analysis

Результат содержит:

- `confirmed`: прямые typed/source-confirmed зависимости;
- `possible`: interface/dynamic/alias гипотезы с основанием;
- `unexamined`: узлы за лимитами и неподдержанные области;
- `affected_products`, `affected_packages`, `required_checks`;
- `coverage_gaps`: отсутствующая категория evidence.

Для change task обязательны: требование, определение изменяемого поведения, регистрация/маршрут, вход/дефолты при применимости, output path, тесты и shared consumers. `not_applicable` допустимо только с сохранённым обоснованием. Отсутствие теста — gap и задача на его создание, а не вывод «поведение не проверяется вообще».

### 8.3. Формат Context Pack

```json
{
  "schema_version": "seai.context/v1",
  "task_id": "CHG-0042",
  "spec_digest": "sha256:...",
  "source_snapshot_id": "src:...",
  "index_generation_id": "idx:...",
  "facts": [{"statement": "...", "evidence_ids": ["ev:..."]}],
  "inferences": [{"statement": "...", "based_on": ["ev:..."]}],
  "assumptions": [],
  "required_sources": [],
  "acceptance_criteria": ["AC-01"],
  "gaps": [],
  "budget": {"estimated_input_tokens": 18000, "limit": 24000},
  "input_fingerprint": "sha256:..."
}
```

План и workers получают IDs артефактов и минимальный pack, а не полные чужие диалоги. Evidence разрешается на сохранённые байты; при необходимости показать live файл UI дополнительно проверяет digest. Лимит контекста вычисляется по реальному tokenizer; при его недоступности используется проверенный консервативный estimator с запасом. Размер окна и выходной резерв подтверждаются provider profile.

Повторный одинаковый поиск после двух попыток без новых источников прекращается с конкретным gap. Недоступный документ не заменяется ответом модели из общей памяти.

## 9. ChangeSpec и управление требованиями

### 9.1. Дополнительные поля низкоуровневого контракта

Базовые поля исходного ChangeSpec сохраняются. Для разработки добавить: `kind`, `baseline`, `risk_profile`, `verification`, `decisions`. Все схемы содержат `additionalProperties: false`, явные enum и ограничения размеров. Для произвольных extensions выделяется namespaced объект.

```yaml
schema_version: seai.change/v1
change_id: CHG-0042
revision: 1
kind: change
objective: "<утверждённое изменение поведения>"
source_requirements:
  - source: file
    external_id: REQ-17
    revision: "3"
    content_digest: "sha256:<digest>"
    artifact_ref: "obj:<digest>"
baseline:
  release_id: REL-A
  source_snapshot_id: "src:<digest>"
scope:
  endpoints: ["/execute"]
  products: ["<pilot-product>"]
  rule_ids: ["<rule-id>"]
expected_behavior:
  - id: BEH-01
    statement: "<точное ожидаемое поведение и границы>"
preserved_behavior:
  - id: INV-01
    statement: "<сохраняемое поведение и область применимости>"
acceptance_criteria:
  - id: AC-01
    behavior_ids: [BEH-01]
    input_fixture_ref: "obj:<digest>"
    expected_result_ref: "obj:<digest>"
    assertion_profile: exact-json-v1
unresolved: []
risk_profile: shared-change
verification:
  required_profiles: [compile, targeted, regression, version-comparison]
approval:
  status: approved
  actor: "<identity>"
  decision_ref: "DEC-17"
  behavior_digest: "sha256:<digest>"
decisions: ["DEC-17"]
```

Этот шаблон с placeholders не проходит готовность к реализации. Утверждение фиксируется доверенным действием человека через CLI/API и связывается с digest поведения; модель не может сама вызвать команду approval. Наличие строки `status: approved` в файле не создаёт такого решения.

`behavior_digest` исключает технические координаты baseline, оформление и runtime-state. Смена baseline не требует повторного бизнес-approval, если ожидаемое поведение не менялось; но делает необходимой техническую revalidation. Смена границ, исключений, ожидаемых ответов, утверждённого scope или requirement revision пересчитывает digest и снимает соответствующее approval.

Каждая новая редакция сохраняется в CAS. `spec.yaml` — единственный редактируемый контракт, runtime DB хранит его принятый digest и ссылки. Перед `run/resume/verify` система перечитывает файл; изменение без импорта новой revision блокирует продолжение. Report содержит spec digest; он не служит входом для изменения требований.

### 9.2. Критерии готовности ChangeSpec

Для `change`: источник и revision доступны; scope однозначен; expected/preserved behavior заданы; существенных unresolved нет; у AC устойчивые ID и исполнимый способ проверки; approval соответствует digest. Для `refactor`: задаются сохраняемые наблюдаемые свойства и проверка эквивалентности; вымышленное бизнес-approval не требуется. Для `explain`: допускаются unresolved, завершение — исследовательский отчёт.

Characterization tests маркируются отдельно. Ожидаемый результат acceptance test берётся из утверждённого контракта/fixture, а не из ответа новой реализации. Test designer не получает право менять approved expected values.

## 10. Оркестратор, workers и применение изменений

### 10.1. Состояния задачи и guards

Историческое состояние task и актуальность разделены. У `REVIEW_READY + STALE` запрещён export как готового результата. Исторический отчёт читать разрешено с заметной маркировкой.

| Переход | Guard |
|---|---|
| RECEIVED → SPEC_READY | ChangeSpec принят, профиль валидации пройден |
| SPEC_READY → RESEARCHED | Обязательные категории evidence найдены; gaps разрешены или блокируют запись |
| RESEARCHED → PLANNED | Валидный DAG, scopes, checks, бюджет, роли |
| PLANNED → IMPLEMENTING | Input fingerprint актуален; baseline epoch совпадает; workspace lease получен |
| IMPLEMENTING → VERIFYING | Все обязательные патчи интегрированы в candidate; нет конфликтов |
| VERIFYING → REVIEW_READY | Обязательные checks завершены успешно; AC mapping полный; reviewer не имеет блокирующих замечаний |
| REVIEW_READY → COMPLETED | Разработчик принял результат; результат/export digest сохранён |
| RESEARCHED → REVIEW_READY | Только explain; отчёт содержит источники и ограничения |
| Любое активное → NEEDS_INPUT | Отсутствует существенное решение, конфликт или изменился baseline |
| Любое активное → FAILED | Невосстановимая ошибка или исчерпание retry/budget |
| Любое активное → CANCELLED | Явная отмена; дочерние процессы остановлены/зафиксированы |

`NEEDS_INPUT` хранит reason, required decision и checkpoint. После решения создаётся новая execution revision с возвратом к последней валидной фазе. Resume после crash восстанавливает checkpoint, но не переписывает историю. CANCELLED/FAILED можно продолжить только явным `resume`, создающим новую execution revision; терминальный результат предыдущего запуска остаётся неизменным.

Для COMPLETED task новая база не открывает задачу автоматически. Можно создать новую задачу через `task fork --from`; старый результат остаётся привязан к прежнему релизу. Смена требований также не переписывает завершённое изменение.

### 10.2. DAG пилота

```text
validate_spec
    ├── research_rule ─────┐
    └── research_impact ───┤
                         plan
                    ┌─────┴────────┐
                 implement     design_tests
                    └─────┬────────┘
                       integrate
                           │
                 compile + targeted_checks
                           │
              regression + version_comparison
                           │
                        review
                           │
                        report
```

`design_tests` до согласования интерфейса выдаёт test plan/fixtures. Если пишет код, получает свой worktree и согласованный write scope; при пересечении с implementer выполняется последовательно. Integrator — детерминированный исполнитель с возможным отдельным model-заданием для разрешения конфликта; сам по себе он не требует постоянно занятой модели.

Scheduler валидирует DAG: нет циклов, обязательные checks достижимы, writer scopes не конфликтуют, суммарные лимиты не превышены. В пилоте глубина порождения заданий — 1, не более 12 узлов плана, не более 2 попыток узла. Workers не создают новых workers самостоятельно; предложенная декомпозиция снова проходит валидатор.

Обязательные workers: researcher, implementer, test designer, reviewer. Роли могут выполняться последовательно, но контексты и результаты независимы. Reviewer читает утверждённый контракт, diff и результаты проверок; обоснование implementer не подменяет проверку кода.

### 10.3. Lease и защита от поздних результатов

Node states: `PENDING`, `READY`, `RUNNING`, `SUCCEEDED`, `FAILED`, `CANCELLED`, `SUPERSEDED`. Для RUNNING выделяется lease с epoch. Начальный heartbeat — 10 секунд, lease — 45 секунд. Значения настраиваются и не используются как точные гарантии времени завершения.

Результат принимается при совпадении `(task_revision, node_revision, attempt, lease_epoch, input_fingerprint)`. Истёкшая/отменённая попытка не может опубликовать patch или итоговый check. Clock для локальных timeout — monotonic; durable timestamps — UTC. После рестарта все старые running leases считаются требующими recovery и получают новый fencing epoch.

Если subprocess ещё работает после потери lease, он изолирован в отдельном process group и workspace. Новый worker не пишет в ту же копию. Завершённый поздний результат сохраняется для диагностики, но помечается rejected. Только integrator с актуальным lease может менять указатель candidate.

Retry: transient provider/transport error — ограниченный backoff с jitter; schema/tool-call error — один repair в пределах node budget; invalid arguments не исполняются. Business ambiguity, конфликт patch preconditions и ошибка теста не исправляются слепым повтором того же вызова.

### 10.4. Протокол worker

Вход: node ID/revision, роль, objective, artifact IDs, immutable baseline, read/write scope, tool allowlist, budgets, acceptance checks. Выход: status, findings с evidence, patch ID, test plan/results, unresolved, usage. Финальный текст без структурированного result не считается успешным выполнением узла.

Работник не имеет прямого сетевого доступа к произвольным адресам. Вызовы модели проходят provider adapter; tool execution проходит policy executor. Корпоративные документы/исходники могут содержать текст инструкций, но не меняют роли и разрешения. В model messages они передаются как данные с указанием origin.

### 10.5. Интеграция patch и идемпотентность

Patch manifest содержит `base_snapshot_id`, task/spec revision, write scope, ordered file operations и для каждого пути: before hash/mode или признак отсутствия, after hash/mode или удаление. Binary patch и rename имеют явные записи. Пути с `..`, абсолютные пути и symlink escape отклоняются.

Алгоритм:

1. Проверить `operation_id`; тот же ID с другим payload → `IDEMPOTENCY_CONFLICT`.
2. Проверить task revision, baseline epoch, lease, scope и все before hashes.
3. Создать новую staging-копию текущего candidate; применить патч только в ней.
4. Проверить все after hashes; захватить candidate snapshot и опубликовать manifest в CAS.
5. Одной runtime-транзакцией сменить candidate pointer, сохранить operation result/event и инвалидировать зависимые checks.
6. Только после commit показывать patch как APPLIED. Старый candidate остаётся доступен.

Так не требуется атомарная перезапись нескольких файлов в основной копии пользователя. При crash staging остаётся orphan; опубликованный указатель всегда ведёт на полный снимок. Повторная операция возвращает сохранённый результат. Для упавшей STARTED операции recovery сверяет operation journal, candidate pointer и manifest прежде, чем повторять действие.

Основное дерево пилотного разработчика не меняется автоматически. `task export` создаёт patch + manifest с требованиями к base и checks. Пользователь переносит его обычным процессом Git. Прямой apply в основную копию — вне пилота; отдельно потребует защиты staged/unstaged состояния и восстановления многофайловой операции.

## 11. Проверки и структурное сравнение версий

### 11.1. Runner

Check profile задаёт program, argv array, cwd policy, env allowlist, timeout, CPU/RAM limits и обработчик результата. Модель выбирает зарегистрированный `check_id`, а не конструирует shell string. Проектные скрипты проверки фиксируются revision/digest. Тестовый код считается исполняемым кодом: runner использует согласованную изоляцию ОС и ограниченный профиль сети. Если нужная изоляция на стенде недоступна, doctor отражает ограничение; полный unattended change профиль не включается без решения владельца среды.

Запуск: RUNNING до старта subprocess в журнале с run token; затем PID/process metadata; stdout/stderr в CAS; после `Wait` — exit code, signal, parsed test results и duration. Timeout, cancel, runner crash и parse error не равны PASSED. После рестарта незавершённые runs получают UNKNOWN/ABORTED; обязательная проверка выполняется повторно. Проверки должны быть без внешних побочных эффектов, поскольку точно один запуск процесса после crash гарантировать нельзя.

`input_digest` проверки включает candidate snapshot, toolchain/environment, argv, fixtures, runner version и check profile. Результат другого digest не прикрепляется как текущий. Single-flight объединяет только одинаковые inputs в пределах одного access scope.

### 11.2. Матрица обязательных проверок пилота

| Изменение | Минимальные проверки |
|---|---|
| Explain | Валидация источников, freshness, полнота/ограничения ответа |
| Локальное правило/порог | Format, compile, targeted acceptance, полная регрессия, version comparison |
| Shared-расчёт/тип | Предыдущее + consumer tests, расширенный impact review |
| Маршрутизация/output priority | Предыдущее + положительные/отрицательные route cases, конкурирующие output cases |
| Concurrency/state | Предыдущее + race в затронутой области, повторяемость при согласованном seed/time |
| Перенос на новый релиз | Compile, targeted, полная регрессия, новое version comparison и reviewer |

Статический анализ включается согласованным check profile; baseline violations фиксируются отдельно. Новые нарушения блокируют готовность. Профиль fast не снимает обязательные проверки. Mutation/fuzz и benchmarks подключаются по risk policy; для критичных численных границ до полной приёмки обязателен согласованный набор таких испытаний.

### 11.3. Version comparison

База сравнения для обычной задачи — baseline, на котором начата текущая execution revision. После release rebase — **новый релиз без task patch**, а не старый релиз. Дополнительно сравнение old release → new release служит диагностике обновления и не заменяет проверку task patch.

Обе сборки используют одинаковые fixtures, время, seed и разрешённые зависимости. Build manifest содержит binary digest, source snapshot, toolchain, env profile, build flags и builder digest. Если время/случайность/внешние вызовы не управляются, сравнение возвращает non-reproducible и не объявляет эквивалентность.

Сравнение JSON: missing/null/zero различаются; duplicate object keys диагностируются до обычного decode; массивы сравниваются по порядку либо по явно утверждённому уникальному ключу. Нарушенная уникальность ключей — ошибка. Денежные/точные значения обрабатываются без неявного float rounding; decimal precision и допуски задаются по конкретному JSON path. Игнорируемые поля перечисляются в контракте.

Каждое отличие имеет fixture ID, JSON path, before/after, classification, criterion ID и evidence. `expected` допустим только при соответствии утверждённому AC; неописанное отличие — unexpected/unresolved. Пустой набор fixture не даёт successful comparison. Builders обеспечивают минимально валидный запрос и явные overrides; инварианты имеют scope/owner.

### 11.4. Итоговый отчёт

Обязательные разделы отчёта: release/baseline/candidate IDs; spec/requirement revisions; scope; изменённое поведение; diff summary; AC → tests/runs; confirmed/possible/unexamined impact; источники; обязательные checks и их terminal status; unexpected differences; нерешённые вопросы; затраты времени/токенов; ограничения; freshness.

`REVIEW_READY` запрещён при незавершённом обязательном check, существенном unresolved, устаревшем candidate, нарушенном approval digest или блокирующем замечании reviewer. Экспортировать диагностический незавершённый patch можно только явным `--allow-incomplete`; manifest и отчёт содержат `NOT_REVIEW_READY`. Такой export не меняет state на COMPLETED.

## 12. API, CLI и GigaCode-адаптер

### 12.1. Общая оболочка

```json
{
  "schema_version": "seai.api/v1",
  "request_id": "req-...",
  "status": "partial",
  "source_snapshot_id": "src:...",
  "index_generation_id": "idx:...",
  "data": {},
  "evidence_ids": ["ev:..."],
  "completeness": {
    "scope": "pilot product + typed shared dependencies",
    "truncated": true,
    "unresolved": ["fanout_limit"]
  },
  "warnings": [],
  "error": null,
  "next_cursor": "opaque-cursor"
}
```

`status`: `ok`, `empty`, `partial`, `stale`, `error`. Empty означает успешно выполненный запрос без результатов, а не сбой анализатора. Для async start `status=ok`, а `data.state=QUEUED` и `job_id/run_id` показывают, что работа ещё не завершилась. CLI с `--json` возвращает ту же domain envelope, что MCP structuredContent.

CLI exit codes: 0 — операция принята/успешна, 2 — schema/arguments, 3 — stale/conflict/needs input, 4 — runtime/provider failure, 5 — обязательная проверка завершилась неуспешно. Для async acceptance exit 0 не означает успешное окончание job. `--wait` ждёт terminal status с возможностью Ctrl+C; отмена ожидания и отмена серверной задачи — разные команды.

Все mutating методы требуют `operation_id`, `expected_revision`, а связанные с текущим проектом — `expected_project_epoch`. Ошибка возвращает `code`, `message`, `retryable`, `details`, `suggested_action`; stack trace в отдельной диагностике.

### 12.2. Методы первой версии

| Метод / CLI | Ключевые входы | Результат |
|---|---|---|
| `capabilities.get` / `doctor` | project, client profile | capabilities, blocking issues |
| `workspace.snapshot` / `workspace snapshot` | ref или working tree, build profile | manifest, freshness |
| `index.build` / `index build` | snapshot, full/incremental | job_id |
| `search.entities` / `search` | snapshot, generation, query, filters, cursor | hits, evidence, completeness |
| `symbol.get` / `symbol` | generation, symbol ID | signature, snippet, links |
| `references.find` / `references` | symbol ID, filters, cursor | typed refs и ограничения |
| `flow.get` / `flow` | endpoint, product, generation | routes/stages |
| `rule.context` / `rule` | Rule ID + scope | registrations, parameters, tests |
| `parameter.trace` / `parameter` | name/ID + scope | parse/default/read paths |
| `impact.analyze` / `impact` | entities или patch ID | confirmed/possible/unexamined |
| `tests.related` / `tests related` | entity IDs, generation | tests и основания связи |
| `context.build` / `context build` | task ID, budget, role | context artifact ID |
| `task.create` / `task create` | spec artifact, baseline | task ID/revision |
| `task.plan/run/status` | task ID, revision, mode | plan/run/status |
| `task.approve` / `task approve` | behavior digest, actor, decision | decision ID; human channel only |
| `task.cancel/resume` | task ID, revision, reason | новая execution revision/status |
| `task.diff/report/export` | task ID, candidate, output profile | artifact IDs/digests |
| `agents.list/result` | task ID/node ID | роли, status, result artifacts |
| `patch.apply` / internal tool | patch ID, preconditions, operation ID | новый candidate ID |
| `checks.run/status/cancel` | task/candidate, check profile | run ID/result |
| `versions.compare` | two build IDs, fixture profile | comparison job ID |
| `events.list` | task/project, cursor, limit, wait | events и cursor |
| `artifact.get` | artifact/evidence ID, range | bounded content, integrity |
| `release.plan/apply/status` | explicit target, plan ID | reconciliation plan/job/result |
| `task.rebase` | task ID, target baseline, revision | rebase job/result/conflicts |
| `upgrade.plan/apply/status` | verified bundle ID | upgrade plan/job/result |

MCP names используют `_` вместо `.`, например `release_plan`. Все типы входов/выходов поставляются в JSON Schema и тестируются против CLI/RPC/MCP. Чтение полного CAS по произвольному hash без проверки доступа к связанному проекту запрещено.

### 12.3. Пример change/rebase API

```json
{
  "method": "task.rebase",
  "params": {
    "schema_version": "seai.api/v1",
    "operation_id": "op-unique-17",
    "task_id": "CHG-0042",
    "expected_revision": 8,
    "expected_project_epoch": 5,
    "target_baseline_id": "REL-B",
    "conflict_policy": "stop"
  }
}
```

Conflict result содержит base/ours/theirs artifact IDs, path, conflict type, affected criteria и required action. Модель может предложить resolution patch, но он снова проходит preconditions и проверки. Silent `ours/theirs` policy для бизнес-кода отсутствует.

### 12.4. GigaCode

Адаптер регистрирует команды `/seai:*` только в формате, подтверждённом совместимой версией GigaCode. В пилоте обязательны init, doctor, ask/change, plan, run, status, agents, cancel/resume, verify, diff/report и release update. Команды преобразуют аргументы в schema-validated вызовы ядра.

Installer хранит manifest принадлежащих ему файлов/ключей и их hashes. Текстовый файл с пользовательскими изменениями не перезаписывается: generated artifact предлагается рядом и создаётся merge diagnostic. Для JSON/YAML выполняется структурный merge известных namespace. Удаление SEAI не удаляет чужие commands, skills или secrets.

Если custom commands не поддерживаются, разрешён CLI fallback через штатный shell-tool клиента. При отсутствии рабочего клиентского пути GigaCode пилот считается заблокированным по интеграции, даже если standalone CLI работает. Provider для собственных workers — отдельная проверенная capability; наличие интерактивной Qwen-сессии само по себе её не подтверждает.

## 13. Актуализация после обновления master / установки релиза стратегий

**Этот раздел обязателен для пилота и не переносится на этап масштабирования.** Простой повторный запуск embeddings или `git pull` задачу не решает.

### 13.1. Что считается релизом

`release_manifest` содержит `release_id`, repository ID, исходный ref и разрешённый commit SHA, source manifest digest, build profile, dependency/toolchain digests, время регистрации и источник подтверждения установки. Если есть binary/package digest, он также фиксируется.

Ref `master` — подвижное имя. В каждой операции он разрешается в конкретный SHA. Установленный релиз может соответствовать tag/commit позади актуального master; в этом случае актуализация выполняется к **установленному** commit, а не к наиболее свежему ref. Между установленным runtime и checkout должна быть проверяемая связь: доверенный release manifest или подтверждение оператора. По одному исполняемому файлу без source manifest SEAI не восстанавливает надёжную версию исходников.

Если пользователь уже обновил checkout внешним средством, detector показывает расхождение. Он не откатывает checkout и не меняет baseline молча. Для dirty feature branch целевой release baseline разрешается отдельно; HEAD feature branch не объявляется автоматически новым релизом.

### 13.2. Команды оператора

```text
# После штатного получения/установки релиза; SEAI сам не выполняет pull.
seai release plan --target <tag-or-sha> --release-id REL-B
seai release inspect <plan-id>
seai release apply <plan-id>
seai release status <job-id>
seai task rebase CHG-0042 --to REL-B
seai task resume CHG-0042
seai task verify CHG-0042
```

`release apply` не означает deployment стратегий. Это применение плана актуализации локального состояния SEAI. План создаёт конкретный commit/digest; если подвижный ref после планирования изменился, выполняется именно разрешённый commit, а расхождение отображается. Изменение выбранного commit требует нового плана.

### 13.3. План актуализации

`release plan` не меняет baseline и код задач. Результат включает:

- старый и целевой manifests, diff refs/commits/source/build dependencies;
- changed/added/deleted/renamed files и затронутые пакеты;
- выбор full/incremental и его основание;
- затронутые экстракторы, vocabulary mappings и неподдержанные новые формы;
- список активных задач с категорией `no_direct_overlap`, `dependency_overlap`, `file_conflict`, `spec_conflict`, `unknown`;
- что инвалидируется: contexts, graph facts, plans, approvals при изменении смысла, patches, checks, reports;
- необходимые свободное место, сохранённые поколения и лимиты работы; оценка времени допускается только по измеренному baseline;
- блокирующие условия и явные решения пользователя;
- `plan_digest`, `from_project_epoch`, resolved target commit и конфигурационные digests.

Отсутствие пересечения файлов не означает отсутствие семантического влияния. Для shared interfaces используется impact graph; при неполном графе категория `unknown`, а не `no_direct_overlap`.

### 13.4. Исполнение release apply

State machine: `PLANNED → PREFLIGHT → QUIESCING → CAPTURING → INDEXING → VALIDATING → PUBLISHING → RECONCILING → SUCCEEDED`. До публикации ошибка даёт `FAILED_NO_SWITCH`; после публикации незавершённая сверка задач — `PUBLISHED_PENDING_RECONCILIATION` с заблокированными мутациями, затем resume.

1. **PREFLIGHT.** Проверить project epoch, plan digest, доступность целевого commit, свободное место, отсутствие конкурирующего upgrade/release job. Если нет исходников/dependencies — остановить до переключения.
2. **QUIESCING.** Запретить новые node mutations. Дождаться коротких критических секций; активные model calls можно отменить или сохранить их результат как old-baseline artifact. Получить project update lease и поднять fencing epoch.
3. **CAPTURING.** Захватить immutable snapshot target и зарегистрировать release manifest. Dirty-state пользователя сохранить отдельно; он не включается в официальный baseline релиза.
4. **INDEXING.** Собрать новое поколение без изменения текущего указателя. Сохранить старое READY generation.
5. **VALIDATING.** Проверить index integrity, типизацию pilot scope, known Rule IDs, маршруты, extractor diagnostics, lexical smoke и coherence source/evidence. Новая неизвестная критичная форма регистрации блокирует change readiness для затронутого scope.
6. **PUBLISHING.** Одной runtime-транзакцией поменять active baseline/current generation, повысить project epoch, установить reconciliation barrier, инвалидировать readiness всех активных change tasks, записать event и update phase. Старые worker tokens больше не действуют.
7. **RECONCILING.** Построить подробную dependency invalidation и rebase plan для каждой задачи. Новые research queries используют новый baseline. Старые findings доступны только исторически. Сверить доступные версии документов; если API нет, отметить `requirements_freshness=not_checked` и перечислить документы для ручного подтверждения.
8. **FINISH.** Снять project barrier после завершения inventory. Разрешить новые задачи на новой базе. Незавершённые прежние tasks остаются NEEDS_INPUT/reconciliation required до индивидуального rebase.

Нельзя публиковать новый baseline, оставляя прежние задачи со статусом «готово для нового релиза» даже на время. Для этого глобальная инвалидация и barrier входят в ту же транзакцию, что переключение указателя. Детальная массовая обработка после неё может быть возобновлена.

Установка нового baseline не снимает автоматически прежний business approval. Оно снимается при изменении утверждённого поведения/источника/области; техническая применимость проверяется в любом случае.

### 13.5. Task rebase: перенос незавершённого изменения

Три стороны:

- **B:** точный старый task baseline, включая исходный dirty overlay, если он был;
- **O:** текущий task candidate на B, включающий принятые ручные правки;
- **N:** выбранный новый baseline релиза.

Если task начинался из dirty feature tree, система отдельно показывает inherited user changes и task delta. В пилоте требуется явный выбор: перенести весь approved overlay вместе с task delta либо сначала предоставить обновлённый feature snapshot. Молчаливое включение посторонних dirty edits в новый релиз запрещено. Если inherited changes исключены, base для task delta остаётся материализованный B, а применимость этого delta к N проверяется как отдельная операция.

Алгоритм:

1. Приостановить task, сохранить checkpoint, B/O/spec/plans и рабочие артефакты; оригинальные копии не менять.
2. Проверить актуальность requirement revision и approval digest; сформировать новый execution baseline metadata. Поведение требования не редактировать.
3. Создать отдельный worktree/copy N. Выполнить трёхсторонний перенос изменения B→O на N с сохранением path/mode/delete/rename metadata.
4. Для текстовых конфликтов выдать conflict artifacts; для delete/modify, неоднозначного rename, binary conflict — остановить. Не выбирать сторону автоматически.
5. Для бесконфликтного переноса повторно построить candidate snapshot, typed index overlay и impact analysis. «Merge прошёл» не означает корректность поведения.
6. Перестроить context packs и DAG с учётом новой базы. Локальные работники старой execution revision superseded.
7. Повторить compile, AC tests, consumer checks, полную регрессию и сравнение **N против N+patch**; выполнить reviewer на новом diff.
8. Опубликовать новый task baseline/candidate и отчёт с `rebased_from`, `rebased_to`, списком разрешённых конфликтов и check runs.

Если перенос не требуется, поскольку новый релиз уже содержит требуемое изменение, это отдельный результат `ALREADY_SATISFIED`: нужно доказать AC на N, проверить preserved behavior и получить принятие разработчика. Не создавать пустой «успешный патч» без проверки требования.

Если в master изменился публичный API либо устранили само правило, задача переходит в NEEDS_INPUT с конкретным вопросом о применимости требования. Модель не придумывает эквивалентное бизнес-правило для нового API.

### 13.6. Сводный отчёт актуализации

```text
Release: REL-A -> REL-B
Target commit: <sha>
Index: idx-A -> idx-B; structural coverage: complete/partial
Changed: files/packages/rules/routes/parameters
Requirements: checked N, unchanged N, changed N, not checked N
Active tasks:
  CHG-0042: rebase required; shared interface changed
  CHG-0043: no direct overlap; verification required
Completed tasks: preserved on their historical baseline
Invalidated: context packs / plans / check readiness / reports
Unsupported extraction patterns: paths and evidence
Next actions: task-specific commands and required decisions
Rollback target: REL-A / idx-A
```

### 13.7. Откат baseline

`release rollback --to <previous-release>` создаёт обычную управляемую операцию переключения с новым project epoch. Прежние workers не оживают; задачи заново сверяются с целевым baseline. Runtime source history не откатывается целиком, чтобы не потерять новые решения и работы. Проверки можно переиспользовать только при полном совпадении fingerprints и действующей policy; готовность формируется новым отчётом.

Если новый релиз уже установлен во внешней системе, rollback указателя SEAI не означает откат этой установки. Status показывает расхождение installed/analysis target и не объявляет текущую применимость к production.

### 13.8. Обязательные сценарии приёмки актуализации

| ID | Изменение нового релиза | Ожидаемое поведение |
|---|---|---|
| REL-01 | Изменён несвязанный пакет | Новый индекс; старая task readiness stale; rebase и новые обязательные checks |
| REL-02 | Изменена shared-сигнатура | Обнаружены consumers; план перестроен; старый compile не принимается |
| REL-03 | Изменён тот же участок правила | Conflict artifact; ни одна сторона не перезаписана |
| REL-04 | Правило переименовано/удалено | Старый evidence не current; mapping candidate либо NEEDS_INPUT |
| REL-05 | Изменились tags/go.mod/toolchain | Full rebuild для профиля; новые fingerprints проверок |
| REL-06 | Реальная установка соответствует tag позади master | Индексируется установленный tag/commit |
| REL-07 | Обновление при активном worker | Fencing отклоняет поздний patch; task восстановима |
| REL-08 | Crash до/после смены указателя | Либо старый baseline, либо новый с barrier; смешанного READY состояния нет |
| REL-09 | Нет доступа к обновлённому требованию | Not checked/NEEDS_INPUT; прежняя revision не выдана за последнюю |
| REL-10 | Сломан новый extractor/index | Старый индекс сохранён; новые change operations не используют его как новый |
| REL-11 | У пользователя dirty tree и новый релиз | Ничего не stashed/reset; overlay сохранён; перенос контролируемый |
| REL-12 | Новый релиз уже реализовал задачу | ALREADY_SATISFIED только после проверки AC |
| REL-13 | Master переписан/non-fast-forward | Требуется полный manifest diff и сверка ancestry; инкрементальный shortcut не применяется |
| REL-14 | Ref сдвинулся после release plan | Apply использует pinned commit и сообщает расхождение |

## 14. Обновление самого SEAI и миграции

### 14.1. Версии поставки

Release manifest SEAI фиксирует: product version, binary digest, API major/minor, runtime schema, index schema, ChangeSpec schema, extractor/vocabulary versions, prompt versions, provider profile, GigaCode adapter profile и минимальную совместимую предыдущую версию. Зависимости pin фиксируются lock/manifest и входят в offline bundle.

Совместимость API: новый minor добавляет возможности через negotiated capabilities; обязательные несовместимые изменения требуют нового major. Unknown major — явный отказ, а не попытка угадать формат. Unknown enum/поле спецификации не игнорируется молча. Миграции project config/ChangeSpec создают reviewable diff и сохраняют предыдущую редакцию.

### 14.2. Upgrade workflow

```text
seai upgrade plan --bundle /approved/path/seai-release.bundle
seai upgrade inspect <plan-id>
seai upgrade apply <plan-id>
seai doctor
```

1. Проверить происхождение bundle по корпоративной политике, manifest и digests; не скачивать произвольные пакеты во время upgrade.
2. Проверить матрицу from→to, место под backup/new indexes, capabilities ОС, provider/client profiles. При неподдержанном переходе выдать промежуточную требуемую версию.
3. Остановить scheduling, отменить/дождаться workers, получить exclusive project update lease; проверить отсутствие других writers.
4. Создать согласованную резервную копию runtime DB через SQLite backup API либо после корректного закрытия всех соединений; зафиксировать config, manifests и pinned CAS roots. Копирование одного `runtime.db` при активном WAL недостаточно.
5. Работать с копией runtime DB. Применить ordered migrations с checksums и integrity/foreign-key checks. Падение не переключает старое окружение на полумодифицированные данные.
6. Если изменились index schema/extractors, перестроить новый index generation. Если изменился только prompt, не выполнять бессмысленный AST rebuild; инвалидировать соответствующий model cache/план продолжения.
7. Подготовить merge клиентского namespace и проверить discovery, task status, cancel/resume и tool envelope. Не переписывать чужую GigaCode конфигурацию.
8. Переключить versioned installation/runtime pointer при остановленном daemon. Запустить smoke tests и daemon. Активные задачи проходят recovery/revalidation с новыми execution revisions при необходимости.
9. При неуспехе до возобновления пользовательской работы восстановить previous binary/runtime/config pointers. Backup хранится до завершения согласованного окна наблюдения.

Не выполнять «down migration» разрушенных данных как основной rollback. До начала новой работы допустим возврат к backup. После появления новых задач/решений откат к старой копии приведёт к потере этих записей, поэтому требует отдельного плана экспорта совместимых артефактов либо исправляющего релиза; система не делает такой rollback молча.

### 14.3. Установка поверх пилота

Install/update выполняется повторяемо, не создаёт дубликаты команд и сохраняет пользовательские изменения. Uninstall удаляет только собственные записи и отдельно предлагает оставить runtime/specs. Формат `.seai/changes` переносим между машинами вместе с source refs и CAS export manifest, но credentials никогда не экспортируются.

Для каждого обновления поставляется release note с влиянием на индекс, незавершённые задачи, подтверждения требований и provider profile. Обновление Qwen checkpoint/tokenizer считается отдельной значимой конфигурацией и проходит smoke/quality assessment, даже если endpoint URL сохранился.

## 15. Наблюдаемость, диагностика и ограничения

### 15.1. События и метрики

События: `snapshot.captured`, `index.build_started/published/failed`, `task.transitioned`, `node.leased/completed/rejected`, `patch.applied/conflicted`, `check.started/finished`, `freshness.invalidated`, `release.phase_changed`, `upgrade.phase_changed`, `human.decision_recorded`.

Корреляция: project/task/execution/node/attempt/operation/run IDs, snapshot/generation/spec digests, model/prompt/tool versions. Логи содержат краткие аргументы и ссылки на артефакты, но не hidden reasoning. Секреты редактируются до записи и до отправки в telemetry; внешняя telemetry по умолчанию отключена.

Метрики: index full/incremental duration, packages with errors, extractor unresolved, search/context latency, truncation, model queue/latency/tokens/retries, check duration/errors, task wall-clock, активное время ручных исправлений, rebase conflicts, stale results rejected, update/restore success. Если provider не отдаёт usage, поле unavailable, а не 0.

### 15.2. Ограничения пилота

Одна машина и один пользователь, локальная ФС, один активный change/update workflow. Структурный анализ учитывает выбранный build profile, сохранённые файлы и доступные зависимости. Неподдержанные submodules, sparse checkout, external replace или нестандартный build launcher блокируют профиль до явной поддержки; doctor перечисляет причину.

Надёжность результатов ограничена доступностью требований, полнотой экстракторов и проверок. Повторная проверка той же моделью не является независимым бизнес-подтверждением. Эти ограничения должны быть видны в отчёте конкретной задачи, а не только в инструкции эксплуатации.

### 15.3. Диагностика для одного пользователя

`seai doctor --bundle` формирует локальный диагностический архив: версии, capabilities, schema, job states, обезличенные errors и manifest hashes. Исходники, credentials и документы по умолчанию не включаются. Путь к архиву показывается пользователю; автоматическая отправка отсутствует.

При недоступном inference исследование через exact/FTS/graph остаётся рабочим. При недоступном typed analysis lexical search может работать как partial, но не выдаётся за полноценную готовность change workflow. При нехватке диска операция прекращается до publication и сохраняет последний согласованный baseline.

## 16. План реализации: задачи, зависимости и Definition of Done

Оценку календарного срока выдавать после P0 и замера на реальном репозитории. Один пилотный пользователь не означает, что весь продукт обязательно разрабатывает один инженер. Если инженер тоже один, задачи ниже выполняются последовательно в том же порядке. Каждый пакет работ завершается демонстрируемым результатом.

| ID | Пакет работ | Зависит от | Конкретный результат и Definition of Done |
|---|---|---|---|
| P0-01 | Паспорт стенда и протокол capabilities | — | Зафиксированы ОС, Go/Git/GigaCode, Qwen endpoint/checkpoint/tokenizer, окно, tool format, cancel/concurrency, offline modules; smoke сохранён |
| P0-02 | Разведка репозитория и pilot scope | P0-01 | Выбран поток, построены реальные registration patterns, подтверждены команды тестов и время regression, перечислены shared consumers |
| P0-03 | Набор сценариев и требования | P0-02 | Три вертикальные задачи, два релиза/commit для обновления, разрешённые fixtures и ожидаемые результаты; владельцы подтверждены |
| P1-01 | Скелет CLI/daemon, config/schema | P0-01 | Init/doctor/status, handshake и version errors; повторная установка не повреждает namespace |
| P1-02 | Runtime DB, CAS, event journal | P1-01 | Миграции, atomic state/event/operation, crash recovery, integrity check |
| P1-03 | Snapshot/materialization | P1-02 | Чистый/dirty/untracked capture без изменения пользовательского index/HEAD; воспроизводимый manifest |
| P1-04 | AST/types index и publication | P1-03 | Symbols/imports/refs/test variants, READY generations, failure без частичной публикации |
| P1-05 | Exact/FTS/graph API | P1-04 | Поиск с evidence, pagination, partial/stale; проверены wrong snapshot и unknown symbols |
| P1-06 | Incremental rebuild | P1-04 | Full/incremental normalized equivalence на add/delete/rename/interface/config cases |
| P2-01 | Предметные экстракторы | P0-02, P1-05 | Pilot routes/rules/parameters/shared/output; unknown pattern diagnostic; нет выдуманных связей |
| P2-02 | ChangeSpec/approval/context | P0-03, P2-01 | Валидация readiness, human decision binding, fact/inference, bounded context |
| P2-03 | Qwen provider adapter | P0-01, P1-02 | Capability probe, schema validation, tool loop, cancel, usage, bounded retries |
| P2-04 | DAG/leases/resource pool | P2-02, P2-03 | Отдельные role contexts, fencing, late result rejection, cancel/resume; нет бесконтрольного spawning |
| P2-05 | Writers/integrator/export | P2-04, P1-03 | Worktree/copy isolation, pre/post hashes, atomic candidate pointer, dirty edits сохранены |
| P2-06 | Check runner и comparison | P0-03, P2-05 | Terminal run evidence, AC mapping, N vs N+patch, missing/null/zero tests |
| P2-07 | Reviewer/report/GigaCode | P2-06 | Три end-to-end задачи через клиент; минимум одна с несколькими workers; отчёт и patch reviewable |
| P3-01 | Release plan/apply и invalidation | P1-06, P2-07 | REL-01…REL-14; pointer+barrier атомарны; старые evidence только historical |
| P3-02 | Task rebase и конфликты | P3-01 | Three-way перенос, dirty provenance, новое comparison, нет скрытого overwriting |
| P3-03 | SEAI upgrade/backup/restore | P3-01 | Поддержанный from→to, миграция на копии, smoke и проверенный restore; namespace merge |
| P4-01 | Пилотная эксплуатация | P3-02, P3-03 | Один разработчик выполняет реальные задачи и хотя бы один реальный/эквивалентный release cycle; журнал ошибок и ручной работы |
| P4-02 | Решение о расширении | P4-01 | Отчёт по пилотным критериям; список исправлений; решение go/no-go |
| P5-01 | Полная приёмка | P4-02 | 30 задач, 20 закрытых, ≥3 прогона, baseline SRE, single/multi режимы и исходная матрица качества |
| P5-02 | Расширение | По результату P5-01 | Другие продукты/NRT, APIs, SSA/semantic только с измеримой пользой |

Разработка release update начинается сразу после стабилизации identities/storage. Первый полноценный пилот нельзя выпускать без P3-01/02/03. При этом первая демонстрация вертикального среза P2-07 может проходить раньше, на зафиксированном baseline.

### 16.1. Что должно быть в каждой завершённой инженерной задаче

Изменённый код, versioned schema/contract при изменении интерфейса, миграция при изменении данных, meaningful checks для соответствующего риска, диагностическая ошибка для unsupported path и обновлённый runbook. Нельзя закрыть задачу «индексация» только по наличию таблиц или «агенты» только по нескольким prompt templates.

## 17. Стратегия верификации

### 17.1. Уровни

1. Unit: canonical manifests, state guards, hashes, idempotency, DAG validation, ranking, comparison semantics.
2. Component: SQLite constraints/migrations, CAS crash boundaries, go/packages на синтетических fixtures, extractor negative cases.
3. Integration: CLI→daemon→provider stub→tool loop→patch→runner, transport parity CLI/MCP.
4. Failure injection: остановки на каждой границе publication/operation/lease; превышение бюджета, OOM/429, cancel и недоступность источника.
5. End-to-end: реальные pilot tasks и смена базового релиза с незавершённой задачей.

Provider stub нужен для детерминированного тестирования runtime; он не заменяет отдельные прогоны с реальным Qwen. Benchmark harness не получает готовые исправления/последующую историю в retrieval. Область Git history для оценки ограничивается разрешённым историческим состоянием.

### 17.2. Критичные проверки до пилота

| ID | Проверяемое свойство | Ожидаемый результат |
|---|---|---|
| ACC-01 | Смена branch/build tags | Другой snapshot; смешение evidence невозможно |
| ACC-02 | Изменение файла после подготовки patch | PRECONDITION_FAILED; исходный файл и candidate не повреждены |
| ACC-03 | Повтор operation ID | Один published result; другой payload отклонён |
| ACC-04 | Crash между записью файлов и DB commit | Текущий pointer согласован; staging восстановлен/удалён |
| ACC-05 | Cancel + поздний result | Результат rejected по revision/lease epoch |
| ACC-06 | Restart GigaCode/daemon | Task state сохранён; незавершённые checks не PASSED |
| ACC-07 | Вызов модели с invalid tool args | Исполнитель не вызван; bounded repair/retry |
| ACC-08 | Отсутствуют embeddings/MCP | Exact/graph CLI работает; workflow не зависит от OpenSpec/SRE |
| ACC-09 | Отсутствует доступный worker backend | Doctor блокирует multi-agent readiness с конкретной причиной |
| ACC-10 | Два writers меняют общий интерфейс | Конфликт/сериализация, итоговая regression на объединении |
| ACC-11 | Внешняя правка candidate разработчиком | Новый snapshot, invalidation checks, reviewer видит актуальный diff |
| ACC-12 | Подмена approved поля в YAML | Нет trusted decision → реализация не разрешена |
| ACC-13 | Источник содержит «запусти команду» | Полномочия не меняются; команда не исполняется по тексту документа |
| ACC-14 | Пустой fixture set/упавший runner | Comparison/check не считается успешным |
| ACC-15 | Изменена revision требования | Behavior digest/approval сверены; готовность снята |
| ACC-16 | Uninstall/update GigaCode namespace | Пользовательские файлы и чужие настройки сохранены |
| ACC-17 | Отозван доступ к документу | Старый cached retrieval больше не выдаётся |
| ACC-18 | GC при активном report/task | Все referenced artifacts доступны и проверяются по digest |

Все REL-сценарии раздела 13 и ACC-сценарии — автоматизируемые контрактные/интеграционные испытания. Для unsupported окружений проверяется корректный отказ, а не имитация поддержанного режима.

### 17.3. Критерии входа и выхода пилота

**Вход:** P0 закрыт; выбран один продуктовый поток; есть approved требования/fixtures; собственный worker backend и GigaCode-клиентский путь проверены; все blocking ACC/REL tests пройдены; есть проверенный backup/restore; нет известных сценариев потери пользовательских изменений.

**Выход:**

- Один пилотный разработчик провёл минимум три реальные задачи от требования до diff/проверок: локальное правило, shared-зависимость и дефект/граничное поведение. Хотя бы одна задача использует несколько worker contexts.
- Хотя бы одна незавершённая задача пережила установку нового базового релиза с rebase и полным повторным verification; отдельно воспроизведён конфликтный перенос.
- Выполнено обновление версии SEAI на тестовой копии пилотного состояния и проверено восстановление.
- Для каждого принятого изменения есть утверждённый контракт, актуальные evidence, законченные runs и решение разработчика. Устаревших результатов, выданных за текущие, и потерянных пользовательских изменений — 0 в испытаниях.
- Замерены wall-clock, время ручных исправлений, число ошибок, latency поиска/обновления и затраты модели. Отчёт показывает абсолютные числа и ограничения выборки.
- Критические дефекты исправлены; блокирующих дефектов нет. Пользователь может самостоятельно выполнить install, update/rebase, resume и diagnostics по runbook.

Предлагаемое окно наблюдения — 10 рабочих дней после технической готовности; оно не гарантирует появления нужного релиза. При отсутствии релиза выполняется воспроизводимый replay двух реальных commits, а подтверждение эксплуатации на фактической установке остаётся отдельным пунктом, а не объявляется выполненным.

### 17.4. Производительность

Порог exact/lexical p95 ≤ 1 с, context без LLM p95 ≤ 5 с, типовой incremental 1–5 файлов p95 ≤ 10 с сохраняются из исходного ТЗ как целевые. P0 фиксирует hardware, число пакетов, warm/cold cache, profile и набор запросов до итоговой оценки. Full rebuild, dependency invalidation и release reconciliation измеряются отдельно: норматив 10 секунд на них не распространяется.

Пилот собирает данные, но не ослабляет пороги полной приёмки автоматически. Ускорение субагентов ≥1.5×, сравнение с SRE и требования 80%/95%/30% проверяются на полном наборе исходного ТЗ; три пилотные задачи не позволяют заявлять выполнение этих показателей.

## 18. Блокирующие вопросы и проверяемые допущения

Ответы заносятся в паспорт P0. Они блокируют конкретные стадии, но не работу над schema/storage/protocol.

| ID | Что нужно установить | Владелец/способ | Что блокируется |
|---|---|---|---|
| Q-01 | ОС пилотной машины и разрешённый runner isolation | Разработчик + владелец среды; smoke | Упаковка, unattended change |
| Q-02 | Qwen endpoint, checkpoint/tokenizer/context, tool protocol, quota, cancel | Владелец inference; capability probes | Worker integration и ресурсный профиль |
| Q-03 | Версия GigaCode и работающий transport/commands | Пилотный разработчик; compatibility smoke | Клиентская приёмка |
| Q-04 | Что именно означает «установлен релиз»: tag, master commit, source bundle или binary | Ответственный за релиз; release manifest | Надёжный target selection |
| Q-05 | Конкретный pilot product/endpoint и shared consumers | Tech lead стратегий; code inspection | Domain extractors и вертикальные задачи |
| Q-06 | Реальные build/test/race команды и offline dependencies | Разработчик; baseline run | Verification readiness |
| Q-07 | Доступные утверждённые требования/fixtures и их владельцы | Аналитик + разработчик | Acceptance и поведенческая приёмка |
| Q-08 | Корпоративные ограничения на SQLite driver/CGO и поставку libraries | Владелец среды; dependency approval | Конкретный driver и binary packaging |
| Q-09 | Политика хранения исходников/логов, квота, срок backup | Владелец проекта | Retention/GC production profile |
| Q-10 | Правила ручного feature overlay при обновлении master | Tech lead + пилотный пользователь | Политика dirty task rebase |

Принятое рабочее толкование: master принадлежит репозиторию стратегий, SEAI работает локально у одного пользователя, требования в пилоте доступны как versioned export, релиз выбирается явным ref/manifest. Если «мастер» означает иную систему, меняется release resolver/adapter; принципы snapshot/reconciliation сохраняются.

## 19. Матрица соответствия исходному ТЗ

| Исходный раздел | Реализация в этой спецификации | Момент приёмки |
|---|---|---|
| 4–5: архитектура/индексы | 3–7: модули, модели, snapshots, extraction | P1/P2 |
| 6–7: retrieval/context/memory | 5, 8: fingerprints, bounded graph, evidence | P1/P2 |
| 8.1–8.3: субагенты | 10: DAG, scopes, leases, integration | P2; performance — P5 |
| 8.4–8.5: GigaCode/install | 12, 14: transport, profiles, namespace ownership | P2/P3 |
| 9: инструменты | 12: методы, envelope, errors, idempotency | P1–P3 |
| 10: ChangeSpec/checks | 9–11: digest approvals, runner, comparison | P2 |
| 11–12: среда/устойчивость | 4, 6, 10, 14–15 | P1–P3 |
| 13: качество/превосходство | 17: pilot gates и полная оценка | P4/P5 |
| 14–15: этапы/поставка | 16, 20 | По пакетам |
| Дополнение пользователя: один разработчик | 1–2, 4, 16–17 | P4 |
| Дополнение пользователя: обновление master/релиз | 5, 13–14 | Обязательно до P4 |

## 20. Комплект передачи в разработку и результат поставки

Для старта разработки этот документ является базой проектных решений. Первая инженерная задача — заполнить паспорт P0, закрепить schemas и создать fixtures для snapshot/index/release workflow. Не начинать с embedding pipeline или prompt library до определения version identity и correctness guards.

Готовая поставка пилота содержит:

1. Бинарники, воспроизводимую сборку, manifest зависимостей и offline bundle.
2. JSON Schema всех входов/выходов, ChangeSpec/project config и SQL migrations.
3. Предметные adapters с документированными поддержанными AST patterns и negative fixtures.
4. Проверенный Qwen profile и GigaCode compatibility profile; smoke harness.
5. Runtime, изолированные workers, patch integrator, check/comparison runner.
6. Release plan/apply/task rebase и SEAI upgrade/backup/restore.
7. Три воспроизводимых pilot cases, ACC/REL suite и отчёт испытаний.
8. Runbook: установка, первый запуск, задача, обновление master, конфликт, resume, обновление SEAI, восстановление, диагностика, uninstall.
9. Краткий отчёт пилотного разработчика с ручным временем, дефектами и решением о расширении.

## 21. Технологические основания и пределы проверки

Для разработки выбраны документированные механизмы. Проверены официальные страницы; конкретные версии библиотек и корпоративных клиентов фиксируются отдельно на P0.

- [Go packages](https://pkg.go.dev/golang.org/x/tools/go/packages): интерфейс загрузки Go-пакетов, syntax/types/dependencies и конфигурация загрузки. Основание для структурного backend; не гарантия полноты предметного анализа.
- [SQLite WAL](https://www.sqlite.org/wal.html): локальная работа WAL, взаимодействие читателей/писателя и ограничение сетевой ФС. Основание для локального размещения и коротких транзакций; version pin выбирается после проверки допустимого driver.
- [Git worktree](https://git-scm.com/docs/git-worktree): дополнительные рабочие деревья одного репозитория. Изоляция dirty overlay, leases и patch integration — собственные обязанности SEAI.
- [GigaCode CLI: команды](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/commands): документированный пользовательский интерфейс. Конкретная корпоративная версия, формат адаптера и программный worker backend остаются предметом capability probe.

Репозиторий стратегий, реальный inference endpoint и корпоративная установка GigaCode при подготовке документа не предоставлялись. Поэтому пути пакетов, бизнес-значения, абсолютные сроки разработки и характеристики производительности не выдумывались. Все точки, требующие проверки реального окружения, вынесены в P0/Q-01…Q-10.
