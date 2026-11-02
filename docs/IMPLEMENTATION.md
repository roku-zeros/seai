# Статус реализации 0.4.1-dev

Исправления по пользовательскому прогону большого Go-проекта: bulk tracked/untracked enumeration, Git cat-file --batch, read-only стабильный Observe для freshness без CAS writes, 15s freshness budget и async workspace check. RPC timeout/cancel/отсутствующий socket различаются; client/daemon RPC budget настраивается. UNKNOWN freshness не становится REVIEW_READY.

Agent metadata (.agents/.codex/.gigacode/AGENTS.md/GIGACODE.md) исключена в working/ref capture без изменения ссылок на диске. Source symlinks остаются unsupported; root-scoped reads не выходят за пределы repo. Исторические регулярные metadata-files остаются валидными для backup/restore; runtime schema 3 сохранена. Smoke waits используют monotonic runtime budget и не выдают client timeout за terminal check verdict.

Поведение, обновление daemon и pinned reads — [TROUBLESHOOTING.md](TROUBLESHOOTING.md). Прежние P0/GigaCode/Qwen/Docker и ограничения бизнес-графа не объявляются закрытыми этими исправлениями.

---

# Исторический статус реализации 0.4.0-dev

Добавлены три запрошенных направления: typed предметный Go-индекс; bounded flow/impact с shared consumers; бизнес-сценарии и comparison reports. Поддержаны struct/call/function adapters, constant IDs/context/priority, handlers и source facts, revisioned requirement links, duplicate IDs по context и явные diagnostics. Адаптеры и их config входят в identity structural-v4/business-v1; старые поколения сохраняются.

При require_for_changes:true проверяется candidate domain graph и наличие business fixtures для затронутых rule/context, включая обратные потребители shared helpers. Business cases различают normal/boundary/exception/combination/regression, optional baseline expectation и optional observed trace. Required categories/trace входят в fixture_digest. Readiness не обходит checks/AC/independent review.

Команды, настройка реального Go-проекта и ограничения — [BUSINESS.md](BUSINESS.md). Поставка проверяется на fixtures/business; универсальное распознавание любых Go-конструкций, полный runtime/branch graph, фактический GigaCode/Qwen/Docker и продуктовая приёмка не заявляются. Rules остаются в Go; config задаёт только typed mapping и утверждённые ожидания. Новая SQL migration не нужна: runtime schema 3, immutable index schema/version обновлены.

---

# Исторический статус реализации 0.3.0-dev

Этап 4: operator check profiles, private snapshot execution, timeout/cancel/process groups, bounded durable logs, go test JSON evidence, exact baseline/candidate comparison и привязка AC к fixture_digest; readiness и независимый human review.

Этап 5: OpenAI-compatible Qwen adapter, явный protocol probe, пять изолированных role contexts, фиксированный DAG, до двух research workers, deterministic integrator, registered checks и independent reviewer. Durable attempts/leases/heartbeat, recovery после restart, bounded context/call budgets, resume после candidate checkpoint без повторного patch. Реальный Qwen не настроен по решению пользователя; проверен детерминированный HTTP stub.

Этап 6: pinned release plan/apply/rollback, atomic invalidation barrier, three-way task rebase и явные resolutions/dirty-overlay policies, provenance/history, human requirements reconfirmation. Consistent backup, verified restore в новый каталог, staged local bundle upgrade с checksum/API/schema checks и offline daemon smoke, ручная активация/rollback до новой работы.

Контракты, команды, конфигурация и реальные ограничения — [EXECUTION.md](EXECUTION.md). Выпущенные migrations 001/002 не изменены; добавлена 003. Pilot readiness остаётся false: реальный Qwen/GigaCode, Docker backend, предметные extractors, incremental/FTS5/full dynamic graph и приёмка на продукте остаются отдельной работой. Нет произвольного scheduler, автоматического COMPLETED, auto-checkout, in-place runtime replacement или технического SEAI-only sandbox для GigaCode.

---

# Исторический статус реализации 0.2.0-dev

