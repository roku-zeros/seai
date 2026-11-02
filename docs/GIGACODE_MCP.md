# GigaCode CLI через SEAI MCP (0.4.2)

MCP даёт GigaCode прямые инструменты SEAI вместо вызовов shell. Skills и GIGACODE.md выбирают порядок работы; настройки MCP подключают инструменты и задают доверие. Это разные части установки. Техническое ограничение всех остальных инструментов GigaCode не реализуется одним skill: контролируйте фактические tool calls и правила клиента.

## Обновление и установка

Все команды выполняются из checkout SEAI. Замените абсолютные пути на свои. PROJECT — корень Git-проекта, WORKSPACE — папка, из которой запускается GigaCode (может содержать PROJECT). DATA — существующий private runtime вне проекта. Не используйте пути/PID со скриншотов.

1. Обновите checkout (`git pull --ff-only`) и выполните `make build`. Остановите **свой** прежний seaid через используемый process manager/SIGTERM и запустите новый бинарник с прежними `--repo`, `--data`, `--project`, `--config`. Для медленной FS добавьте `--freshness-budget 5m`; это настраивает ожидание, не ускоряет диск. Runtime schema остаётся 3.
2. Установите/обновите инструкции:

```sh
python3 scripts/install_gigacode_skills.py \
  --workspace /absolute/workspace --project /absolute/workspace/project \
  --seai /absolute/seai/build/seai --data /absolute/seai-runtime \
  --layout cli --upgrade
```

`--upgrade` явно заменяет управляемые SEAI skills/policy/session; старые изменённые версии сохраняются в `.gigacode/seai/backups/`. Текст вне SEAI-блока GIGACODE.md сохраняется. Передавайте upgrade только для обновления установки SEAI; пользовательские изменения управляемых файлов могут потребовать переноса из backup. По умолчанию установщик отказывается заменять отличающиеся файлы.

3. Подключите MCP. Режим с минимальным числом подтверждений **для ограниченных инструментов SEAI**:

```sh
python3 scripts/configure_gigacode_mcp.py \
  --workspace /absolute/workspace --project /absolute/workspace/project \
  --seai /absolute/seai/build/seai --data /absolute/seai-runtime \
  --trust-readonly --candidate-writes --trust-candidate-writes
```

Скрипт объединяет `.gigacode/settings.json` с существующими настройками. Чужие MCP entries и настройки shell не меняются. Конфликтующая запись `seai`/`seai-candidate` вызывает отказ без записи — объедините её вручную. Для режима только чтения уберите `--candidate-writes --trust-candidate-writes`. Чтобы подтверждать каждую мутацию private candidate, уберите только `--trust-candidate-writes`.

4. Перезапустите GigaCode из WORKSPACE. В `/mcp` сервер `seai` должен быть connected. Попросите: «Объясни, где рассчитывается условие X». Первый вызов — `seai_connect`, затем `seai_read`; каждое чтение не должно запускать shell. Установка файлов не доказывает загрузку их клиентом.

GigaCode документирует `mcpServers`, project settings и `trust: true`, отключающий подтверждения инструментов конкретного сервера: [официальная инструкция MCP](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/mcp). В SEAI доверие разделено между чтением и ограниченными мутациями; глобальный auto-approve shell не нужен.

## Что доступно агенту

- `seai_connect {}` проверяет root daemon относительно `--repo`, сообщает capabilities и закрепляет current generation. Не делает live scan и не пересобирает индекс.
- `seai_contract {"name":"change"}` возвращает точную схему; также доступны `plan`, `patch`, `runtime`. Проверяйте её до составления контракта.
- `seai_read {"method":"search.entities","params":{"query":"Eligible"}}` ищет определения. `search.text` — literal, case sensitive. Пустой результат не доказывает отсутствия поведения.
- `seai_read {"method":"file.read","params":{"path":"internal/rules.go"}}` читает сохранённые байты без live scan. Для плана используйте file_evidence_id; evidence_id отдельной страницы не обязательно зарегистрирован как chunk.
- `seai_read` также предоставляет files/symbol/references/calls/tests, business rules/flow/impact, profiles, job/run status. `task.get` и `task.diff` возвращают stored metadata с freshness UNKNOWN.
- `seai_candidate` появляется только с флагом сервера `--candidate-writes`. Поддерживает `task.create`, `task.revise`, `task.plan`, `patch.apply`. Параметры — стандартные SEAI RPC: `operation_id`, `expected_project_epoch`, `expected_revision`, `task_id` и соответствующий spec/plan/patch. Для create ревизия 0; epoch из connect, task revision из stored status. После submit опрашивайте `seai_read` → `runs.get` с `params.id=run_id`. Дождитесь SUCCEEDED и читайте result. Повтор с тем же operation_id возвращает тот же receipt.

MCP не предоставляет human approval/review/requirements, произвольный shell, настройку runtime, export, release, index build, запуск проверок или запись исходного working tree. Эти операции остаются явными CLI/операторскими действиями. Human approval поведения сохраняется и при `trust-candidate-writes`; доверие MCP разрешает вызов инструмента, но не одобряет бизнес-изменение.

## Свежесть и время ожидания

Один раз постройте индекс (`seai --data DATA index build --wait`), затем вызовите connect. После нового index build снова вызовите connect. При пустом индексе bridge возвращает NOT_READY, не начинает скрытую установку.

Pinned reads отражают зафиксированную версию и не утверждают актуальность working tree. Для проверок текущего состояния есть async `workspace check`; его успешный результат не отключает live guards у последующих мутаций.

Для медленной FS runtime-конфигурация может содержать `"freshness_budget_seconds": 300` (полная схема runtime остаётся обязательной). Эквивалентный `seaid --freshness-budget 5m` имеет приоритет. Default 15s; максимум 30m. `doctor` показывает действующий лимит. Увеличьте `max_run_seconds`, чтобы хватило на обе live-проверки и работу. Async receipt позволяет не держать клиентский запрос всё это время.

Для CLI доступны `task create/revise/plan ... --async` и `patch apply ... --async`. До настоящего изменения Spec/Plan проходят статическую проверку; исходная защита от устаревшего workspace, CAS mismatch, scope violation и неодобренного behavior сохраняется.

## Что пока остаётся

Реальный двухчасовой прогон на вашем проекте не воспроизведён локально; проверены протокол stdio, границы инструментов, Unix RPC и fixture workflow. Конкретная версия GigaCode и её prompts требуют проверки у вас. Если клиент продолжает спрашивать подтверждение, убедитесь, что вызов идёт через `seai_read`/`seai_candidate`, а не shell, и что trust относится к нужному server entry.

Автосборка/форматирование патча, удобный полнофайловый export, более крупные patch payload, журнал фаз, сохранение плана при косметической ревизии и полнотекстовый поиск остаются отдельными задачами. Настройка настоящих product check profiles и бизнес-адаптеров вашего проекта обязательна для полноценной проверки; отсутствие профилей не заменяется фиктивным PASSED.
