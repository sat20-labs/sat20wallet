export class CredentialAttemptLimiter {
  private failures: number[] = []
  private blockedUntil = 0

  constructor(
    private readonly maximumAttempts = 5,
    private readonly windowMs = 60_000,
    private readonly lockoutMs = 30_000,
    private readonly now = Date.now,
  ) {}

  assertAllowed() {
    const current = this.now()
    this.failures = this.failures.filter((attempt) => attempt > current - this.windowMs)
    if (current < this.blockedUntil) throw new Error('Too many password attempts; try again later')
  }

  recordFailure() {
    const current = this.now()
    this.failures = this.failures.filter((attempt) => attempt > current - this.windowMs)
    this.failures.push(current)
    if (this.failures.length >= this.maximumAttempts) this.blockedUntil = current + this.lockoutMs
  }

  reset() {
    this.failures = []
    this.blockedUntil = 0
  }
}

