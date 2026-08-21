package wallet

import (
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

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

func TestDKVSManagerVerificationHeightUsesStatusAndExplicitMaximum(t *testing.T) {
	manager := &Manager{status: &Status{SyncHeightL2: 20}}
	dkvs := newDKVSManager(manager)
	if height, known := dkvs.verificationHeight(); !known || height != 20 {
		t.Fatalf("status height unavailable: %d %v", height, known)
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
