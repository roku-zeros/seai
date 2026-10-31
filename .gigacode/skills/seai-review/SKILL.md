---
name: seai-review
description: Проверять исходники и task diff через SEAI по snapshot evidence, различая baseline, candidate и отсутствие выполненных product checks.
---

# Ревью через SEAI

Соблюдай `.gigacode/seai/POLICY.md`. Для задачи запроси `task status ID`, затем `task diff ID` и `read PATH --snapshot BASELINE_OR_CANDIDATE`. Для отдельных функций используй seai-research. Все замечания связывай с конкретными источниками/ranges; live файлы, git diff и IDE buffers напрямую не читай.

Сопоставь scope, expected/preserved behavior и AC с diff; различай найденные дефекты, гипотезы и неохваченные зависимости. Graph относится к указанному generation baseline; новый candidate ещё не прошёл повторную типизацию или тесты. Diff/state могут быть историческими и stale — покажи это явно.

В этой версии task diff/export доступны, но product check runner и независимый review verdict отсутствуют. Наличие тестовой функции не является terminal run evidence. Не запускай проверки напрямую и не объявляй PASSED/REVIEW_READY/COMPLETED. Публикуй review findings и gaps; diagnostic export с --allow-incomplete остаётся NOT_REVIEW_READY даже при отсутствии найденных замечаний.
