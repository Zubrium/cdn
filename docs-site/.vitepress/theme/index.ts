import DefaultTheme from 'vitepress/theme'
import { nextTick } from 'vue'
import './custom.css'

let renderSequence = 0
let renderQueue = Promise.resolve()

function scheduleDiagramRender() {
  const sequence = ++renderSequence
  renderQueue = renderQueue.catch(() => undefined).then(() => renderDiagrams(sequence))
  return renderQueue
}

async function renderDiagrams(sequence: number) {
  await nextTick()
  if (typeof document === 'undefined' || sequence !== renderSequence) return

  const { default: mermaid } = await import('mermaid')
  if (sequence !== renderSequence) return
  mermaid.initialize({
    startOnLoad: false,
    securityLevel: 'strict',
    theme: document.documentElement.classList.contains('dark') ? 'dark' : 'default',
    fontFamily: 'inherit',
  })
  await mermaid.run({ querySelector: '.vp-doc pre.mermaid:not([data-processed])' })
}

export default {
  extends: DefaultTheme,
  enhanceApp(context) {
    if (typeof window === 'undefined') return
    DefaultTheme.enhanceApp?.(context)
    void scheduleDiagramRender()
    context.router.onAfterRouteChange = () => void scheduleDiagramRender()
  },
}
