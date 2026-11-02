# Бизнес-логика Go: SEAI 0.4

Добавлены предметный индекс, flow/impact и бизнес-прогоны. Правила остаются **в Go-коде**. Конфиг описывает ваши типы и точки регистрации, не дублирует реализацию правил. Qwen для этих команд не нужен.

## Подключение к вашему проекту

1. Обновите SEAI, пересоберите CLI/daemon через `make build`. Остановите прежний daemon; сохранение данных — по [EXECUTION.md](EXECUTION.md).
2. Запустите daemon с вашим repo/data/project, выполните `index build --wait`. Затем `rules discover`: это предложения Go-типов со string-полями и function-полями, а не автоматически подтверждённые правила. Обычную бизнес-функцию можно сразу исследовать через `flow PACKAGE.Function` / `impact PACKAGE.Function`.
3. Добавьте `business` в operator runtime JSON и перезапустите daemon с `--config /absolute/runtime.json`. Выполните `index build --wait` ещё раз: adapters входят в identity нового поколения.
4. Проверьте `rules diagnostics`, `rules search RULE_ID`, `rules show ENTITY_ID`, `flow` и `impact`. Если реальная конструкция не поддержана, сохраните diagnostic и небольшой Go-пример для расширения адаптера.
5. Подключите свои compile/tests/comparison profiles. Для строгого change workflow включите `require_for_changes:true` после настройки adapters/fixtures.

Минимальный runtime без исполняемых checks:

```json
{
  "schema_version": "seai.runtime/v1",
  "checks": [],
  "max_workers": 2,
  "max_node_attempts": 1,
  "max_run_seconds": 1800,
  "business": {
    "schema_version": "seai.business/v1",
    "require_for_changes": false,
    "adapters": [{
      "name": "project-rules",
      "kind": "struct",
      "package": "example.org/project/internal/rules",
      "symbol": "Rule",
      "id_field": "ID",
      "handler_fields": {"condition": "When", "exception": "Except", "action": "Apply"},
      "context_fields": ["Product", "SourceSystem"],
      "priority_field": "Priority"
    }]
  }
}
```

Замените package/symbol/field names на фактические. Package — **Go import path**, не путь на диске; его видно в результатах SEAI search/symbol или rules discover. Если у вашего типа нет Except, SourceSystem или Priority, уберите соответствующую настройку. Неверные/missing поля не превращаются в вымышленные значения.

Поддержанные формы:

- `kind:struct`: keyed и positional composite literals именованного Go-типа, включая импорт под alias и type aliases. ID/context/priority читаются как Go compile-time constants, в том числе именованные const. Handler fields разрешаются через Go types в конкретные локальные функции/методы. Глобальные таблицы, вложенные литералы в slices/maps и литералы внутри функций обрабатываются по исходным позициям.
- `kind:call`: typed function/method identity; arguments — с нуля. Например `Register(id, product, handler)` описывается `symbol:"Register", id_arg:0, handler_args:{"action":2}, context_args:{"Product":1}`. Для метода symbol имеет вид `Registry.Register`. Случайное совпадение имени у другого Go-типа/пакета не является регистрацией.
- `kind:function`: конкретная функция/метод как бизнес-узел: `{"name":"scoring","kind":"function","package":"example.org/project/scoring","symbol":"Service.Calculate"}`. Ключ такого узла — `example.org/project/scoring.Service.Calculate`; отдельный бизнес-ID не придумывается.

Function values, closures, interface handlers, вычисляемые ID/context/priority и неподдержанная factory-конструкция дают possible/unresolved и diagnostics. Отсутствие matches у настроенного adapter тоже diagnostic. Неподдержанные формы нельзя считать автоматически распознанными; discovery — помощник настройки, не универсальный детектор всех бизнес-правил.

Можно добавить `business.requirements`: rule_key, context, external_id, revision, content, content_digest (SHA-256 UTF-8 content). Это явная revisioned связь с требованием; отсутствие такой связи показывается как gap. Реальная реализация по-прежнему берётся из Go.

