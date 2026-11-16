# Feature Lab

Feature Lab — локальная среда предпросмотра фич из `src/features/<key>`. Она помогает проверить manifest, runtime context, SDK mock и адаптивность без запущенного Engine. Это инструмент разработки внутри репозитория Website Engine; он не регистрирует ZIP и не заменяет интеграционную проверку в движке.

## Запуск

Текущая интеграция запускает Feature Lab из frontend-проекта с установленными Vite, React и Tailwind-плагинами:

```sh
cd /path/to/feature-project/frontend
VITASIL_FRONTEND_DIR="$PWD" FEATURE_LAB_PORT=5178 \
  pnpm exec vite \
  --config /path/to/website-engine/feature-lab/vite.config.mjs \
  --host 127.0.0.1 --port 5178 --strictPort
```

Откройте `http://127.0.0.1:5178`. Переменная `VITASIL_FRONTEND_DIR` — историческое имя обязательного пути к frontend-проекту; путь должен содержать `package.json` и `src/features`. `FEATURE_LAB_PORT` должен быть явно задан числом. Вспомогательный запускатель feature-проекта может задать их за вас.

Хост привязан к loopback и не предназначен для удалённого публичного предпросмотра.

## Как находятся фичи

Лаборатория сканирует `src/features/*/manifest.json`, сортирует каталоги по имени и проверяет соответствие `manifest.key` имени каталога. Ошибка одного manifest или файла entry остаётся на карточке этой фичи и не скрывает остальные.

### Исходный React ABI

Если в manifest нет `runtime`, лаборатория ищет entry последовательно: `index.tsx`, `index.ts`, `index.jsx`, `index.js`. Компонент загружается с React toolchain frontend-проекта и отображается через портал в same-origin iframe. Core CSS и стили фичи отражаются в предпросмотре; hot reload снимает предыдущий React root перед повторной загрузкой.

### `static-module-v1`

Укажите уже собранный `runtime.entry` и необязательные `runtime.styles`. Каждый путь должен быть относительным, вести к обычному файлу внутри каталога фичи и не обходить его через symlink. Сборка самого framework остаётся ответственностью toolchain фичи; Feature Lab не устанавливает пакеты и не компилирует исходники.

Лаборатория вызывает `mount(root, context)`, ждёт `ready`, сериализует последующие `update()` и вызывает `unmount()` при замене фичи или сбросе состояния. `sdk.listCollection` читает локальные fixture-записи; `sdk.submitForm` добавляет событие в локальный журнал. Используется тот же ABI, что и в [runtime reference](../reference/feature-runtime).

### `static-html-v1`

Укажите HTML fragment и локальные стили. Предпросмотр вставляет literal fragment без content binding и SDK. Скрипты, embedded documents, inline event handlers и `javascript:` URL блокируются до вставки.

## Fixtures и инструменты

- `arguments` создаются из схемы параметров manifest, а `content` — из typed `dataInputs`;
- model input с `kind: "collection"` даёт локальный набор записей для preview и mock `listCollection`;
- контекст включает `featureKey`, версию, маршрут, систему ресурсов и `AbortSignal`;
- manifest-derived defaults `designTokens` видны как CSS custom properties в iframe;
- редактор fixture JSON показывает ошибку и сохраняет последний корректный preview;
- ширина меняет viewport iframe, а управление путём обновляет смоделированный маршрут;
- локальные ссылки продолжают работать внутри preview; внешняя навигация сохраняет обычное поведение браузера.

Fixture — временные примеры runtime-входа, не данные CMS и не ER-модель. Они не сохраняются между перезагрузками. Feature Lab не поддерживает manifest format 1 и не использует `content.schema` fallback.

## Обновления и ошибки

Изменения feature source/style обновляются через Vite HMR. Новый или удалённый каталог и изменение manifest обновляют виртуальный registry. Ошибки manifest, загрузки модуля, lifecycle и JSON изолируются в preview выбранной фичи; панель и выбор других фич остаются доступны.

## Граница безопасности

Feature Lab сам не вызывает Engine API, не читает DB/S3 credentials, пользовательские данные или секреты и не изменяет ZIP. Он ограничивает filesystem allowlist каталогом лаборатории и настроенным frontend-путём. Код самой фичи может отправлять собственные сетевые запросы; используйте внедрённый mock SDK для Engine-операций и проверяйте вкладку Network.

Не помещайте чувствительные данные в fixtures. Локальная лаборатория — не sandbox для недоверенного JavaScript, не проверяет production CORS и не доказывает, что архив пройдёт серверный валидатор.

## Проверка перед регистрацией

1. Проверьте каждую ABI-ветку, которую поддерживает manifest.
2. Измените контент и маршрут; убедитесь, что lifecycle получает новый контекст.
3. Для коллекций проверьте list fixtures и состояния списка, пустого результата и ошибки.
4. Для формы проверьте submit handler и состояние подтверждения; убедитесь, что запрос остался локальным.
5. Проверьте несколько ширин iframe и обработку сломанного JSON.
6. Затем отдельно соберите ZIP, загрузите его в изолированный Engine и назначьте в тестовый снапшот.

Только проверка ZIP и публичного listener подтверждает Engine-совместимость, media processing, схемы, prerender и HTTP-контракты.
