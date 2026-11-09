import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'ru-RU',
  title: 'Zubrium Engine',
  description: 'Руководство по Website Engine: создание сайтов, контент, ветки, снапшоты и разработка фич.',
  appearance: true,
  cleanUrls: true,
  lastUpdated: true,
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }]],
  markdown: {
    config(md) {
      const defaultFence = md.renderer.rules.fence!
      md.renderer.rules.fence = (tokens, index, options, env, renderer) => {
        const token = tokens[index]
        if (token.info.trim() === 'mermaid') {
          return `<pre class="mermaid">${md.utils.escapeHtml(token.content)}</pre>\n`
        }
        return defaultFence(tokens, index, options, env, renderer)
      }
    },
  },
  themeConfig: {
    logo: {
      light: '/assets/brand-light.svg',
      dark: '/assets/brand-dark.svg',
      alt: 'Zubrium Engine',
    },
    siteTitle: 'Zubrium Engine',
    search: {
      provider: 'local',
      options: {
        translations: {
          button: { buttonText: 'Поиск', buttonAriaLabel: 'Поиск' },
          modal: {
            displayDetails: 'Показать подробности',
            resetButtonTitle: 'Сбросить запрос',
            backButtonTitle: 'Закрыть поиск',
            noResultsText: 'Ничего не найдено',
            footer: {
              selectText: 'Выбрать',
              selectKeyAriaLabel: 'Выбрать',
              navigateText: 'Навигация',
              navigateUpKeyAriaLabel: 'Вверх',
              navigateDownKeyAriaLabel: 'Вниз',
              closeText: 'Закрыть',
              closeKeyAriaLabel: 'Закрыть',
            },
          },
        },
      },
    },
    outline: { level: [2, 3], label: 'На этой странице' },
    returnToTopLabel: 'Наверх',
    sidebarMenuLabel: 'Разделы',
    darkModeSwitchLabel: 'Тема',
    lightModeSwitchTitle: 'Светлая тема',
    darkModeSwitchTitle: 'Тёмная тема',
    docFooter: { prev: 'Предыдущая страница', next: 'Следующая страница' },
    nav: [
      { text: 'Начать', link: '/tutorials/first-site' },
      { text: 'Руководства', link: '/guides/content' },
      { text: 'Система', link: '/explanations/architecture' },
      { text: 'Разработка', link: '/development/local' },
      { text: 'Справочник', link: '/reference/api' },
    ],
    sidebar: {
      '/tutorials/': [
        { text: 'Обучение', items: [{ text: 'Создать и запустить сайт', link: '/tutorials/first-site' }] },
      ],
      '/guides/': [
        {
          text: 'Работа с сайтом',
          items: [
            { text: 'Страницы и размещения', link: '/guides/site-structure' },
            { text: 'Контент и публикация', link: '/guides/content' },
            { text: 'Ветки и снапшоты', link: '/guides/branches-snapshots' },
            { text: 'Дизайн-система', link: '/guides/design-system' },
            { text: 'Медиа и файлы', link: '/guides/media' },
            { text: 'Формы и заявки', link: '/guides/forms' },
            { text: 'Перенос снапшота', link: '/guides/sitepack' },
          ],
        },
        {
          text: 'Администрирование',
          items: [
            { text: 'Пользователи и токены', link: '/admin/access' },
            { text: 'Развёртывание и конфигурация', link: '/admin/operations' },
          ],
        },
        {
          text: 'Интеграции',
          items: [{ text: 'Файловый HTTP API', link: '/guides/file-service' }],
        },
      ],
      '/admin/': [
        {
          text: 'Администрирование',
          items: [
            { text: 'Пользователи и токены', link: '/admin/access' },
            { text: 'Развёртывание и конфигурация', link: '/admin/operations' },
          ],
        },
      ],
      '/explanations/': [
        {
          text: 'Устройство системы',
          items: [
            { text: 'Архитектура', link: '/explanations/architecture' },
            { text: 'Ветки, снапшоты и контент', link: '/explanations/snapshots-content' },
            { text: 'Публичный runtime и SEO', link: '/explanations/runtime-seo' },
            { text: 'Файлы и медиа', link: '/explanations/files' },
          ],
        },
      ],
      '/development/': [
        {
          text: 'Разработка',
          items: [
            { text: 'Локальная среда и проверки', link: '/development/local' },
            { text: 'Фичи и manifest', link: '/development/features' },
            { text: 'Feature Lab', link: '/development/feature-lab' },
            { text: 'Поддержка документации', link: '/development/docs-maintenance' },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Справочник',
          items: [
            { text: 'HTTP API', link: '/reference/api' },
            { text: 'Конфигурация', link: '/reference/configuration' },
            { text: 'Поля manifest и схемы', link: '/reference/manifest' },
            { text: 'Контракт фич и manifest', link: '/reference/feature-runtime' },
            { text: 'CLI', link: '/reference/cli' },
          ],
        },
      ],
    },
  },
})
