# Установка и использование SEAI агентом GigaCode

Этот runbook предназначен для GigaCode или другого агента с файловыми и терминальными инструментами. Выполняй его, когда пользователь попросил установить или проверить SEAI. Для первого запуска используй демонстрационный репозиторий; реальный проект подключай по указанному пользователем пути.

Текущая поставка — `0.1.0-dev`, Go 1.26.6. SEAI обеспечивает snapshot/index/search; объяснения и правки выполняет GigaCode. Нет встроенного чата, Qwen workers, MCP-сервера, task workflow, безопасного patch runner и release rebase. Средние/большие задачи допускаются как исследование с явными ограничениями, не как автономный change workflow.

## 1. Проверить окружение и найти проект

Работай из корня checkout `roku-zeros/seai`. Если он ещё не получен, клонируй `https://github.com/roku-zeros/seai.git` в отдельный выбранный каталог и перейди в него. Существующий checkout не сбрасывай. Убедись, что корневой go.mod содержит `module seai.local/seai`; Git root должен совпадать с этим каталогом. Не вызывай `git init` для SEAI поверх родительского репозитория.

Проверь:

```sh
pwd
git rev-parse --show-toplevel
git status --short
GOTOOLCHAIN=local go version
git --version
make --version
cc --version
python3 --version
```

Поддерживаемый transport — Unix socket (macOS/Linux); проверенный стенд — macOS arm64. Windows/named pipe и WSL отдельно не проверены. Нужны Go 1.26.6 или новее, CGO/C compiler, Git, make, Python 3. Если compiler/make/Python отсутствуют, сообщи конкретную недостающую зависимость и команду проверки; исходники SEAI для обхода ошибки не меняй.

Проверь доступность terminal tool GigaCode. При наличии только режима чтения установку выполнить нельзя: сообщи, что нужен режим с исполнением команд. Учти текущие разрешения среды; ошибка инструмента — не успешный запуск.

## 2. Создать изолированную демонстрационную сессию

Следующие команды рассчитаны на bash/sh. Все пути заключай в кавычки. При нескольких вызовах terminal tool shell-переменные могут не сохраняться: запиши фактический абсолютный путь SESSION, а затем подключай созданный session.env в каждом новом shell.

```sh
SEAI_ROOT="$(pwd -P)"
SEAI_SESSION="$(mktemp -d /tmp/seai-demo.XXXXXX)"
export SEAI_ROOT SEAI_SESSION
mkdir -p "$SEAI_SESSION/repo"
mkdir -m 700 "$SEAI_SESSION/run"
```

`SEAI_ROOT` — исходники инструмента, `SEAI_SESSION/repo` — отдельный demo-проект, `SEAI_SESSION/run` — приватный runtime. Runtime не может находиться внутри индексируемого Git root. Короткий путь нужен для Unix socket (проверяется длина <=100 байт). Каталог `/tmp` используется только для демонстрации и может быть очищен ОС; реальные данные размещай в устойчивом локальном каталоге вне исходников.

### Если Go 1.26.6 ещё не установлен

Не заменяй системный Go. Установи официальный toolchain в каталог этой сессии. Сеть нужна только для загрузки; в offline-среде используй заранее предоставленный проверенный toolchain и module cache.

```sh
python3 - "$SEAI_SESSION/toolchain" <<'PY'
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import sys
import urllib.request

root = Path(sys.argv[1])
if root.exists():
    raise SystemExit("toolchain directory already exists: choose/reuse it explicitly")
system = {"Darwin": "darwin", "Linux": "linux"}.get(platform.system())
arch = {"arm64": "arm64", "aarch64": "arm64", "x86_64": "amd64"}.get(platform.machine())
if not system or not arch:
    raise SystemExit("unsupported demo platform; select an approved Go archive")
def fetch(url):
    with urllib.request.urlopen(url, timeout=120) as response:
        return response.read()
releases = json.loads(fetch("https://go.dev/dl/?mode=json&include=all"))
release = next(r for r in releases if r["version"] == "go1.26.6")
artifact = next(f for f in release["files"] if f["os"] == system and f["arch"] == arch and f["kind"] == "archive")
body = fetch("https://go.dev/dl/" + artifact["filename"])
if hashlib.sha256(body).hexdigest() != artifact["sha256"]:
    raise SystemExit("Go archive checksum mismatch")
root.mkdir(parents=True)
archive = root / artifact["filename"]
archive.write_bytes(body)
subprocess.run(["tar", "-xzf", str(archive), "-C", str(root)], check=True)
subprocess.run([str(root / "go/bin/go"), "version"], check=True)
PY
export PATH="$SEAI_SESSION/toolchain/go/bin:$PATH"
```

