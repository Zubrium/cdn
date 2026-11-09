import { mkdir, readFile, writeFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const docsRoot = path.resolve(here, '..')
const projectRoot = path.resolve(docsRoot, '..')
const checkOnly = process.argv.includes('--check')

async function load(relativePath) {
  return readFile(path.join(projectRoot, relativePath), 'utf8')
}

function routeAccess(expression, route) {
  const normalized = expression.toLowerCase()
  if (route.startsWith('/api/v2/files/')) {
    return route.includes('/content') ? 'service key: read' : 'service key: scoped capability'
  }
  if (/\badmin\(/.test(normalized) || /\bjsonadmin\(/.test(normalized)) return 'Только администратор'
  if (normalized.includes('sitemanager(')) return 'Администратор или модератор'
  if (normalized.includes('jsonuser(') || /\buser\(/.test(normalized)) return 'Авторизованный пользователь'
  if (normalized.includes('authmw(')) return 'Действующий Bearer-токен'
  if (normalized.includes('cors(')) return 'Публичный маршрут; CORS по ветке'
  return 'Публичный маршрут'
}

function routeArea(route) {
  if (route.startsWith('/health/')) return 'Здоровье сервиса'
  if (route.startsWith('/admin')) return 'Админка'
  if (route.startsWith('/api/v1/admin')) return 'Административный API v1'
  if (route.startsWith('/api/v2/admin')) return 'Управление сайтом v2'
  if (route.startsWith('/api/v2/files')) return 'Файловый API интеграций'
  if (route.startsWith('/api/v2/forms')) return 'Публичные формы'
  if (route.startsWith('/api/v2')) return 'Публичный runtime API v2'
  if (route.startsWith('/api/v1/forms')) return 'Публичные формы'
  if (route.startsWith('/media')) return 'Публичные медиа'
  if (route.startsWith('/robots') || route.startsWith('/sitemap')) return 'Индексация'
  if (route.startsWith('/feature-assets')) return 'Файлы сборок фич'
  return 'Публичный сайт'
}

function handlerName(expression) {
  const match = expression.match(/(?:d\.)?([A-Z][A-Za-z0-9]*)\.([A-Z][A-Za-z0-9]*)\b/)
  if (match) return `${match[1]}.${match[2]}`
  const fileHandler = expression.match(/h\.([a-z][A-Za-z0-9]*)\b/)
  if (fileHandler) return `Files.${fileHandler[1]}`
  if (expression.includes('HandleFunc')) return 'HTTP handler'
  return 'HTTP handler'
}

function routeRecords(routesSource, fileHandlerSource) {
  const records = []
  for (const line of routesSource.split(/\r?\n/)) {
    const match = line.match(/mux\.(?:Handle|HandleFunc)\(\s*"((?:GET|HEAD|POST|PUT|PATCH|DELETE)\s+[^" ]+[^\"]*)"\s*,?\s*(.*)\)/)
    if (!match) continue
    const [, pattern, expression] = match
    const split = pattern.indexOf(' ')
    const method = pattern.slice(0, split)
    const route = pattern.slice(split + 1)
    records.push({ method, route, access: routeAccess(expression, route), handler: handlerName(expression) })
  }
  for (const [, route] of routesSource.matchAll(/"OPTIONS (\/[^\"]+)"/g)) {
    records.push({ method: 'OPTIONS', route, access: 'Публичный CORS preflight', handler: 'CORS preflight' })
  }

  // File routes are registered from a table in Handler.register rather than as
  // literal ServeMux calls. Derive the path, method, permission and handler
  // from that table and the two prefixes used by the registration functions.
  const fileRoutes = [...fileHandlerSource.matchAll(/\{"(GET|HEAD|POST|PUT|PATCH|DELETE)",\s*"([^"]*)",\s*"([^"]+)",\s*h\.([a-z][A-Za-z0-9]*)\}/g)]
  for (const [, method, suffix, permission, handler] of fileRoutes) {
    const tail = suffix ? `/${suffix.replace(/^\//, '')}` : ''
    records.push({ method, route: `/api/v2/files${tail}`, access: `Service key: ${permission}`, handler: `Files.${handler}` })
    const adminRule = method === 'DELETE' && suffix === '/objects/{id}'
      ? 'Авторизованный пользователь; удаление объекта — только администратор'
      : 'Авторизованный пользователь'
    records.push({ method, route: `/api/v2/admin/files${tail}`, access: adminRule, handler: `Files.${handler}` })
  }
  for (const method of ['GET', 'HEAD']) {
    records.push({ method, route: '/api/v2/files/objects/{id}/content', access: 'Service key: read', handler: 'Files.Content' })
    records.push({ method, route: `/api/v2/admin/files/objects/{id}/content`, access: 'Авторизованный пользователь', handler: 'Files.AdminContent' })
  }
  records.push({ method: 'GET', route: '/api/v2/admin/files/limits', access: 'Авторизованный пользователь', handler: 'Files.limits' })

  const deduped = new Map()
  for (const record of records) {
    if (!record.route || record.route.startsWith('/api/"')) continue
    deduped.set(`${record.method} ${record.route}`, record)
  }
  return [...deduped.values()].sort((a, b) => a.route.localeCompare(b.route) || a.method.localeCompare(b.method))
}

function escapeTable(value) {
  return String(value).replaceAll('|', '\\|').replaceAll('`', '\\`')
}

function makeAPIPage(routes) {
  const rows = routes.map((route) => `| \`${route.method}\` | \`${escapeTable(route.route)}\` | ${escapeTable(routeArea(route.route))} | ${escapeTable(route.access)} | \`${escapeTable(route.handler)}\` |`).join('\n')
  return `<!-- Generated by scripts/generate-references.mjs from presentation/http/routes.go and presentation/http/handlers/files/handler.go. Do not edit by hand. -->
# HTTP API

Этот каталог отражает зарегистрированные HTTP-маршруты текущего checkout. Таблица формируется из Go-кода. Авторизация указана по middleware и отдельным проверкам файлового handler.

## Соглашения

- Админские API используют \`Authorization: Bearer <token>\`; роль ограничивается серверными route guards.
- Публичные runtime-маршруты применяют CORS-политику назначенной ветки.
- Файловый API интеграций принимает отдельные Bearer-ключи с областями доступа.
- JSON-контракты и значения статусов задают handler/DTO Go-кода. В репозитории нет OpenAPI или Protobuf-спецификации, поэтому route registry не выдумывает неподтверждённые JSON-схемы.
- \`{id}\`, \`{key}\` и другие фигурные сегменты — параметры маршрута Go ServeMux.

Подробные сценарии: [ветки и снапшоты](../guides/branches-snapshots), [контент](../guides/content), [заявки](../guides/forms) и [файловый API](../guides/file-service).

## Зарегистрированные маршруты

| Метод | Маршрут | Область | Доступ | Go-обработчик |
| --- | --- | --- | --- | --- |
${rows}

Всего маршрутов: **${routes.length}**. Источники генерации — \`presentation/http/routes.go\` и \`presentation/http/handlers/files/handler.go\`.
`
}

function configGroup(name) {
  if (/^(POSTGRES|DATABASE|MIGRATION)/.test(name)) return 'База данных и миграции'
  if (/^(S3|FILES_)/.test(name)) return name.startsWith('S3_') ? 'Объектное хранилище' : 'Файловый сервис'
  if (/^(BRANCHES|HTTP_|HEALTH_|LOG_|TRUST_|MAX_JSON|MAX_UPLOAD|FORM_|ADMIN_PAGE|PUBLIC_PAGE|SUBMISSION_|READ_|SHUTDOWN_|MIGRATE_)/.test(name)) return 'HTTP, ветки и лимиты'
  if (/^(MEDIA_|MAX_FEATURE|FEATURE_BUILD|DESIGN_)/.test(name)) return 'Медиа и сборки фич'
  if (/^(PRERENDER_|SITEPACK_)/.test(name)) return name.startsWith('PRERENDER_') ? 'Пререндеринг' : 'Sitepack'
  return 'Основные параметры'
}

function safeExample(name, value) {
  const isSecret = /(_PASSWORD|_SECRET|_TOKEN|_ACCESS_KEY|^BOOTSTRAP_ADMIN_TOKEN$)/.test(name)
  if (isSecret || value === '') return 'задайте через окружение или secret file'
  if (value.includes('replace-with-')) return 'замените пример секретом из окружения/secret file'
  return value
}

function makeConfigurationPage(envExample) {
  const rows = []
  for (const line of envExample.split(/\r?\n/)) {
    const match = line.match(/^([A-Z][A-Z0-9_]*)=(.*)$/)
    if (!match) continue
    const [, name, raw] = match
    let value = raw
    if (value.startsWith("'") && value.endsWith("'")) value = value.slice(1, -1)
    rows.push({ group: configGroup(name), name, example: safeExample(name, value) })
  }
  const groups = [...new Set(rows.map((row) => row.group))]
  const sections = groups.map((group) => {
    const groupRows = rows.filter((row) => row.group === group)
    return `### ${group}\n\n| Переменная | Пример из .env.example |\n| --- | --- |\n${groupRows.map((row) => `| \`${row.name}\` | \`${escapeTable(row.example)}\` |`).join('\n')}`
  }).join('\n\n')
  return `<!-- Generated by scripts/generate-references.mjs from .env.example. Do not edit by hand. -->
# Конфигурация сервера

Таблица строится по переменным из корневого файла .env.example. Все эксплуатационные значения передаются явно; значения файла — примеры, не скрытые defaults приложения. Секреты задаются через окружение или secret files и не должны попадать в Git.

При запуске конфигурация проверяется fail-fast. Обязательные группы включают PostgreSQL, listener Engine и веток, настройки объектного хранилища и операционные лимиты. Пререндеринг можно включить только в образе с заранее установленными Node.js, Playwright и Chromium; во время запуска загрузки инструментов нет.

Источник конфигурации: .env.example, pkg/config.

${sections}
`
}

function extractGoStruct(source, name) {
  const match = source.match(new RegExp(`type ${name} struct \\{([\\s\\S]*?)\\n\\}`))
  if (!match) return []
  return match[1].split(/\r?\n/).flatMap((line) => {
    const field = line.match(/^\s*([A-Z][A-Za-z0-9_]*)\s+(.+?)\s+`json:"([^\"]+)"`/)
    if (!field) return []
    const [, goName, goType, jsonTag] = field
    const jsonName = jsonTag.split(',')[0]
    if (jsonName === '-') return []
    return [{ goName, goType: goType.trim(), jsonName, optional: jsonTag.split(',').includes('omitempty') }]
  })
}

function typeUnion(source, name) {
  const match = source.match(new RegExp(`export type ${name} =([\\s\\S]*?);`))
  if (!match) return []
  return [...match[1].matchAll(/\|\s*"([^"]+)"/g)].map((item) => item[1])
}

function makeManifestPage(manifestSource, inputSource, modelSource, tokenSource, mediaSource) {
  const structs = [
    ['Manifest', manifestSource],
    ['RuntimeContract', manifestSource],
    ['DataInputPort', inputSource],
    ['DataInputField', inputSource],
    ['FormContract', manifestSource],
    ['DataModel', modelSource],
    ['DataModelEntity', modelSource],
    ['DataModelField', modelSource],
    ['DataModelRelation', modelSource],
    ['DataModelPosition', modelSource],
    ['FeatureTokenDefinition', tokenSource],
    ['TokenValue', tokenSource],
    ['TokenColor', tokenSource],
    ['TokenShadow', tokenSource],
    ['FieldProfile', mediaSource],
    ['Rendition', mediaSource],
    ['VideoProfile', mediaSource],
  ]
  const sections = structs.map(([name, source]) => {
    const fields = extractGoStruct(source, name)
    if (!fields.length) return ''
    return `### \`${name}\`\n\n| JSON field | Go type | Optional |\n| --- | --- | --- |\n${fields.map((field) => `| \`${field.jsonName}\` | \`${escapeTable(field.goType)}\` | ${field.optional ? 'Да' : 'Нет'} |`).join('\n')}`
  }).filter(Boolean).join('\n\n')
  const modelTypes = [...modelSource.matchAll(/"([A-Za-z][A-Za-z0-9]*)": true/g)].map((match) => match[1])
  return `<!-- Generated by scripts/generate-references.mjs from the current Go manifest, data input, model, token, and media contracts. Do not edit by hand. -->
# Поля manifest и схемы

Этот справочник описывает формат manifest 2 и snapshot-модель данных. Manifest задаёт аргументы фичи и типизированную форму runtime-входа, а редактор записей строится только по ER-модели выбранного снапшота. Правила проверки описаны в [руководстве автора фич](../development/features) и [руководстве по контенту](../guides/content).

## Go-контракты manifest

Поле с тегом omitempty может быть пропущено в JSON. Старые per-feature CMS-схемы и manifest format 1 не поддерживаются.

${sections}

## Типы полей модели данных

Список берётся из валидатора \`DataModelField\` в \`features/sitebuilder/data_model.go\`. Форма \`/content\` генерируется по полям модели; типы в \`dataInputs\` проверяют совместимость runtime-проекции и не задают форму редактирования.

| Тип | Назначение |
| --- | --- |
${modelTypes.map((type) => `| \`${type}\` | ${{ text: 'Короткий текст', longText: 'Многострочный текст', integer: 'Целое число', number: 'Число', boolean: 'Логическое значение', date: 'Дата', dateTime: 'Дата и время', url: 'Ссылка', email: 'Адрес электронной почты', enum: 'Выбор из вариантов', media: 'Ссылка на разрешённый медиафайл' }[type] ?? 'Поле модели'} |`).join('\n')}

Все значения модели необязательны; заданные значения проверяются по типу, формату, связям, уникальности и доступу к файлам. Публичная форма остаётся строгой: \`form.submissionSchema\` задаёт валидацию отправки.

## Где расположен контракт

- Manifest и JSON Schema проверяются в features/sitebuilder/manifest.go.
- Типизированные runtime-входы проверяются в features/sitebuilder/data_inputs.go.
- Сущности, поля и связи модели проверяются в features/sitebuilder/data_model.go.
- Типизированные feature tokens проверяются в features/sitebuilder/design_system_structured.go.
- Профили медиа проверяются в features/media/profile.go.
`
}

