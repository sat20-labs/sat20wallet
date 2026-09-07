import walletManager from '@/utils/sat20'
import { getConfig, logLevel } from '@/config/wasm'
import { Network } from '@/types'
import { walletStorage } from '@/lib/walletStorage'
import { WASM_INTEGRITY_MANIFEST } from '@/generated/wasm-integrity'

type IntegrityAsset = (typeof WASM_INTEGRITY_MANIFEST.assets)[number]

const runtimeError = (message: string, cause?: unknown) => {
  const error = new Error(message)
  if (cause !== undefined) error.cause = cause
  return error
}

const sha256Hex = async (bytes: ArrayBuffer) => {
  if (!globalThis.crypto?.subtle) {
    throw runtimeError('Web Crypto is unavailable; wallet runtime integrity cannot be verified')
  }
  const digest = await crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

const assetUrl = (asset: IntegrityAsset) => {
  const url = new URL(`${import.meta.env.BASE_URL}${asset.path}`, window.location.origin)
  url.searchParams.set('release', WASM_INTEGRITY_MANIFEST.releaseId)
  if (import.meta.env.DEV) url.searchParams.set('dev', String(Date.now()))
  return url.toString()
}

const fetchVerifiedAsset = async (asset: IntegrityAsset) => {
  const response = await fetch(assetUrl(asset), { cache: 'no-store' })
  if (!response.ok) {
    throw runtimeError(`${asset.role} returned HTTP ${response.status}`)
  }

  const mime = (response.headers.get('content-type') || '').split(';', 1)[0].trim().toLowerCase()
  if (!(asset.mimeTypes as readonly string[]).includes(mime)) {
    throw runtimeError(`${asset.role} returned unexpected MIME type ${mime || '(missing)'}`)
  }

  const bytes = await response.arrayBuffer()
  if (bytes.byteLength !== asset.size) {
    throw runtimeError(`${asset.role} size mismatch`)
  }
  if (await sha256Hex(bytes) !== asset.sha256) {
    throw runtimeError(`${asset.role} SHA-256 mismatch`)
  }
  return bytes
}

const loadVerifiedGoRuntime = async () => {
  const asset = WASM_INTEGRITY_MANIFEST.assets.find((item) => item.role === 'go-runtime')
  if (!asset) throw runtimeError('Go runtime is missing from the integrity manifest')
  const bytes = await fetchVerifiedAsset(asset)
  const blobUrl = URL.createObjectURL(new Blob([bytes], { type: 'text/javascript' }))
  try {
    await new Promise<void>((resolve, reject) => {
      const script = document.createElement('script')
      script.src = blobUrl
      script.onload = () => {
        script.remove()
        resolve()
      }
      script.onerror = () => {
        script.remove()
        reject(runtimeError('Verified Go runtime could not be executed'))
      }
      document.head.appendChild(script)
    })
  } finally {
    URL.revokeObjectURL(blobUrl)
  }
  if (typeof Go !== 'function') {
    throw runtimeError('Verified Go runtime did not register window.Go')
  }
}

const getRuntimeConfig = async () => {
  await walletStorage.initializeState()
  if (walletStorage.getValue('env') !== 'prd') {
    await walletStorage.setValue('env', 'prd')
  }
  const network = walletStorage.getValue('network') || Network.MAINNET
  return getConfig('prd', network)
}

const instantiateGoWasm = async () => {
  if (`${__SAT20_APP_VERSION__}-${__SAT20_BUILD_ID__}` !== WASM_INTEGRITY_MANIFEST.releaseId) {
    throw runtimeError('Wallet JavaScript and WASM release metadata do not match')
  }

  await loadVerifiedGoRuntime()
  const asset = WASM_INTEGRITY_MANIFEST.assets.find((item) => item.role === 'wallet-wasm')
  if (!asset) throw runtimeError('Wallet WASM is missing from the integrity manifest')
  const bytes = await fetchVerifiedAsset(asset)
  const go = new Go()
  let wasmModule: WebAssembly.WebAssemblyInstantiatedSource
  try {
    wasmModule = await WebAssembly.instantiate(bytes, go.importObject)
  } catch (error) {
    throw runtimeError('Wallet WASM instantiation failed', error)
  }

  const runtimeFailure = Promise.resolve()
    .then(() => go.run(wasmModule.instance))
    .then(
      () => Promise.reject(runtimeError('Wallet WASM runtime exited unexpectedly')),
      (error) => Promise.reject(runtimeError('Wallet WASM runtime failed', error))
    )

  void runtimeFailure.catch((error) => {
    window.dispatchEvent(new CustomEvent('sat20:wasm-runtime-error', { detail: error }))
  })
  return { runtimeFailure }
}

const loadWalletWasm = async () => {
  const config = await getRuntimeConfig()
  const { runtimeFailure } = await instantiateGoWasm()
  const [initError] = await Promise.race([
    walletManager.init(config, logLevel),
    runtimeFailure,
  ])
  if (initError) throw runtimeError('Wallet WASM initialization failed', initError)
}

export const loadWasm = async () => {
  await loadWalletWasm()
}
