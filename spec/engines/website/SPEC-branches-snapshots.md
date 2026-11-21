# Snapshot trees Website

## Границы

Website управляет независимыми деревьями page snapshots. Router отдельно использует статически заданный `branch_id` своего процесса и связывает настроенный route с готовым `static-page-artifact/v1`; Website не назначает snapshots веткам и не хранит URL/route/branch assignment.

Snapshot представляет узел дерева. Структурное изменение создаёт child snapshot; создание или сохранение само по себе не меняет Router routes или активный artifact. Website snapshot не выбирается для Router branch; публичная активация выполняется только явной outer Presentation/Coordinator orchestration через Router target CAS.
Snapshot представляет узел дерева. Структурное изменение создаёт child snapshot; создание или сохранение само по себе не меняет Router route mappings или активный artifact. Website snapshot не назначается Router branch; публичная активация выполняется только явной outer Presentation/Coordinator orchestration через Router target CAS.

Pages имеют server-generated UUID и независимые snapshot trees в пределах `page_id`. System-wide модель и Content records/publication общие для страниц. Page snapshots хранят placements и typed bindings к system-wide модели. Структурное редактирование выполняется в `/site`; record values и publication принадлежат `/content`.

## Сохранение и согласованность

Новый самостоятельный сайт начинается с root snapshot и пустой главной страницы по `/`, без feature placements и редакционного контента. Создание root не меняет текущий выбор редактора или Router route targets. Изменение выбранной структуры создаёт потомка; создание/сохранение использует optimistic revision checks. Устаревшая revision приводит к конфликту и не перезаписывает данные.

Snapshot fork создаёт независимое состояние. Он сохраняет валидные feature builds, placements, structural bindings и page metadata; Content-owned records, drafts, publication history и page-scoped selections копируются или сохраняются согласно [SPEC-content-editor-acceptance.md](../content/SPEC-content-editor-acceptance.md) и [SPEC-data-model.md](SPEC-data-model.md). Сохранение структуры не публикует Content и не меняет Router route targets.

Удаление требует подтверждения, сериализуется с изменениями дерева и проверяет, что после операции в каждой странице остаётся допустимое дерево согласно текущему Website-контракту. Конфликтующая revision отклоняется без частичного изменения. История остаётся аудиторской записью и не запрещает разрешённое удаление; ссылки на удалённый узел очищаются безопасно.

## Публичная выдача

Router выдаёт только route, явно включённые в выбранный build profile. Публичный route связывается с настроенным target и не принимает от посетителя произвольные Core IDs, group IDs, FileSet IDs или handlers. Branch policy и разрешённые Origins задаются при запуске; `noindex` не является контролем доступа. Публичные ответы по умолчанию `no-store`; неизвестный route возвращает 404, fallback используется только при явной конфигурации.

Публикация system-wide schema или records запускает durable rebuild затронутых страниц. Router переключает route только на готовый artifact и выполняет CAS по revision. При частичном отказе прежняя публикация сохраняется; stale results отбрасываются, retry идемпотентен. Междвижковую работу координирует внешняя Presentation orchestration или generic Core Coordinator; engines друг друга не вызывают.

## Проверяемые требования

- Независимые page snapshot trees в одной логической системе; system-wide модель и Content publication остаются общими.
- Root и child creation, immutable parent state, concurrent saves, stale revisions и безопасный retry.
- Website snapshots и Router route targets независимы; snapshots не назначаются веткам.
- Durable rebuild fan-out, stale-result rejection, per-route CAS и сохранение предыдущего artifact при сбое.
- Разделение route targets и snapshot state; branch isolation и startup-configured topology.
- API/browser проверки выполняются на disposable resources. Полная Website приёмка остаётся открытой до фиксации результатов в [QA_REPORT.md](../../../docs/QA_REPORT.md); активные задачи перечислены в [TODO](../../../docs/TODO-admin-snapshots-content-files.md).
