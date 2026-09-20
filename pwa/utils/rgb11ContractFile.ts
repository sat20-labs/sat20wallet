const contractMagic = [0x52, 0x47, 0x42, 0x00, 0x43, 0x4f, 0x4e]

export function downloadRGB11ContractFile(fileBase64: string, contractId: string): void {
  if (!fileBase64) throw new Error('RGB11 contract file is empty')
  const bytes = Uint8Array.from(atob(fileBase64), value => value.charCodeAt(0))
  if (bytes.length < contractMagic.length || contractMagic.some((value, index) => bytes[index] !== value)) {
    throw new Error('RGB11 contract file does not use the standard RGB\\0CON envelope')
  }
  const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }))
  try {
    const link = document.createElement('a')
    const id = contractId.replace(/[^A-Za-z0-9_-]/g, '').slice(0, 24) || 'contract'
    link.href = url
    link.download = `rgb11-contract-${id}.rgb`
    link.click()
  } finally {
    URL.revokeObjectURL(url)
  }
}
