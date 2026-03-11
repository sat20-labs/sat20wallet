/**
 * Setup localStorage polyfill for Node.js build environment
 * Run this before the build process
 */

if (typeof localStorage === 'undefined') {
  const localStoragePolyfill = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {},
    key: () => null,
    length: 0,
  }

  globalThis.localStorage = localStoragePolyfill
  global.localStorage = localStoragePolyfill

  if (typeof window === 'undefined') {
    globalThis.window = {}
  }
  globalThis.window.localStorage = localStoragePolyfill

  console.log('[Polyfill] localStorage injected for Node.js environment')
}

if (typeof sessionStorage === 'undefined') {
  const sessionStoragePolyfill = {
    getItem: () => null,
    setItem: () => {},
    removeItem: () => {},
    clear: () => {},
    key: () => null,
    length: 0,
  }

  globalThis.sessionStorage = sessionStoragePolyfill
  global.sessionStorage = sessionStoragePolyfill

  if (typeof window !== 'undefined') {
    globalThis.window.sessionStorage = sessionStoragePolyfill
  }

  console.log('[Polyfill] sessionStorage injected for Node.js environment')
}
