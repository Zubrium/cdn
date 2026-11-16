import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import { chromium } from 'playwright'

const adminOrigin = process.env.DOCS_ADMIN_ORIGIN
const unassignedBranchOrigin = process.env.DOCS_UNASSIGNED_BRANCH_ORIGIN
if (!adminOrigin || !unassignedBranchOrigin) {
  throw new Error('Укажите изолированные DOCS_ADMIN_ORIGIN и DOCS_UNASSIGNED_BRANCH_ORIGIN; скрипт выполняет только навигацию и скриншоты.')
}

const output = path.resolve('public/assets')
await mkdir(output, { recursive: true })
const browser = await chromium.launch({ headless: true, ...(process.env.DOCS_BROWSER_CHANNEL ? { channel: process.env.DOCS_BROWSER_CHANNEL } : {}) })
try {
  const page = await browser.newPage({ viewport: { width: 1440, height: 960 }, reducedMotion: 'reduce' })
  const admin = await page.goto(new URL('/admin/login', adminOrigin).href, { waitUntil: 'networkidle' })
  if (!admin?.ok() || !page.url().includes('/admin/login')) throw new Error('Страница входа админки недоступна или перенаправляет на неожиданный путь.')
  await page.screenshot({ path: path.join(output, 'engine-admin-login.png'), animations: 'disabled' })

  const publicResponse = await page.goto(new URL('/', unassignedBranchOrigin).href, { waitUntil: 'networkidle' })
  const title = await page.title()
  if (!publicResponse || (publicResponse.status() !== 200 && !(publicResponse.status() === 503 && title === 'Сайт ещё не настроен'))) {
    throw new Error(`Публичный listener ответил неожиданным статусом ${publicResponse?.status() ?? 'без ответа'} (${title}).`)
  }
  if (title !== 'Сайт ещё не настроен') throw new Error('Для снимка системной страницы выберите изолированную ветку без назначенного снапшота.')
  await page.screenshot({ path: path.join(output, 'engine-unassigned-branch.png'), fullPage: true, animations: 'disabled' })
  console.log(`Сохранены снимки: ${path.relative(process.cwd(), output)}`)
} finally {
  await browser.close()
}
