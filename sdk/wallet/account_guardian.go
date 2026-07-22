package wallet

import (
	"encoding/json"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *SatsNetDKVSClient) PutAccountGuardianCapsuleWithAutopay(owner common.Wallet, mailboxID string, capsule account.GuardianShareCapsule, options dkvsindexer.RecordOptions, autopay DKVSAutopayOptions) error {
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
	record, err := NewDKVSSignedRecord(owner, key, encoded, options)
	if err != nil {
		return err
	}
	if err := attachDKVSAutopayFeeProof(owner, record, autopay); err != nil {
		return err
	}
	_, err = p.PutMailboxShare(record)
	return err
}

func (p *SatsNetDKVSClient) GetMailboxShare(mailboxID, packageID, shareID string) (*swire.DKVSRecord, error) {
	key, err := dkvsindexer.MailShareKey(mailboxID, packageID, shareID)
	if err != nil {
		return nil, err
	}
	return p.GetVerifiedRecord(key, dkvsindexer.RecordVerificationOptions{ExpectedKey: key})
}
