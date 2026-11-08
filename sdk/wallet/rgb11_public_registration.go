package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var (
	ErrRGB11PublicRegistrationRequired = errors.New("RGB11 public registration required")
	ErrRGB11PublicDataUnavailable      = errors.New("RGB11 public data unavailable")
	ErrRGB11PublicIdentityMismatch     = errors.New("RGB11 public identity mismatch")
)

func (p *Manager) rgb11PublicRegistryVerifier() dkvsindexer.SystemVerifier {
	if p != nil && p.rgb11RegistryVerifier != nil {
		return p.rgb11RegistryVerifier
	}
	return rgb11NetworkRegistryVerifier{}
}

// requireRGB11PublicRegistration checks a signed, independently readable
// technical record before an RGB business operation reserves funds. Naming
// aliases do not authorize an asset or supply its ContractID.
func (p *Manager) requireRGB11PublicRegistration(asset *swire.AssetName) error {
	if asset == nil || asset.Protocol != rgb11wallet.Protocol {
		return nil
	}
	// The first transcend deployment precedes L2 ticker creation. The current
	// protocol key is the full ContractID hex, so the authenticated public
	// record supplies its identity even when no local ticker exists yet.
	contractID := asset.Ticker
	if _, err := rgb11wallet.RGB11RegistryKey(contractID); err != nil {
		return ErrRGB11PublicIdentityMismatch
	}
	client, err := p.ensureDKVSManager().primaryClient()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	registration, _, err := client.getRGB11ContractWithVerifier(contractID, p.rgb11PublicRegistryVerifier())
	if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrRGB11PublicRegistrationRequired, contractID)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	if registration.ContractID != contractID || registration.AssetType != asset.Type {
		return ErrRGB11PublicIdentityMismatch
	}
	info := p.getTickerInfo(asset)
	if info == nil {
		return nil
	}
	if info.AssetName != *asset {
		return ErrRGB11PublicIdentityMismatch
	}
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil || ext.ContractID == "" ||
		ext.SchemaID == "" || !rgb11wallet.ContractAssetKeyMatches(*asset, ext.ContractID) {
		return ErrRGB11PublicIdentityMismatch
	}
	base, err := rgb11wallet.NormalizeRGB11Ticker(ext.Ticker)
	if err != nil || registration.SchemaID != ext.SchemaID || registration.BaseTicker != base {
		return ErrRGB11PublicIdentityMismatch
	}
	return nil
}

// verifyRGB11PublicContract compares the independently authenticated shared
// contract with the private recovery object's public projection. A public
// record with the same ContractID but different content is a hard conflict.
func (p *Manager) verifyRGB11PublicContract(contractID string, private []byte) error {
	public, err := rgb11wallet.PublicRGB11Contract(private)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11PublicIdentityMismatch, err)
	}
	client, err := p.ensureDKVSManager().primaryClient()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	registration, value, err := client.getRGB11ContractWithVerifier(contractID, p.rgb11PublicRegistryVerifier())
	if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrRGB11PublicRegistrationRequired, contractID)
	}
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	if registration.ContractID != contractID || !bytes.Equal(value.ContractContent, public) {
		return ErrRGB11PublicIdentityMismatch
	}
	return nil
}

