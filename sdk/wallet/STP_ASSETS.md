# STP asset protocol boundary

STP uses one asset registry in the SDK for both wallet clients and Transcend.
The built-in implementations cover BTC, ORDX (including existing DID/NFT/exotic
sat classes), Runes and BRC20. The current settlement route is Bitcoin/SatoshiNet.

## Responsibilities

`STPAssetProtocol` in `stp_asset.go` composes four interfaces:

| Interface | Responsibility |
| --- | --- |
| `STPAssetRules` | Asset and operation admission, contract naming, carrier/stub and fee policy. These methods are deterministic and perform no I/O. |
| `STPAssetPlanner` | Select or prepare asset inputs, build a transfer, allocate commitment outputs, estimate and bind splice payloads. |
| `STPAssetLifecycle` | Reserve recovery value, prepare post-commitment transfers, materialize server recovery inputs, recover from stored commitment transactions. |
| `STPAssetVerifier` | Check an input before asset projection and check the final transaction against the asset plan without wallet keys or hidden network reads. |

STP retains peer authentication, channel roles, spending scripts, time locks,
fee ownership, commitment/revocation sequencing, signature exchange, transaction
broadcast, reservation persistence and restart handling. An adapter receives the
scripts chosen by STP. It must preserve their spending conditions.

The existing protocol-specific transaction builders remain reusable helpers.
The registry replaces protocol dispatch in commitment/close construction,
splicing payloads and input preparation, contract transfers, funding policies,
and sweep/punishment input recovery. Transcend calls the same SDK admission and
preparation APIs rather than choosing a BRC20 implementation in its handler.

## Registry and capabilities

`newBuiltinSTPAssetRegistry` is the single static registration point. The registry
rejects duplicates and is immutable once constructed. `Resolve` validates the
requested operation and asset class; supporting a wallet transfer is insufficient
to admit an asset into a channel. Unknown protocols fail before entry.

Registration order is deliberate: BRC20 materializes transfer inscriptions,
ORDX lays out sat-bound assets, and Runes binds its output indexes. STP then adds
plain-sat funding, recovery reserves and fee outputs. This preserves existing
transaction ordering. Map iteration must never determine protocol order.

BTC continues to use native transaction `Value`; it is not encoded as an asset
entry in `TxAssets`. Native identity remains the responsibility of each protocol;
contract display-name normalization is a separate method.

## Transaction packages and final layout

`STPAssetPlan` carries the transfer/commitment transaction, its previous-output
fetcher, fee, prerequisite inscriptions, subsequent recovery inscriptions and
additional recovery transactions. STP converts these into the existing
`PrevTxs`, `NextTxs` and `Others` records. Existing public transaction-building
signatures and persisted commitment formats remain compatible.

For commitments, the execution order is:

1. Validate funded assets and active balances, then verify inputs before projection.
2. Build the asset plans in registry order.
3. Add plain funding and unused stub inputs.
4. Ask each protocol for its recovery reserve; STP allocates that value.
5. Finish fee and plain-output accounting.
6. Prepare recovery transactions using the final commitment transaction ID.
7. Verify every plan against the final layout before returning it for signing.

The layout verifier rejects missing or duplicated inputs and changes to planned
asset output values/scripts. Runes also fixes the output ordinal. ORDX/BRC20
outputs can move when Runes prepends a carrier, while their value and script stay
fixed. Plain fee/change outputs remain under STP control. Contract transfers are
checked again after preparation of the signing package.

The built-in verifiers retain the existing indexer-backed asset interpretation.
Layout verification does not itself prove a new protocol's ownership or history.
Native-proof protocols need a verifier that checks their native evidence, plus
durable evidence delivery and recovery before capability admission.

## Recovery

BTC, ORDX and Runes use the existing direct UTXO recovery behavior. BRC20 uses
its lifecycle implementation to reserve inscription fees, prepare subsequent
commit/reveal pairs, and recover from their reveal outputs. Missing or malformed
recovery material returns an error. Recovery does not create transfer inscriptions
on demand after the previous exit state has been revoked.

The same lifecycle supplies materialized inputs for the server-held commitment's
additional sweep. STP continues to select the recipient and construct/sign the
spending branch, including the existing bootstrap co-signing requirements.

## Adding a protocol

For an asset using the current Bitcoin transaction and settlement model, implement
the four interfaces, register it once, and test every advertised operation:
input preparation, lock/unlock, both splice directions, cooperative close,
force close, punishment and restart recovery. Preserve or reject co-carried
assets before projection; keep asset-bearing inputs out of plain fee selection.

A protocol with additional proof/state transport must also provide its native
evidence codec, peer validation and durable recovery material. Those artifacts
must survive transaction rebinding and restart, and be ready before STP releases
spend-enabling signatures or revokes the old exit. The current legacy inscription
records are not a substitute for a native proof format.

RGB11 keeps its existing wallet/channel-transfer path. Its STP capabilities remain
unregistered and return `ErrRGB11STPUnavailable` until native proof/recovery and
SatoshiNet settlement validation are complete. RUA and Atomicals likewise require
their own implementations and acceptance coverage before registration.

Asset adapters are separate from future chain integration. Another public chain
needs its own transaction/signing/finality backend and a settlement endpoint for
source authorization, lock/release or mint/burn. The Bitcoin `wire.MsgTx` used here
is intentionally specific to the current route.

## Validation

Use sibling checkouts because the repositories use local Go replacements:

```text
work/
  sat20wallet/sdk/
  transcend/
  satoshinet/
  indexer/
  rgb11/
```

SDK regression command, from `sat20wallet/sdk`:

```sh
go test ./wallet \
  -run '^(TestSTP|Test.*(Splicing|SweepFee|CommitAsset|ChannelRuntime|ChannelReservation|SatsNetInput))' \
  -count=1 -timeout=10m -v
```

End-to-end channel acceptance, from `transcend`:

```sh
go test ./stp -run '^TestRunAll_channel$' -count=1 -timeout=60m -v
```

`TestRunAll_channel` uses the existing fake chain/indexer and runs 16 scenarios
in its original order. It uses `/private/tmp/transcend-stp-testdb`; provide a
writable parent in an isolated test environment. Run instances sequentially,
because they share this database path. No test flags that skip aggregate members,
and no modifications of the existing channel test source, are required.
