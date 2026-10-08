package wallet

import (
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// PutPrimaryDID stores only the selected canonical DID in the account's
// owner-exclusive personal namespace. DID ownership is validated by the
// SatoshiNet DKVS indexer against the L1 Ordinals resolver.
func (p *SatsNetDKVSClient) PutPrimaryDID(owner common.Wallet, did string,
	opts dkvsindexer.RecordOptions, autopay *DKVSAutopayOptions) (*swire.DKVSRecord, error) {

	if err := dkvsindexer.ValidatePrimaryDIDName(did); err != nil {
		return nil, err
	}
	return p.PutAccountPersonalRecord(owner, dkvsindexer.PrimaryDIDPersonalPath, []byte(did), opts, autopay)
}

// GetPrimaryDID returns the account-owned personal selection. It does not claim
// current DID ownership by itself; callers making an authorization decision
// must resolve the DID against the L1 indexer at that time.
func (p *SatsNetDKVSClient) GetPrimaryDID(pubKey []byte) (string, *swire.DKVSRecord, error) {
	record, err := p.GetPersonalRecord(pubKey, dkvsindexer.PrimaryDIDPersonalPath)
	if err != nil {
		return "", nil, err
	}
	did := string(record.Value)
	if err := dkvsindexer.ValidatePrimaryDIDName(did); err != nil {
		return "", nil, err
	}
	return did, record, nil
}
