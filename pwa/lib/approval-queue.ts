export interface ApprovalQueueEntry<T> {
  id: string
  origin: string
  createdAt: number
  expiresAt: number
  request: T
  resolve: (result: unknown) => void
  reject: (error: Error) => void
}

export interface ApprovalQueueOptions {
  maxSize?: number
  maxPerOrigin?: number
  ttlMs?: number
  maxTtlMs?: number
  rateWindowMs?: number
  maxRequestsPerWindow?: number
  now?: () => number
  onChange?: () => void
}

export class BoundedApprovalQueue<T> {
  private readonly entries: ApprovalQueueEntry<T>[] = []
  private readonly rateHistory = new Map<string, number[]>()
  private readonly options: Required<ApprovalQueueOptions>
  private timer: ReturnType<typeof setTimeout> | null = null

  constructor(options: ApprovalQueueOptions = {}) {
    this.options = {
      maxSize: options.maxSize ?? 16,
      maxPerOrigin: options.maxPerOrigin ?? 4,
      ttlMs: options.ttlMs ?? 2 * 60_000,
      maxTtlMs: options.maxTtlMs ?? 5 * 60_000,
      rateWindowMs: options.rateWindowMs ?? 10_000,
      maxRequestsPerWindow: options.maxRequestsPerWindow ?? 8,
      now: options.now ?? Date.now,
      onChange: options.onChange ?? (() => undefined),
    }
  }

  get current(): ApprovalQueueEntry<T> | null {
    this.expire()
    return this.entries[0] ?? null
  }

  get size(): number {
    this.expire()
    return this.entries.length
  }

  enqueue(id: string, origin: string, request: T, requestedExpiresAt?: number): Promise<unknown> {
    const now = this.options.now()
    this.expire(now)
    if (!id || this.entries.some((entry) => entry.id === id)) {
      return Promise.reject(new Error('Duplicate approval request id'))
    }
    if (this.entries.length >= this.options.maxSize ||
      this.entries.filter((entry) => entry.origin === origin).length >= this.options.maxPerOrigin) {
      return Promise.reject(new Error('Wallet approval queue is full'))
    }
    const recent = (this.rateHistory.get(origin) ?? []).filter((value) => value > now - this.options.rateWindowMs)
    if (recent.length >= this.options.maxRequestsPerWindow) {
      return Promise.reject(new Error('Wallet approval rate limit exceeded'))
    }
    recent.push(now)
    this.rateHistory.set(origin, recent)

    const expiresAt = Math.min(
      requestedExpiresAt ?? now + this.options.ttlMs,
      now + this.options.maxTtlMs,
    )
    if (!Number.isSafeInteger(expiresAt) || expiresAt <= now) {
      return Promise.reject(new Error('Wallet approval request expired'))
    }

    const result = new Promise<unknown>((resolve, reject) => {
      this.entries.push({ id, origin, createdAt: now, expiresAt, request, resolve, reject })
    })
    this.changed()
    return result
  }

  confirm(id: string, result: unknown): void {
    const entry = this.takeCurrent(id)
    entry.resolve(result)
  }

  // Approved identity changes own their result after leaving the pending queue.
  // Cancelling pending approvals during the change must not cancel this result.
  execute(id: string, action: () => Promise<unknown>): void {
    const entry = this.takeCurrent(id)
    Promise.resolve().then(action).then(entry.resolve, entry.reject)
  }

  reject(id: string, error = new Error('User rejected')): void {
    const entry = this.takeCurrent(id)
    entry.reject(error)
  }

  rejectAll(error = new Error('Wallet approval requests are no longer valid')): void {
    if (!this.entries.length) return
    const entries = this.entries.splice(0)
    for (const entry of entries) entry.reject(error)
    this.changed()
  }

  expire(now = this.options.now()): void {
    let changed = false
    while (this.entries[0] && this.entries[0].expiresAt <= now) {
      this.entries.shift()!.reject(new Error('Wallet approval request expired'))
      changed = true
    }
    for (const [origin, values] of this.rateHistory) {
      const recent = values.filter((value) => value > now - this.options.rateWindowMs)
      if (recent.length) this.rateHistory.set(origin, recent)
      else this.rateHistory.delete(origin)
    }
    if (changed) this.changed()
  }

  private takeCurrent(id: string): ApprovalQueueEntry<T> {
    this.expire()
    const entry = this.entries[0]
    if (!entry || entry.id !== id) throw new Error('Approval request id does not match the visible request')
    this.entries.shift()
    this.changed()
    return entry
  }

  private changed(): void {
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    if (this.entries[0]) {
      const delay = Math.max(0, this.entries[0].expiresAt - this.options.now())
      this.timer = setTimeout(() => this.expire(), delay)
      ;(this.timer as any).unref?.()
    }
    this.options.onChange()
  }
}
