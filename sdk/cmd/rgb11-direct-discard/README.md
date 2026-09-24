# Exact C/172d RGB11 Direct discard

This build-tagged Testnet4 maintenance tool permanently tombstones one exact
unrecoverable historical Direct mailbox message and then marks the same
application processed in the local wallet profile. It never creates a proof or
asset, changes sender A, starts monitors, or scans/deletes other mailbox data.

The checked-in target is `target-c-172d.json`. The implementation rejects every
other target even if another JSON file is supplied.

## Mandatory backup

Stop the ordinary C PWA runner and close every ordinary C tab first. Confirm no
browser process is using `/private/tmp/sat20-pwa-r2-c`, then make a fresh copy
of the entire profile. The older backup predates the later Standard transfer
and is not sufficient for this apply.

```sh
stamp=$(date +%Y%m%dT%H%M%S)
backup=/private/tmp/sat20-pwa-r2-c-before-rgb11-discard-$stamp
cp -a /private/tmp/sat20-pwa-r2-c "$backup"
du -sh /private/tmp/sat20-pwa-r2-c "$backup"
find /private/tmp/sat20-pwa-r2-c -type f | wc -l
find "$backup" -type f | wc -l
```

Keep the backup until mailbox sync, refresh, and state readback have completed.
Do not clear site data, localStorage, IndexedDB, cookies, or the profile.

## Browser profile build

The PWA wallet database is the browser's `localStorage`, so the native command
cannot operate the C profile. Build a dedicated maintenance WASM and page:

```sh
cd /Users/yingfeng/github/sat20wallet/sdk
out=/private/tmp/sat20-rgb11-direct-discard
mkdir -p "$out"
GOOS=js GOARCH=wasm go build -trimpath -tags rgb11discard \
  -o "$out/sat20wallet.wasm" ./wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/wasm_exec.js"
cp cmd/rgb11-direct-discard/maintenance.html "$out/index.html"
cp cmd/rgb11-direct-discard/target-c-172d.json "$out/target-c-172d.json"
cp cmd/rgb11-direct-discard/target-c-69fa-lock.json "$out/target-c-69fa-lock.json"
shasum -a 256 "$out/sat20wallet.wasm" "$out/wasm_exec.js" \
  "$out/index.html" "$out/target-c-172d.json" "$out/target-c-69fa-lock.json"
```

Do not copy this WASM into `pwa/public`, `client/public`, or a release. Serve the
directory at the exact origin used by C (currently `http://127.0.0.1:4178`) and
open it with the existing C browser profile `/private/tmp/sat20-pwa-r2-c`.
`localhost`, another port, another browser profile, or a different scheme will
open a different wallet database. The ordinary PWA server must not be running
on that port. The maintenance page must not be controlled by the PWA service
worker; if it is, stop/unregister that worker without clearing storage.

```sh
python3 -m http.server 4178 --bind 127.0.0.1 \
  --directory /private/tmp/sat20-rgb11-direct-discard
```

Before stopping the ordinary page, copy its exact runtime config (`Env`,
`Chain`, `Peers`, `IndexerL1`, optional `SlaveIndexerL1`, `IndexerL2`, optional
`SlaveIndexerL2`). Paste it into the maintenance page; do not save the password
in the config, page, URL, target, plan, shell history, or logs.

1. `Initialize` calls `initRGB11Discard(config, 2)` and opens the existing
   localStorage without starting monitors.
2. `Dry-run plan` calls `planRGB11Discard(password, targetJSON)`. It performs
   read-only local/DKVS/Bitcoin checks. Save the returned plan.
3. Review every plan field below. Paste that unchanged saved plan into
   `Reviewed plan JSON` only after approval.
4. `Apply reviewed plan` calls
   `applyRGB11Discard(password, targetJSON, approvedPlanJSON)`. It re-runs the
   complete plan and requires byte-equivalent structured fields/fingerprint,
   then deletes exactly one record, confirms the result from the authoritative
   node, removes only the matching stale local replica entry, and adds one
   local processed marker.
5. Call `Release manager`, close the maintenance tab, stop the temporary HTTP
   server, and restore the ordinary C runner/profile.

The dry-run approval record must contain exactly:

- all eight `target` fields from `target-c-172d.json`;
- `wallet_id` for the current reimported C profile;
- `sub_account: 0`;
- `receiver_address` equal to the full known C account0 address;
- `record_key` ending in Direct message ID
  `00065c102d719ec8c329515259e04904`;
- non-empty 64-lowercase-hex `record_hash`, `consignment_hash`, and
  `fingerprint`;
