# Большой репозиторий: freshness, симлинки и таймауты (0.4.1)

Исправления подготовлены по вашему прогону GigaCode на проекте примерно с 2,6 тыс. файлов. Отчёт с фотографий использован как описание симптомов; команды остановки процессов и изменения чужих конфигов из него не выполнялись.

## Почему запросы тормозили

В 0.4.0 рабочий snapshot делал отдельный `git ls-files --error-unmatch` для каждого файла. Для Git ref отдельно вызывался `git cat-file` на каждый blob. При live freshness guard повторялся полный захват с записью CAS, иногда до и после операции. Это тысячи Git subprocesses плюс повторные fsync. Истечение 35-секундного HTTP timeout ошибочно сообщалось как `daemon unavailable` с предложением запуска daemon.

В 0.4.1:

- tracked/untracked paths перечисляются двумя Git-командами; несущественные untracked files фильтруются до filesystem access;
- Git ref читается одним `cat-file --batch`;
- freshness использует read-only Observe: те же bytes/modes/commit/build и два стабильных прохода, без записи CAS и без доверия к одному mtime;
- live freshness budget — 15s. Превышение даёт FRESHNESS_TIMEOUT с инструкцией продолжения, а task status/diff — UNKNOWN с freshness_error. Это не доказательство STALE/CURRENT и не основание для REVIEW_READY;
- client timeout и daemon response timeout по умолчанию 2m; timeout/cancellation/missing socket различаются. Увеличение timeout не заменяет исправление алгоритма.

На локальном синтетическом стенде 2629 tracked files: Observe около 0,31s и 6 Git subprocesses на два прохода; Git ref — 3 subprocesses. Это измерение конкретного стенда, не обещание latency на другом диске/FS. Первый durable capture может занимать больше времени из-за первичной записи и fsync CAS; он остаётся асинхронным index job.

## Как обновиться

В checkout SEAI обновите код и выполните `make build`. Остановите свой прежний seaid обычным SIGTERM/своим process manager и запустите новый бинарник с прежними `--repo`, `--data`, `--project`, `--config`. Сборка файла на диске не обновляет уже запущенный процесс. Не используйте PID с чужого скриншота и не останавливайте другой project daemon.

Выполните `index build --wait`: source policy поменялась, старый snapshot мог включать agent metadata. Исторические поколения не удаляются; изменение baseline/epoch требует обычного task reconciliation. Runtime schema остаётся 3, старые snapshots с обычными metadata-файлами сохраняют поддержку backup/restore.

Не требуется удалять симлинки `.agents`, `.codex`, `.gigacode`, AGENTS.md → GIGACODE.md. В обеих capture modes эти служебные entries исключены из source snapshot (`seai.source-policy/v2`). Самих файлов/ссылок SEAI не меняет и по ним не проходит. Симлинки исходного Go-кода, build/config-файлов, testdata и прочего включённого source по-прежнему отклоняются: внешние roots молча не подмешиваются. Working-tree reads дополнительно ограничены os.Root репозитория.

## Работа на большом проекте

```sh
seai --data DATA doctor
seai --data DATA project status
seai --data DATA index build --wait
```

Сохраните `index_generation_id` и `source_snapshot_id` из результата задания. Для исследования одной версии используйте их явно:

```sh
seai --data DATA files --generation GENERATION --prefix internal/ --limit 200
seai --data DATA search Execute --generation GENERATION
seai --data DATA read internal/module/module.go --snapshot SNAPSHOT
```

Такие запросы читают выбранный снимок, не проверяя live tree заново. Это явное историческое/immutable чтение, а не обещание текущей свежести. После правок нужен новый index build; старые IDs остаются историческими.

Для live проверки с неизвестной/медленной FS:

```sh
seai --data DATA workspace status
# При FRESHNESS_TIMEOUT:
seai --data DATA workspace check
seai --data DATA run status RUN_ID
```

workspace check — асинхронный read-only scan. Он не меняет current generation/epoch, не публикует новый index и не превращает старые данные в свежие. Проверка снимает фактическое состояние, а следующие мутации всё равно проверяют live bytes; TTL-cache не используется. Пока run активен, controlled mutations/index jobs получают WORKFLOW_BUSY, но project/job/run status и pinned reads продолжают обслуживаться.

При RPC timeout не запускайте loop одинаковых live requests и не считайте daemon умершим. Сначала проверьте project status; посмотрите job/run status, если операция асинхронная. Не меняйте настройки клиента/GigaCode TLS и не убивайте seaid ради повторения медленного read.

При необходимости timeout задаётся **до команды**:

```sh
seai --data DATA --timeout 3m files --generation GENERATION
seaid --repo REPO --data DATA --project PROJECT --rpc-timeout 3m
```

Это timeout одного RPC, а не всего index/check job. Для долгой обработки используйте async-команду, а не длительное соединение. Freshness budget остаётся 15s, увеличение RPC timeout не отключает guards.

## Smoke: wait budget не равен результату тестов

Прежний smoke опрашивал run 600 раз с паузой 0,1s: около 60s. Профиль разрешал процессу до 120s, а comparison запускал несколько процессов на fixtures. Поэтому внешний скрипт мог закончиться раньше разрешённого серверного прогона.

Теперь оба smoke используют monotonic deadline, по умолчанию `max_run_seconds + 30s` из runtime config. Можно задать `--run-timeout SECONDS`. Ошибка ожидания включает run_id и последнее состояние; она не объявляет проверки PASSED или FAILED вместо daemon. `run status` / `run logs` содержат реальный итог.

```sh
python3 scripts/smoke.py --bin build
python3 scripts/business_smoke.py --bin build
```

Qwen/provider_ready:false, pilot_ready:false и ненастроенные business adapters/profiles — отдельные capability/configuration вопросы, а не диагноз зависшего daemon.
