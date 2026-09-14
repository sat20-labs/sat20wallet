# Independent RGB11 single-key maintenance

This directory is **not** imported by PWA startup, the service worker or wallet
SDK. The verifier is compiled only with `-tags rgb11repair`. No normal wallet
runtime is loaded by this HTML page. Production green4 files are unchanged.

## Test before use (executor only)

From `sat20wallet/sdk`:

```
go test -tags rgb11repair ./wallet -run '^TestRGB11SingleKeyRepairTool$' -count=1 -v
go build -tags rgb11repair -o /private/tmp/rgb11-repair ./cmd/rgb11-repair
```

From `sat20wallet/pwa`:

```
node tools/rgb11-repair/repair.test.mjs
```

Stop on the first failure. No tests or builds were executed by the author.

## Closed profile copy, then exact real target

1. Close **all** pages/processes using the selected profile. Copy the whole
   stopped profile to a private directory, preserving permissions. Retain the
   original unchanged backup. Do not export mnemonic/account secret. Do not run
   the ordinary wallet in the maintenance copy: it would start reconciliation.
2. Serve `index.html` and `repair.mjs` as static files at a dedicated path on the
   **same origin** as the selected PWA (R2: `http://127.0.0.1:4178`). They must not
   be the server's SPA fallback. Executor must GET the path first and confirm it
   is this maintenance document, then open it. Unregister the existing SW in
   the copy first; the page refuses to work while any registration remains.
   Keep all other same-origin tabs closed. This is a required single-writer
   maintenance procedure, not a claim of cross-tab compare-and-swap support.
3. Enter independently reviewed public identity:
   `prd|testnet|<rootAccountId>|1788775138589000|0|<compressed-account-pubkey>`.
   Use the exact public values already recorded in the RGB08 evidence, not a
   copied unreviewed export claim. Prefix:
   `rgb11-wallet-1788775138589000-account-0-rgb11v2-`.
   The page reads only public IndexedDB `local:wallet_state_snapshot_v1` to
   cross-check this identity and selected scope. It reads values only for
   selected `pending/transfer/proof/validation/object` keys; no secret or other
   wallet key values are read. It exports those RGB records, which are private
   proof material, to a local file. Keep downloads mode0600; never paste payloads
   into chat/logs. It displays only the fingerprint.
4. Create the separate reviewed target JSON (not derived blindly from input):

```
{
  "identity": "<exact reviewed identity>",
  "prefix": "rgb11-wallet-1788775138589000-account-0-rgb11v2-",
  "fingerprint": "<export fingerprint>",
  "transfer_id": "rgb:csg:3wTJqSI8-pDK82xg-66k3cEp-7ii5vHm-o~6UH4k-BCimchg#couple-emerald-brush",
  "witness": "85be9e663afe72d86f06cec8b30f13f2014a04b8ed2a735a13cdda7862e44b0f",
  "change": "85be9e663afe72d86f06cec8b30f13f2014a04b8ed2a735a13cdda7862e44b0f:2",
  "successor": "966bda3a3c604946cee1ebb9f1a91d414cabb2797527941467b5255bb7e6d309"
}
```

5. Dry-run: `/private/tmp/rgb11-repair -input <private-export.json> -target
   <reviewed-target.json>`. It only GETs fixed Testnet4 Bitcoin evidence; no DB
   access, signature or broadcast capability. Original persisted receipt/hash,
   proof/seal, original signed witness, exact confirmed successor input and
   successor consensus proof must agree. Unknown/inconsistent evidence rejects.
   Existing erroneous pending is handled only here, never by production refresh.
6. After dry-run passes, repeat with `-patch <NEW-private-patch.json>`. Existing
   output refuses overwrite. Compute SHA256 of that exact artifact for the
   reviewed patch field. The patch is base64 binary material, not a public
   report; do not print it. Generation rechecks current public chain evidence.
7. Select patch in the maintenance page and enter its independently approved
   SHA256 plus reviewed export fingerprint. Apply checks full selected export
   fingerprint again, then synchronously rechecks all values and changes exactly
   one pending key with one `localStorage.setItem`. Quota failure leaves the old
   value; proof/lock/other scope are not written. If the redundant transfer key
   is also pending, verifier **rejects**; no two-key transaction or journal is
   attempted. No `ImportSnapshot`, refresh, backup sync or wallet restart occurs.
8. Export again and compare all unchanged records byte-for-byte; only the target
   pending status may differ. Repeat apply must reject. Preserve both exports
   and the stopped-profile backup. Only after the copied-profile checks pass may
   the executor repeat the same approved maintenance procedure on original R2,
   with a fresh export/fingerprint and freshly verified patch. Do not transplant
   a patch across differing profile state.

## Limits

The trusted identity comes from the separately reviewed public R2 scope and
public PWA state; this tool does not decrypt a root secret to prove ownership.
That is deliberate: it is a local administrator maintenance tool, not an
untrusted backup importer. It refuses the wrong reviewed identity/fingerprint.
Web Storage offers one-key atomic replacement, not multi-key transactions or
cross-process CAS. Enforce the closed-wallet single-writer procedure. Full
profile backup is the rollback mechanism. Do not restore an old backup after
new transactions; reconciliation after tool completion is a separate normal
wallet operation. This tool does not alter SDK safe-height policy or Iris.
