package wallet

import (
	"context"
	"errors"
	"strings"
	"time"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountManagedDataReadyTimeout = 30 * time.Second

var ErrAccountManagedDataImportIncomplete = errors.New("account-managed data import is incomplete; restore the wallet before uploading")

func accountRecordMatchesStorage(value *dkvsValue, profile *accountManagementProfile) bool {
	if value == nil || value.record == nil || profile == nil {
		return false
	}
	proof, err := dkvsindexer.ParseFeeProof(value.record.FeeProof)
	if err != nil {
		return false
	}
	switch profile.StorageMode {
	case AccountStorageTemporary:
		return proof.Mode == dkvsindexer.FeeModeFreeLocal &&
			value.record.TTL == profile.RecordTTL && profile.RecordTTL != 0
	case AccountStoragePaid:
		return proof.Mode == dkvsindexer.FeeModeAutopay && value.record.TTL == 0 &&
			strings.EqualFold(strings.TrimSpace(proof.PoolContract),
				strings.TrimSpace(profile.AutopayContract))
	default:
		return false
	}
}

// Confirmation follows the signed local receipt, without a network read.
func accountStorageConfirmed(value *accountLocalKV, profile *accountManagementProfile) bool {
	return value != nil && accountRecordMatchesStorage(&dkvsValue{record: value.Baseline}, profile)
}

func (p *Manager) WaitAccountManagedDataReady(ctx context.Context) error {
	return p.WaitAccountBackup(ctx, "", "")
}