- `spend_evidence: "confirmed-raw-tx"` and `spending_vin: 1`;
- `spend_block_height: 153534`, the exact 64-lowercase-hex confirmed block
  hash, and a positive `spend_confirmations` value.

Apply is idempotent. Internal `FREE_LOCAL` mailbox deletion removes the record,
so the authoritative key-state normally becomes `never_seen`; an endpoint that
retains tombstones may return `deleted`. Apply accepts only those two remote
states, after the exact approved plan/delete request, before touching the local
replica. It rejects an active or unconfirmed remote record. A retry with the
same reviewed plan handles an earlier successful remote deletion: it confirms
the remote state, removes a local record only when its hash still matches the
approved `record_hash`, clears that exact local key-state, and rewrites the
processed marker idempotently.

## Exact stale 69fa lock

Before any further stale-lock plan attempt, use `Read-only lock diagnosis`.
It reports the SDK KV implementation and configured DB label, exact formal raw
lock key state/hash/decoded shape, lock marker state/hash/value, and the current
manager's in-memory locker entry without reloading or writing. It also reports
the runtime origin/path, selected SDK browser storage backend, service-worker
control, and the same exact keys in `localStorage`. The PWA wrapper IndexedDB
name/store are reported separately because that database does not back SDK KV.
Do not enter the wallet password for this read-only diagnosis.

If production reports `consistency_status=broken`, use `Read-only consistency
diagnosis` before any discard plan. It decrypts only the already-selected
wallet scope while deliberately suppressing lock rebuilding, then checks every
send journal and signed transaction, local consignment validation, output/proof
pair, expected spender, and chain outspend. It does not run refresh, mailbox
sync, Plan, Apply, or write lock state. Enter the password only in the page's
memory field and clear it after the result is captured.

The same maintenance build has a separate dry-run/apply pair for the one stale
C lock `69fa...:1`. It does not share the Direct-message plan or fingerprint.
The dry-run refuses to produce a plan unless all of these facts hold together:

- the active account is the fixed C account0 scope;
- the lock is ownerless `reason=rgb`, `value=0`, has no assets and no
  reservation fields;
- the fixed `4Dmrd...` r2direct send is `settled`, ACK `accepted`, amount `1`,
  `STANDARD_PROXY`, and uniquely references that input;
- the target r2direct balance is exactly zero, with no non-settled transfer,
  receive reservation, or `allReservations` record referring to the input or
  transfer;
- confirmed raw transaction `976...` hashes to the expected txid and consumes
  `69fa...:1` at vin 0.

Save and review the returned stale-lock plan, paste it into the separate
reviewed-plan field, then apply. Apply re-runs every check, deletes only that
lock in one persistent lock-map batch, and returns success only after readback
shows it absent. It is idempotent and does not remove the independent
`c57...:1` r2paid lock, any proof, asset, transfer, or reservation.

## Exact stale 4Dmrd reservation owner

The maintenance page has a separate `Dry-run stale-owner plan` for the fixed
C `4Dmrd...` Standard send. Use it only when ordinary refresh reports a 69fa
reservation-owner mismatch after the strict expected-spender check passes.

The plan requires the settled/accepted private send journal, its non-empty
stale reservation owner, the exact confirmed raw `976...` transaction with
`69fa...:1` at vin 0 and `0bbc...:1` as the other input, a spending 69fa proof,
no target lock or live wallet/receive reservation, and the independent
`c57...:1` r2paid amount/proof/RGB lock intact. The reservation identifier is
reported only as a SHA-256 hash.

Apply re-runs every check and clears only `ReservationID` in that one settled
pending journal. It preserves the journal, signed transaction, transfer,
proofs, outputs, assets, c57 lock, and PWA operation log. Apply is idempotent
and verifies the protected c57 state again after readback. Save and review this
plan separately from the Direct-message and stale-lock plans.

## Native stopped-profile command

For a LevelDB profile, dry-run and apply are available through the CLI. This is
not the path for the current browser C profile.

```sh
cd /Users/yingfeng/github/sat20wallet/sdk
go run -tags rgb11discard ./cmd/rgb11-direct-discard \
  -config /path/config.json -db /path/stopped/db \
  -target ./cmd/rgb11-direct-discard/target-c-172d.json \
  -plan /path/NEW-reviewed-plan.json

go run -tags rgb11discard ./cmd/rgb11-direct-discard \
  -config /path/config.json -db /path/stopped/db \
  -target ./cmd/rgb11-direct-discard/target-c-172d.json \
  -approved /path/NEW-reviewed-plan.json -apply
```

Supply the password through `SAT20_WALLET_PASSWORD`; do not put it in arguments.
