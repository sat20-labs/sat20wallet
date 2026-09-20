# Independent RGB11 maintenance

This directory is **not** imported by PWA startup, the service worker or wallet
SDK. The verifier is compiled only with `-tags rgb11repair`. No normal wallet
runtime is loaded by this HTML page. Production SDK/WASM files are unchanged.
The page supports the original single-key repair and an approved orphan
self-receive snapshot repair.

## Test before use (executor only)

From `sat20wallet/sdk`:

```
go test -tags rgb11repair ./wallet -run '^TestRGB11SingleKeyRepairTool$' -count=1 -v
go build -tags rgb11repair -o /private/tmp/rgb11-repair ./cmd/rgb11-repair
```

From `sat20wallet/pwa`:

```
node --test tools/rgb11-repair/repair.test.mjs
```

Stop on the first failure.

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
   `prd|testnet|<rootAccountId>|<walletId>|<accountIndex>|<compressed-account-pubkey>`.
   Use the exact public values already recorded in the RGB08 evidence, not a
   copied unreviewed export claim. Prefix:
   `rgb11-wallet-<walletId>-account-<accountIndex>-rgb11v2-`.
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
  "prefix": "rgb11-wallet-<walletId>-account-<accountIndex>-rgb11v2-",
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

## Approved orphan self-receive repair

This path is for the exact engine/projection repair produced by the build-tagged
`rgb11-repair` CLI. It does not infer damage, import an arbitrary backup, start
the wallet, refresh RGB state, sign or broadcast.

1. Complete the closed-profile-copy and same-origin maintenance setup in steps
   1–3 above. Keep the wallet runtime stopped, all other origin tabs closed and
   the service worker unregistered until the journal is gone.
2. Preserve the exact full source `RGB11WalletSnapshot` used by the offline CLI.
   Run the CLI dry plan with `-orphan-target`, review and approve the emitted
   plan, then run `-approved <plan> -apply <NEW-candidate.json>`. The CLI repeats
   every evidence and snapshot check and writes a new complete candidate; it
   still does not write browser storage.
3. Compute SHA256 for the exact source, candidate and approved-plan files by an
   independent local command. In the orphan section of the maintenance page,
   select all three files and enter all three hashes. Identity and projection
   prefix are the same independently reviewed public binding used by the page
   guard.
4. Press **Dry validate current storage** first. It verifies the three file
   hashes, snapshot identity, CLI before/after hashes, sorted canonical records,
   per-record hashes, the exact approved changes and the complete current engine
   plus projection namespace. It must report four operations for a target that
   has a validation receipt. Dry validation performs zero writes.
5. Press **Apply approved four-record repair**. The tool first repeats dry
   validation, then durably stores a journal outside both RGB namespaces. Each
   exact write/delete uses a before-or-after compare, immediate readback and
   journal progress update. It updates only `wallet/receive/<request>` in the
   engine namespace and deletes only the approved `transfer`,
   `prepared-receive` and matching `validation` projection records. The rejected
   sender pending record and every unrelated key remain byte-identical.
6. If the page/browser/process stops after the journal was created, do not open
   the wallet. Reopen this same maintenance page in the stopped profile, enter
   the same reviewed identity/prefix and press **Resume interrupted repair**.
   Resume validates the journal's full source/candidate hashes and reconstructs
   its exact operation set before idempotently continuing. A write completed
   before an interrupted progress update is recognized as already applied.
7. Success is returned only after a complete live snapshot equals the approved
   candidate hash. The tool then marks the journal verified and deletes it with
   readback. Preserve the source/candidate/plan and stopped-profile backup as
   private evidence. Rehearse on the profile copy before applying fresh approved
   artifacts to the original profile.

## Limits

The trusted identity comes from the separately reviewed public R2 scope and
public PWA state; this tool does not decrypt a root secret to prove ownership.
That is deliberate: it is a local administrator maintenance tool, not an
untrusted backup importer. It refuses the wrong reviewed identity/fingerprint.
Web Storage offers one-key atomic replacement, not multi-key transactions or
cross-process CAS. The orphan path therefore uses a durable roll-forward journal
and full after-snapshot verification; it is crash-recoverable only while the
closed-wallet single-writer procedure is enforced. Full profile backup remains
the external rollback artifact. Do not restore an old backup after new
transactions; reconciliation after tool completion is a separate normal wallet
operation. This tool does not alter SDK safe-height policy or Iris.