## Исследование

```sh
seai --data DATA rules discover
seai --data DATA rules diagnostics --limit 50 --offset 0
seai --data DATA search AGE_MIN
seai --data DATA rules search AGE_MIN
seai --data DATA rules search AGE_MIN --context Product=retail
seai --data DATA rules show ENTITY_ID
seai --data DATA flow ENTITY_ID --depth 3 --limit 50
seai --data DATA impact MinimumAge --depth 5 --limit 100
```

Одинаковые Rule ID в разных продуктах/регистрациях сохраняются как отдельные entity_id. `rules show AGE_MIN` при неоднозначности отклоняется: используйте entity_id или достаточный набор `--context Name=Value` (повторяемый). Поиск возвращает source snapshot/generation, точные file/blob/byte ranges и evidence IDs. Эти evidence можно использовать в plan. У коротких snippets есть snippet_truncated; полный участок читается через SEAI read по snapshot. Полный requirement content доступен в rules show; list/search/impact оставляют его ID/revision/hash.

`rules show` содержит роли condition/exception/action и source facts обработчиков: if/switch/case, return, assignment и доступы к полям. `flow` проходит вперёд по typed calls и references, включая зависимости от констант. Это синтаксические факты с источниками, **не исполнение и не доказательство достижимости ветки**. Declared priority — значение в исходнике, не обещание фактического порядка запуска.

`impact` идёт назад от символа/правила к потребителям и связанным тестам. Общий helper показывает затронутые контексты всех продуктов: фильтр выбора исходного правила не отрезает shared consumers. Отдельно возвращаются confirmed, possible, unexamined, source_facts, related_tests, coverage_gaps, diagnostics и suggested_checks. Suggested checks — подсказка, найденный тест — не PASS.

Ограничения обхода: depth до 8, до 500 nodes, fanout 50, до 2000 возвращённых edges, до 50 подробных affected rules и 200 facts. При ограничении есть truncated/unexamined и способ продолжить (больше depth, rules search/context, diagnostics paging). В reverse impact присутствует ограниченный список возможных динамических потребителей из модуля: это предупреждения о неизвестном target, не доказательство влияния на выбранный символ. Coverage всегда partial: интерфейсы, reflection, function fields, wrappers и зависимости вне разрешённого модуля не становятся полным runtime graph.

Без --generation запрос проверяет свежесть source и adapter config. С --generation читается выбранная историческая версия; конфигурация из нового daemon не переписывает старые факты. Source/build drift требует нового index build. SQL runtime schema остаётся 3; индексатор — structural-v4 + business-v1. Исторические structural-v3 читаются структурными API, бизнес-запросы потребуют нового поколения.

## Бизнес-проверки

Comparison profile продолжает исполнять зарегистрированный argv на baseline/candidate по два раза. stdin — fixture.input, stdout — один JSON. Добавлены:

```json
{
  "id": "age-boundary",
  "input": "{\"age\":17,\"vip\":false,\"product\":\"retail\"}",
  "expected": {"allowed": false, "reason": "age", "checked_rules": ["AGE_MIN"]},
  "expected_baseline": {"allowed": false, "reason": "age", "checked_rules": ["AGE_MIN"]},
  "criterion_ids": ["AC1"],
  "allowed_paths": [],
  "rule_keys": ["AGE_MIN"],
  "context": {"Product": "retail"},
  "category": "boundary",
  "description": "One below the inclusive minimum age"
}
```

Category: normal, boundary, exception, combination, regression. Значения входов и ожидаемые результаты задаются по вашему контракту; SEAI не выводит правильные ответы из текущей программы. Сам тег boundary не доказывает, что проверены все реальные границы — нужны явные min−1/min/min+1 fixtures. Для разных contexts нужны свои примеры.

В comparison profile можно задать:

