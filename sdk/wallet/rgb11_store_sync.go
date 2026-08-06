package wallet

import (
	"encoding/json"
	"errors"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func rgb11SnapshotHasState(snapshot *RGB11WalletSnapshot) bool {
	return snapshot != nil && (len(snapshot.ProjectionRecords) != 0 ||
		len(snapshot.EngineRecords) != 0)
}

// importRGB11WalletSnapshot restores only module-owned local state. DKVS,
// retention, AUTOPAY and wallet/account catalog lifecycle are owned by account
// management and are deliberately absent from this method.
func (p *rgb11Manager) importRGB11WalletSnapshot(snapshot *RGB11WalletSnapshot) error {
	if p == nil || snapshot == nil || p.rgbManager == nil ||
		p.rgbManager.engineStore == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	if len(snapshot.ProjectionRecords) != 0 || len(snapshot.EngineRecords) != 0 {
		if err := rgb11wallet.ValidateWalletSnapshot(snapshot); err != nil {
			return err
		}
	}
	tickerInfos, err := p.tickerInfosFromRGB11Snapshot(snapshot)
	if err != nil {
		return err
	}
	if err := p.rgbManager.engineStore.ImportSnapshot(snapshot.EngineRecords); err != nil {
		return err
	}
	if err := p.rgbManager.projectionStore.ImportSnapshot(snapshot.ProjectionRecords); err != nil {
		return err
	}
	for _, info := range tickerInfos {
		if err := p.RegisterRGB11TickerInfo(info); err != nil {
			return err
		}
	}
	p.rgbManager.consistencyStatus = "ok"
	return nil
}

func (p *rgb11Manager) tickerInfosFromRGB11Snapshot(snapshot *RGB11WalletSnapshot) ([]*indexer.TickerInfo, error) {
	if p == nil || snapshot == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	refs, err := rgb11wallet.TickerRefsFromProjectionSnapshot(snapshot.ProjectionRecords)
	if err != nil {
		return nil, err
	}
	infos := make([]*indexer.TickerInfo, 0, len(refs))
	for _, ref := range refs {
		name := indexer.NewAssetNameFromString(ref.AssetName)
		if name.Protocol != rgb11wallet.Protocol || name.String() != ref.AssetName {
			return nil, ErrRGB11Inconsistent
		}
		if info := p.getTickerInfo(name); info != nil {
			var ext rgb11wallet.TickerExt
			if json.Unmarshal(info.Content, &ext) != nil || ext.ContractID != ref.ContractID {
				return nil, ErrRGB11Inconsistent
			}
			continue
		}
		raw, receipt, err := rgb11wallet.ContractObjectForTickerRef(snapshot.ProjectionRecords, ref)
		if err != nil {
			return nil, err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil || container.ContractID != ref.ContractID {
			return nil, ErrRGB11Inconsistent
		}
		info, err := rgb11TickerInfoFromValidatedContract(container, receipt)
		if err != nil || info.AssetName.String() != ref.AssetName {
			return nil, ErrRGB11Inconsistent
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (p *Manager) registerDKVSDomainObservers() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.addObserver(func(_ []string) {
		if err := p.SyncAccountManagementState(nil); err != nil &&
			!errors.Is(err, ErrDKVSPathNotSynced) &&
			!errors.Is(err, ErrDKVSRecordNotFound) {
			Log.Warningf("apply account management replica failed: %v", err)
		}
	})
}
