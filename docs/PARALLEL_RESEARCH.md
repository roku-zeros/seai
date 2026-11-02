# Параллельный ресерч в GigaCode (0.4.3-dev)

SEAI теперь координирует несколько read-only исследователей одной версии проекта. Например, один изучает правила/ветвления, второй — потребителей и зависимости, третий — тесты и границы. Основной агент собирает сохранённые отчёты, сверяет источники и отвечает пользователю. Это отдельный режим исследования, без создания ChangeSpec и изменения кода.

Модели запускает ваш GigaCode через фоновые fork-субагенты. Отдельный Qwen endpoint не нужен. Daemon SEAI выдаёт задания и принимает отчёты; сам GigaCode или новые модели он не запускает. В [официальной документации GigaCode](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/subagents) описаны background fork и отсутствие автоматического возврата результата в родительский диалог. Поэтому отчёты передаются через SEAI, а не через текст fork-окна. Named subagents могут блокировать родителя; их наличие само по себе не подтверждает параллельность.

## Установка

Из обновлённого checkout SEAI выполните `make build` и перезапустите свой daemon с прежними параметрами. Runtime schema остаётся 3, миграции не меняются. Затем обновите инструкции и добавьте отдельный MCP-сервер исследовательской координации:

```sh
python3 scripts/install_gigacode_skills.py \
  --workspace /absolute/workspace --project /absolute/workspace/project \
  --seai /absolute/seai/build/seai --data /absolute/seai-runtime \
  --layout cli --upgrade

python3 scripts/configure_gigacode_mcp.py \
  --workspace /absolute/workspace --project /absolute/workspace/project \
  --seai /absolute/seai/build/seai --data /absolute/seai-runtime \
  --trust-readonly --research --trust-research
```

Скрипт сохраняет прочие настройки MCP и shell. Существующие отличающиеся записи требуют ручного объединения; не меняйте путь runtime случайно. Если раньше seai был установлен с другими trust/параметрами, передайте согласованные флаги либо объедините settings вручную. Ранее установленный seai-candidate остаётся как есть. `--trust-research` относится только к claim/report/start/cancel research metadata, не к task mutation, shell или human approvals.

Устанавливаются шестой skill `seai-parallel-research` и три agent profile в `.gigacode/agents/`: `seai-rule-researcher`, `seai-impact-researcher`, `seai-test-researcher`. Их явный tools allowlist содержит только SEAI reads/contracts/claim/report. Fork наследует контекст и инструменты родителя: эти named profiles не являются sandbox для fork и не закрывают остальные tools главной сессии. SEAI по-прежнему не обходит бизнес-гейты.

Перезапустите GigaCode в WORKSPACE, проверьте `/mcp`, `/skills`, `/agents manage`. Для первого прогона: «Исследуй, как рассчитывается X, где используется результат и какие тесты покрывают границы. Используй параллельный ресерч SEAI». Затем проверьте фактический старт фоновых forks и появление отдельных reports в research.get. После этого обычные средние/большие вопросы автоматически маршрутизируются через новый skill, если клиент загрузил инструкции и поддерживает fork.

## Как устроен протокол

1. `seai_connect` сообщает project epoch и фиксирует generation/snapshot.
2. Координатор вызывает `seai_research_start` с objective, topics (1–6), operation_id, expected_project_epoch и явным index_generation_id. Возвращается durable run_id. Создание не делает live scan.
3. `seai_read(method=research.get, params={id:run_id})` показывает jobs. До initializing=false нужно дождаться записи всех входов.
4. Координатор запускает не больше max_workers фоновых forks. Каждый получает конкретный run_id/job_id/topic и вызывает `seai_research_claim`. Ответ содержит lease_epoch и immutable input с index_generation_id/source_snapshot_id. Каждый последующий seai_read **явно** использует этот generation, независимо от connect другого агента.
5. Исследователь публикует `seai_research_report` с run/job/lease/operation_id и report: summary, findings [{statement,evidence_ids}], gaps []. Каждый evidence ID проверяется по индексу назначенного поколения; произвольные hash и evidence из другой версии не принимаются, если их нет в этом индексе. `file_evidence_id` из file.read подходит для факта о целом файле.
6. Для ожидания координатор передаёт research.get последний cursor и wait_ms=10000. При отсутствии изменений сервер ждёт до 10s и отвечает компактным unchanged=true без повторной передачи reports; shell sleep не нужен. После успешного report fork завершается. Координатор освобождает слот и запускает следующую тему. Результаты собираются через research.get; текст fork не обязан вернуться в основной чат.
7. Итоговый `runs.get` сохраняет reports. research.get выдаёт их один раз в jobs, без дублирования итогового массива. Координатор объединяет дубли и проверяет противоречия по источникам. Сервер не определяет истинность утверждения только по наличию существующего evidence ID.

