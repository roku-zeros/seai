# GigaCode: работа с кодом только через SEAI

Пакет содержит пять project skills и постоянную политику. Они направляют GigaCode к SEAI и требуют сообщать об отсутствующих возможностях вместо прямого чтения/правок. Это инструкции модели, не изоляция ОС и не блокировка разрешённых ей native tools.

## Состав

| Skill | Когда применяется | Текущий результат |
|---|---|---|
| seai-connect | Подключить runtime, проверить root, обновить индекс | doctor/status/index/job |
| seai-research | Вопросы о функциях, типах, правилах и тестах | search + evidence + ограничения |
| seai-plan | Составить план изменения по источникам | План в ответе, без применения |
| seai-change | Change/refactor through SEAI | Contract/approval/preconditions -> candidate -> registered checks |
| seai-review | Review task diff through SEAI | Current check reports, AC coverage and independent review |

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

Нужно указать фактические absolute пути. `--project` должен быть Git root. Установщик не индексирует и не читает product code. Он копирует skills/policy, записывает `.gigacode/seai/session.json`, добавляет свой блок к GIGACODE.md и правило игнорирования session.json. Существующие инструкции сохраняются, отличающиеся managed-файлы вызывают ошибку до записи изменений. Пользовательские конфликты надо объединить вручную; молчаливого overwrite нет. Повторная установка того же набора идемпотентна. При переходе между версиями, включая 0.2 → 0.3, изменённые managed skill/policy файлы будут конфликтовать: установщик не делает force overwrite. Сохрани свои дополнения и объедини новый набор с установленным; внутри checkout SEAI обновление tracked skills приходит обычным git pull. session.json содержит только локальные пути, не credentials; он исключён из Git.

CLI layout по умолчанию — `.gigacode/skills/`. Для VS Code используй дополнительный аргумент `--layout vscode`: skills попадут в `.gigacode_vsc/skills/`, управляющая политика/config останутся в `.gigacode/seai/`. Если клиент не подхватил память автоматически, явно прикрепи GIGACODE.md и POLICY.md и проверь результат. Глобальные personal skills и настройки разрешений клиента установщик не меняет.

Перезапусти GigaCode. В CLI проверь `/skills` (пять имён) и `/memory` (GIGACODE.md/политика). Для IDE проверь список Skills и подключённые project instructions. Формат и размещение взяты из [CLI skills](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/skills), [memory](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/memory), [VS Code skills](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-for-vscode/skills). Реальная совместимость конкретной корпоративной версии не заявляется.

## Автоматическое использование без слов «через SEAI»

Skills выбираются моделью, поэтому они не должны быть единственным носителем обязательного правила. Постоянный GIGACODE.md теперь прямо требует: для любого запроса о коде сначала подключиться к SEAI по session.json, затем выбрать workflow. Пользователь не обязан каждый раз упоминать инструмент. Для CLI важно, чтобы правило лежало в папке **запуска текущей сессии**, а не только внутри дочернего checkout SEAI.

Если структура такая:

```text
workspace/              ← отсюда запускается gigacode
  my-project/           ← Git root анализируемого проекта
  seai/                 ← checkout инструмента
```

устанавливай так из workspace:

```sh
python3 seai/scripts/install_gigacode_skills.py \
  --workspace "$PWD" \
  --project "$PWD/my-project" \
  --seai "$PWD/seai/build/seai" \
  --data /absolute/path/existing-runtime
```

Укажи реальное имя my-project и тот runtime, для которого уже запущен daemon. В workspace появятся GIGACODE.md и .gigacode/skills/seai-*, а session.json будет указывать на **my-project**, не на общую папку. Сам workspace не обязан быть Git-репозиторием. Существующие инструкции дочернего проекта установщик не изменяет. Без --workspace сохраняется прежнее поведение: установка в --project, и CLI нужно запускать именно оттуда.

Проверка размещения без изменений файлов:

```sh
python3 seai/scripts/install_gigacode_skills.py \
  --check --workspace "$PWD" --project "$PWD/my-project"
```

Добавь `--probe`, чтобы также вызвать doctor/project status через настроенный SEAI и сравнить daemon root. Проверка не индексирует код и не выбирает другой runtime. Exit 3 означает ошибки настройки, exit 0 — ожидаемые файлы на месте (и probe успешен, если запрошен). Наличие правил только в дочерних папках диагностируется как NESTED_RULE_IS_NOT_WORKSPACE_RULE. Проверяется именно выбранный --layout, поэтому VS Code skills не считаются установленными CLI skills.

`client_loaded_verified=false` в отчёте принципиален: файловая проверка не доказывает, что GigaCode загрузил инструкции или соблюдает их. После установки заверши старую CLI-сессию, запусти новую из workspace, проверь /memory на **workspace/GIGACODE.md** и /skills на пять skills. Не ограничивайся вопросом модели «какие правила ты знаешь»: проверь реально загруженные файлы и tool calls.

Затем задай обычный вопрос без подсказки про SEAI:

```text
Объясни структуру проекта и найди функцию обработки заявки.
```

Ожидается чтение только session/skill/policy metadata, затем seai doctor/project status и SEAI source queries. Native read/rg/IDE source search означают нарушение режима. Если runtime недоступен, ожидается сообщение о подключении, а не fallback к исходникам.

В 0.3 доступны task/spec/plan/patch/diff/export, registered checks, Qwen workers и release/maintenance; см. [EXECUTION.md](EXECUTION.md). Readiness берётся из task status. Qwen остаётся ненастроенным до настройки оператора.

В checkout самого SEAI доступны /seai:setup и /seai:ask как необязательные shortcuts. Они не нужны для каждого обычного вопроса. В отдельный workspace установщик переносит skills/policy/session и постоянное правило, а не shortcuts.

## Проверка соблюдения режима

Для поиска должны быть видны вызовы seai doctor/project/index/search и JSON-источники, без native file read/rg/git grep. Задай вопрос о неизвестном символе: ожидается gap/уточнение, а не чтение дерева. Попроси исправление: ожидаются task/spec/plan и patch apply в candidate, а не native edit. Подтверждение change делает человек, модель не вызывает approve. Проверки выполняются через task verify и run status/logs. Если профилей нет, агент сообщает gap без прямого go test.

Если нужны гарантии, а не следование промпту, потребуется отдельно проверенный профиль прав GigaCode: закрыть direct read/edit/search и исполнение произвольного shell, оставив только контролируемые обращения к SEAI. В текущей поставке такого профиля и broker/MCP нет; не включай непроверенный deny-конфиг с неизвестными именами tools. При полном запрете terminal потребуется реализовать иной transport к SEAI. Skills сами этих технических ограничений не устанавливают.
