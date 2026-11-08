import en from './locales/en.json'
import zh from './locales/zh.json'

const lockName = 'sat20-wallet-manager-v1'
const root = document.getElementById('app')
const locale = (navigator.language || 'en').toLowerCase().startsWith('zh') ? 'zh' : 'en'
const copy = (locale === 'zh' ? zh : en).pwaUi.bootstrap

let ownsLock = false
let statusNode: HTMLElement | undefined
let runChan: BroadcastChannel | undefined

const holdLock = () => new Promise(() => {})

const renderShell = (titleText: string, message: string, showFocus: boolean, showRetry: boolean, state: 'waiting' | 'error') => {
  if (!root) return
  const shell = document.createElement('main')
  shell.dataset.testid = 'wallet-startup-shell'
  shell.dataset.state = state
  shell.style.cssText = 'min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:16px;background:#09090b;color:#f4f4f5;padding:24px;font-family:system-ui,sans-serif;text-align:center;'
  const heading = document.createElement('h1')
  heading.dataset.testid = 'wallet-startup-title'
  heading.textContent = titleText
  statusNode = document.createElement('p')
  statusNode.dataset.testid = 'wallet-startup-status'
  statusNode.dataset.state = state
  statusNode.setAttribute('role', 'status')
  statusNode.textContent = message
  shell.append(heading, statusNode)
  if (showFocus) {
    const button = document.createElement('button')
    button.type = 'button'
    button.dataset.testid = 'wallet-focus-existing'
    button.disabled = !runChan
    button.textContent = copy.focus
    button.style.cssText = 'padding:10px 20px;border:1px solid currentColor;border-radius:6px;cursor:pointer;'
    button.addEventListener('click', () => {
      runChan?.postMessage({ type: 'focus' })
      if (statusNode) {
        statusNode.dataset.state = 'focus-requested'
        statusNode.textContent = copy.focusRequested
      }
    })
    shell.append(button)
  }
  if (showRetry) {
    const button = document.createElement('button')
    button.type = 'button'
    button.dataset.testid = 'wallet-retry-startup'
    button.textContent = copy.retry
    button.style.cssText = 'padding:10px 20px;border:1px solid currentColor;border-radius:6px;cursor:pointer;'
    button.addEventListener('click', () => window.location.reload())
    shell.append(button)
  }
  root.replaceChildren(shell)
}

try {
  if (typeof BroadcastChannel === 'function') runChan = new BroadcastChannel('sat20-wallet-run-v1')
} catch {
  runChan = undefined
}

runChan?.addEventListener('message', event => {
  const data = event.data
  if (ownsLock && data?.type === 'focus') {
    try { window.focus() } catch { /* Focus is controlled by the browser. */ }
  }
})

window.addEventListener('pageshow', event => {
  if ((event as PageTransitionEvent).persisted) {
    document.documentElement.inert = true
    window.location.reload()
  }
})

const lockManager = (navigator as Navigator & { locks?: LockManager }).locks
if (!lockManager) {
  renderShell(copy.unsupportedTitle, copy.unsupportedMessage, false, true, 'error')
} else {
  renderShell(copy.busyTitle, copy.busyMessage, true, true, 'waiting')
  void lockManager.request(lockName, { mode: 'exclusive' }, async lock => {
    if (!lock) {
      renderShell(copy.lockErrorTitle, copy.lockErrorMessage, false, true, 'error')
      return
    }
    ownsLock = true
    try {
      await import('./main')
    } catch (error) {
      console.error('Wallet entry failed while holding the instance lock:', error)
      try { renderShell(copy.lockErrorTitle, copy.startErrorMessage, false, true, 'error') } catch {}
    }
    await holdLock()
  }).catch(error => {
    console.error('Wallet instance lock request failed:', error)
    try { renderShell(copy.lockErrorTitle, copy.lockErrorMessage, false, true, 'error') } catch {}
  })
}
