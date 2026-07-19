package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coresync "github.com/sat20-labs/rgb11/sync"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"strings"
	"sync"
)

var (
	ErrRGB11Inconsistent   = errors.New("RGB11 wallet state is inconsistent")
	ErrRGB11STPUnavailable = errors.New("RGB11 is L1-only until full STP support is available")
)

// RGB11Manager owns the wallet-local RGB11 runtime and synchronization state.
// The outer Manager remains responsible for shared wallet, indexer and UTXO
// services, while exposing the existing public RGB11 API.
type RGB11Manager struct {
	projectionStore   *rgb11wallet.ProjectionStore
	engineStore       *rgb11wallet.EngineStore
	engine            *corewallet.Engine
	evidence          rgb11wallet.BitcoinEvidenceProvider
	rejectLists       RGB11RejectListProvider
	consistencyStatus string
	dkvsStatus        string
	head              *coresync.WalletHead
	autoBackup        *RGB11AutoBackupPolicy
	backupMutex       sync.Mutex
}

func newRGB11Manager(database indexer.KVDB, locker *UtxoLocker,
	evidence rgb11wallet.BitcoinEvidenceProvider) (*RGB11Manager, error) {
	projectionStore := rgb11wallet.NewProjectionStore(database, locker)
	engineStore := rgb11wallet.NewEngineStore(database)
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		return nil, err
	}
	return &RGB11Manager{
		projectionStore: projectionStore,
		engineStore:     engineStore,
		engine:          engine,
		evidence:        evidence,
	}, nil
}

func rejectRGB11STPAsset(asset *indexer.AssetName) error {
	if asset != nil && asset.Protocol == rgb11wallet.Protocol {
		return ErrRGB11STPUnavailable
	}
	return nil
}

func (p *Manager) GetRGB11ProjectionStore() *rgb11wallet.ProjectionStore {
	if p == nil || p.rgbManager == nil {
		return nil
	}
	return p.rgbManager.projectionStore
}

func (p *Manager) selectRGB11Scope() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil || p.status == nil {
		return rgb11wallet.ErrWalletScope
	}
	scope := fmt.Sprintf("wallet-%d-account-%d", p.status.CurrentWallet, p.status.CurrentAccount)
	if err := p.rgbManager.projectionStore.SetScope(scope); err != nil {
		return err
	}
	if err := p.rgbManager.engineStore.SetScope(scope); err != nil {
		return err
	}
	p.rgbManager.head = nil
	p.rgbManager.autoBackup = nil
	encoded, err := p.rgbManager.projectionStore.LoadLocalMetadata("wallet-head")
	if err != nil {
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
	} else {
		var head coresync.WalletHead
		expectedWalletID := ""
		if p.wallet != nil && p.wallet.GetPubKey() != nil {
			expectedWalletID = hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed())
		}
		if json.Unmarshal(encoded, &head) != nil || head.Validate(expectedWalletID) != nil {
			p.rgbManager.dkvsStatus = "conflict"
		} else {
			p.rgbManager.head = &head
		}
	}
	policy, err := p.loadRGB11AutoBackupPolicy()
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	if err == nil {
		p.rgbManager.autoBackup = policy
	}
	return nil
}