```json
"rule_trace_path": "/checked_rules",
"required_cases": [{
  "rule_key": "AGE_MIN",
  "context": {"Product": "retail"},
  "categories": ["normal", "boundary", "exception", "combination"]
}]
```

Profile с отсутствующей обязательной категорией отклоняется. Если rule_trace_path задан, ожидаемый и фактический stdout должны содержать массив string rule IDs по этому JSON pointer; каждый fixture.rule_keys должен в нём присутствовать. Trace подтверждается **выводом тестового адаптера**, не автоматической инструментализацией Go. Например checked_rules означает проверенные engine правила, а applied_rules — сработавшие; выберите нужную семантику для вашего адаптера. Без trace показывается заявленная привязка, trace_verified:false.

expected_baseline опционален и ловит дрейф исходной версии отдельно от ошибки candidate. Report содержит business_cases с category/context/description, статусом, baseline_matched, candidate_matched, observed_rules, trace_verified и различиями JSON path/before/after. business_coverage перечисляет passed/missing categories по rule/context. Expected change, unexpected delta и baseline expectation failure различаются. Остановленные/неисполненные cases не становятся PASS.

`fixture_digest` из checks profiles учитывает fixtures, required_cases и rule_trace_path. Его надо включить в AC; изменение ожидаемых результатов или смысла coverage требует новой привязки и human approval для change. Старые fixtures без расширений сохраняют прежнюю identity.

```sh
seai --data DATA checks profiles
seai --data DATA task verify TASK
seai --data DATA run status RUN_ID
seai --data DATA run logs RUN_ID --profile business
seai --data DATA task status TASK
```

При require_for_changes:true перед проверками автоматически запускается `domain-analysis` (зарезервированный ID). Он строит индекс **candidate**, сохраняет affected rule IDs, diagnostics/gaps и требует бизнес-примеры из принятого plan для затронутых rule/context. Scope консервативен: включает все декларации изменяемых файлов и обратные потребители shared helpers; удалённые регистрации сохраняют обязанность проверить прежний rule/context. Анализ может потребовать больше регрессий, чем минимальный diff. Unknown registrations/type errors/лимиты обхода и отсутствующие fixtures блокируют готовность. Успешный candidate generation возвращается в domain_verification.index_generation_id: его можно читать через rules/flow/impact --generation. Current pointer проекта остаётся на baseline.

Domain PASS означает прохождение перечисленных gates в поддержанном scope, а не полноту понимания проекта. Далее по-прежнему нужны все проверки плана, AC coverage и независимый review. Human approval/review выполняет человек через TTY, модель их не имитирует. Диагностический export доступен с --allow-incomplete; REVIEW_READY не означает, что SEAI уже поменял основной checkout.

## Проверка перед вашим боевым прогоном

`fixtures/business` — отдельный Go-проект: два продукта, общий AgeThreshold, VIP-исключение, income rule, declared priority и выдача лимита. Test probe исполняет настоящий engine и возвращает checked/applied rules. Генератор создаёт локально разрешённые profiles только для этой синтетики:

```sh
python3 scripts/configure_business_fixture.py \
  --output /absolute/NEW_CONFIG.json --go /absolute/go/bin/go \
  --approve-local-execution
make business-smoke
```

Для своего проекта создайте аналогичный маленький executable/test adapter, вызывающий вашу реальную бизнес-функцию. Local-approved не имеет OS sandbox; Docker профиль требует вашего закреплённого образа и проверки. Qwen/GigaCode на корпоративном стенде остаются отдельной проверкой.

Промпт для GigaCode после обновления skills и настройки адаптеров:

> Работай с кодом через SEAI. Найди правило X, покажи регистрации, условия, исключения и source evidence. Через flow и impact проверь shared consumers всех продуктов и перечисли gaps. Подготовь изменение в candidate по контракту и плану; включи compile/tests/business fixtures с правильным fixture_digest. Выполни task verify и покажи бизнес-отчёт. Human approval/review оставь мне; основной checkout не меняй.
