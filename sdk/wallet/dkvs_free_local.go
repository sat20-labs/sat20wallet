package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func newDKVSAccountSignedRecordWithFreeLocal(owner common.Wallet, key string, value []byte,
	opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if owner == nil {
		return nil, fmt.Errorf("DKVS account owner is required")
	}
	record, err := dkvsindexer.NewAccountRecord(key, value, opts)
	if err != nil {
		return nil, err
	}
	parsed, err := dkvsindexer.ParseKey(key)
	if err != nil {
		return nil, err
	}
	proof, err := dkvsindexer.NewFreeLocalFeeProof(
		key, parsed.Namespace, uint32(dkvsindexer.RecordSize(record)), record.ExpiryHeight,
	)
	if err != nil {
		return nil, err
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		return nil, err
	}
	if err := SignDKVSAccountRecord(owner, record); err != nil {
		return nil, err
	}
	return record, nil
}

func (p *SatsNetDKVSClient) PutAccountSignedRecordWithFreeLocal(owner common.Wallet, key string,
	value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	record, err := newDKVSAccountSignedRecordWithFreeLocal(owner, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutRecord(record)
}

func (p *SatsNetDKVSClient) PutAccountPersonalRecordWithFreeLocal(owner common.Wallet, path string,
	value []byte, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	accountID, err := dkvsAccountID(owner)
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.AccountPersonalKey(accountID, path)
	if err != nil {
		return nil, err
	}
	return p.PutAccountSignedRecordWithFreeLocal(owner, key, value, opts)
}

func (p *SatsNetDKVSClient) PutAccountGuardianCapsuleWithFreeLocal(owner common.Wallet, mailboxID string,
	capsule account.GuardianShareCapsule, options dkvsindexer.RecordOptions) error {
	if owner == nil {
		return fmt.Errorf("guardian mailbox owner wallet is required")
	}
	encoded, err := json.Marshal(capsule)
	if err != nil {
		return err
	}
	if len(encoded) > account.MaxRecoveryObjectSize {
		return fmt.Errorf("guardian capsule exceeds DKVS value limit")
	}
	key, err := dkvsindexer.MailShareKey(mailboxID, capsule.PackageID, capsule.ShareID)
	if err != nil {
		return err
	}
	record, err := newDKVSAccountSignedRecordWithFreeLocal(owner, key, encoded, options)
	if err != nil {
		return err
	}
	_, err = p.PutMailboxShare(record)
	return err
}