Операции start/claim/report идемпотентны по operation_id и исходному payload. Повтор команды с тем же ID возвращает сохранённый receipt; другой payload даёт IDEMPOTENCY_CONFLICT. После потери ответа не генерируйте новый ID вслепую.

SUCCEEDED означает, что все отчёты собраны без явно указанных gaps. Это не доказательство полноты бизнес-анализа: review_required=true оставляет сверку координатору. Пустое исследование должно содержать явный gap. Отчёт с gaps сохраняется, задача помечается NEEDS_INPUT; итоговый run также NEEDS_INPUT. Каждый report привязан к job lease и проверенному поколению, но его смысл всё ещё нужно сопоставить с исходниками.

## Лимиты и сбои

По умолчанию одновременно можно держать два claimed job. Оператор может добавить в полную runtime-конфигурацию `"research_max_workers": 1`…`4` (0/отсутствие = 2). `doctor` сообщает эффективный лимит. Stdio MCP допускает до восьми параллельных RPC calls; ожидающий research.get не блокирует reads, JSON-RPC ответы могут приходить в другом порядке и сопоставляются по ID. Это отдельно от числа исследовательских jobs. Больший лимит не гарантирует ускорения при rate limit или последовательной обработке запросов моделью.

В одном runtime остаётся один активный run. Пока research выполняется, index publication и task mutations блокируются; pinned reads доступны. Максимальная длительность — существующий max_run_seconds (по умолчанию 1800). Отдельного LLM-бюджета SEAI нет: токены и стоимость внешних forks учитывает GigaCode. Простые вопросы проходят обычным seai_read без делегирования.

Если worker завис или завершился без report, координатор вызывает `seai_research_cancel`. Сохранённые reports остаются доступны, незавершённые jobs получают CANCELLED. При restart активный run и queued/running jobs становятся ABORTED с изменённым lease; готовые отчёты сохраняются. Старый worker не может дописать отчёт в отменённую/прерванную сессию. Автоматического reassign/resume нет: нужно явно начать новое исследование оставшихся тем.

Чтения pinned snapshot имеют freshness=NOT_CHECKED относительно текущего working tree. Этот режим не подменяет live guards перед изменениями кода. Возможность parallel_research=true означает готовность координации; research_client_verified=false и общий multi_agent_ready=false сохраняются до проверки реального клиента/полного автономного runtime.

## Эквивалентные CLI-команды

```sh
seai --data DATA research start --generation GENERATION \
  --objective 'Как устроено правило X?' --topic 'Правила и ветки' \
  --topic 'Потребители и зависимости' --operation-id research-001 --epoch EPOCH
seai --data DATA research status RUN_ID
seai --data DATA research claim RUN_ID --job JOB_ID --operation-id claim-001
seai --data DATA research report RUN_ID --job JOB_ID --lease LEASE \
  --report REPORT.json --operation-id report-001
seai --data DATA research cancel RUN_ID
```

CLI-команды не создают LLM-агентов. Проверенный локально MCP smoke запускает два независимых клиента одновременно и собирает три reports без provider. Реальные фоновые forks вашей версии GigaCode и ускорение на боевой задаче ещё нужно проверить; обещания 2×/4× нет.
