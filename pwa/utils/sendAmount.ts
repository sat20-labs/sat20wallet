export type SendAmountError = 'required' | 'decimal' | 'precision' | 'positive' | 'balance' | 'unavailable' | 'scope'

// Keep the user text exact: neither JS Number nor exponent notation is used.
export function validateSendAmount(text: string, precision: number | undefined, balance: unknown): SendAmountError | null {
  if (!text) return 'required'
  if (text.length > 256 || !/^\d+(?:\.\d+)?$/.test(text)) return 'decimal'
  if (!Number.isInteger(precision) || precision! < 0 || precision! > 64) return 'unavailable'
  const parse = (value: string): bigint | null => {
    if (value.length > 256 || !/^\d+(?:\.\d+)?$/.test(value)) return null
    const [whole, fraction = ''] = value.split('.')
    if (fraction.length > precision!) return null
    return BigInt(whole + fraction.padEnd(precision!, '0'))
  }
  const amount = parse(text)
  if (amount === null) return 'precision'
  if (amount <= 0n) return 'positive'
  // A legacy numeric balance outside exact integer range is not trusted.
  if (typeof balance === 'number' && !Number.isSafeInteger(balance)) return 'unavailable'
  const available = parse(String(balance ?? ''))
  if (available === null) return 'unavailable'
  return amount > available ? 'balance' : null
}

export function validateSendDispatch(text: string, precision: number | undefined, balance: unknown, openedScope: string, currentScope: string): SendAmountError | null {
  if (!openedScope || openedScope !== currentScope) return 'scope'
  return validateSendAmount(text, precision, balance)
}
