# SEAI 0.3: исследование → контракт → изолированный candidate

Реализованы первые три этапа: чтение/навигация по неизменяемым исходникам, сохранение задачи и плана, применение ограниченных file operations в CAS candidate. Этапы 4–6 добавляют зарегистрированные checks, Qwen workers, независимый human/model review, release reconciliation и backup/restore/upgrade: см. [EXECUTION.md](EXECUTION.md). Без настройки и актуальных доказательств итог остаётся `NOT_REVIEW_READY`; готовность пилота не заявляется.

## Исследование

Daemon запускается как прежде: `seaid --repo <Git-root> --data <runtime-вне-репозитория> --project <id>`. Все примеры ниже предполагают `seai --data <runtime>` перед командой.

```text
workspace status
index build --wait
files --prefix internal/ --limit 100 --offset 0
read internal/example.go --offset 0 --limit 65536
search Eligible
search "below minimum age" --text --limit 25
symbol <symbol_id> --limit 65536
references <symbol_id> --limit 50 --offset 0
calls <symbol_id>
tests related <symbol_id>
```

Результат index job содержит source_snapshot_id и index_generation_id. Поиск возвращает symbol_id/evidence_id/file_path. Read и symbol — страницы байтов: `content_base64` всегда точен; `text` заполняется только для UTF-8 страницы. Следующую страницу запрашивай по next_offset; truncated означает, что тело не прочитано целиком. File read возвращает также file_evidence_id для привязки целого файла в плане. Файлы доступны только по manifest зарегистрированного снимка, а не по произвольному пути диска/CAS.

По умолчанию запросы используют текущий индекс и проверяют live source/build. При расхождении — `STALE_WORKSPACE`; нужен новый `index build`. Историческое чтение выполняется явно: `--generation <id>`, а read/files также принимают `--snapshot <id>`, включая candidate задачи. Несовместимые snapshot/generation отклоняются. Для symbol/refs/calls/tests/text нужен соответствующий generation; candidate без построенного индекса читается через read/files/diff.

Text search — регистрозависимый literal поиск внутри строк/перекрывающихся 8 KiB chunks; multiline query не поддерживается. Это не FTS5/reranker. `--cursor` продолжает text search, связан с generation/query; выдача ограничена 50 chunks. На границах перекрытия возможны повторные контекстные фрагменты. Ограничения/ошибки типизации видны в search diagnostics.

References подтверждаются go/types для локальных индексируемых определений. Calls в именованных функциях: статический target — exact; interface dispatch — possible; function values/неразрешённые внешние targets — unresolved. Вызовы внутри вложенных function literals не включаются в caller graph; это ограничение анализа. SSA, полный dynamic dispatch, interface implementation graph и внешние исходники не заявляются. Tests related — прямые вызовы из Test/Benchmark/Fuzz-функций, не доказательство запуска или полная транзитивная test selection.

Одинаковый повторный build того же generation не увеличивает epoch. Новое generation увеличивает epoch и делает старые task baselines устаревшими. Существующий индекс structural-v2 нужно явно перестроить до structural-v3.

## Контракт задачи

`task create --spec <JSON-or-YAML>` принимает `seai.change/v1`. Все коллекции задаются явно массивами, в том числе пустыми. Схема — [change-v1.schema.json](../schemas/change-v1.schema.json). Минимальный change пример:

```yaml
schema_version: seai.change/v1
change_id: CHG-0042
revision: 1
kind: change
objective: Повысить минимальный возраст с 18 до 21
source_requirements:
  - external_id: REQ-17
    revision: "3"
    content_digest: "<SHA256 UTF-8 content ниже>"
    content: "Минимальный возраст равен 21. Прочее поведение сохраняется."
scope:
  files: [rule.go]
expected_behavior:
  - id: BEH-01
    statement: Порог равен 21
preserved_behavior:
  - id: INV-01
    statement: Сравнение остаётся включительным
acceptance_criteria:
  - id: AC-01
    behavior_ids: [BEH-01, INV-01]
    expected: "20 → false; 21 → true; 22 → true"
    assertion_profile: boundary-v1
unresolved: []
```

Заменить digest реальным SHA-256 обязательно; placeholder не проходит validation. Требование хранится с точным content/revision/digest в CAS вместе со spec. Это локальный импорт, не внешнее подтверждение аналитика. Файл spec не перечитывается daemon из произвольного live пути: источником принятого контракта является сохранённая редакция. Изменение локального YAML импортируется явно через `task revise`.

Kind: change, refactor, explain. Change требует revisioned requirement content, expected/preserved behavior и AC. Refactor требует preserved behavior и AC эквивалентности, но не бизнес-approval. Не называй изменение поведения рефакторингом ради обхода подтверждения. Explain не разрешает patch. Есть schema/unknown key/duplicate key checks; YAML aliases и дробные числа в контрактах запрещены.

```text
task create --spec /path/spec.yaml
task status <task_id-or-alias>
task plan <task_id> --plan /path/plan.yaml
task revise <task_id> --spec /path/spec-revision-2.yaml
task cancel <task_id>
task resume <task_id>
```

Plan `seai.plan/v1` содержит steps: id, description, files, criterion_ids, evidence_ids; required_checks содержит имена **планируемых** проверок. Это последовательный план, не worker DAG. До 12 steps; scope paths и criterion IDs сверяются со spec; все критерии должны быть покрыты. Evidence проверяется в выбранном baseline index (symbols/refs/text chunks/file_evidence_id), а не только по формату digest. В 0.3 required_checks ссылаются на зарегистрированные operator profiles; `task verify` запускает их, см. EXECUTION.md.