// The ordinary wallet submits raw public material to its CoreNode. The Core
// validates it and signs the registry record; the wallet never signs as an
// authority and never sends the indexer-only {record} request by mistake.
func (p *NodeClient) RegisterPublicRGB11(content []byte) (*rgb11wallet.RGB11Registration, error) {
	if p == nil || p.RESTClient == nil || p.Http == nil {
		return nil, ErrRGB11PublicDataUnavailable
	}
	public, err := rgb11wallet.PublicRGB11Contract(content)
	if err != nil || !bytes.Equal(public, content) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	_, expected, err := rgb11wallet.NewRGB11ContractValue(public)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		Content string `json:"contract_consignment_base64"`
	}{
		Content: base64.StdEncoding.EncodeToString(public),
	})
	if err != nil {
		return nil, err
	}
	raw, err := p.Http.SendPostRequest(p.GetUrl("/v3/rgb11/contracts/register"), body)
	if err != nil {
		return nil, err
	}
	var response struct {
		Code         int                            `json:"code"`
		Msg          string                         `json:"msg"`
		Registration *rgb11wallet.RGB11Registration `json:"registration"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, fmt.Errorf("Core RGB11 registration: %s", response.Msg)
	}
	if response.Registration == nil || *response.Registration != *expected {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return response.Registration, nil
}

func (p *Manager) ensureRGB11PublicContract(contractID string, private []byte) error {
	err := p.verifyRGB11PublicContract(contractID, private)
	if err == nil || !errors.Is(err, ErrRGB11PublicRegistrationRequired) {
		return err
	}
	public, err := rgb11wallet.PublicRGB11Contract(private)
	if err != nil {
		return err
	}
	p.mutex.RLock()
	node := p.getAccountServiceNode()
	p.mutex.RUnlock()
	if node == nil {
		return ErrRGB11PublicDataUnavailable
	}
	registrar, ok := node.RPCClient().(interface {
		RegisterPublicRGB11([]byte) (*rgb11wallet.RGB11Registration, error)
	})
	if !ok {
		return ErrRGB11PublicDataUnavailable
	}
	if _, err := registrar.RegisterPublicRGB11(public); err != nil {
		return err
	}
	// A successful submit response alone does not complete a private backup.
	return p.verifyRGB11PublicContract(contractID, private)
}

// VerifyRGB11PublicRegistration is a read-only SDK preflight for operations
// whose current protocol AssetName is known. It cannot register data; Core
// must independently enforce the same precondition at its own entry.
func (p *Manager) VerifyRGB11PublicRegistration(assetName string) error {
	asset := ParseAssetString(assetName)
	if asset == nil || asset.Protocol != rgb11wallet.Protocol {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	return p.requireRGB11PublicRegistration(asset)
}

// VerifyRGB11PublicContract verifies the exact public contract against an
// authenticated Core record. Unlike asset-name preflight it works before the
// transcend asset has ticker metadata on SatoshiNet.
func (p *Manager) VerifyRGB11PublicContract(contractID, contentBase64 string) (*rgb11wallet.RGB11Registration, error) {
	id, err := consensus.ParseContractID(contractID)
	if err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	public, err := base64.StdEncoding.Strict().DecodeString(contentBase64)
	if err != nil {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	canonical, err := rgb11wallet.PublicRGB11Contract(public)
	if err != nil || !bytes.Equal(canonical, public) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	_, expected, err := rgb11wallet.NewRGB11ContractValue(public)
	contractHex := hex.EncodeToString(id[:])
	if err != nil || expected.ContractID != contractHex {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	client, err := p.ensureDKVSManager().primaryClient()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	registration, value, err := client.getRGB11ContractWithVerifier(contractHex, p.rgb11PublicRegistryVerifier())
	if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return nil, ErrRGB11PublicRegistrationRequired
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRGB11PublicDataUnavailable, err)
	}
	if *registration != *expected || !bytes.Equal(value.ContractContent, public) {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return registration, nil
}

// verifyRGB11PublicRecoveryReferences runs outside local state locks, just
// before a private account bundle is submitted. It never publishes a missing
// public record itself. The private object remains in the recovery package.
func (p *Manager) verifyRGB11PublicRecoveryReferences(bundle account.ManagedDataBundle) error {
	return checkRGB11PublicReferences(bundle, p.verifyRGB11PublicContract)
}

func (p *Manager) prepareRGB11PublicBackup(bundle account.ManagedDataBundle) error {
	return checkRGB11PublicReferences(bundle, p.ensureRGB11PublicContract)
}

func checkRGB11PublicReferences(bundle account.ManagedDataBundle, check func(string, []byte) error) error {
	for _, item := range bundle.Items {
		if item.Provider != rgb11AccountManagedProviderID {
			continue
		}
		packageValue, err := rgb11wallet.DecodeRecoveryPackage(item.Payload)
		if err != nil {
			return fmt.Errorf("%w: invalid recovery package: %v", ErrRGB11PublicIdentityMismatch, err)
		}
		objects := make(map[string][]byte)
		contractRefs := make(map[string]struct{})
		for _, record := range packageValue.ProjectionRecords {
			if strings.HasPrefix(record.Key, "object-") {
				objects[strings.TrimPrefix(record.Key, "object-")] = record.Value
			}
		}
		for _, record := range packageValue.ProjectionRecords {
			if !strings.HasPrefix(record.Key, "contract-object-") {
				continue
			}
			private := objects[string(record.Value)]
			contract, err := consignment.DecodeFile(private)
			if err != nil || contract.Armor == nil || contract.Armor.Type != "contract" {
				return ErrRGB11PublicIdentityMismatch
			}
			wantHash := sha256.Sum256([]byte(contract.ContractID))
			if record.Key != "contract-object-"+hex.EncodeToString(wantHash[:]) {
				return ErrRGB11PublicIdentityMismatch
			}
			contractRefs[record.Key] = struct{}{}
			id, err := consensus.ParseContractID(contract.ContractID)
			if err != nil {
				return ErrRGB11PublicIdentityMismatch
			}
			if err := check(hex.EncodeToString(id[:]), private); err != nil {
				return err
			}
		}
		// A valid recovery package can retain proof/receipt history even if a
		// contract-object root was omitted. Such a bundle must not be ACKed as
		// independently restorable public identity material.
		receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(packageValue.ProjectionRecords)
		if err != nil {
			return fmt.Errorf("%w: invalid recovery receipt: %v", ErrRGB11PublicIdentityMismatch, err)
		}
		for _, receipt := range receipts {
			contractHash := sha256.Sum256([]byte(receipt.ContractID))
			if _, ok := contractRefs["contract-object-"+hex.EncodeToString(contractHash[:])]; !ok {
				return fmt.Errorf("%w: missing public reference for %s", ErrRGB11PublicIdentityMismatch, receipt.ContractID)
			}
		}
	}
	return nil
}
