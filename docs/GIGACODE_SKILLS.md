# GigaCode: работа с кодом только через SEAI

Пакет содержит пять project skills и постоянную политику. Они направляют GigaCode к SEAI и требуют сообщать об отсутствующих возможностях вместо прямого чтения/правок. Это инструкции модели, не изоляция ОС и не блокировка разрешённых ей native tools.

## Состав

| Skill | Когда применяется | Текущий результат |
|---|---|---|
| seai-connect | Подключить runtime, проверить root, обновить индекс | doctor/status/index/job |
| seai-research | Вопросы о функциях, типах, правилах и тестах | search + evidence + ограничения |
| seai-plan | Составить план изменения по источникам | План в ответе, без применения |
| seai-change | Исправить, реализовать, рефакторить, добавить тесты | Исследование/предложение; применение блокируется отсутствием patch/check API |
| seai-review | Проверить определение/логику по источникам | Ограниченное ревью; diff/test verdict без API блокируется |

В `.gigacode/seai/POLICY.md` указаны точные разрешённые CLI-команды, freshness guards и правила источников. `GIGACODE.md` загружает политику постоянно, чтобы она действовала и тогда, когда модель не выбрала отдельный skill. Управляющие документы можно читать напрямую; исходники исследуемого проекта, тесты и build-файлы доступны агенту только из JSON SEAI. Наличие test symbol не означает успешный test run.

## Установка в ваш проект

Сначала собери SEAI и запусти seaid с `--repo` вашего проекта и runtime вне его дерева. [Установка самого инструмента](AGENT_QUICKSTART.md) — отдельный административный этап, не работа с целевым кодом через обходной канал.

Затем из checkout SEAI выполни:

```sh
python3 scripts/install_gigacode_skills.py \
  --project /absolute/path/strategies \
  --seai /absolute/path/seai/build/seai \
  --data /absolute/path/seai-runtime
```

Нужно указать фактические absolute пути. `--project` должен быть Git root. Установщик не индексирует и не читает product code. Он копирует skills/policy, записывает `.gigacode/seai/session.json`, добавляет свой блок к GIGACODE.md и правило игнорирования session.json. Существующие инструкции сохраняются, отличающиеся managed-файлы вызывают ошибку до записи изменений. Пользовательские конфликты надо объединить вручную; молчаливого overwrite нет. Повторная установка того же набора идемпотентна. session.json содержит только локальные пути, не credentials; он исключён из Git.

CLI layout по умолчанию — `.gigacode/skills/`. Для VS Code используй дополнительный аргумент `--layout vscode`: skills попадут в `.gigacode_vsc/skills/`, управляющая политика/config останутся в `.gigacode/seai/`. Если клиент не подхватил память автоматически, явно прикрепи GIGACODE.md и POLICY.md и проверь результат. Глобальные personal skills и настройки разрешений клиента установщик не меняет.

Перезапусти GigaCode. В CLI проверь `/skills` (пять имён) и `/memory` (GIGACODE.md/политика). Для IDE проверь список Skills и подключённые project instructions. Формат и размещение взяты из [CLI skills](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/skills), [memory](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/memory), [VS Code skills](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-for-vscode/skills). Реальная совместимость конкретной корпоративной версии не заявляется.

## Как спрашивать

```text
Работай с кодом только через SEAI. Проверь подключение,
обнови индекс и объясни Eligible. Покажи evidence и snapshot ID.
Если SEAI не даёт нужной операции, сообщи gap и не обходи его.
```

Skills выбираются моделью по description. Следующие запросы активируют разные процессы: «Подключи SEAI», «Почему Eligible отклоняет запрос?», «Составь план изменения», «Исправь правило», «Проверь эту функцию». Последние два не создают отсутствующие capabilities. Сейчас агент должен честно сообщить `BLOCKED_BY_SEAI_CAPABILITY` для применения patch, полноценного diff review и запуска product checks. Это статус ответа агента, не реализованная RPC-ошибка.

В checkout самого SEAI доступны также `/seai:setup` и `/seai:ask` как prompt shortcuts. В отдельный target проект установщик переносит skills/policy/session, а не shortcuts; там можно задавать обычные вопросы после проверки `/skills`.

## Проверка соблюдения режима

Для поиска должны быть видны вызовы seai doctor/project/index/search и JSON-источники, без native file read/rg/git grep. Задай вопрос о неизвестном символе: ожидается gap/уточнение, а не чтение дерева. Попроси исправление: ожидается блокировка отсутствующего patch/check API, а не native edit. Попроси подтвердить тесты: ожидается отсутствие terminal run evidence, а не прямой go test.

Если нужны гарантии, а не следование промпту, потребуется отдельно проверенный профиль прав GigaCode: закрыть direct read/edit/search и исполнение произвольного shell, оставив только контролируемые обращения к SEAI. В текущей поставке такого профиля и broker/MCP нет; не включай непроверенный deny-конфиг с неизвестными именами tools. При полном запрете terminal потребуется реализовать иной transport к SEAI. Skills сами этих технических ограничений не устанавливают.
