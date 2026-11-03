#!/usr/bin/env node

import { readdir, readFile } from 'node:fs/promises'
import path from 'node:path'
import process from 'node:process'

const engineUrl = (process.env.ENGINE_URL ?? '').replace(/\/+$/, '')
const token = process.env.ENGINE_TOKEN?.trim()
const legacyRoot = process.env.LEGACY_SITE_DIR
const featureBuildDir = process.env.FEATURE_BUILD_DIR

if (!engineUrl || !token || !legacyRoot) {
  throw new Error('Set ENGINE_URL, ENGINE_TOKEN, and LEGACY_SITE_DIR before migration.')
}

const legacyDir = path.resolve(legacyRoot)
const dataDir = path.join(legacyDir, 'backend', 'data')
const assetsDir = path.join(legacyDir, 'backend', 'assets')
const archivesDir = path.resolve(featureBuildDir ?? path.join(legacyDir, 'frontend', 'dist', 'feature-builds'))
const adminHeaders = { Authorization: `Bearer ${token}`, Accept: 'application/json' }

async function readLegacy(name) {
  return JSON.parse(await readFile(path.join(dataDir, name), 'utf8'))
}

async function walkFiles(directory, prefix = '') {
  const entries = (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name.localeCompare(b.name))
  const files = []
  for (const entry of entries) {
    const relative = path.posix.join(prefix, entry.name)
    const absolute = path.join(directory, entry.name)
    if (entry.isDirectory()) files.push(...await walkFiles(absolute, relative))
    else if (entry.isFile()) files.push(relative)
  }
  return files
}

async function request(route, options = {}) {
  const response = await fetch(`${engineUrl}${route}`, {
    ...options,
    headers: {
      ...adminHeaders,
      ...(options.headers ?? {}),
    },
  })
  const raw = await response.text()
  let body
  try { body = raw ? JSON.parse(raw) : undefined } catch { body = raw }
  if (!response.ok) {
    const message = body?.error?.message ?? body?.message ?? raw ?? response.statusText
    throw new Error(`${options.method ?? 'GET'} ${route} (${response.status}): ${message}`)
  }
  return body
}

async function listAll(route) {
  const items = []
  let cursor
  do {
    const url = new URL(route, engineUrl)
    url.searchParams.set('limit', '200')
    if (cursor) url.searchParams.set('cursor', cursor)
    const result = await request(url.pathname + url.search)
    items.push(...(result.items ?? []))
    cursor = result.next_cursor || result.nextCursor || undefined
  } while (cursor)
  return items
}

async function uploadFeatureBuilds() {
  const files = (await readdir(archivesDir)).filter((name) => name.endsWith('.zip')).sort()
  if (files.length === 0) throw new Error(`No feature archives found in ${archivesDir}; build the site features first.`)
  for (const name of files) {
    const archive = await readFile(path.join(archivesDir, name))
    const imported = await request('/api/v2/admin/feature-builds', {
      method: 'POST',
      headers: { 'Content-Type': 'application/zip' },
      body: archive,
    })
    process.stdout.write(`feature ${imported.key}@${imported.version} uploaded (${imported.digest.slice(0, 12)})\n`)
  }
}

async function uploadLegacyMedia() {
  const files = (await walkFiles(assetsDir)).filter((name) => !name.toLowerCase().endsWith('.txt'))
  const existing = await listAll('/api/v1/admin/media?status=ready')
  const byFilename = new Map(existing.map((media) => [media.original_filename, media.id]))
  const ids = new Map()
  for (const relative of files) {
    const filename = path.basename(relative)
    let id = byFilename.get(filename)
    if (!id) {
      const bytes = await readFile(path.join(assetsDir, relative))
      const ext = path.extname(filename).toLowerCase()
      const contentType = ext === '.svg' ? 'image/svg+xml' : ext === '.woff2' ? 'font/woff2' : undefined
      const form = new FormData()
      form.set('file', new Blob([bytes], contentType ? { type: contentType } : undefined), filename)
      const uploaded = await request('/api/v1/admin/media', { method: 'POST', body: form })
      id = uploaded.id
      process.stdout.write(`media ${filename} uploaded (${id})\n`)
    } else {
      process.stdout.write(`media ${filename} reused (${id})\n`)
    }
    ids.set(filename, id)
  }
  return ids
}

