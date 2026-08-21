# SAT20 PWA Verification Scripts

These scripts exercise the SAT20 PWA wallet against the test networks.

## Prerequisites

Start the PWA dev server, then launch a Chromium-based browser with remote debugging:

```bash
npm run dev
```

Example browser command:

```bash
"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge" \
  --headless=new \
  --remote-debugging-port=9223 \
  --user-data-dir=/private/tmp/sat20-pwa-verify-profile \
  --no-first-run \
  --disable-extensions \
  --disable-gpu \
  about:blank
```

## L1 Orderbook

The L1 script uses the built-in test mnemonic and password by default. Override them with:

- `SAT20_TEST_MNEMONIC`
- `SAT20_TEST_PASSWORD`
- `SAT20_CDP_URL`

The seller, buyer, and order size can be selected without editing the script:

```bash
SAT20_L1_SELLER_ACCOUNT_INDEX=1 \
SAT20_L1_BUYER_ACCOUNT_INDEX=0 \
SAT20_L1_SELL_AMOUNT=1000 \
npm run verify:l1-orderbook
```

Defaults remain seller account `0`, buyer account `1`, and `1000` DOGCOIN.
The seller needs the requested DOGCOIN amount in a spendable asset UTXO. With
the default sell amount, the buyer needs one plain BTC UTXO of at least `4900`
sats. In general the minimum source value is `3900 + sell amount` sats because
the fixed unit price is one sat per DOGCOIN. The script broadcasts on the real
testnet by default; set `SAT20_DRY_RUN=1` only for a no-broadcast preflight.

## Wallet Basics

The wallet basics script is a no-broadcast verification path. It imports/unlocks the built-in test wallet, sets production environment plus testnet network, reloads the PWA, checks account/subaccount reads, queries L1/L2 assets and UTXOs through wallet helpers and indexer APIs, signs/extracts a local L1 PSBT without broadcasting it, and validates DApp bridge success/error envelopes.

```bash
npm run verify:wallet-basics
```

Covered bridge cases:

- direct `getAccounts`, `getPublicKey`, `getNetwork`, `getAssetAmount`
- expired request rejection
- duplicate request rejection
- request-origin mismatch rejection
- user rejection propagation for an approval action

## Generic L1/L2 Send

The generic send script uses account 0 as the source and account 1 as the
destination. Real testnet broadcast is the default: it sends 1 sat on
SatoshiNet and 600 sats on Bitcoin testnet4, then verifies both raw
transactions.

```bash
npm run verify:generic-send
```

Use `SAT20_BATCH_SEND=1` to create two outputs on each chain. Use
`SAT20_DRY_RUN=1` for a no-broadcast preflight. Already-broadcast transactions
can be rechecked without spending again:

```bash
SAT20_EXISTING_L1_TXID=<txid> \
SAT20_EXISTING_L2_TXID=<txid> \
npm run verify:generic-send
```

Use the read-only network round trip and optional endpoint timing trace when
diagnosing network-switch latency:

```bash
SAT20_NETWORK_SWITCH_ONLY=1 \
SAT20_TRACE_NETWORK_SWITCH=1 \
npm run verify:generic-send
```

## Prediction Deploy

The prediction deploy script uses the current unified agent request format.
Dry-run estimate does not broadcast:

```bash
SAT20_DRY_RUN=1 node scripts/verify/prediction-deploy-flow.mjs
```

Every run prints and includes a fixed deploy nonce in the request and result.
Set `SAT20_DEPLOY_NONCE` and `SAT20_PREDICTION_NOW` to reuse a known positive
JavaScript-safe nonce and identical prediction content for deterministic
contract-address lookup after an uncertain submission.

The real testnet path is the default: it submits the order, locks it, builds and broadcasts the low-value L1 buy, then verifies the result. Use an explicit dry-run switch when a no-write check is required:

```bash
SAT20_DRY_RUN=1 npm run verify:l1-orderbook
```

The legacy write flags are still accepted for compatibility, but are no longer required:

```bash
SAT20_ALLOW_ORDERBOOK_WRITE=1 npm run verify:l1-orderbook
```

To explicitly disable all broadcasts:

```bash
SAT20_DISABLE_BROADCAST=1 npm run verify:l1-orderbook
```

To avoid spending another buyer UTXO on a dummy split, reuse an already broadcast dummy split:

```bash
SAT20_EXISTING_DUMMY_TXID=<txid> \
SAT20_EXISTING_DUMMY_CHANGE=<change_sats> \
npm run verify:l1-orderbook
```

The existing dummy transaction must belong to the selected buyer account. Its
change must be at least `2200 + sell amount` sats (`3200` for the default order)
so the final buy can fund the seller output, replacement dummy outputs, and buy
fee.

Current known L1 result on `2026-05-25`:

