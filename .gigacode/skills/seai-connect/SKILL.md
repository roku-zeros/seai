---
name: seai-connect
description: Подключить GigaCode к уже установленному SEAI, проверить runtime и подготовить индекс перед исследованием кода без прямого чтения проекта.
---

# Подключение SEAI

Соблюдай `.gigacode/seai/POLICY.md` относительно project root. Управляющие файлы skill/policy/session можно читать напрямую; исходники — только через SEAI.

1. Получи absolute `seai_bin`, `data_dir`, `project_root` из `.gigacode/seai/session.json` либо явных данных пользователя. Если их нет, запроси недостающие координаты. Не сканируй файловую систему для угадывания проекта/runtime.
2. Выполни `<seai_bin> --data <data_dir> --json doctor`, затем `project status` с теми же глобальными флагами. Убедись, что data.root соответствует project_root. JSON `status=error`/ненулевой exit code не являются успешным подключением.
3. Для свежего исследования выполни `index build --wait`; для явно исторического запроса используй `index build --ref <заданный-ref> --wait`. Не заменяй dirty working tree на HEAD молча. Дождись terminal `data.state=SUCCEEDED`; сохраняй job_id.
4. Сообщи actual snapshot/generation IDs из результата задания, capabilities и ограничения. `pilot_ready=false` здесь ожидаем; ошибки загрузки/типизации остаются gaps.

Если daemon недоступен, не читай исходники вместо него: верни ошибку и предложи отдельную установку/запуск по docs/AGENT_QUICKSTART.md из checkout SEAI. Этот skill не устанавливает toolchain, не запускает произвольный shell и не меняет целевой код. Истёкшее ожидание не равно отмене server job; его состояние проверяется `job status ID`.