func (p *Manager) rebuildRGB11Locks() error {
	outputs, err := p.rgbManager.projectionStore.ListOutputs()
	if err != nil {
		p.rgbManager.consistencyStatus = "broken"
		return err
	}
	for _, output := range outputs {
		for _, asset := range output.Assets {
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			if err := p.rgbManager.projectionStore.AssertConsistent(output.OutPointStr, asset.Name); err != nil {
				_ = p.utxoLockerL1.LockUtxo(output.OutPointStr, rgb11wallet.LockReasonRGB)
				p.rgbManager.consistencyStatus = "broken"
				return fmt.Errorf("%w: %v", ErrRGB11Inconsistent, err)
			}
			if err := p.utxoLockerL1.LockUtxo(output.OutPointStr, rgb11wallet.LockReasonRGB); err != nil {
				p.rgbManager.consistencyStatus = "broken"
				return err
			}
			if p.rgbManager.evidence == nil {
				p.rgbManager.consistencyStatus = "warning"
				return fmt.Errorf("RGB11 Bitcoin evidence provider is unavailable")
			}
			outspend, err := p.rgbManager.evidence.GetOutspend(output.OutPointStr)
			if err != nil {
				p.rgbManager.consistencyStatus = "warning"
				return fmt.Errorf("verify RGB11 carrier %s: %w", output.OutPointStr, err)
			}
			if outspend.Spent {
				p.rgbManager.consistencyStatus = "broken"
				return fmt.Errorf("%w: RGB11 carrier %s was spent by %s", ErrRGB11Inconsistent, output.OutPointStr, outspend.SpendingTx)
			}
		}
	}
	p.rgbManager.consistencyStatus = "ok"
	return nil
}

func (p *Manager) RebuildRGB11Locks() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	return p.rebuildRGB11Locks()
}

func (p *Manager) GetRGB11ConsistencyStatus() string {
	if p == nil || p.rgbManager == nil || p.rgbManager.consistencyStatus == "" {
		return "warning"
	}
	return p.rgbManager.consistencyStatus
}

func (p *Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || asset == nil || proof == nil || outpoint == "" {
		return rgb11wallet.ErrProjectionMismatch
	}
	if p.rgbManager.evidence == nil {
		return fmt.Errorf("RGB11 Bitcoin evidence provider is unavailable")
	}
	evidence, err := p.rgbManager.evidence.GetUTXO(outpoint)
	if err != nil {
		return err
	}
	if evidence == nil {
		return fmt.Errorf("%w: Bitcoin outpoint %s not found", ErrRGB11Inconsistent, outpoint)
	}
	output := indexer.NewTxOutput(evidence.Value)
	output.OutPointStr = outpoint
	output.OutValue.PkScript = append([]byte(nil), evidence.PkScript...)
	proof.Confirmations = evidence.Confirmations
	return p.rgbManager.projectionStore.CommitProjection(output, asset, proof)
}