Версия из `go version` должна быть >=1.26.6. Для воспроизведения отчёта используй именно 1.26.6. Бинарник daemon тоже ищет Go через PATH: сборки новым Go при старом Go в PATH недостаточно.

Подготовь окружение и запиши пути без потери пробелов/кавычек:

```sh
export GOCACHE="$SEAI_SESSION/go-build"
export GOMODCACHE="$SEAI_SESSION/go-mod"
export GOTOOLCHAIN=local
python3 - <<'PY'
import os
from pathlib import Path
import shlex
keys = ["SEAI_ROOT", "SEAI_SESSION", "PATH", "GOCACHE", "GOMODCACHE", "GOTOOLCHAIN"]
path = Path(os.environ["SEAI_SESSION"]) / "session.env"
path.write_text("".join("export " + k + "=" + shlex.quote(os.environ[k]) + "\n" for k in keys))
path.chmod(0o600)
print("Session environment:", path)
PY
```

Для следующего shell выполни `. /фактический/путь/session.env`. Не подставляй в shell пример с русским путём буквально. Online-сборка использует стандартные GOPROXY/GOSUMDB и проверку module checksums. Если сеть недоступна, новый пустой GOMODCACHE не подойдёт: укажи существующий заранее заполненный cache. Не отключай проверку сумм ради обхода ошибки сети.

## 3. Собрать и проверить

Из корня SEAI с окружением сессии:

```sh
cd "$SEAI_ROOT"
go version
go mod download
make test
make vet
make smoke
go -C fixtures/strategy test -race ./...
```

`make smoke` сам собирает `build/seai` и `build/seaid`, проверяет временный daemon и удаляет свою временную fixture. Для последующих вопросов нужен отдельный daemon из шага 4. При запрете Unix socket сообщи ограничение среды; не подменяй transport сетевым listener.

Проверяй exit code каждого шага. Ожидается PASS тестов, успешный vet, `status: passed` smoke. При ошибке останови зависимые шаги и покажи stderr. При исчерпании лимита terminal tool дождись/проверь завершение процесса, а не объявляй успех заранее.

## 4. Поднять постоянный daemon на время демонстрации

```sh
cp -R "$SEAI_ROOT/fixtures/strategy/." "$SEAI_SESSION/repo/"
git -C "$SEAI_SESSION/repo" init -b main
git -C "$SEAI_SESSION/repo" add go.mod rule.go rule_test.go
git -C "$SEAI_SESSION/repo" -c user.name='SEAI demo' -c user.email='demo@example.invalid' commit -m 'Create synthetic demo baseline'
```

Локальная identity выше применяется только к этому demo commit; глобальный Git config не меняется. Не передавай `fixtures/strategy` напрямую в daemon: в checkout SEAI это вложенный каталог, а daemon требует настоящий корень отдельного Git-репозитория с commit.

Предпочтительно запустить foreground-процесс в отдельной сохраняемой terminal session:

```sh
"$SEAI_ROOT/build/seaid" --repo "$SEAI_SESSION/repo" --data "$SEAI_SESSION/run" --project demo
```

Если GigaCode не сохраняет такую terminal session, используй фоновой запуск с логом и PID:

```sh
nohup "$SEAI_ROOT/build/seaid" --repo "$SEAI_SESSION/repo" --data "$SEAI_SESSION/run" --project demo > "$SEAI_SESSION/daemon.log" 2>&1 < /dev/null &
printf '%s\n' "$!" > "$SEAI_SESSION/daemon.pid"
```

Запускай только один из этих вариантов. Дождись socket и проверь живой процесс/лог. Для фонового процесса PID берётся только из созданного тобой daemon.pid; если процесс завершился, покажи daemon.log. Ожидание startup ограничь 30 секундами. Не удаляй существующие lock/socket другого daemon.

```sh
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" --json doctor
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" project status
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" index build --wait
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" search Eligible
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" search MinimumAge
"$SEAI_ROOT/build/seai" --data "$SEAI_SESSION/run" search TestEligibleBoundary
```

Критерии успеха: doctor доступен; `project status` указывает на demo root; index job имеет `data.state=SUCCEEDED`; search содержит нужные hits, source_snapshot_id/index_generation_id и исходные snippets. `pilot_ready=false`, `coverage=partial` и search `status=partial` ожидаемы, но diagnostics ошибок типизации нельзя игнорировать. Сохрани идентификаторы снимка/поколения и абсолютные пути для следующего вопроса.

