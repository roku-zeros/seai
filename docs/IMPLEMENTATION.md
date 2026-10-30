# Статус реализации

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
