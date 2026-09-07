export interface DappRequestGuardOptions {
  maxActive?: number; maxActivePerOrigin?: number; maxHandled?: number; handledTtlMs?: number
  rateWindowMs?: number; maxRequestsPerWindow?: number; maxRequestLifetimeMs?: number; now?: () => number
}

export class DappRequestGuard {
  private readonly activeByOrigin = new Map<string, number>()
  private readonly handled = new Map<string, number>()
  private readonly rateHistory = new Map<string, number[]>()
  private active = 0
  private readonly options: Required<DappRequestGuardOptions>

  constructor(options: DappRequestGuardOptions = {}) {
    this.options = {
      maxActive: options.maxActive ?? 16, maxActivePerOrigin: options.maxActivePerOrigin ?? 4,
      maxHandled: options.maxHandled ?? 256, handledTtlMs: options.handledTtlMs ?? 5 * 60_000,
      rateWindowMs: options.rateWindowMs ?? 10_000, maxRequestsPerWindow: options.maxRequestsPerWindow ?? 30,
      maxRequestLifetimeMs: options.maxRequestLifetimeMs ?? 5 * 60_000, now: options.now ?? Date.now,
    }
  }

  begin(origin: string, requestId: string, nonce: string, expiresAt: number) {
    const now = this.options.now()
    if (!Number.isSafeInteger(expiresAt) || expiresAt <= now || expiresAt > now + this.options.maxRequestLifetimeMs) {
      throw new Error('SAT20 DApp request expiry is outside the allowed window')
    }
    this.prune(now)
    const key = `${origin}:${requestId}:${nonce}`
    if (this.handled.has(key)) throw new Error('Duplicate SAT20 DApp request')
    if (this.active >= this.options.maxActive || (this.activeByOrigin.get(origin) ?? 0) >= this.options.maxActivePerOrigin) {
      throw new Error('SAT20 DApp request queue is full')
    }
    const recent = (this.rateHistory.get(origin) ?? []).filter((value) => value > now - this.options.rateWindowMs)
    if (recent.length >= this.options.maxRequestsPerWindow) throw new Error('SAT20 DApp request rate limit exceeded')
    recent.push(now)
    this.rateHistory.set(origin, recent)
    this.handled.set(key, Math.min(expiresAt, now + this.options.handledTtlMs))
    while (this.handled.size > this.options.maxHandled) {
      const oldest = this.handled.keys().next().value
      if (oldest === undefined) break
      this.handled.delete(oldest)
    }
    this.active += 1
    this.activeByOrigin.set(origin, (this.activeByOrigin.get(origin) ?? 0) + 1)
    let finished = false
    return () => {
      if (finished) return
      finished = true
      this.active = Math.max(0, this.active - 1)
      const count = Math.max(0, (this.activeByOrigin.get(origin) ?? 1) - 1)
      if (count === 0) this.activeByOrigin.delete(origin)
      else this.activeByOrigin.set(origin, count)
    }
  }

  private prune(now: number) {
    for (const [key, expiry] of this.handled) if (expiry <= now) this.handled.delete(key)
    for (const [origin, values] of this.rateHistory) {
      const recent = values.filter((value) => value > now - this.options.rateWindowMs)
      if (recent.length) this.rateHistory.set(origin, recent)
      else this.rateHistory.delete(origin)
    }
  }
}

