---
name: seai-review
description: Проверять исходники и task diff через SEAI по snapshot evidence, различая baseline, candidate и отсутствие выполненных product checks.
---

# Ревью через SEAI

Соблюдай `.gigacode/seai/POLICY.md`. Для задачи запроси `task status ID`, затем `task diff ID` и `read PATH --snapshot BASELINE_OR_CANDIDATE`. Для отдельных функций используй seai-research. Все замечания связывай с конкретными источниками/ranges; live файлы, git diff и IDE buffers напрямую не читай.

Сопоставь scope, expected/preserved behavior и AC с diff; различай найденные дефекты, гипотезы и неохваченные зависимости. Graph относится к указанному generation baseline; новый candidate ещё не прошёл повторную типизацию или тесты. Diff/state могут быть историческими и stale — покажи это явно.

В 0.3 checks запускаются через task verify ID или checks run ID --profile PROFILE, затем run status и run logs. Используй актуальный check_summary из task status, различай обнаруженный тест и его фактический запуск. Проверь criterion fixture_digest против checks profiles. Новый check set инвалидирует прежний review. Independent Qwen reviewer вызывается внутри task run; без Qwen человек может выполнить task review, но агент не вызывает эту команду/private route и не имитирует TTY. Не объявляй REVIEW_READY по собственному заключению: его возвращает SEAI после gates; COMPLETED автоматически не выставляется. При неподтверждённых gates доступен diagnostic export --allow-incomplete. Подробности — docs/EXECUTION.md.