// getL1TxOutput is the mandatory composition point for transaction builders:
// public Bitcoin facts come from the Indexer, while locally validated RGB11
// allocations are overlaid from the wallet DB.
func (p *Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	base, err := p.l1IndexerClient.GetTxOutput(outpoint)
	if err != nil || base == nil || p.rgbManager.projectionStore == nil {
		return base, err
	}
	projected, err := p.rgbManager.projectionStore.LoadOutput(outpoint)
	if err != nil {
		// No local RGB11 projection is the normal case.
		return base, nil
	}
	result := base.Clone()
	for _, asset := range projected.Assets {
		if asset.Name.Protocol != rgb11wallet.Protocol {
			continue
		}
		if err := p.rgbManager.projectionStore.AssertConsistent(outpoint, asset.Name); err != nil {
			_ = p.utxoLockerL1.LockUtxo(outpoint, rgb11wallet.LockReasonRGB)
			return nil, fmt.Errorf("%w: %v", ErrRGB11Inconsistent, err)
		}
		result.RemoveAsset(&asset.Name)
		if err := result.Assets.Add(&asset); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (p *Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ListOutputs()
}

func (p *Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	if name == nil || name.Protocol != rgb11wallet.Protocol || p.rgbManager.projectionStore == nil {
		return nil, rgb11wallet.ErrInvalidRGB11Asset
	}
	return p.rgbManager.projectionStore.Balance(*name)
}

func (p *Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
	if info == nil || info.AssetName.Protocol != rgb11wallet.Protocol {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	if _, err := rgb11wallet.OfficialAssetID(info.AssetName); err != nil {
		return err
	}
	if err := saveTickerInfo(p.db, info); err != nil {
		return err
	}
	p.mutex.Lock()
	p.tickerInfoMap[info.AssetName.String()] = info
	p.mutex.Unlock()
	return nil
}

// RGB11State exposes existing SAT20 asset/output models plus RGB-only proof
// sidecars. Assets is rebuilt from Outputs and is never a second writable
// balance ledger.
type RGB11State struct {
	Initialized       bool                           `json:"initialized"`
	SyncStatus        string                         `json:"sync_status"`
	ConsistencyStatus string                         `json:"consistency_status"`
	DKVSStatus        string                         `json:"dkvs_status"`
	AutoBackupEnabled bool                           `json:"auto_backup_enabled"`
	TickerInfos       []*indexer.TickerInfo          `json:"ticker_infos"`
	Assets            indexer.TxAssets               `json:"assets"`
	Outputs           []*indexer.TxOutput            `json:"outputs"`
	Proofs            []*rgb11wallet.AllocationProof `json:"proofs"`
	Transfers         []*rgb11wallet.TransferState   `json:"transfers"`
}

func (p *Manager) GetRGB11State() (*RGB11State, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	outputs, err := p.rgbManager.projectionStore.ListOutputs()
	if err != nil {
		return nil, err
	}
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	proofIndex := make(map[string]*rgb11wallet.AllocationProof, len(proofs))
	for _, proof := range proofs {
		proofIndex[fmt.Sprintf("%s|%s", proof.OutPoint, proof.AssetName.String())] = proof
	}

	var assets indexer.TxAssets
	for _, output := range outputs {
		for index := range output.Assets {
			asset := &output.Assets[index]
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			if _, ok := proofIndex[fmt.Sprintf("%s|%s", output.OutPointStr, asset.Name.String())]; !ok {
				return nil, fmt.Errorf("%w: proof missing for %s %s", ErrRGB11Inconsistent, output.OutPointStr, asset.Name.String())
			}
			if err := assets.Add(asset); err != nil {
				return nil, err
			}
		}
	}

	p.mutex.RLock()
	tickers := make([]*indexer.TickerInfo, 0)
	for _, info := range p.tickerInfoMap {
		if info != nil && info.AssetName.Protocol == rgb11wallet.Protocol {
			tickers = append(tickers, info)
		}
	}
	p.mutex.RUnlock()

	dkvsStatus := p.rgbManager.dkvsStatus
	if dkvsStatus == "" {
		dkvsStatus = "offline"
	}
	p.mutex.RLock()
	autoBackupEnabled := p.rgbManager.autoBackup != nil && p.rgbManager.autoBackup.Enabled
	p.mutex.RUnlock()
	return &RGB11State{
		Initialized:       true,
		SyncStatus:        "idle",
		ConsistencyStatus: p.GetRGB11ConsistencyStatus(),
		DKVSStatus:        dkvsStatus,
		AutoBackupEnabled: autoBackupEnabled,
		TickerInfos:       tickers,
		Assets:            assets,
		Outputs:           outputs,
		Proofs:            proofs,
		Transfers:         transfers,
	}, nil
}

func (p *Manager) rgb11CarrierBinding(allocation rgb11wallet.ValidatedAllocation,
	utxo *rgb11wallet.BitcoinUTXO) (*rgb11wallet.CarrierBinding, error) {
	if p == nil || p.wallet == nil || utxo == nil || allocation.OutPoint != utxo.OutPoint {
		return nil, rgb11wallet.ErrInvalidProof
	}
	method := allocation.CommitmentMethod
	if method == "" {
		method = "genesis"
	}
	derivationIndex := p.wallet.GetSubAccount()
	logicalAddress := p.wallet.GetAddressByIndex(derivationIndex)
	if logicalAddress == "" || logicalAddress != p.wallet.GetAddress() {
		return nil, fmt.Errorf("RGB11 carrier derivation index %d is not the active BIP86 subaccount", derivationIndex)
	}
	binding := &rgb11wallet.CarrierBinding{
		DerivationIndex:  derivationIndex,
		LogicalAddress:   logicalAddress,
		OutPoint:         allocation.OutPoint,
		ActualPkScript:   append([]byte(nil), utxo.PkScript...),
		InternalPubKey:   append([]byte(nil), allocation.CarrierInternalKey...),
		TapretRoot:       append([]byte(nil), allocation.TapretRoot...),
		TapretProof:      append([]byte(nil), allocation.TapretProof...),
		CommitmentMethod: method,
	}
	if txscript.IsPayToTaproot(utxo.PkScript) && len(utxo.PkScript) == 34 {
		binding.ActualOutputKey = append([]byte(nil), utxo.PkScript[2:]...)
	}
	if method == "tapret1st" {
		if len(binding.InternalPubKey) != 32 || len(binding.TapretRoot) != 32 || len(binding.ActualOutputKey) != 32 {
			return nil, rgb11wallet.ErrInvalidProof
		}
		internal, err := btcec.ParsePubKey(append([]byte{0x02}, binding.InternalPubKey...))
		if err != nil {
			return nil, err
		}
		output := txscript.ComputeTaprootOutputKey(internal, binding.TapretRoot)
		expected, err := txscript.PayToTaprootScript(output)
		if err != nil || !bytes.Equal(expected, binding.ActualPkScript) {
			return nil, fmt.Errorf("RGB11 Tapret carrier binding does not match %s", allocation.OutPoint)
		}
	}
	return binding, nil
}

func (p *Manager) ownsRGB11Carrier(binding *rgb11wallet.CarrierBinding, walletScript []byte) bool {
	if p == nil || p.wallet == nil || binding == nil {
		return false
	}
	if binding.DerivationIndex != p.wallet.GetSubAccount() ||
		binding.LogicalAddress == "" || binding.LogicalAddress != p.wallet.GetAddress() ||
		binding.LogicalAddress != p.wallet.GetAddressByIndex(binding.DerivationIndex) {
		return false
	}
	if binding.CommitmentMethod != "tapret1st" {
		return bytes.Equal(binding.ActualPkScript, walletScript)
	}
	pubkey := p.wallet.GetPubKeyByIndex(binding.DerivationIndex)
	if pubkey == nil {
		return false
	}
	compressed := pubkey.SerializeCompressed()
	return len(compressed) == 33 && bytes.Equal(binding.InternalPubKey, compressed[1:])
}

// rgb11BitcoinEvidenceRPC is intentionally separate from IndexerRPCClient.
// Production clients implement it with the /v3/bitcoin evidence endpoints;
// legacy test doubles can continue using the narrow compatibility fallback.
type rgb11BitcoinEvidenceRPC interface {
	GetBitcoinUTXOStatus(outpoint string) (*indexerwire.BitcoinUTXOStatus, error)
	GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx, error)
	GetBitcoinTxStatus(txid string) (*indexerwire.BitcoinTxStatus, error)
	GetBitcoinOutspend(outpoint string) (*indexerwire.BitcoinOutspend, error)
	GetBitcoinTip() (*indexerwire.BitcoinTip, error)
	BroadcastBitcoinTx(rawTx []byte) (string, error)
}

// indexerBitcoinEvidenceProvider is the single Wallet-side adapter for public
// Bitcoin facts. RGB11 validation never treats Indexer asset projections as
// authoritative state.
type indexerBitcoinEvidenceProvider struct {
	client IndexerRPCClient
}

func newIndexerBitcoinEvidenceProvider(client IndexerRPCClient) rgb11wallet.BitcoinEvidenceProvider {
	return &indexerBitcoinEvidenceProvider{client: client}
}

func (p *indexerBitcoinEvidenceProvider) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		status, err := client.GetBitcoinUTXOStatus(outpoint)
		if err != nil {
			return nil, err
		}
		if status == nil || !status.Exists || !status.Unspent {
			return nil, fmt.Errorf("Bitcoin outpoint %s is not an unspent output", outpoint)
		}
		script, err := hex.DecodeString(status.PkScript)
		if err != nil {
			return nil, err
		}
		return &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: status.Value, PkScript: script,
			Confirmations: status.Confirmations,
		}, nil
	}
	output, err := p.client.GetTxOutput(outpoint)
	if err != nil {
		return nil, err
	}
	if output == nil {
		return nil, fmt.Errorf("Bitcoin outpoint %s not found", outpoint)
	}
	confirmations := int64(0)
	parts := strings.Split(outpoint, ":")
	if len(parts) == 2 {
		if status, statusErr := p.GetTxStatus(parts[0]); statusErr == nil {
			confirmations = status.Confirmations
		}
	}
	return &rgb11wallet.BitcoinUTXO{
		OutPoint:      outpoint,
		Value:         output.OutValue.Value,
		PkScript:      append([]byte(nil), output.OutValue.PkScript...),
		Confirmations: confirmations,
	}, nil
}

