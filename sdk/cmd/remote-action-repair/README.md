# Remote action ACK repair

This build-tagged Testnet4 maintenance command resumes one already persisted
initiator remote action. It does not call Prepare, create an invoice or
reservation, charge a fee, or construct/broadcast commit and reveal
transactions. The wallet process using the profile must be stopped first.

The target JSON must explicitly contain:

```json
{
  "reservation_id": 123,
  "action": "deployrunes",
  "signer_pubkey": "02...",
  "fee_txid": "...",
  "commit_txid": "...",
  "reveal_txid": "..."
}
```

Dry-run is the default. Supply the password through an environment variable;
never place it in the target, plan, command line, or logs:

```sh
go run -tags remoteactionrepair ./cmd/remote-action-repair \
  -config /path/to/config.json -db /path/to/stopped/profile/db \
  -target /path/to/target.json -plan /path/to/new-plan.json
```

Review the new plan, then apply that exact plan:

```sh
go run -tags remoteactionrepair ./cmd/remote-action-repair \
  -config /path/to/config.json -db /path/to/stopped/profile/db \
  -target /path/to/target.json -approved /path/to/reviewed-plan.json -apply
```

Apply rechecks every identity and transaction field plus the reservation
fingerprint, then invokes the existing remote-action status handler once. A
transport or server error leaves the reservation active and retryable.

## Browser PWA localStorage profile

The clean-A wallet is an ordinary PWA at `http://127.0.0.1:4178`. In a normal
browser page, the WASM light-node database uses `window.localStorage`, which is
scoped by scheme, host, port and browser profile. The native command cannot
open that browser storage. Build a dedicated maintenance WASM instead:

```sh
mkdir -p /private/tmp/sat20-remote-action-maintenance
GOOS=js GOARCH=wasm go build -tags remoteactionrepair \
  -o /private/tmp/sat20-remote-action-maintenance/sat20wallet.wasm ./wasm
```

Use a minimal static page under `/private/tmp/sat20-remote-action-maintenance`
that loads only the matching Go `wasm_exec.js` and the maintenance
`sat20wallet.wasm`. It must not import the PWA `main.ts`, the ordinary WASM,
wallet stores, a service worker, or any code that starts a monitor. Stop the
ordinary runner and close every ordinary PWA tab on this origin before serving
the maintenance directory at the exact origin `http://127.0.0.1:4178`. Do not
use `localhost`, another port, HTTPS, another browser profile, or clear site
data. If an existing service worker controls the maintenance page, stop and
disable/unregister that worker without clearing localStorage before proceeding.

Use the same PWA configuration for `Env`, `Chain`, peers and L1/L2 indexers.
The maintenance `sat20wallet_wasm` object adds four build-tag-only calls:

1. `initRemoteActionRepair(config, logLevel)` opens the current origin's
   existing localStorage without starting monitors.
2. `inspectRemoteActionRepairTarget(reservationID)` reads the persisted active
   initiator `deployrunes` reservation and returns its exact target template.
   Pass the ID as a decimal string (preferred) or a JavaScript safe integer.
   This step does not unlock a wallet, contact the network, or change state.
   Save the returned target as a new file and review its signer and transaction
   IDs before continuing.
3. `planRemoteActionRepair(password, targetJSON)` unlocks the original wallet
   and returns the dry-run plan. Save and review the exact returned plan.
4. `applyRemoteActionRepair(password, targetJSON, approvedPlanJSON)` rechecks
   the reviewed plan and advances the existing reservation once.

Keep the password interactive; do not put it in the page, URL, target, plan or
logs. After the reviewed apply, call the ordinary `release()` export, close the
maintenance tab, stop the temporary server, and restore the ordinary runner at
`http://127.0.0.1:4178`. The ordinary WASM build does not expose these methods.
Do not replace the production WASM artifact or run the maintenance WASM in
another origin/profile.