function makeCLIPage(renameBranchSource, renameServiceSource) {
  const entries = [
    ['cmd/api/rename.go', renameBranchSource],
    ['cmd/api/rename_service.go', renameServiceSource],
  ].flatMap(([source, text]) => {
    const match = text.match(/usage: engine ([^"\n]+)/)
    return match ? [{ source, usage: match[1].split(' (then')[0] }] : []
  })
  return `<!-- Generated by scripts/generate-references.mjs from cmd/api rename command usage strings. Do not edit by hand. -->
# Операторский CLI

Список строится по usage-строкам команд в Go-коде.

| Команда | Назначение |
| --- | --- |
${entries.map(({ source, usage }) => `| \`engine ${usage}\` | Переименование конфигурируемой сущности; источник: \`${source}\` |`).join('\n')}

Команды используют DATABASE_URL текущего окружения и должны запускаться с остановленным сервером. После изменения записи обновите конфигурацию и перезапустите Engine. Пошаговые действия: [переименование ветки или сервиса](../admin/operations).
`
}

async function buildOutputs() {
  const [routesSource, fileHandlerSource, envExample, featureAuthoring, manifestSource, inputSource, modelSource, tokenSource, mediaSource, renameBranchSource, renameServiceSource] = await Promise.all([
    load('presentation/http/routes.go'),
    load('presentation/http/handlers/files/handler.go'),
    load('.env.example'),
    load('docs/FEATURE-RUNTIME-AUTHORING.md'),
    load('features/sitebuilder/manifest.go'),
    load('features/sitebuilder/data_inputs.go'),
    load('features/sitebuilder/data_model.go'),
    load('features/sitebuilder/design_system_structured.go'),
    load('features/media/profile.go'),
    load('cmd/api/rename.go'),
    load('cmd/api/rename_service.go'),
  ])
  const routes = routeRecords(routesSource, fileHandlerSource)
  const featurePage = `<!-- Generated from docs/FEATURE-RUNTIME-AUTHORING.md. Do not edit here; update the source document. -->\n\n${featureAuthoring}`
  return new Map([
    ['reference/api.md', makeAPIPage(routes)],
    ['reference/configuration.md', makeConfigurationPage(envExample)],
    ['reference/manifest.md', makeManifestPage(manifestSource, inputSource, modelSource, tokenSource, mediaSource)],
    ['reference/feature-runtime.md', featurePage],
    ['reference/cli.md', makeCLIPage(renameBranchSource, renameServiceSource)],
  ])
}

const outputs = await buildOutputs()
let mismatches = 0
for (const [relativePath, content] of outputs) {
  const target = path.join(docsRoot, relativePath)
  let current = ''
  try { current = await readFile(target, 'utf8') } catch {}
  if (current === content) continue
  if (checkOnly) {
    console.error(`Generated reference is stale: ${relativePath}`)
    mismatches += 1
  } else {
    await mkdir(path.dirname(target), { recursive: true })
    await writeFile(target, content)
    console.log(`Generated ${relativePath}`)
  }
}
if (checkOnly && mismatches > 0) process.exitCode = 1
