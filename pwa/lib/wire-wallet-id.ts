// STP's JSON wire identity is an int64 number; the selected PWA wallet ID is
// a string. Only exactly representable, nonnegative JSON integers may cross
// this boundary. Never coerce a string or round an unsafe number.
export const wireWalletIdToStoreId = (value: unknown): string | null => {
  if (typeof value !== 'number' || !Number.isSafeInteger(value) || value < 0 || Object.is(value, -0)) return null
  return String(value)
}
