export type MarketFrameState = 'loading' | 'ready' | 'error' | 'timeout'
// Each parent navigation replaces the iframe; bind its new WindowProxy only.
export class MarketFrameReady {
  private generation = 0
  private source: MessageEventSource | null = null
  private origin = ''
  private timer: ReturnType<typeof setTimeout> | undefined
  constructor(private changed: (state: MarketFrameState) => void, private timeoutMs = 30_000) {}
  private clear() { if (this.timer !== undefined) clearTimeout(this.timer); this.timer = undefined }
  begin(url: string) {
    this.clear()
    const generation = ++this.generation
    this.source = null
    this.origin = new URL(url).origin
    this.changed('loading')
    this.timer = setTimeout(() => { if (generation === this.generation) this.changed('timeout') }, this.timeoutMs)
    return generation
  }
  bind(generation: number, source: MessageEventSource | null) {
    if (generation === this.generation) this.source = source
  }
  matchesSource(source: MessageEventSource | null) { return this.source !== null && source === this.source }
  ready(event: MessageEvent, allowed: (origin: string) => boolean): boolean {
    const data = event.data
    if (!this.source || event.source !== this.source || event.origin !== this.origin || !allowed(event.origin)
      || data?.protocol !== 'sat20-dapp-connect' || data.type !== 'SAT20_DAPP_CLIENT_READY'
      || (data.origin !== undefined && data.origin !== event.origin) || typeof data.href !== 'string') return false
    try { if (new URL(data.href).origin !== event.origin) return false } catch { return false }
    this.clear()
    this.changed('ready')
    return true
  }
  error(generation: number) {
    if (generation !== this.generation) return
    this.clear()
    this.changed('error')
  }
  dispose() { this.clear(); ++this.generation; this.source = null }
}
