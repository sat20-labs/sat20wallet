package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// RegisterRGB11SignedContract submits an authority-signed public technical
// record to the Core business route. The route independently validates its
// contract; this client additionally requires an authenticated readback.
// Ordinary wallet clients have no Core signing key and cannot use this to
// register arbitrary names or private recovery data.
func (p *SatsNetDKVSClient) registerRGB11SignedContract(record *swire.DKVSRecord) (*rgb11wallet.RGB11Registration, error) {
	if record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	id := strings.TrimPrefix(record.Key, rgb11wallet.RGB11RegistryPath+"/")
	key, err := rgb11wallet.RGB11RegistryKey(id)
	if err != nil || key != record.Key {
		return nil, dkvsindexer.ErrInvalidKey
	}
	if _, err := rgb11wallet.VerifyRGB11RegistrationForClient(record, id, rgb11NetworkRegistryVerifier{}); err != nil {
		return nil, err
	}
	var response struct {
		ContractID string `json:"contract_id"`
	}
	if err := p.postDKVSApplication("/v3/rgb11/contracts/register", map[string]any{"record": record}, &response); err != nil {
		return nil, err
	}
	if response.ContractID != id {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	registration, value, err := p.getRGB11ContractWithVerifier(id, rgb11NetworkRegistryVerifier{})
	if err != nil {
		return nil, err
	}
	local, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	if err != nil || !bytes.Equal(value.ContractContent, local.ContractContent) {
		return nil, dkvsindexer.ErrWriteConflict
	}
	return registration, nil
}

// CommitRGB11Name signs the Anchor proof only after the authority endpoint
// has durably recorded and returned the same immutable short-name claim.
func (p *Manager) CommitRGB11Name(signer common.Wallet, ticker, id string, height uint64) error {
	key, err := dkvsindexer.RGB11NameKey(ticker)
	if err != nil {
		return err
	}
	value, err := dkvsindexer.RGB11NameValue(id)
	if err != nil {
		return err
	}
	client, err := p.accountDKVSClient()
	if err != nil {
		return err
	}
	for _, seq := range []uint64{dkvsindexer.RGB11NameClaimSeq, dkvsindexer.RGB11NameProofSeq} {
		record, err := NewDKVSSignedRecord(signer, key, value, dkvsindexer.RecordOptions{Seq: seq, IssueHeight: height})
		if err != nil {
			return err
		}
		if err := client.putRGB11Name(record, ticker, id); err != nil {
			return err
		}
	}
	return nil
}

func (p *SatsNetDKVSClient) putRGB11Name(record *swire.DKVSRecord, ticker, id string) error {
	if signedID, err := dkvsindexer.VerifyRGB11NameRecord(record, ticker); err != nil || signedID != id {
		return dkvsindexer.ErrInvalidRecord
	}
	var response struct {
		ContractID string `json:"contract_id"`
	}
	if err := p.postDKVSApplication("/v3/rgb11/contracts/register", map[string]any{"record": record}, &response); err != nil {
		return err
	}
	stored, err := p.GetRecordDirect(record.Key)
	if err != nil {
		return err
	}
	storedID, err := dkvsindexer.VerifyRGB11NameRecord(stored, ticker)
	if err != nil {
		return err
	}
	if response.ContractID != id || storedID != id || stored.Seq < record.Seq {
		return dkvsindexer.ErrWriteConflict
	}
	return nil
}

func (p *Manager) readRGB11Name(ticker string) (*swire.DKVSRecord, string, error) {
	return p.readRGB11NameCtx(nil, ticker)
}

func (p *Manager) readRGB11NameCtx(ctx context.Context, ticker string) (*swire.DKVSRecord, string, error) {
	record, id, err := p.readRGB11RegistryName(ctx, ticker)
	if err != nil {
		return nil, "", err
	}
	if record.Seq != dkvsindexer.RGB11NameProofSeq {
		return nil, "", dkvsindexer.ErrRecordNotFound
	}
	return record, id, nil
}

// A signed claim reserves the short name even before activation.
func (p *Manager) readRGB11RegistryName(ctx context.Context, ticker string) (*swire.DKVSRecord, string, error) {
	key, err := dkvsindexer.RGB11NameKey(ticker)
	if err != nil {
		return nil, "", err
	}
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, "", err
	}
	if ctx == nil {
		ctx = client.requestContext()
	}
	record, err := client.GetRecordDirectContext(ctx, key)
	if err != nil {
		return nil, "", err
	}
	id, err := dkvsindexer.VerifyRGB11NameRecord(record, ticker)
	return record, id, err
}

// rgb11NetworkRegistryVerifier uses the same locally configured, network-stable
// trust roots as SatoshiNet's CoreNode registry verifier. An HTTP endpoint may
// relay registry records, but cannot choose the keys that authenticate them.
type rgb11NetworkRegistryVerifier struct{}

func (rgb11NetworkRegistryVerifier) CanWriteSystem(key string, pubKey []byte) error {
	if !strings.HasPrefix(key, rgb11wallet.RGB11RegistryPath+"/") {
		return dkvsindexer.ErrPermissionDenied
	}
	signer := hex.EncodeToString(pubKey)
	if signer != indexercommon.GetCoreNodePubKey() {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

// GetRGB11Registration reads an authenticated public technical record.
// It does not establish a DID alias or give the submitting wallet issuer rights.
func (p *SatsNetDKVSClient) getRGB11Registration(contractID string) (*rgb11wallet.RGB11Registration, error) {
	return p.getRGB11RegWithVerifier(contractID, rgb11NetworkRegistryVerifier{})
}

// GetRGB11RegistrationWithVerifier supports explicitly configured registry
// authorities. The verifier must come from trusted local network configuration,
// never from the endpoint response. A nil verifier fails closed.
func (p *SatsNetDKVSClient) getRGB11RegWithVerifier(contractID string,
	verifier dkvsindexer.SystemVerifier) (*rgb11wallet.RGB11Registration, error) {
	registration, _, err := p.getRGB11ContractWithVerifier(contractID, verifier)
	return registration, err
}

func (p *SatsNetDKVSClient) getRGB11ContractWithVerifier(contractID string,
	verifier dkvsindexer.SystemVerifier) (*rgb11wallet.RGB11Registration, *rgb11wallet.RGB11ContractValue, error) {

	if verifier == nil {
		return nil, nil, dkvsindexer.ErrPermissionDenied
	}
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	key, err := rgb11wallet.RGB11RegistryKey(contractID)
	if err != nil {
		return nil, nil, err
	}
	// Public technical records are independently readable. They are not part
	// of an account-managed replica subscription, so a local cache miss must
	// not be reported as an unsynchronized private DKVS path.
	record, err := p.GetRecordDirect(key)
	if err != nil {
		return nil, nil, err
	}
	registration, err := rgb11wallet.VerifyRGB11RegistrationForClient(record, contractID, verifier)
	if err != nil {
		return nil, nil, err
	}
	value, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	if err != nil {
		return nil, nil, err
	}
	return registration, value, nil
}