func (p *indexerBitcoinEvidenceProvider) GetRawTx(txid string) ([]byte, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinRawTx(txid)
		if err != nil {
			return nil, err
		}
		return hex.DecodeString(item.RawTx)
	}
	raw, err := p.client.GetRawTx(txid)
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(raw)
}

func (p *indexerBitcoinEvidenceProvider) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinTxStatus(txid)
		if err != nil {
			return nil, err
		}
		if item == nil {
			return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
		}
		return &rgb11wallet.BitcoinTxStatus{
			TxID: item.TxID, InMempool: item.InMempool, Confirmed: item.Confirmed,
			BlockHeight: item.BlockHeight, BlockHash: item.BlockHash,
			Confirmations: item.Confirmations,
		}, nil
	}
	info, err := p.client.GetTxInfo(txid)
	if err != nil {
		return nil, err
	}
	status := &rgb11wallet.BitcoinTxStatus{
		TxID:          txid,
		InMempool:     info.Confirmations == 0,
		Confirmed:     info.Confirmations > 0,
		BlockHeight:   info.BlockHeight,
		Confirmations: int64(info.Confirmations),
	}
	if status.Confirmed {
		status.BlockHash, err = p.client.GetBlockHash(int(info.BlockHeight))
		if err != nil {
			return nil, err
		}
	}
	return status, nil
}