const alumniSubsets = {
  'alumni-sans-sc-cyrillic-ext-wght-normal.woff2': 'U+0460-052F,U+1C80-1C8A,U+20B4,U+2DE0-2DFF,U+A640-A69F,U+FE2E-FE2F',
  'alumni-sans-sc-cyrillic-wght-normal.woff2': 'U+0301,U+0400-045F,U+0490-0491,U+04B0-04B1,U+2116',
  'alumni-sans-sc-vietnamese-wght-normal.woff2': 'U+0102-0103,U+0110-0111,U+0128-0129,U+0168-0169,U+01A0-01A1,U+01AF-01B0,U+0300-0301,U+0303-0304,U+0308-0309,U+0323,U+0329,U+1EA0-1EF9,U+20AB',
  'alumni-sans-sc-latin-ext-wght-normal.woff2': 'U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF',
  'alumni-sans-sc-latin-wght-normal.woff2': 'U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD',
}

async function migrateDesignSystem(mediaIDs) {
  const missingFonts = Object.keys(alumniSubsets).filter((filename) => !mediaIDs.has(filename))
  if (missingFonts.length) throw new Error(`Legacy Alumni Sans SC files are missing: ${missingFonts.join(', ')}`)
  const current = await request('/api/v2/admin/site/design-system')
  const fonts = Object.entries(alumniSubsets).map(([filename, unicodeRange]) => ({
    family: 'Alumni Sans SC',
    mediaId: mediaIDs.get(filename),
    weight: '100 900',
    style: 'normal',
    unicodeRange,
  }))
  const designSystem = {
    ...current.state.draft,
    brandLogoMediaId: mediaIDs.get('issit_logo.svg'),
    fonts,
    fontRoles: { body: 'Alumni Sans SC', display: 'Alumni Sans SC', header: 'Alumni Sans SC', hero: 'Alumni Sans SC' },
  }
  const saved = await request('/api/v2/admin/site/design-system', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision: current.state.revision, designSystem }),
  })
  process.stdout.write(`design system revision ${saved.state.revision} saved (logo and ${fonts.length} Alumni Sans SC subsets)\n`)
}

async function migrateSiteMetadata(legacySite, home, mediaIDs) {
  const current = await request('/api/v2/admin/site/metadata')
  const title = legacySite.documentTitle || legacySite.header?.brand || 'ВитаСил'
  const description = home.hero?.subtitle || legacySite.footer?.description || ''
  const metadata = {
    ...current.draft,
    siteName: title,
    description,
    ogTitle: title,
    ogDescription: description,
  }
  if (legacySite.faviconSrc) metadata.faviconMediaId = mediaID(mediaIDs, legacySite.faviconSrc)
  const saved = await request('/api/v2/admin/site/metadata', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision: current.revision, metadata }),
  })
  const published = await request('/api/v2/admin/site/metadata/publish', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({}),
  })
  process.stdout.write(`site metadata revision ${saved.revision} saved and published to dev (title: ${published.dev.siteName})\n`)
}

function mediaID(ids, reference) {
  if (typeof reference !== 'string' || !reference.startsWith('/media/')) return reference
  const filename = decodeURIComponent(reference.slice('/media/'.length))
  const id = ids.get(filename)
  if (!id) throw new Error(`Legacy media reference ${reference} has no uploaded asset.`)
  return id
}

function replaceMediaRefs(value, ids) {
  if (Array.isArray(value)) return value.map((item) => replaceMediaRefs(item, ids))
  if (value && typeof value === 'object') {
    return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, replaceMediaRefs(item, ids)]))
  }
  return typeof value === 'string' && value.startsWith('/media/') ? mediaID(ids, value) : value
}

async function publishContent(feature, data) {
  let entries = await listAll(`/api/v1/admin/features/${encodeURIComponent(feature.id)}/entries`)
  if (entries.length === 0) {
    const result = await request(`/api/v2/admin/features/${encodeURIComponent(feature.id)}/entries/import`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ data }),
    })
    entries = result.items ?? []
  }
  for (const entry of entries) {
    if (entry.dev_version_id) continue
    if (!entry.draft_version_id) {
      process.stdout.write(`content ${feature.key}/${entry.id} skipped; it has no draft to publish\n`)
      continue
    }
    // Publication blocks until required media renditions are ready; retry
    // while the engine reports files as processing.
    let published = null
    for (let attempt = 0; attempt < 24; attempt += 1) {
      try {
        published = await request(`/api/v1/admin/entries/${encodeURIComponent(entry.id)}/publish`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ revision: entry.revision }),
        })
        break
      } catch (error) {
        if (attempt === 23 || !/(ready|processing)/i.test(String(error.message))) throw error
        process.stdout.write(`content ${feature.key}/${entry.id} waits for renditions; retrying in 5 s\n`)
        await new Promise((resolve) => setTimeout(resolve, 5000))
      }
    }
    process.stdout.write(`content ${feature.key}/${published.id} published to dev\n`)
  }
}

