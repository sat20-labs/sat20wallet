export interface CompilerResult { version: string; output: string }
export type CompilerWorker = Pick<Worker, 'postMessage' | 'terminate' | 'onmessage' | 'onerror' | 'onmessageerror'>

// The factory is lazy and injectable for lifecycle tests; no main-thread solc.
export class SolcWorkerClient {
  private worker: CompilerWorker | null = null
  private sequence = 0
  private pending: { id: number; resolve: (value: CompilerResult) => void; reject: (error: Error) => void; timer: ReturnType<typeof setTimeout> } | null = null
  constructor(private createWorker: () => CompilerWorker, private timeoutMs = 120_000) {}
  private stop(error: Error) {
    const pending = this.pending
    this.pending = null
    if (pending) { clearTimeout(pending.timer); pending.reject(error) }
    if (this.worker) {
      this.worker.onmessage = this.worker.onerror = this.worker.onmessageerror = null
      this.worker.terminate()
      this.worker = null
    }
  }
  compile(input: string): Promise<CompilerResult> {
    if (this.pending) return Promise.reject(new Error('A Solidity compilation is already running'))
    return new Promise((resolve, reject) => {
      const id = ++this.sequence
      this.pending = { id, resolve, reject, timer: setTimeout(() => this.stop(new Error('Solidity compiler timed out')), this.timeoutMs) }
      try {
        if (!this.worker) {
          const worker = this.createWorker()
          this.worker = worker
          worker.onerror = () => { if (this.worker === worker) this.stop(new Error('Solidity compiler worker failed')) }
          worker.onmessageerror = () => { if (this.worker === worker) this.stop(new Error('Solidity compiler response could not be read')) }
          worker.onmessage = ({ data }) => {
            if (this.worker !== worker || !this.pending || data?.id !== this.pending.id) return
            if (typeof data.error === 'string') { this.stop(new Error(data.error)); return }
            if (typeof data.version !== 'string' || !data.version || typeof data.output !== 'string') {
              this.stop(new Error('Invalid Solidity compiler response')); return
            }
            const pending = this.pending
            this.pending = null
            clearTimeout(pending.timer)
            pending.resolve({ version: data.version, output: data.output })
          }
        }
        this.worker.postMessage({ id, input })
      } catch { this.stop(new Error('Solidity compiler worker could not start')) }
    })
  }
  dispose() { this.stop(new Error('Solidity compilation cancelled')) }
}
