# STP asset protocol boundary

STP uses one asset registry in the SDK for both wallet clients and Transcend.
The built-in implementations cover BTC, ORDX (including existing DID/NFT/exotic
sat classes), Runes, BRC20, RGB11 fungible assets and Atomicals ARC20. The current
settlement route is Bitcoin/SatoshiNet.

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

Registration order is deliberate: ARC20 occupies the first spendable outputs,
BRC20 materializes transfer inscriptions, ORDX lays out sat-bound assets, and
Runes binds its output indexes. RGB reserves its zero-value commitment marker
before planning and adds its carriers after these protocols. STP then completes
plain-sat funding, recovery reserves and fee outputs. The relative ordering of
the existing protocols remains unchanged. Map iteration must never determine
protocol order.

BTC continues to use native transaction `Value`; it is not encoded as an asset
entry in `TxAssets`. Native identity remains the responsibility of each protocol;
contract display-name normalization is a separate method.

Asset quantity, decimal precision and Bitcoin carrier value are separate facts.
Use `STPAssetBindingSat` for sat binding. RGB11 has binding zero and a canonical
32-byte contract ID as its asset key; its precision comes from validated native
metadata. ARC20 has integral quantity and binding one, but a carrier may contain
more sats than its colored quantity. Native protocols must not be reconstructed
from ordinal offset maps or from `amount * binding` alone.

## Transaction packages and final layout

`STPAssetPlan` carries the transfer/commitment transaction, its previous-output
fetcher, fee, prerequisite inscriptions, subsequent recovery inscriptions,
additional recovery transactions and native proofs. STP retains the existing
`PrevTxs`, `NextTxs` and `Others` records and persists native channel funding,
proofs and journal state alongside them. Optional interfaces provide native
layout preparation, splice planning, output classification and metadata without
forcing indexer-backed adapters to manufacture native evidence.

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

The legacy verifiers retain their existing indexer-backed asset interpretation.
ARC20 rebuilds the whole transaction's allocation with the indexer's native
regular coloring function and rejects burns, unrelated assets and layouts that
cannot preserve the requested balances. RGB11 validates real consignments and
seal disclosures against Bitcoin evidence. Layout verification alone is never
an ownership proof for either protocol.

RGB public transfers use the managed prepare, receiver-ACK, authorization and
broadcast path. Its generic unsigned `BuildTransfer` deliberately returns an
error: callers must use the managed entry point so an alternate builder cannot
bypass proof delivery and durable recovery.

## Recovery

BTC, ORDX and Runes use the existing direct UTXO recovery behavior. BRC20 uses
its lifecycle implementation to reserve inscription fees, prepare subsequent
commit/reveal pairs, and recover from their reveal outputs. Missing or malformed
recovery material returns an error. Recovery does not create transfer inscriptions
on demand after the previous exit state has been revoked.

The same lifecycle supplies materialized inputs for the server-held commitment's
additional sweep. STP continues to select the recipient and construct/sign the
spending branch, including the existing bootstrap co-signing requirements.

RGB channel funding and commitment packages contain native proofs for the exact
source allocations. STP prepares recovery packages before releasing signatures,
persists journal state and validates the package again when restoring the
channel. Observed final wallet allocations are projected only into their
captured local account. Public P2WSH allocations belong in native pool inventory,
not in an ordinary wallet's selectable RGB balance.

Protocols whose outputs need a co-signed dependent sweep set
`ExchangeRecoveryPackages`. Their existing commitment RPC first performs a
`RecoveryOnly` exchange. The peer independently reconstructs the candidate in
an isolated reservation and signs only its dependent recovery transactions.
This phase rejects parent, splice, deanchor and revocation authorizations. The
caller checks the exact transaction identities, native proofs and complete
input signatures, then atomically saves the recovery package before sending
the normal commitment request. A peer without this capability cannot admit a
new native update; existing non-native exchanges keep their original wire flow.

A restart before the normal response retains the old commitment with its old
funding context and blocks new signing. It does not combine the old exit with
unconfirmed replacement funding. Once the exact prepared peer commitment is
observed on the canonical Bitcoin chain, recovery validates the actual parent
and funding transactions, selects the durable candidate and reuses its signed
sweep package. It does not require another signature from the unavailable peer.
Existing half-signed commitment records remain compatible: stored peer
signatures are applied to a verification copy without changing the stored
commitment format or signing a new transaction.

Native settlement projection also preserves account and RGB snapshot comparison.
If concurrent recovery wins publication, the losing operation rebuilds and
revalidates its candidate from a fresh snapshot, with a bounded retry. A failure
after successful publication remains an error; stale snapshots are never
written over newer account state.

The current RGB private transition supports one RGB contract per Bitcoin
transaction. Several funding sources for that same contract can be merged using
their validated histories. This does not promise arbitrary combinations of
different RGB contracts or other native allocation schemes in one transition;
unsupported combinations must fail before signatures are released.

## Public contracts and native settlement

