// Only this worker imports the bundled compiler. No remote loader/imports.
let loading: Promise<typeof import('../utils/solc-browser')> | undefined
self.onmessage = async ({ data }: MessageEvent<{ id: number; input: string }>) => {
  if (!Number.isSafeInteger(data?.id) || typeof data.input !== 'string') return
  try {
    loading ??= import('../utils/solc-browser')
    const { default: compiler } = await loading
    const version = compiler.version()
    const output = compiler.compile(data.input)
    self.postMessage({ id: data.id, version, output })
  } catch (error) {
    self.postMessage({ id: data.id, error: error instanceof Error ? error.message : 'Solidity compiler failed' })
  }
}