Глобальные `--data`/`--json` идут **до** команды. Реализованы только `doctor`, `project status`, `index build`, `job status ID`, `search QUERY`. Не вызывай проектные примеры `task run`, `flow`, `impact`, `release` из спецификации: этих CLI-команд ещё нет.

## 5. Отвечать на вопросы

Для каждого вопроса выдели имена символов и выполни один или несколько `search` с реальным SEAI_ROOT и runtime. Объяснение формируй по snippets и прикладывай file_path, symbol/evidence IDs и generation/snapshot IDs. Не считай собственные предположения фактами индекса. Поиск по имени не является семантическим поиском по всему русскому вопросу.

Первые вопросы и проверочные ответы:

| Вопрос | Что искать | Проверочный результат |
|---|---|---|
| Как работает допуск? | `Eligible`, `MinimumAge` | Возраст >=18 |
| Что для возрастов 17/18/19? | `Eligible`, `MinimumAge` | false/true/true |
| Что сообщает отказ? | `EligibleReason` | `below minimum age` для 17 |
| Где проверена граница? | `TestEligibleBoundary` | Случаи 17/18/19 |

Это синтетическая fixture, не утверждённое бизнес-правило. Если evidence недостаточно, укажи gap. Дополнительное чтение файлов или rg выполняет сам GigaCode; помечай такие источники отдельно от результатов SEAI. Не выдавай rg за готовый typed graph/call analysis.

## 6. Первая задача на изменение

Только по просьбе пользователя измени **demo** порог с 18 на 21, проверь случаи 20=false, 21=true, 22=true и выполни `go test ./...` из demo root. Сначала сохрани исходный snapshot/generation. Покажи diff и терминальный результат тестов. Не изменяй исходную fixture в SEAI и не делай push без запроса пользователя.

Затем выполни `index build --wait` **без `--ref HEAD`**, чтобы захватить незакоммиченные правки. Повтори search и сравни snapshot/generation IDs: они должны измениться. Ответ должен отражать новый порог 21. SEAI сам не применяет patch и не запускает product checks: изменения/тесты в этом сценарии выполняет GigaCode.

После любой live-правки, checkout или изменения build inputs явно перестраивай индекс: freshness watcher ещё отсутствует. Для исторического committed-состояния можно использовать `index build --ref <ref> --wait`, но оно не включает dirty-файлы.

## 7. Подключить реальный проект

Используй абсолютный путь, названный пользователем, и отдельный project_id/runtime. Проверь Git root, наличие commit и корневого go.mod. Сначала только индексирование и вопросы; не переноси автоматически демонстрационную задачу «возраст 21» в продукт.

На этой стадии unsupported: symlink/submodule, go.work, local replace. Индексатор не скачивает зависимости; необходимые разрешённые modules должны быть в cache. Результаты с ошибками загрузки/типизации пригодны только для ограниченного исследования. На среднем/большом проекте не обещай полную карту зависимостей или время выполнения без измерения.

## 8. Отчёт и остановка

Верни пользователю: версии Go/GigaCode (если доступны), результаты проверок, точные пути исходников/бинарников/demo/runtime/session.env, способ запуска daemon, PID/terminal session, пути логов, снимок и поколение, примеры search, ограничения и точную команду остановки.

Foreground daemon останавливается Ctrl+C в его terminal session. Фоновый daemon останавливай `kill -TERM <фактический_PID>` после сверки процесса с созданной сессией. Дождись завершения; большие index jobs могут завершаться до 10 минут. Не используй общий `pkill seaid` и не удаляй данные работающего daemon.

Demo не удаляй, пока пользователь продолжает задавать вопросы. После окончания можно удалить только созданный SEAI_SESSION, предварительно остановив его daemon и сохранив нужные результаты. Реальный runtime не удаляется как часть очистки demo.

## Подключение инструкции в GigaCode

В открытом корне SEAI отправь:

```text
@docs/AGENT_QUICKSTART.md Выполни установку и демонстрацию по шагам 1–4, затем покажи отчёт. Правило пока не меняй.
```

В репозитории также есть prompt shortcuts `.gigacode/commands/seai/setup.md` и `ask.md`: `/seai:setup` и `/seai:ask`. Это Markdown-промпты, не native SEAI tools. Их формат взят из [официальной документации GigaCode CLI](https://gitverse.ru/docs/ai/ai-assistant-gigacode/gigacode-cli/commands); конкретная установленная версия клиента ещё не проверена. Если shortcuts не обнаруживаются, используй прямое прикрепление файла выше. MCP здесь не настраивается.
