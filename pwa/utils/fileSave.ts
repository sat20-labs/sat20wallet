import type { ExportFile } from './walletViews'

export function downloadFile(filename: string, blob: Blob): string {
  const name = filename.replace(/[<>:"/\\|?*\u0000-\u001f]/g, '-')
  const url = URL.createObjectURL(blob)
  try {
    const link = document.createElement('a')
    link.href = url
    link.download = name
    link.click()
  } finally {
    // Let the browser start reading the Blob before revoking its URL.
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return name
}

export function downloadExportFile(file: ExportFile): string {
  if (!file.data) throw new Error('Export file is empty')
  const bytes = Uint8Array.from(atob(file.data), ch => ch.charCodeAt(0))
  return downloadFile(file.name, new Blob([bytes], { type: file.mime }))
}
