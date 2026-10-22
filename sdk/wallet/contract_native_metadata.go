package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Native assets share the runtime used by every public channel template.
// Metadata is either derived from local consensus validation or read from the
// already authenticated L2 registry. L1 indexers never invent RGB metadata.
func (p *ContractRuntimeBase) contractAssetTicker() *indexer.TickerInfo {
	if p.stp == nil || p.runtime == nil {
		return nil
	}
	name := p.runtime.GetAssetName()
	info := p.stp.GetTickerInfo(name)
	if name.Protocol == rgb11wallet.Protocol && info == nil && p.stp.GetWalletMgr() != nil {
		info = p.stp.GetWalletMgr().GetSendTickerInfo_SatsNet(name)
	}
	if info != nil && info.AssetName != *name {
		return nil
	}
	return info
}

func (p *ContractRuntimeBase) nativePublicContract() bool {
	if p.runtime == nil || p.runtime.GetAssetName().Protocol != rgb11wallet.Protocol {
		return false
	}
	switch p.runtime.GetTemplateName() {
	case TEMPLATE_CONTRACT_TRANSCEND, TEMPLATE_CONTRACT_AMM, TEMPLATE_CONTRACT_SWAP,
		TEMPLATE_CONTRACT_DAO, TEMPLATE_CONTRACT_FAUCET, TEMPLATE_CONTRACT_LAUNCHPOOL:
		return true
	}
	return false
}

type nativePublicMetadata struct {
	Version     int               `json:"version"`
	ContractURL string            `json:"contract_url"`
	Asset       indexer.AssetName `json:"asset"`
	Precision   int               `json:"precision"`
}

func (p *ContractRuntimeBase) nativePublicMetadataKey() []byte {
	h := sha256.Sum256([]byte(p.URL()))
	return []byte(GetDBKeyPrefix() + "native-public-metadata/" + _chain + "/" + hex.EncodeToString(h[:]))
}

func (p *ContractRuntimeBase) readNativePublicMetadata() (*nativePublicMetadata, error) {
	if p.db == nil {
		return nil, fmt.Errorf("native public metadata database unavailable")
	}
	raw, err := p.db.Read(p.nativePublicMetadataKey())
	if err != nil {
		return nil, err
	}
	var metadata nativePublicMetadata
	if json.Unmarshal(raw, &metadata) != nil || metadata.Version != 1 ||
		metadata.ContractURL != p.URL() || metadata.Asset != *p.runtime.GetAssetName() ||
		metadata.Precision < 0 || metadata.Precision > MAX_ASSET_DIVISIBILITY {
		return nil, fmt.Errorf("invalid native public metadata")
	}
	return &metadata, nil
}

func (p *ContractRuntimeBase) checkNativePublicLedgerPrecision(precision int) error {
	for _, item := range p.history {
		if item == nil || item.AssetName != p.runtime.GetAssetName().String() {
			continue
		}
		for _, amount := range []*Decimal{item.InAmt, item.RemainingAmt, item.OutAmt} {
			if amount != nil && amount.Sign() != 0 && amount.Precision != precision {
				return fmt.Errorf("native public ledger conflicts with genesis precision")
			}
		}
	}
	return nil
}

func (p *ContractRuntimeBase) applyNativePublicMetadata(metadata *nativePublicMetadata) error {
	if p.nativeMetadataInitialized && p.Divisibility != metadata.Precision {
		return fmt.Errorf("native public genesis precision cannot change")
	}
	if err := p.checkNativePublicLedgerPrecision(metadata.Precision); err != nil {
		return err
	}
	p.N, p.Divisibility = metadata.Precision, metadata.Precision
	p.dealDivisibility = min(metadata.Precision, DEAL_DIVISIBILITY)
	p.nativeMetadataInitialized = true
	return nil
}

func (p *ContractRuntimeBase) restoreNativePublicMetadata() error {
	if !p.nativePublicContract() {
		return nil
	}
	metadata, err := p.readNativePublicMetadata()
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return p.applyNativePublicMetadata(metadata)
}

// The caller supplies only a fully validated source receipt. Keeping a durable
// marker distinguishes unknown precision from a genuine zero-decimal genesis.
func (p *ContractRuntimeBase) initializeNativePublicMetadata(output *TxOutput) error {
	if !p.nativePublicContract() || output == nil || len(output.Assets) != 1 ||
		output.Assets[0].Name != *p.runtime.GetAssetName() || output.Assets[0].BindingSat != 0 {
		return fmt.Errorf("native receipt does not contain the contract's exact asset")
	}
	precision := output.Assets[0].Amount.Precision
	if precision < 0 || precision > MAX_ASSET_DIVISIBILITY {
		return fmt.Errorf("invalid native genesis precision")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	metadata, err := p.readNativePublicMetadata()
	if err == nil {
		if metadata.Precision != precision {
			return fmt.Errorf("native receipt conflicts with persisted genesis precision")
		}
		return p.applyNativePublicMetadata(metadata)
	}
	if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	if err := p.checkNativePublicLedgerPrecision(precision); err != nil {
		return err
	}
	metadata = &nativePublicMetadata{Version: 1, ContractURL: p.URL(), Asset: *p.runtime.GetAssetName(), Precision: precision}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if err := p.db.Write(p.nativePublicMetadataKey(), raw); err != nil {
		return err
	}
	return p.applyNativePublicMetadata(metadata)
}
