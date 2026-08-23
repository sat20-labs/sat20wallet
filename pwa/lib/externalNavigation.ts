type ExternalBridgeOpen = (url: string, target: string, features: string) => unknown

const parseExternalURL = (url: string): string => {
  const parsed = new URL(url)
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error(`Unsupported external URL protocol: ${parsed.protocol}`)
  }
  return parsed.href
}

const getExternalBridge = (): ExternalBridgeOpen | undefined => {
  if (typeof window === 'undefined') return undefined
  const bridge = (window as Window & {
    cordova?: { InAppBrowser?: { open?: ExternalBridgeOpen } }
  }).cordova?.InAppBrowser?.open
  return typeof bridge === 'function' ? bridge : undefined
}

export function openExternalWindow(
  openWindow: (url: string, target: string, features: string) => unknown,
  url: string,
  target: string,
  features: string,
	assignCurrent = (nextURL: string) => window.location.assign(nextURL),
	bridgeOpen: ExternalBridgeOpen | undefined = getExternalBridge(),
): void {
	const safeURL = parseExternalURL(url)
	if (bridgeOpen) {
		try {
			if (bridgeOpen(safeURL, target, features)) return
		} catch (error) {
			console.warn('External navigation bridge failed:', error)
		}
	}

	try {
		if (openWindow(safeURL, target, features)) return
	} catch (error) {
		console.warn('Opening an external window failed:', error)
	}
	assignCurrent(safeURL)
}