func (p *indexerBitcoinEvidenceProvider) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinOutspend(outpoint)
		if err != nil {
			return nil, err
		}
		if item == nil || !item.Exists {
			return nil, fmt.Errorf("Bitcoin outpoint %s does not exist", outpoint)
		}
		spendingTx := item.SpendingTx
		if item.Spent && spendingTx == "" {
			spendingTx = "unknown"
		}
		return &rgb11wallet.BitcoinOutspend{Spent: item.Spent, SpendingTx: spendingTx}, nil
	}
	txid, err := p.client.GetUtxoSpentTx(outpoint)
	if err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinOutspend{Spent: txid != "", SpendingTx: txid}, nil
}

func (p *indexerBitcoinEvidenceProvider) GetTip() (*rgb11wallet.BitcoinTip, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		item, err := client.GetBitcoinTip()
		if err != nil {
			return nil, err
		}
		return &rgb11wallet.BitcoinTip{Height: item.Height, BlockHash: item.BlockHash}, nil
	}
	height := p.client.GetBestHeight()
	if height < 0 {
		return nil, fmt.Errorf("Bitcoin tip is unavailable")
	}
	hash, err := p.client.GetBlockHash(int(height))
	if err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinTip{Height: height, BlockHash: hash}, nil
}

func (p *indexerBitcoinEvidenceProvider) Broadcast(rawTx []byte) (string, error) {
	if client, ok := p.client.(rgb11BitcoinEvidenceRPC); ok {
		return client.BroadcastBitcoinTx(rawTx)
	}
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return "", err
	}
	return p.client.BroadCastTx(tx)
}
