# UTXO lock reason definitions

`common/utxo_lock_reason.go` is the single SDK definition location. The stored
string values are unchanged. There are no old aliases, format migrations, or
compatibility translations. Public callers may still supply their own reason.
A reason identifies the source; it does not by itself authorize an unlock.

| Constant | Stored value | Existing source |
| --- | --- | --- |
| UtxoLockManual | `manual` | Explicit manual lock request |
| UtxoLockBroadcasted | `broadcasted` | Inputs of a broadcast transaction |
| UtxoLockRGB | `rgb` | RGB allocation carrier protection |
| UtxoLockRGBPending | `pending-rgb` | RGB operation reservation, including fee inputs |
| UtxoLockContractDeposit | `contract deposit` | Contract deposit protection |
| UtxoLockRGBWithdrawal | `rgb11-contract-withdrawal` | RGB contract withdrawal inputs on SatoshiNet |
| UtxoLockClosedTopup | `closed-channel-topup` | Closed-channel top-up inputs |
| UtxoLockStubPrefix | `stub for ` + operation | Contract stub operation |

Lock owner, reservation ID, previous reason, persistence, and release rules are
unchanged. Signing purposes, transaction states, transport modes, and reclaim
sources are not UTXO lock reasons and are not converted to these constants.

## Read-only presentation

The existing WASM lock-list responses retain their map-of-JSON-strings shape and
add `category`: `manual`, `rgb`, or `other`. This is a view DTO, not a persisted
field. A non-RGB current reservation with previous reason `rgb` is also protected
in the RGB category; the actual current reason is retained.

`GetRGBView` exposes an explicitly identified selection and read-only lock rows.
The rows include both network lock stores. `current` means the selected account's
existing RGB records associate the outpoint/reservation with that account; it is
not a fresh chain ownership assertion. Unassociated rows, including L2 withdrawal
records without authoritative scope links, are `unknown`. No query imports,
refreshes chain state, broadcasts, releases locks, or changes their reasons.
L1 and L2 rows are read snapshots, not a claim of cross-network atomicity.

The RGB tab has no unlock action. BTC/SatoshiNet generic pages exclude the SDK's
RGB category even if the detailed RGB view cannot be read. Missing classification
fails visibly instead of exposing unclassified locks as generic unlock actions.
Ordinals continues to use its separate protocol API. Transfer links only navigate
to the existing wallet; they do not confirm, retry, cancel, or release anything.
