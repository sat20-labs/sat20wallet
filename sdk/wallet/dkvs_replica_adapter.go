package wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// The wallet package owns orchestration; durable replica implementation lives
// in sdk/wallet/dkvs and is exposed here only through wallet-internal aliases.
type DKVSSubscriptionState = dkvscore.SubscriptionState
type DKVSLocalKeyState = dkvscore.LocalKeyState
type DKVSPersistedMutation = dkvscore.PersistedMutation
type DKVSBatchOutboxEntry = dkvscore.BatchOutboxEntry
type dkvsReplicaStore = dkvscore.ReplicaStore
type dkvsOutboxOrigin = dkvscore.OutboxOrigin
type DKVSCASMutationRequest = dkvscore.CASMutationRequest
type DKVSBatchCASRequest = dkvscore.BatchCASRequest
type DKVSBatchCASResult = dkvscore.BatchCASResult

var dkvsOutboxPrefix = dkvscore.OutboxPrefix()

const (
	DKVSSubscriptionSyncing       = dkvscore.DKVSSubscriptionSyncing
	DKVSSubscriptionReady         = dkvscore.DKVSSubscriptionReady
	DKVSSubscriptionOfflineReady  = dkvscore.DKVSSubscriptionOfflineReady
	DKVSSubscriptionResetRequired = dkvscore.DKVSSubscriptionResetRequired
	DKVSSubscriptionError         = dkvscore.DKVSSubscriptionError
	DKVSOutboxPending             = dkvscore.DKVSOutboxPending
	DKVSOutboxInflight            = dkvscore.DKVSOutboxInflight
	DKVSOutboxConflict            = dkvscore.DKVSOutboxConflict
	DKVSOutboxTerminal            = dkvscore.DKVSOutboxTerminal
)

func newDKVSReplicaStore(db indexer.KVDB) *dkvsReplicaStore {
	return dkvscore.NewReplicaStore(db)
}

func normalizeWalletSubscriptionPrefixes(prefixes []string) ([]string, error) {
	return dkvscore.NormalizeSubscriptionPrefixes(prefixes)
}

func sameStringList(left, right []string) bool {
	return dkvscore.SameStringList(left, right)
}

func keyCoveredByPrefixes(key string, prefixes []string) bool {
	return dkvscore.KeyCoveredByPrefixes(key, prefixes)
}

func walletSubscriptionMatches(prefix, key string) bool {
	return dkvscore.SubscriptionMatches(prefix, key)
}

func dkvsOutboxKey(namespace, requestID string) []byte {
	return dkvscore.OutboxKey(namespace, requestID)
}

func newDKVSRequestID() (string, error) {
	return dkvscore.NewRequestID()
}

func batchContainsFreeLocal(mutations []dkvsindexer.CASMutation) bool {
	return dkvscore.BatchContainsFreeLocal(mutations)
}

func writeResultToBatchResult(result *dkvsindexer.WriteResult) *DKVSBatchCASResult {
	return dkvscore.BatchResult(result)
}

func mergeSubscriptionPrefixes(values ...[]string) ([]string, error) {
	return dkvscore.MergeSubscriptionPrefixes(values...)
}

func newDKVSBatchOutboxEntryFinal(namespace string, mutations []dkvsindexer.CASMutation,
	endpointID string, origin dkvsOutboxOrigin) (*DKVSBatchOutboxEntry, error) {
	return dkvscore.NewBatchOutboxEntry(namespace, mutations, endpointID, origin)
}
