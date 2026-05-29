# SAT20 PWA Wallet Completion Tracker

Updated: 2026-05-29

## Current Status

- PWA module exists at `sat20wallet/pwa` and builds successfully.
- Market support is in `market_satsnet` commit `9d9fd7a` (`support pwa wallet`).
- Production PWA URL is planned as `https://sat20.org/pwa/`.
- `sat20-website` is the deployment repo for `sat20.org`; its GitHub Actions workflow deploys `./dist/` to `/var/www/sat20.org/`.
- PWA serves `manifest.webmanifest` and `service-worker.js` successfully in local dev.
- Market `/market/` serves successfully in local dev and does not emit local `X-Frame-Options` or `frame-ancestors` headers.
- PWA has an embedded Market route at `#/wallet/dapp`.
- PWA DApp bridge validates origin, request id, nonce, expiry, duplicate requests, and requires connect before other actions.
- Current PWA build is configured for `/pwa/` in production and `/` in local dev:
  - Vite base defaults to `/` for `npm run dev`.
  - Vite base defaults to `/pwa/` for `npm run build`.
  - Manifest uses relative `id/start_url/scope` and icon URLs so it works at both `/` and `/pwa/`.
  - Service worker registration, WASM loading, and app shell cache paths use `import.meta.env.BASE_URL`.

## Verified Locally

- `npm run compile` in `sat20wallet/pwa`: pass.
- `npm run build` in `sat20wallet/pwa`: pass.
- `npm run build` in `market_satsnet`: pass.
- Local dev servers:
  - Market: `http://localhost:3006`
  - PWA: `http://127.0.0.1:5173`
- `sat20-website` repository:
  - Remote: `git@github.com:sat20-labs/sat20-website.git`.
  - Deployment workflow target: `/var/www/sat20.org/`.
  - Workflow currently deploys committed/generated `dist` directly and does not run `npm run build`.
- `sat20-website` build/sync:
  - Added `sync:pwa` to copy `sat20wallet/pwa/dist` into `sat20-website/dist/pwa`.
  - Local preview of `sat20-website/dist` returns 200 for `/`, `/pwa/`, `/pwa/manifest.webmanifest`, `/pwa/service-worker.js`, and `/pwa/wasm/sat20wallet.wasm`.

## Open Tasks

- Validate embedded Market connect flow in a real browser:
  - Open PWA, unlock/import wallet, go to Market.
  - Confirm Market detects `pwa-embedded` provider and shows connected wallet state.
  - Confirm account and network changes propagate.
- Re-run automated verification after fixing the current CDP hang in `verify:wallet-basics`.
- Run focused transaction regression:
  - Sell order.
  - Buy/take order.
  - PSBT sign/split/merge/finalize/extract.
  - UTXO lock/unlock.
  - Swap, launchpool, DAO, limit order, transcend contract invoke paths.
- Configure production embedding headers at the deployment layer:
  - Allow SAT20 PWA wallet origin `https://sat20.org` in Market `frame-ancestors`.
  - Avoid `X-Frame-Options: DENY` or `SAMEORIGIN` on Market pages used inside PWA.
  - Confirm CSP works for production and test Market domains.
- Validate PWA install prompt on real devices:
  - Homepage keeps the Google Play wallet button.
  - Homepage removes the Chrome extension wallet download button.
  - Homepage adds `Install PWA Wallet`, linking to `/pwa/?install=1`.
  - PWA handles `?install=1` by triggering the browser install prompt when available, with add-to-home-screen fallback guidance.
- Mobile device validation:
  - Android Chrome install to home screen.
  - iOS Safari add to home screen.
  - Standalone launch, offline launch, IndexedDB persistence, WASM cache, Market iframe, back/reload behavior.
- Security review:
  - Confirm every approval page displays origin, action, and key parameters clearly.
  - Confirm rejected, expired, duplicate, and origin-mismatch requests never return success.
- Storage hardening:
  - Current wallet state and authorization storage uses IndexedDB, but there is no explicit TS-layer encryption wrapper.
  - Confirm whether WASM wallet internals encrypt seed/private material; add explicit documentation or encryption if not.

## Needs User Assistance

- Confirm the production deployment mechanism for publishing `sat20wallet/pwa/dist` to `/var/www/sat20.org/pwa/`.
- Run or allow running mobile-device checks on Android and iOS devices.
- Confirm whether external browser fallback/relay remains required for v1 or can stay post-MVP.