function literal(value) {
  return { source: 'literal', value }
}

function placement(build, slot, argumentsMap = {}, extra = {}) {
  return {
    id: `${build.key}-${slot}-${Math.random().toString(36).slice(2, 8)}`,
    build: { featureKey: build.key, version: build.version },
    slot,
    arguments: argumentsMap,
    ...extra,
  }
}

async function main() {
  const [legacySite, home, catalog, form, messages] = await Promise.all([
    readLegacy('site.json'),
    readLegacy('home.json'),
    readLegacy('catalog.json'),
    readLegacy('form.json'),
    readLegacy('messages.json'),
  ])
  await uploadFeatureBuilds()
  const media = await uploadLegacyMedia()
  await migrateDesignSystem(media)
  const builds = await request('/api/v2/admin/feature-builds')
  const byKey = new Map()
  for (const build of builds.items ?? []) if (!byKey.has(build.key)) byKey.set(build.key, build)
  const required = ['background', 'header', 'hero', 'main-data', 'catalog', 'catalog-detail', 'form', 'footer', 'not-found']
  for (const key of required) if (!byKey.has(key)) throw new Error(`Feature build ${key} was not registered.`)

  const allFeatures = await listAll('/api/v1/admin/features?status=active')
  const features = new Map(allFeatures.map((feature) => [feature.key, feature]))

  const homeBackground = mediaID(media, home.background.imageSrc)
  const catalogBackground = mediaID(media, catalog.background.imageSrc)
  const featureData = [
    ['header', replaceMediaRefs(legacySite.header, media)],
    ['hero', replaceMediaRefs(home.hero, media)],
    ['main-data', replaceMediaRefs(home.mainData, media)],
    ['footer', legacySite.footer],
    ['form', form],
    ['not-found', {
      title: messages.notFoundTitle,
      description: messages.notFoundDescription,
      backToHomeLabel: messages.backToHomeLabel,
      backToHomeTo: '/',
    }],
  ]

  const publishAllContent = async () => {
    for (const [key, data] of featureData) {
      const feature = features.get(key)
      if (!feature) throw new Error(`Manifest import did not register feature ${key}.`)
      await publishContent(feature, data)
    }

    const catalogFeature = features.get('catalog')
    const catalogItems = (catalog.content.items ?? []).map((item) => ({
      slug: String(item.id),
      title: item.title,
      price: item.price,
      ...(item.imageSrc ? { imageSrc: mediaID(media, item.imageSrc) } : {}),
      ...(item.description ? { description: item.description } : {}),
    }))
    await publishContent(catalogFeature, catalogItems)

    // Collection settings live in a dedicated single-slot settings entry; the
    // catalog build reads labels and footer actions from it, not arguments.
    const settingsData = {
      title: catalog.content.title,
      breadcrumb: catalog.content.breadcrumb,
      action: catalog.content.action,
    }
    let settingsEntry = null
    try {
      settingsEntry = await request(`/api/v2/admin/features/${encodeURIComponent(catalogFeature.id)}/settings`)
    } catch {
      settingsEntry = null
    }
    if (!settingsEntry) {
      settingsEntry = await request(`/api/v2/admin/features/${encodeURIComponent(catalogFeature.id)}/settings`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ data: settingsData }),
      })
      process.stdout.write(`catalog settings draft created (${settingsData.title ?? ''})\n`)
    }
    if (settingsEntry && !settingsEntry.dev_version_id && settingsEntry.draft_version_id) {
      const published = await request(`/api/v1/admin/entries/${encodeURIComponent(settingsEntry.id)}/publish`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ revision: settingsEntry.revision }),
      })
      process.stdout.write(`catalog settings published to dev (v${published.dev_version_id ?? ''} → ${published.dev?.version ?? ''})\n`)
    }
  }

  const headerPlacement = () => placement(byKey.get('header'), 'header')
  const footerPlacement = () => placement(byKey.get('footer'), 'footer')
  const backgroundPlacement = (imageSrc) => placement(byKey.get('background'), 'background', { imageSrc: literal(imageSrc) })
  const pages = [
    {
      id: 'home', name: 'Главная', kind: 'page',
      placements: [backgroundPlacement(homeBackground), headerPlacement(), placement(byKey.get('hero'), 'main'), placement(byKey.get('main-data'), 'main'), footerPlacement()],
    },
    {
      id: 'catalog', name: 'Каталог', kind: 'collection-list',
      placements: [backgroundPlacement(catalogBackground), headerPlacement(), placement(byKey.get('catalog'), 'main', {}, { detailLink: { routeId: 'product-detail', field: 'slug', pathTemplate: '/catalog/{slug}' } }), footerPlacement()],
    },
    {
      id: 'product-detail', name: 'Карточка товара', kind: 'detail',
      placements: [backgroundPlacement(catalogBackground), headerPlacement(), placement(byKey.get('catalog-detail'), 'main', {
        slug: { source: 'path', pathArg: 'slug' },
      }, { collectionSource: { featureKey: 'catalog', version: byKey.get('catalog').version, field: 'slug', argument: 'slug' } }), footerPlacement()],
    },
    {
      id: 'form', name: 'Форма заявки', kind: 'page',
      placements: [backgroundPlacement(homeBackground), headerPlacement(), placement(byKey.get('form'), 'main'), footerPlacement()],
    },
    {
      id: 'not-found', name: 'Страница не найдена', kind: 'page',
      placements: [backgroundPlacement(homeBackground), headerPlacement(), placement(byKey.get('not-found'), 'main'), footerPlacement()],
    },
  ]
  const config = {
    formatVersion: 3,
    pages,
    routeTree: {
      id: 'site',
      type: 'site',
      children: [
        { id: 'home', type: 'path', path: '/', pageId: 'home' },
        {
          id: 'catalog', type: 'path', path: '/catalog', pageId: 'catalog',
          children: [{ id: 'product-detail', type: 'param', name: 'slug', pageId: 'product-detail' }],
        },
        { id: 'form', type: 'path', path: '/form', pageId: 'form' },
        { id: 'not-found', type: 'catchAll', name: 'path', pageId: 'not-found' },
      ],
    },
  }

  const draft = await request('/api/v2/admin/site/draft')
  const saved = await request('/api/v2/admin/site/draft', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ revision: draft.revision, config }),
  })
  await migrateSiteMetadata(legacySite, home, media)

  // Renditions for background images may still be processing; wait for them
  // so that the first snapshot does not fail media readiness checks.
  let snapshot = null
  let snapshotsList = null
  try {
    snapshotsList = await listAll('/api/v2/admin/site/snapshots')
  } catch {
    snapshotsList = []
  }
  if (snapshotsList.length > 0) {
    snapshot = snapshotsList[snapshotsList.length - 1]
    process.stdout.write(`reusing dev snapshot #${snapshot.sequence}\n`)
  }
  for (let attempt = 0; attempt < 24 && !snapshot; attempt += 1) {
    try {
      snapshot = await request('/api/v2/admin/site/snapshots', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: 'Перенос legacy сайта' }),
      })
    } catch (error) {
      if (attempt === 23 || !/(ready|processing)/i.test(String(error.message))) throw error
      process.stdout.write('media renditions are processing; waiting 5 s\n')
      await new Promise((resolve) => setTimeout(resolve, 5000))
    }
  }
  const activeDev = (await request('/api/v2/admin/site/snapshots')).activeSnapshots?.dev
  if (!activeDev || activeDev.id !== snapshot.id) {
    await request(`/api/v2/admin/site/snapshots/${encodeURIComponent(snapshot.id)}/deploy`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ environment: 'dev' }),
    })
  }
  process.stdout.write(`site draft r${saved.revision} saved; dev snapshot #${snapshot.sequence} (${snapshot.id}) published\n`)
  // Listing and publishing entries needs an active site snapshot, so content
  // migration only runs after the snapshot exists.
  await publishAllContent()
  process.stdout.write('The migration publishes to dev only. Promote this same snapshot to prod from /admin/site after review.\n')
}

await main()
