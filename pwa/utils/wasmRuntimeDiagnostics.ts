export type WasmRuntimeBreadcrumb = {
  method: string
  phase: string
  at: number
}

const maxBreadcrumbs = 32
const breadcrumbs: WasmRuntimeBreadcrumb[] = []

const safeToken = (value: string) =>
  String(value || '').replace(/[^A-Za-z0-9_.:-]/g, '_').slice(0, 96)

export const noteWasmOperation = (method: string, phase: string) => {
  breadcrumbs.push({
    method: safeToken(method),
    phase: safeToken(phase),
    at: Date.now(),
  })
  if (breadcrumbs.length > maxBreadcrumbs) {
    breadcrumbs.splice(0, breadcrumbs.length - maxBreadcrumbs)
  }
}

export const snapshotWasmRuntimeDiagnostics = () =>
  breadcrumbs.map(item => ({ ...item }))

export const resetWasmRuntimeDiagnostics = () => {
  breadcrumbs.splice(0, breadcrumbs.length)
}