- Standard L1 order PSBT build and PWA signing succeed.
- `SubmitBatchOrders`, `LockBulkOrder`, `UnlockBulkOrder`, and signed `CancelOrder` succeed.
- With `SAT20_DRY_RUN=1` (or `SAT20_DISABLE_BROADCAST=1`), the script does not submit or broadcast and reports the skipped write path.
- Full L1 buy broadcast succeeds through the standard `BulkBuyOrder` path.
- Successful test txs:
  - dummy split: `ddff5cb1069adba95fcd51819e99335f4a34a9d2d020b4579ef6cdd90fe6a05d`
  - buy tx: `ae646dfaaad688ea9dd25b206c4492ab7532f2afdd039136e0de580c244e963f`

Root cause of the earlier `SubmitBatchOrders` panic: the SatoshiNet order builder serializes an asset-aware TxOut. The old `ordx-marketplace` L1 service parses it as a normal Bitcoin PSBT and sees an empty unsigned tx output script, then panics in `LogPsbt` while converting `PkScript` to an address.

Root cause of the earlier `BulkBuyingThirdOrder` mismatch: that endpoint only routes Magisat-sourced orders. SAT20 L1 orderbook buys should construct the final Bitcoin transaction locally and submit it with `BulkBuyOrder`.

## L2 Contract Flow

The L2 script verifies SatoshiNet contract read paths and wallet-side contract helpers against the production environment plus testnet network.

The real testnet path, including a low-value contract invoke, is the default:

```bash
npm run verify:l2-contract
```

Explicit read-only/dry-run mode:

```bash
SAT20_DRY_RUN=1 npm run verify:l2-contract
```

Run against a selected contract kind:

```bash
SAT20_L2_INVOKE_KIND=amm-swap \
SAT20_L2_CONTRACT_URL=<amm-contract-url> \
npm run verify:l2-contract

SAT20_L2_INVOKE_KIND=launchpool-mint \
SAT20_L2_CONTRACT_URL=<launchpool-contract-url> \
SAT20_L2_INVOKE_AMOUNT=1 \
npm run verify:l2-contract
```

Useful overrides:

- `SAT20_L2_CONTRACT_URL`
- `SAT20_L2_INVOKE_KIND` defaults to `swap-v2`; supported low-value test kinds: `swap-v2`, `amm-swap`, `launchpool-mint`
- `SAT20_L2_INVOKE_AMOUNT` defaults to `1`
- `SAT20_L2_INVOKE_UNIT_PRICE` defaults to `1`
- `SAT20_L2_FEE_RATE` defaults to `1`
- `SAT20_STP_API` defaults to `https://apiprd.ordx.market/stp/testnet`
- `SAT20_SATSNET_INDEXER_API` defaults to `https://apiprd.ordx.market/satsnet/testnet`

The default real invoke uses a `swap.tc` contract buy action and pays `::` on SatoshiNet, so keep the amount at `1` unless intentionally testing a larger flow.

Current known L2 result on `2026-05-25`:

- Read-only checks passed for deployed contract list, target contract status, supported contract templates, invoke fee, wallet balances, wallet UTXO selection, and direct SatoshiNet indexer UTXO queries.
- Real low-value invoke succeeded on `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:BITCOIN•TESTNET_swap.tc`.
- Invoke tx: `945905188f63bbfe260f5af602578d478218e7baa7e9cba2308f85f71c8feb37`.
- Chain-side checks found the raw tx and contract history item with `InUtxo=945905188f63bbfe260f5af602578d478218e7baa7e9cba2308f85f71c8feb37:0`; the contract moved to block `3396`, `invokeCount=45`, and `TotalDealTx=21`.
- AMM low-value invoke succeeded on `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:BITCOIN•TESTNET_amm.tc`.
- AMM tx: `59dca74bc49451615c5ac35e5590b9044942b4eb7a1bfac1e9e9cecd983c09be`; contract history later showed `InUtxo=59dca74bc49451615c5ac35e5590b9044942b4eb7a1bfac1e9e9cecd983c09be:0`, `Done=1`, `OrderType=2`, and `TotalDealTx=147`.
- Launchpool low-value mint was accepted by `tb1qw86hsm7etf4jcqqg556x94s6ska9z0239ahl0tslsuvr5t5kd0nq7vh40m_runes:f:SFSFSFFKKK_launchpool.tc`.
- Launchpool tx: `c9ffdcff6774fd0a7fe1fe76fbeeeab8e58e3fdfe22c9927a5f00882d6f6d970`; contract history showed `InUtxo=c9ffdcff6774fd0a7fe1fe76fbeeeab8e58e3fdfe22c9927a5f00882d6f6d970:0`, `OrderType=8`, `OutAmt=1`, and `TotalMinted` moved from `200` to `201`. The item stayed `Done=0` during the short poll window, so this path may need a longer settlement check.
- Read-only matrix checks passed for `swap.tc`, `amm.tc`, `launchpool.tc`, `dao.tc`, and `transcend.tc`. `recycle.tc` status reads passed, but the placeholder `refund` fee probe returned `unsupport action refund`; use the actual recycle action before testing that path.