### Private channel versus public Transcend

Both routes admit the same native asset classes: RGB11 fungible contracts and
Atomicals ARC20 fungible tokens. Their operation sets describe different custody
models. A private channel has commitment updates, expansion, cooperative close,
force close and revoked-state punishment. A public `transcend.tc` pool has
co-signed deposits and withdrawals; private-channel close/punishment operations
are not public-pool operations. Sharing an adapter does not make the two state
machines or their recovery tests interchangeable.

| Boundary | Private channel | Public `transcend.tc` |
| --- | --- | --- |
| RGB11 quantities | Exact contract precision; authenticated consignments and source history | Same precision and proof requirements; pool inventory is separate from wallet inventory |
| ARC20 quantities | Integral native units; authenticated indexer facts and regular coloring | Same native identity and coloring requirements |
| ARC20 partial amount below 330 | Rejected when the selected input has asset change: the splice recipient is currently fixed at vout 0 | Supported when one small recipient can be moved after the exact-quantity change output |
| ARC20 full small allocation | Supported with enough plain BTC to pad the carrier | Supported with enough plain BTC to pad the carrier |
| ARC20 two small colored outputs | Rejected if regular coloring cannot preserve both allocations | Same rejection; padding must not silently change token quantities |

The private partial-splice limit is a current transaction-layout restriction,
not a blanket minimum ARC20 balance and not a limitation on RGB11 quantities.
The relevant amount is the allocation in the selected native inputs, rather
than the wallet's aggregate balance. Rejected layouts must leave the existing
commitment, reservations and asset balances unchanged. Neither route treats BTC
carrier padding as extra tokens.

The shared contract runtime and manager own native deposit, inventory and
withdrawal handling. These facilities do not require a private `Channel`
instance and are not confined to `TranscendContractRuntime`.

| Public contract | RGB11 fungible assets | Atomicals ARC20 |
| --- | --- | --- |
| Transcend | Deposit and withdraw | Deposit and withdraw |
| AMM | Liquidity, buy/sell and public-channel transfers | Liquidity, buy/sell and public-channel transfers |
| Swap | Existing swap operations on mapped assets | Existing swap operations on mapped assets |
| DAO | Donation, validator and distribution operations | Donation, validator and distribution operations |
| Faucet | Funding and claims | Funding and claims |
| Launchpool | Fixed native issuance, distribution and AMM handoff | Outside this upgrade's scope |

Recycle is outside this upgrade's scope.

### Deposit and inventory

`PrepareNativeContractDeposit` and `ResumeNativeContractDeposit` create a real
RGB transfer to the two owners' public P2WSH. Both owners validate and persist the
signed intent, exact full source quantity, Bitcoin carrier and native proof
before it is broadcast. The SatoshiNet anchor commits the native proof before
either owner signs. No RGB balance or ticker is fabricated in the L1 indexer.

This route retains STP's core co-signing bridge model. RGB validation establishes
the source contract, allocation and history; Bitcoin custody is still enforced
by the pool's two-owner script, and availability of both owners is required for
cooperative public withdrawals. The SDK checks actual transaction bytes,
signatures and inclusion against canonical evidence supplied by the configured
chain nodes. It does not turn this bridge into a trustless Bitcoin light client
or remove the existing canonical-chain and finality assumptions of those nodes.

`RegisterNativeChannelSettlementSource` and `ListNativeChannelSources` bind each
source to the captured owner, channel witness, current Bitcoin outpoint and full
native allocation. A source already spent by a known exact transaction remains
historical evidence; replay does not make it selectable again. The original
public proof is immutable. A source derived from a prior mapped exit also needs
a verified settlement receipt.

The exported receipt contains only public proof material and chain locators.
The receiver reconstructs the complete allocation and verifies the exact flow
from canonical SatoshiNet anchors through every mapped return and burn. Private
commitment packages, undisclosed branches and private entropy are not exported.
This permits a public pool to accept a private channel's proven settlement
source while preserving the distinction between pool custody and wallet funds.

### Withdrawal and recovery

A public RGB withdrawal freezes its exact invoke item IDs and recipients. It
prepares a Bitcoin transfer together with a SatoshiNet descend transaction whose
burn commits the Bitcoin transaction ID. Both owners authorize the same pair,
and the complete signed bytes and native proof are durable before broadcasting.
A retry, including a lost signing response after peer broadcast, reuses those
bytes and does not select new inputs or adjust fees. Contract reconciliation
also resumes a durable pair when only its descend was broadcast. It validates
the stored signatures against actual previous transactions and replays the
missing Bitcoin transaction without asking either owner to sign spent L2 inputs.

Contract history settles only after exact transactions on both canonical chains
are verified. Validation checks the full RGB input/output flow, mapped asset
conservation, one matching burn and preservation of all consumed pool Bitcoin
carrier value. The operator funds network fees and recipient carriers. A
zero-asset returned pool carrier remains pool property but is not native asset
inventory.

