const DEBUG_STORAGE_KEY = 'sat20:pwa:debug'

const hasDebugQuery = () => {
  try {
    return new URLSearchParams(window.location.search).get('debug') === '1'
  } catch {
    return false
  }
}

export const isDebugEnabled = () => {
  return import.meta.env.DEV ||
    hasDebugQuery() ||
    localStorage.getItem(DEBUG_STORAGE_KEY) === '1'
}

export const installDebugConsole = () => {
  if (isDebugEnabled()) {
    return
  }

  console.log = () => {}
  console.debug = () => {}
  console.info = () => {}
}

installDebugConsole()
