package wallet

import (
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func newAccountStorageHeightTestManager(t *testing.T, ttl uint64, bestHeight int64,
	bestHeightErr error, syncHeight int) *Manager {
	t.Helper()
	remote := newRGB11MemoryDKVSHTTP()
	remote.freeLocal.MaxTTL = ttl
	remote.bestHeight = bestHeight
	remote.bestHeightErr = bestHeightErr
	manager := &Manager{
		cfg: &common.Config{Chain: "testnet", Env: "test", IndexerL2: &common.Indexer{
			Scheme: "https", Host: "dkvs.test", Proxy: "satsnet/testnet",
		}},
		status: &Status{CurrentChain: "testnet", SyncHeightL2: syncHeight},
		http:   remote,
	}
	manager.dkvs = newDKVSManager(manager)
	return manager
}

func TestNormalizeDKVSRecordVerificationRequiresTrustedHeight(t *testing.T) {
	record := &swire.DKVSRecord{Key: "/personal/test/value", TTL: 100}
	if _, err := normalizeDKVSRecordVerification(record, record.Key,
		dkvsindexer.RecordVerificationOptions{}, 0, false); !errors.Is(err, ErrDKVSVerificationHeightRequired) {
		t.Fatalf("unknown height accepted: %v", err)
	}
	opts, err := normalizeDKVSRecordVerification(record, record.Key,
		dkvsindexer.RecordVerificationOptions{}, 99, true)
	if err != nil || opts.Height != 99 || opts.ExpectedKey != record.Key {
		t.Fatalf("valid height rejected: %+v %v", opts, err)
	}
	if _, err := normalizeDKVSRecordVerification(record, record.Key,
		dkvsindexer.RecordVerificationOptions{}, 100, true); !errors.Is(err, dkvsindexer.ErrExpiredRecord) {
		t.Fatalf("expiry height was not enforced: %v", err)
	}
}

func TestDKVSManagerVerificationHeightIgnoresStatusFromAnotherChain(t *testing.T) {
	manager := &Manager{
		cfg:    &common.Config{Chain: "testnet"},
		status: &Status{CurrentChain: "mainnet", SyncHeightL2: 39794},
	}
	dkvs := newDKVSManager(manager)
	dkvs.observeVerificationOptions(dkvsindexer.RecordVerificationOptions{Height: 3445})
	if height, known := dkvs.verificationHeight(); !known || height != 3445 {
		t.Fatalf("cross-chain status height was used: %d %v", height, known)
	}
}

func TestDKVSManagerVerificationHeightIgnoresStatusAndRetainsExplicitObservation(t *testing.T) {
	manager := &Manager{status: &Status{SyncHeightL2: 20}}
	dkvs := newDKVSManager(manager)
	if height, known := dkvs.verificationHeight(); known || height != 0 {
		t.Fatalf("wallet sync height became DKVS authority: %d %v", height, known)
	}
	dkvs.observeVerificationOptions(dkvsindexer.RecordVerificationOptions{Height: 25})
	manager.status.Lock()
	manager.status.SyncHeightL2 = 22
	manager.status.Unlock()
	if height, known := dkvs.verificationHeight(); !known || height != 25 {
		t.Fatalf("explicit height was not retained: %d %v", height, known)
	}
}

func TestDKVSManagerEndpointHeightReplacesCrossNetworkObservation(t *testing.T) {
	manager := &Manager{
		cfg:    &common.Config{Chain: "testnet"},
		status: &Status{CurrentChain: "testnet", SyncHeightL2: 3452},
	}
	dkvs := newDKVSManager(manager)
	dkvs.observeVerificationHeight(39791, true)
	dkvs.setEndpointVerificationHeight(3452, true)
	if height, known := dkvs.endpointVerificationHeight(); !known || height != 3452 {
		t.Fatalf("endpoint height did not replace cross-network observation: %d %v", height, known)
	}
}

func TestDKVSManagerEndpointHeightIsAuthoritativeOverSameChainStatus(t *testing.T) {
	manager := &Manager{
		cfg:    &common.Config{Chain: "testnet"},
		status: &Status{CurrentChain: "testnet", SyncHeightL2: 39420},
	}
	dkvs := newDKVSManager(manager)
	dkvs.setEndpointVerificationHeight(3467, true)
	if height, known := dkvs.verificationHeight(); !known || height != 3467 {
		t.Fatalf("same-chain status overrode endpoint height: %d %v", height, known)
	}
}

func TestDKVSManagerVerificationHeightRequiresEndpointBeforeEndpointSucceeds(t *testing.T) {
	manager := &Manager{
		cfg:    &common.Config{Chain: "testnet"},
		status: &Status{CurrentChain: "testnet", SyncHeightL2: 39420},
	}
	dkvs := newDKVSManager(manager)
	if height, known := dkvs.verificationHeight(); known || height != 0 {
		t.Fatalf("wallet sync height became pre-endpoint fallback: %d %v", height, known)
	}
}

func TestDKVSManagerCachedEndpointHeightSurvivesRefreshFailure(t *testing.T) {
	manager := newAccountStorageHeightTestManager(t, 7200, 0,
		errors.New("bestheight unavailable"), 39420)
	manager.dkvs.setEndpointVerificationHeight(3467, true)
	client, err := manager.dkvs.primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.dkvs.refreshVerificationBestHeight(client); err == nil {
		t.Fatal("endpoint refresh unexpectedly succeeded")
	}
	if height, known := manager.dkvs.verificationHeight(); !known || height != 3467 {
		t.Fatalf("failed refresh discarded cached endpoint height: %d %v", height, known)
	}
}

func TestDKVSExpiryEstimateUsesEndpointHeightForConfiguredTTL(t *testing.T) {
	const endpointHeight = uint64(3467)
	if got := estimatedDKVSExpiryHeight(endpointHeight, 144); got != 3611 {
		t.Fatalf("FREE_LOCAL expiry=%d, want 3611", got)
	}
	if got := estimatedDKVSExpiryHeight(endpointHeight, 7200); got != 10667 {
		t.Fatalf("paid expiry=%d, want 10667", got)
	}
}

func TestAccountStorageOptionsRefreshesSameEndpointHeight(t *testing.T) {
	manager := newAccountStorageHeightTestManager(t, 7200, 3467, nil, 39420)
	options, err := manager.GetAccountStorageOptions()
	if err != nil {
		t.Fatal(err)
	}
	if len(options) == 0 || options[0].EstimatedExpiryHeight != 10667 {
		t.Fatalf("temporary expiry=%d, want 10667", options[0].EstimatedExpiryHeight)
	}
}

func TestAccountStorageOptionsKeepsCachedEndpointHeightOnRefreshFailure(t *testing.T) {
	manager := newAccountStorageHeightTestManager(t, 7200, 0,
		errors.New("bestheight unavailable"), 39420)
	manager.dkvs.setEndpointVerificationHeight(3467, true)
	options, err := manager.GetAccountStorageOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got := options[0].EstimatedExpiryHeight; got != 10667 {
		t.Fatalf("temporary expiry=%d, want cached endpoint expiry 10667", got)
	}
}

func TestAccountStorageOptionsDoesNotUseWalletSyncHeightBeforeEndpointSuccess(t *testing.T) {
	manager := newAccountStorageHeightTestManager(t, 7200, 0,
		errors.New("bestheight unavailable"), 39420)
	options, err := manager.GetAccountStorageOptions()
	if err != nil {
		t.Fatal(err)
	}
	if got := options[0].EstimatedExpiryHeight; got != 7200 {
		t.Fatalf("temporary expiry=%d, want TTL-only estimate without endpoint height", got)
	}
}

func TestAccountStorageConfirmUsesSharedEndpointHeightEntry(t *testing.T) {
	manager := newAccountStorageHeightTestManager(t, 7200, 3467, nil, 39420)
	manager.wallet = NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", GetChainParam(),
	)
	authorization, err := manager.ConfirmAccountStorage(AccountStorageTemporary, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := authorization.Summary.EstimatedExpiryHeight; got != 10667 {
		t.Fatalf("confirmed expiry=%d, want 10667", got)
	}
}