History indexes, trader accounting, runtime, reservation state, invocation result,
settled marker and pending deletion are committed in one real database batch.
Live state changes only after a successful flush. DB adapters without the
`AtomicWriteBatchDB` capability fail before releasing signatures; repeated
`Put` calls do not implement atomicity. Replays preserve unrelated invoke items
at the same height and never move a newer checkpoint backward.

Managed RGB proof identities remain immutable during settlement. Already public
sibling disclosures can be saved as a separately validated public object, while
an existing pending transfer keeps its original consignment and receipt hashes.
Consumed proofs are retained as non-spendable evidence after the exact observed
spender is verified, so restart recovery cannot recreate spent change. A later
public transfer builds from the complete public source history.

### RGB launchpool

Launchpool prepares and persists real RGB issuance before the static contract is
signed. The genesis-derived canonical asset ID and full supply are therefore
fixed in the contract URL and signature. Installation validates that exact
issuance and its full consumer ancestry, transfers the issued allocation to the
public pool and activates it with the co-signed native anchor. Retries reuse the
same genesis, source and signed anchor. Decimal quotas, prices and AMM allocation
use exact quantities rather than truncating amounts to integer units.

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

RGB11 and ARC20 demonstrate the two additional integration shapes: client-side
proof validation with durable evidence transport, and transaction-wide native
coloring through the existing indexer interfaces. Their admission is limited to
the supported fungible asset classes; adding a protocol name does not admit its
NFTs or other state types. RUA and other future assets still need their own
implementation and acceptance coverage before registration.

Asset adapters are separate from future chain integration. Another public chain
needs its own transaction/signing/finality backend and a settlement endpoint for
source authorization, lock/release or mint/burn. The Bitcoin `wire.MsgTx` used here
is intentionally specific to the current route. These are extension boundaries,
not an implementation or claim that another public chain is already connected.

## Validation

Use sibling checkouts because the repositories use local Go replacements:

```text
work/
  sat20wallet/sdk/
  transcend/
  satoshinet/
  indexer/
  rgb11/
  badger/
```

SDK regression command, from `sat20wallet/sdk`:

```sh
go test ./wallet \
  -run '^(TestSTP|Test.*(Splicing|SweepFee|CommitAsset|ChannelRuntime|ChannelReservation|SatsNetInput))' \
  -count=1 -timeout=10m -v
```

End-to-end acceptance, from `transcend`:

```sh
go test ./stp -run '^TestRunAll_channel$' -count=1 -timeout=60m -v
go test ./stp -run '^TestRunAll_contract$' -count=1 -timeout=60m -v
```

The channel aggregate retains all existing scenarios and adds
`TestChannel_atomicals` and `TestChannel_rgb11`. The contract aggregate retains
its existing members and adds `TestContractNativeAssets`, which runs six RGB
contract families and five ARC20 families. New integration tests use the real
RGB core, real signatures, native ARC20 coloring, observed canonical chain
evidence and isolated databases; L1 indexer fixtures do not invent RGB state.

`TestChannel_atomicals` covers both close modes, complete exit and re-entry,
small full allocations with padded carriers, expansion, duplicate expansion
rejection and revoked-state punishment with already signed recovery packages.
It also verifies that an unsupported small partial splice leaves both peers'
persisted commitments and active reservations unchanged. Empty funding records
after a full exit must not prevent the next plain commitment; a live allocation
with missing balances must still fail.

Focused public contract coverage is available as:

```sh
go test ./stp -run '^TestContractNativeAssets$' -count=1 -timeout=15m -v
```

The public RGB tests cover sequential partial/full withdrawals, settlement
receipt handoff, replay after spent deposits and lost signing replies after
broadcast, including a temporary Bitcoin broadcast failure after the descend
succeeds. Launchpool also covers recovery after a lost activation reply. SDK
`TestNativeContractSettlement*` and `TestNativeLaunchPool*` exercise real atomic
write failures, reopen/replay, fixed issuance recovery and decimal boundaries.
Run aggregates sequentially when they share the legacy test database path; no
additional build tag or skipped aggregate member is required.

The public Transcend tests also reject changed RGB identities, beneficiaries,
quantities, carrier facts, assignments, consignments and Bitcoin witnesses at
both Core owners before receipts, pool inventory or mapped balances appear.
ARC20 public coverage includes fractional-quantity rejection, small outputs at
their actual colored indexes, complete pool drainage and one-unit re-entry.

CI checks out the public dependencies from `main` in dependency order: indexer,
the SatoshiNet native-proof companion, wallet, then Transcend. The private
Transcend repository owns the two aggregate gates and the wallet's live-node
account/PWA and RGB backup/recovery E2E, because those tests build its private
STP plugin. Wallet's public CI retains its standalone Go, WASM and PWA checks.
A wallet-only change must also run Transcend's integration workflow against
that candidate using its `wallet_ref` dispatch input (a branch or commit);
cross-repository runs are not triggered automatically.
