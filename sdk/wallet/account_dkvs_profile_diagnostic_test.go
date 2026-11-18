package wallet

import (
	"strings"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// The pre-1.1 state/blob profile diagnostic and its remote wrapper repair
// were retired: neither describes the independent indexed module protocol.

func retryDiagnosticDKVSWrite(write func() (*swire.DKVSRecord, error)) (*swire.DKVSRecord, error) {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		record, err := write()
		if err == nil {
			return record, nil
		}
		lastErr = err
		if !IsDKVSErrorCode(err, dkvsindexer.ErrorCodeWriteConflict) {
			return nil, err
		}
		time.Sleep(time.Duration(attempt+1) * 250 * time.Millisecond)
	}
	return nil, lastErr
}

func diagnosticExactRemoteRecord(client *SatsNetDKVSClient, prefix, key string) (*swire.DKVSRecord, error) {
	records, _, err := client.ListRecords(prefix, 0, 100)
	if err != nil {
		return nil, err
	}
	for _, record := range records {
		if record != nil && record.Key == key {
			return record, nil
		}
	}
	return nil, nil
}

func diagnosticLoadWallets(database *memoryKVDB) (map[int64]*WalletInfo, error) {
	wallets := make(map[int64]*WalletInfo)
	err := database.BatchRead([]byte(DB_KEY_WALLET), false, func(_, value []byte) error {
		var stored WalletInDB
		if err := DecodeFromBytes(value, &stored); err != nil {
			return err
		}
		wallets[stored.Id] = &WalletInfo{WalletInDB: stored}
		return nil
	})
	return wallets, err
}

func diagnosticKeyClass(key string) string {
	switch {
	case strings.HasSuffix(key, accountManagementProfileDBKey):
		return "account-profile"
	case strings.HasPrefix(key, DB_KEY_WALLET):
		return "encrypted-wallet"
	case strings.HasPrefix(key, string(dkvsOutboxPrefix)):
		return "dkvs-outbox"
	default:
		return "selected"
	}
}

func diagnosticAccountID(value string) string {
	if len(value) < 12 {
		return "invalid"
	}
	return value[:6] + "..." + value[len(value)-6:]
}

func diagnosticShort(value string) string {
	if len(value) < 10 {
		return "invalid"
	}
	return value[:5] + "..." + value[len(value)-5:]
}

func diagnosticRecordSeq(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.Seq
}

func diagnosticRecordHeight(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.IssueHeight
}

func diagnosticRecordTTL(record *swire.DKVSRecord) uint64 {
	if record == nil {
		return 0
	}
	return record.TTL
}

func diagnosticRecordHash(record *swire.DKVSRecord) string {
	if record == nil {
		return ""
	}
	return dkvsindexer.RecordHash(record).String()
}
