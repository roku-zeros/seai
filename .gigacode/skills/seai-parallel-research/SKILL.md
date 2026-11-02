---
name: seai-parallel-research
description: Параллельный ресерч среднего или большого Go-проекта через SEAI и фоновые субагенты GigaCode, когда есть хотя бы две независимые части исследования. Не применять к простому поиску символа и редактированию кода.
---

# Параллельное исследование через SEAI

Используй доступные SEAI MCP tools и правила `.gigacode/seai/POLICY.md`. Этот режим не требует отдельного Qwen endpoint: модели запускает GigaCode, SEAI координирует задания и проверяет привязку evidence. Файлы, задачи и approvals не меняются.

1. Вызови `seai_connect`, сверь root и сохрани generation/snapshot и project epoch. Выбери 2–6 независимых тем, например правила/ветки, потребители/shared helpers, тесты/границы. На простой вопрос отвечай обычным seai_read: стоимость делегирования не оправдана.
2. Проверь наличие `seai_research_start/claim/report/cancel` и возможность фонового fork в текущем GigaCode. Если их нет, сообщи ограничение и продолжи обычное последовательное исследование через SEAI; не изображай параллельных агентов.
3. Создай `seai_research_start` с objective, topics, сохранёнными index_generation_id/expected_project_epoch и уникальным operation_id. Сохрани run_id сразу; при потере ответа повторяй исходные параметры с тем же operation_id.
4. Получи `seai_read(method=research.get, params={id:run_id})`. При FAILED/ABORTED/CANCELLED сообщи error и не запускай workers. Для RUNNING дождись initializing=false; из result возьми max_workers, из jobs — job_id и topic.
5. Для каждого свободного слота запусти один фоновый GigaCode fork (`subagent_type: fork`, если доступно). Передай ему конкретный run_id/job_id/topic и короткое задание:

   «Вызови seai_research_claim для этого job_id с уникальным operation_id. Используй input.index_generation_id явно во всех seai_read, сохрани lease_epoch. Исследуй только назначенную тему. Опубликуй seai_research_report со summary, findings [{statement,evidence_ids}], gaps []. При отсутствии доказательств findings=[] и явный gap. Используй file_evidence_id или indexed evidence_id. Не редактируй исходники, не меняй задачи/approvals, не запускай проверки и не делегируй дальше. После успешного report заверши работу».

   Одновременно запускай не больше max_workers. Не назначай один job двум forks. Подсказки специализаций есть в `.gigacode/agents/seai-*-researcher.md`; named subagents могут блокировать родителя, поэтому сами по себе не доказывают параллельное выполнение.
6. Результаты fork могут не возвращаться в родительский диалог. Собирай их через `research.get`, а не shell или текст UI. Когда job публикует SUCCEEDED/NEEDS_INPUT, освободи слот и запусти следующий. Для ожидания передавай `research.get` сохранённый cursor и wait_ms=10000: unchanged=true возвращает компактный ответ, изменения — новые jobs/reports. Не используй shell sleep и не опрашивай статус плотным циклом. RESEARCH_BUSY означает, что лимит уже занят.
7. Прочитай все reports после завершения. Сверь generation/snapshot, объедини дубли, разберись с противоречиями по evidence. Не определяй истинность голосованием. Gaps и partial graph сохраняй в ответе. SUCCEEDED означает сбор отчётов, а не полноту бизнес-анализа или успешные проверки продукта; review_required=true требует вывода координатора.
8. Если worker завершился без report или завис, сохрани доступные отчёты и вызови seai_research_cancel; объясни незавершённые темы. Старые lease/reports после отмены не дописывай. Не перезапускай всю задачу циклом и не меняй настройки клиента ради обхода ограничений.

В одном runtime доступен один активный run. На время research блокируются index publication и мутации задач; чтения pinned snapshot продолжают работать. Лимит по умолчанию 2, оператор может задать research_max_workers=1..4. Ускорение зависит от поддержки fork/параллельных запросов моделью и серверных rate limits; 2×/4× не обещается.