При revise spec.revision должен увеличиваться, alias/kind неизменны. Spec revision и execution revision задачи — разные значения. Поведение хешируется отдельно: его изменение или новая revision требования снимает approval; техническая revision без изменения поведения его сохраняет. План при revise снимается. В этой версии revise возможен до первого patch; существующий candidate не отбрасывается ради переписывания контракта — для такого случая нужна отдельная задача. Прежние редакции/решения остаются историческими.

Состояния: SPEC_READY → PLANNED → IMPLEMENTING. Cancel сохраняет checkpoint, resume возвращает его с новой execution revision; это восстановление данных задачи, не возобновление inference. Все task/spec/plan/candidate pointers и успешные операции сохраняются после рестарта.

## Подтверждение человеком

Перед patch для kind=change человек в **своём интерактивном терминале** выполняет:

```sh
seai --data /absolute/runtime task approve CHG-0042 --actor developer
```

CLI показывает objective, AC и полный behavior digest, требует набрать `approve <alias> <digest>` через `/dev/tty`. Нельзя передать подтверждение pipe/флагом или строкой `approved` в YAML. Общий model RPC dispatcher не имеет task.approve/human.approve. Отдельный owner-only Unix HTTP route `/human/approve` обслуживает CLI; решения хранят actor, spec/behavior digest и timestamp.

Это разделение интерфейсов и политика использования, **не криптографическая аутентификация человека**: другой процесс того же OS user, имеющий доступ к socket/PTY, технически может вызвать route. Skills запрещают модели approve и имитацию терминала. Для защиты от такого процесса нужен внешний проверенный broker/permission profile; текущая поставка его не содержит. Не утверждай, что dev-терминал доказывает корпоративную identity.

## Изолированный patch

`patch apply <task_id> --patch <JSON-or-YAML>` принимает [seai.patch/v1](../schemas/patch-v1.schema.json). Patch обязан содержать:

- base_snapshot_id текущего candidate и актуальный spec_digest;
- files с уникальными repository-relative paths в scope и принятом плане;
- before_digest и before_mode для существующего файла; явный before_digest:null для нового;
- content **или** content_base64, точный after_digest и mode=100644/100755 для after; delete:true без after-полей для удаления.

Поддержаны создание, полная замена, удаление, изменение executable bit и binary через base64. Rename выражается явными delete+create операциями. До 32 paths, after <=64 KiB/file и <=512 KiB aggregate. Абсолютные/ненормализованные paths, NUL, symlink mode, выход за scope, `.git`, `.seai` и исключённые snapshot policy paths отклоняются. Перед применением необходим успешный baseline type load; ошибки пакетов блокируют patch.

Все before preconditions проверяются до смены candidate. Изменения записываются только как новые CAS blobs и новый manifest; прежний candidate остаётся доступен. Одной runtime-транзакцией обновляются pointer/revision, patch record, event и idempotency result. Никакие файлы основного checkout, HEAD, Git index или stash не меняются. Crash до commit оставляет только orphan CAS objects; GC пока не реализован.

```text
patch apply CHG-0042 --patch /path/patch.yaml
task diff CHG-0042
read rule.go --snapshot <candidate_snapshot_id> --limit 65536
task export CHG-0042 --format patch --output /path/change.patch --allow-incomplete
```

Экспорт без `--allow-incomplete` требует текущих checks, покрытых AC и независимого review (EXECUTION.md). Diagnostic export содержит git-applicable binary patch и `.manifest.json` с before/after hashes/modes, baseline/candidate/spec/behavior digests и NOT_REVIEW_READY. `--format json` сохраняет один JSON. Существующие output files не перезаписываются. Diff формируется через отдельный временный Git index, без user config/hooks/filters, и не запускает код репозитория. Diff ограничен 1 MiB/file input и 1 MiB output.

Live source/build, task revision, baseline epoch, spec/candidate и approval проверяются перед мутацией. Внешняя правка checkout блокирует новые операции, но исторический task diff/read остаётся доступен с маркировкой stale. Новый baseline требует reconciliation, которое относится к следующему этапу и пока не реализовано.

## Повторы и ограничения

CLI автоматически получает текущие expected_revision/expected_project_epoch и генерирует operation_id. Для действий, которые потребуется повторять после разрыва клиента, заранее задавай собственный --operation-id и сохраняй revision/epoch. Для точного повтора используй исходные `--operation-id`, `--revision` и `--epoch`. Тот же ID+payload возвращает сохранённый результат, другой payload — IDEMPOTENCY_CONFLICT. Успешный result/event/revision коммитятся атомарно; отклонённые до commit команды не публикуют candidate.

Для обновления останови старый daemon, собери новые бинарники и запусти seaid с прежними repo/data/project. Затем явно перестрой индекс. SQL migration 001 неизменна; runtime автоматически получает migrations 002/003 с checksum. Backup/restore/staged upgrade описаны в EXECUTION.md; продуктовый пилот требует отдельной приёмки. Новая версия использует Go 1.26.6+, YAML v3.0.1 и structural-v3.

Пока отсутствуют FTS5/ranking, incremental rebuild, полный Go graph, предметные extractors и принудительный SEAI-only sandbox для GigaCode. Worker/check/rebase/maintenance workflow доступен в объёме EXECUTION.md. Эти ограничения остаются видны в doctor/diagnostics/отчётах. Этапы 1–3 проверены в пределах описанных контрактов; готовность реального продуктового пилота требует P0 и следующих этапов исходной спецификации.
