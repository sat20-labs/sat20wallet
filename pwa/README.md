# SAT20 PWA Wallet

This project is the browser-installable wallet shell derived from `sat20wallet/app`.

## Scope

- Runs as a standalone PWA on Android Chrome and iOS Safari.
- Keeps wallet state and DApp authorization in IndexedDB through `lib/storage-adapter.ts`.
- Registers `public/service-worker.js` to precache the wallet shell, WASM files, icons, manifest, and version file.
- Opens SAT20 Market at `https://satsnet.ordx.market` inside `/wallet/dapp`.
- Handles embedded Market requests through the SAT20 DApp Connect `postMessage` bridge.

## DApp Connect Envelope

Market sends requests to the parent wallet frame:

```ts
{
  type: 'SAT20_DAPP_REQUEST',
  protocol: 'sat20-dapp-connect',
  requestId: string,
  origin: string,
  action: string,
  params: object | unknown[],
  network: string,
  nonce: string,
  expiresAt: number
}
```

Wallet replies:

```ts
{
  type: 'SAT20_DAPP_RESPONSE',
  protocol: 'sat20-dapp-connect',
  requestId: string,
  success: boolean,
  result?: unknown,
  error?: { code: string, message: string }
}
```

The wallet accepts only configured DApp origins, rejects expired or duplicate requests, and requires `requestAccounts` before other actions.

## Environment

- `VITE_SAT20_MARKET_URL`: optional Market URL override.
- `VITE_SAT20_DAPP_ALLOWED_ORIGINS`: comma-separated allowed origins. Defaults include production/test Market origins and local dev origins.
- `VITE_CSP_FRAME_SRC`: additional HTTPS frame origins for a production build.
- `VITE_CSP_CONNECT_SRC`: additional HTTPS/WSS backend origins for a production build.

Production CSP rejects localhost and insecure origins. Development remains a separate Vite policy.

For local Market validation, point the wallet at the Market route, not the Next.js root:

```bash
VITE_SAT20_MARKET_URL=http://localhost:3006/swap/ npm run dev
```

## Commands

```bash
npm run dev
npm run compile
npm run build:skip-check
```

Production build profiles write durable `RUNNING`/`PASSED`/`FAILED` state and a
full log under `.build-status/`. Set `SAT20_PWA_BUILD_STATUS_DIR` to store these
artifacts in a supervisor-managed directory. `write-version` and the WASM
integrity manifest generator always run before bundling.
