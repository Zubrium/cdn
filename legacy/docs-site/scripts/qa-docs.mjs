import { spawn } from 'node:child_process'
import { mkdir, readdir } from 'node:fs/promises'
import path from 'node:path'
import { setTimeout as delay } from 'node:timers/promises'
import { chromium } from 'playwright'

const root = process.cwd()
const host = '127.0.0.1'
const port = Number(process.env.DOCS_QA_PORT || 4177)
const baseURL = `http://${host}:${port}`
const server = spawn(process.execPath, [path.join(root, 'node_modules/vitepress/bin/vitepress.js'), 'preview', '.', '--host', host, '--port', String(port), '--strictPort'], { cwd: root, stdio: 'ignore' })
let browser

async function collectMarkdown(directory = root) {
  const entries = await readdir(directory, { withFileTypes: true })
  const files = []
  for (const entry of entries) {
    if (entry.isDirectory()) {
      if (!['.vitepress', 'node_modules', 'public'].includes(entry.name)) {
        files.push(...await collectMarkdown(path.join(directory, entry.name)))
      }
    } else if (entry.isFile() && entry.name.endsWith('.md') && entry.name !== 'README.md') {
      files.push(path.join(directory, entry.name))
    }
  }
  return files
}

async function waitForServer() {
  const deadline = Date.now() + 20000
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`VitePress preview завершился с кодом ${server.exitCode}`)
    try {
      const response = await fetch(baseURL)
      if (response.ok) return
    } catch {}
    await delay(250)
  }
  throw new Error(`VitePress preview не открыл ${baseURL}`)
}

try {
  await waitForServer()
  browser = await chromium.launch({ headless: true, ...(process.env.DOCS_BROWSER_CHANNEL ? { channel: process.env.DOCS_BROWSER_CHANNEL } : {}) })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' })
  const page = await context.newPage()
  const failures = []
  page.on('pageerror', (error) => failures.push(`pageerror ${JSON.stringify({ name: error.name, message: error.message, stack: error.stack, keys: Object.keys(error) })}`))
  page.on('console', (message) => {
    if (message.type() === 'error') failures.push(`console: ${message.text()} @ ${JSON.stringify(message.location())}`)
  })
  page.on('response', (response) => {
    if (response.url().startsWith(baseURL) && response.status() >= 400) {
      failures.push(`HTTP ${response.status()}: ${response.url()}`)
    }
  })

  const markdownPages = await collectMarkdown()
  const routes = markdownPages.map((file) => {
    const relative = path.relative(root, file).replaceAll(path.sep, '/')
    const route = `/${relative.replace(/\.md$/, '')}`
    return route === '/index' ? '/' : route.endsWith('/index') ? route.slice(0, -5) : route
  })

  for (const route of routes) {
    const response = await page.goto(`${baseURL}${route}`, { waitUntil: 'networkidle' })
    if (!response?.ok()) throw new Error(`${route}: HTTP ${response?.status() ?? 'no response'}`)
    const h1 = await page.locator('main h1').first().textContent()
    if (!h1?.trim()) throw new Error(`${route}: нет заголовка страницы`)
  }

  await page.locator('button.DocSearch-Button').click()
  const searchInput = page.locator('#localsearch-input')
  await searchInput.waitFor({ state: 'visible' })
  if (await searchInput.getAttribute('placeholder') !== 'Поиск') throw new Error('Подпись поиска не локализована на русский')
  await searchInput.fill('Feature Lab')
  const featureLabResult = page.locator('.VPLocalSearchBox .result').filter({ hasText: 'Feature Lab' }).first()
  await featureLabResult.waitFor({ state: 'visible', timeout: 10000 })
  if (!(await featureLabResult.getAttribute('href'))?.includes('/development/feature-lab')) {
    throw new Error('Поиск Feature Lab ведёт не на страницу документации инструмента')
  }
  const featureLabHref = await featureLabResult.getAttribute('href')
  await featureLabResult.click()
  await page.waitForFunction((expectedPath) => location.pathname.includes(expectedPath), '/development/feature-lab', { timeout: 10000 }).catch(() => {
    throw new Error(`Результат локального поиска не открыл страницу Feature Lab: href=${featureLabHref}, url=${page.url()}`)
  })

  await page.goto(`${baseURL}/explanations/architecture`, { waitUntil: 'networkidle' })
  await page.locator('.vp-doc .mermaid svg').first().waitFor({ state: 'visible', timeout: 10000 })
  const desktop = await page.evaluate(() => ({ width: innerWidth, clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }))
  if (desktop.scrollWidth > desktop.clientWidth) throw new Error(`Горизонтальное переполнение desktop: ${JSON.stringify(desktop)}`)
  const screenshotDir = path.resolve(root, '../output/playwright')
  await mkdir(screenshotDir, { recursive: true })
  await page.screenshot({ path: path.join(screenshotDir, 'docs-desktop.png'), fullPage: true, animations: 'disabled' })

  const themeButton = page.locator('button.VPSwitchAppearance').first()
  if (await themeButton.count()) {
    const beforeTheme = await page.evaluate(() => document.documentElement.classList.contains('dark'))
    const beforeLogo = await page.locator('.VPNavBarTitle img.VPImage:visible').getAttribute('src')
    const expectedBefore = beforeTheme ? '/assets/brand-dark.svg' : '/assets/brand-light.svg'
    if (!beforeLogo?.endsWith(expectedBefore)) throw new Error(`Логотип не соответствует текущей теме: ${beforeLogo}`)
    await themeButton.click()
    await page.waitForFunction((before) => document.documentElement.classList.contains('dark') !== before, beforeTheme)
    const expectedAfter = beforeTheme ? '/assets/brand-light.svg' : '/assets/brand-dark.svg'
    await page.locator(`.VPNavBarTitle img.VPImage[src$="${expectedAfter}"]`).waitFor({ state: 'visible' })
  }

  await page.setViewportSize({ width: 390, height: 844 })
  const mobile = await page.evaluate(() => ({ width: innerWidth, clientWidth: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth }))
  if (mobile.scrollWidth > mobile.clientWidth) throw new Error(`Горизонтальное переполнение mobile: ${JSON.stringify(mobile)}`)
  await page.screenshot({ path: path.resolve(root, '../output/playwright/docs-mobile.png'), animations: 'disabled' })
  if (failures.length) throw new Error(`Ошибки браузера: ${JSON.stringify(failures)}`)
  console.log(`Проверены ${routes.length} страниц, локальный поиск, Mermaid, тема и адаптивность: desktop ${desktop.clientWidth}px, mobile ${mobile.clientWidth}px.`)
} finally {
  await browser?.close()
  server.kill('SIGTERM')
}
