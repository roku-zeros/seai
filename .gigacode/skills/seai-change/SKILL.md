---
name: seai-change
description: Применять ограниченные изменения Go-кода в изолированный candidate через SEAI по принятому контракту, плану, scope и before hashes без прямой правки checkout.
---

# Изменение только через SEAI

Соблюдай `.gigacode/seai/POLICY.md`. Используй task/spec/plan из seai-plan. Проверь `task status ID`, freshness и состояние. Для change требуется человеческое решение по точному behavior digest: выведи человеку команду `seai --data DATA task approve ID --actor ACTOR`; сам её не выполняй, не вызывай private route и не эмулируй ввод/PTY. При изменении контракта прежнее approval может быть снято. Не называй бизнес-изменение refactor ради обхода решения.

1. Читай текущие bytes/mode/hash через `read PATH --snapshot CANDIDATE_ID`, а не native file read. Candidate после предыдущего patch отличается от baseline; typed graph пока относится к baseline.
2. Подготовь управляющий `seai.patch/v1`: актуальные base_snapshot_id/spec_digest и file operations в scope/плане. Для существующего пути обязательны before_digest+before_mode; для нового before_digest:null. After содержит content либо content_base64, after_digest и mode=100644/100755; delete:true без after. До 32 paths, after <=64 KiB/file и 512 KiB total. Не обходи лимиты прямой записью.
3. Сохрани patch-предложение в `.seai/changes/` или вне исходников; вызови `patch apply ID --patch PATH` с актуальной execution revision/epoch. Ошибки scope/preconditions/stale/approval — причина обновить источники/план или запросить решение, не слепо повторять.
4. Читай новый candidate по snapshot ID, получи `task diff ID`. Не применяй результат в основной checkout. Если пользователь запросил выгрузку, `task export ID --output PATH --allow-incomplete` создаёт diagnostic patch+manifest. Не перезаписывай файлы самостоятельно.

В 0.3 после patch вызови task verify ID, дождись run status RUN_ID; используй checks profiles и run logs RUN_ID --profile PROFILE. Для Qwen orchestration нужны provider_ready и task run. Readiness бери из текущего task status, не вычисляй по собственному мнению. Для REVIEW_READY нужны все checks плана, compile/tests/comparison, AC fixture_digest и независимый review. Human task review/requirements, как и approve, не выполняй за пользователя. Планируемые проверки и найденные тестовые определения не равны PASSED. Не выполняй go test/make/IDE runner или Git apply напрямую. Полные команды/контракты — docs/EXECUTION.md и docs/WORKFLOW.md из checkout SEAI. При отсутствии необходимой операции — BLOCKED_BY_SEAI_CAPABILITY без обхода SEAI.
