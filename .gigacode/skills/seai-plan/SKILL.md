---
name: seai-plan
description: Составить и сохранить ChangeSpec и план изменения по evidence SEAI с критериями результата, scope и revision guards.
---

# Контракт и план через SEAI

Соблюдай `.gigacode/seai/POLICY.md`. Исследуй задачу как seai-research. Для уже существующей задачи сначала `task status ID`, не заменяй её baseline текущим checkout молча.

1. Подготовь JSON/YAML `seai.change/v1`: change_id/spec revision/kind/objective, source_requirements с точным content/revision/SHA-256, scope.files, expected/preserved behavior, acceptance_criteria с behavior IDs/ожиданиями/profile и unresolved. Все коллекции явные массивы. Change требует требования/ожидаемое поведение; refactor требует эквивалентность; explain не даёт права на patch. Не придумывай бизнес-значения и digest источника.
2. Сохрани управляющий spec в `.seai/changes/` либо вне исходников; вызови `task create --spec PATH`. Для принятого контракта новая версия — `task revise ID --spec PATH`, с увеличением spec revision; после patch revise пока не поддерживается. Не переписывай candidate ради смены контракта.
3. Составь `seai.plan/v1`: до 12 последовательных steps с id/description/files/criterion_ids/evidence_ids и required_checks. Используй evidence символов/refs/text chunks или file_evidence_id из baseline. Каждый AC покрыт шагом; paths находятся в scope. Checks здесь только запланированы, не выполнены.
4. Вызови `task plan ID --plan PATH`, сохрани task ID/execution revision, baseline/candidate/spec/behavior digests и epoch. Для точного retry используй исходные operation-id/revision/epoch. Наличие плана не означает REVIEW_READY.

Контракты/команды описаны в docs/WORKFLOW.md из checkout SEAI. Отсутствующие источники/неоднозначные требования остаются gaps. Impact graph неполон. Человек подтверждает behavior digest отдельно; модель не вызывает approve и не подменяет существенное изменение kind=refactor.
