export type CodedWasmError = Error & { code?: string }

export const wasmResponseError = (message: string, errorCode?: unknown): CodedWasmError => {
  const error = new Error(message) as CodedWasmError
  if (typeof errorCode === 'string' && errorCode) error.code = errorCode
  return error
}

export const preserveWasmError = (value: unknown, fallbackMessage: string): CodedWasmError => {
  const source = value as { message?: unknown; error_code?: unknown; code?: unknown } | null
  const message = typeof source?.message === 'string' && source.message
    ? source.message
    : fallbackMessage
  const code = typeof source?.error_code === 'string' ? source.error_code : source?.code
  return wasmResponseError(message, code)
}
