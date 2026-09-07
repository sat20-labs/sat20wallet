const DECIMAL_INTEGER = /^(0|[1-9][0-9]*)$/

// Asset amounts are human decimal strings, not counts of indivisible sats.
// Fractional JS numbers are rejected so callers cannot silently lose precision.
export const toAssetAmountString = (value: unknown, field: string): string => {
  if (typeof value === 'string' && /^(0|[1-9][0-9]*)(\.[0-9]+)?$/.test(value)) return value
  return toDecimalString(value, field)
}

export const toDecimalString = (value: unknown, field: string): string => {
  if (typeof value === 'bigint') {
    if (value < 0n) throw new Error(`${field} must be non-negative`)
    return value.toString(10)
  }
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value) || value < 0) throw new Error(`${field} must be a non-negative safe integer`)
    return String(value)
  }
  if (typeof value !== 'string' || !DECIMAL_INTEGER.test(value)) throw new Error(`${field} must be a decimal integer string`)
  return value
}

export const toBoundedNumber = (value: unknown, field: string, maximum: number): number => {
  if (!Number.isSafeInteger(maximum) || maximum < 0) throw new Error('Invalid integer bound')
  const parsed = BigInt(toDecimalString(value, field))
  if (parsed > BigInt(maximum)) throw new Error(`${field} exceeds ${maximum}`)
  return Number(parsed)
}