Рабочий путь: CLI → daemon → snapshots/CAS → AST/types/refs/calls/literal search → ChangeSpec/plan/approval → изолированный candidate → diff/diagnostic export. Контракты и команды — [WORKFLOW.md](WORKFLOW.md).

В этом инкременте выполнены первые три этапа в указанном там scope: paged snapshot/file/symbol reads; local typed refs/static calls/direct tests; freshness и исторические IDs; JSON/YAML task contract/revisions, сохранённый последовательный план и интерактивный human approval; ограниченные immutable file operations, atomic candidate publication, before hashes/modes, scope, diff/export и resume checkpoint.

Ограничения: literal search вместо FTS5, неполный dynamic graph, нет candidate type-check после patch, нет runner/reviewer/ready export, workers/DAG, domain extractors, incremental rebuild, release reconcile/rebase, backup/restore, MCP или permission enforcement. Открытые P0 остаются. Это не завершённый пилот и не полная приёмка исходной спецификации.

Следующие этапы: зарегистрированный check runner и AC evidence; Qwen/worker orchestration; release/upgrade/restore; проверка реального продукта и GigaCode. История предыдущих инженерных срезов ниже.

---

## Исторический статус 0.1

Срез 0.1.0-dev: работающий путь `CLI → daemon → Git snapshot → CAS → go/packages → SQLite generation → search`.

| Пакет спецификации | Результат | Статус |
|---|---|---|
| P0 | Паспорт известных/неизвестных capabilities | Частично, внешние вопросы открыты |
| P1 storage/identities | Canonical manifest, SHA-256 CAS, миграция с checksum, SQLite WAL, события, epoch | Частично; не вся модель 6.2 |
| P1 transport | Unix JSON-RPC handshake, strict decode, CLI, singleton writer | Частично; MCP/автостарт/полная envelope не сделаны |
| P1 snapshot | Ref/dirty + materialization, проверка повторным чтением | Частично; staged provenance и custom config не сделаны |
| P1 index/retrieval | AST/type loader, definitions, exact/substr search, partial diagnostics | Частично; нет graph/FTS/incremental |
| P2–P5 | Worker/change/release/upgrade/приёмка | Не реализованы |

Следующий инкремент:

1. Расширить versioned project/ChangeSpec schemas и YAML validation; ввести полный runtime schema, typed IDs и trusted approval channel.
2. Добавить typed references/calls, FTS5, generation pinning, freshness guards и тесты равенства full/incremental.
3. На предоставленных AST fixtures реализовать предметные extractors; отдельно проверить provider/GigaCode capabilities.
4. Реализовать DAG/leases и изолированные candidate/patch/check workflows с детерминированным provider stub.
5. Release plan/apply с атомарным invalidation barrier; task rebase; upgrade/backup/restore. До этого пилот не выпускать.

Сначала закрепляются contracts и correctness guards. Проверка реального продукта и inference не заявляется по синтетическим fixtures. Полная ACC/REL suite остаётся отдельным результатом.

## Обновление Go 1.26.6

Целевая версия зафиксирована в корневом и fixture go.mod, а также в создаваемых тестами репозиториях. Обновлены прямые зависимости и `x/sync`; стандартные и внутренние пакеты компилируются toolchain Go 1.26.6. Для старого `go` из PATH добавлен явный отказ `UNSUPPORTED_TOOLCHAIN`. Совместимость AST/types проверяется на generic type alias. Версия анализатора `structural-v2` отделяет новые индексы от поколений старого backend. SQL schema v1 и API `seai.api/v1` не изменены.

## GigaCode skills: SEAI-only

Добавлены seai-connect/research/plan/change/review, постоянная GIGACODE.md policy и project installer для CLI/VS Code layout. Prompt shortcuts согласованы с режимом только через SEAI; прямые read/rg/edit/product tests не используются как fallback. Пока отсутствуют patch/check/diff API, соответствующая часть задачи получает статус ответа агента BLOCKED_BY_SEAI_CAPABILITY. Это набор инструкций и установки metadata, не MCP/broker или принудительный sandbox.
