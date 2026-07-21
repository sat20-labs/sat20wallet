package wallet

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/anchors"
	"github.com/sat20-labs/rgb11/baid64"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	corepsbt "github.com/sat20-labs/rgb11/psbt"
	"github.com/sat20-labs/rgb11/rejectlist"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	coresync "github.com/sat20-labs/rgb11/sync"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrRGB11Inconsistent   = rgb11wallet.ErrRGB11Inconsistent
	ErrRGB11STPUnavailable = rgb11wallet.ErrRGB11STPUnavailable
)

// rgb11Manager owns the wallet-local RGB11 runtime and synchronization state.
// It embeds the outer wallet Manager only as an infrastructure host; all RGB11
// behavior is implemented on this dedicated manager and exposed by thin API
// forwarding methods in rgb11_api.go.
type rgb11Manager struct {
	*Manager
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
	autoBackupMutex   sync.Mutex
	autoBackupRunning bool
	autoBackupPending bool
	autoBackupDone    chan struct{}
}

func newRGB11Manager(owner *Manager, database indexer.KVDB, locker *UtxoLocker,
	evidence rgb11wallet.BitcoinEvidenceProvider) (*rgb11Manager, error) {
	projectionStore := rgb11wallet.NewProjectionStore(database, locker)
	engineStore := rgb11wallet.NewEngineStore(database)
	engine, err := corewallet.NewEngine(engineStore)
	if err != nil {
		return nil, err
	}
	return &rgb11Manager{
		Manager:         owner,
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

func (p *rgb11Manager) GetRGB11ProjectionStore() *rgb11wallet.ProjectionStore {
	if p == nil || p.rgbManager == nil {
		return nil
	}
	return p.rgbManager.projectionStore
}

func (p *rgb11Manager) selectRGB11Scope() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.engineStore == nil || p.status == nil {
		return rgb11wallet.ErrWalletScope
	}
	p.waitForRGB11AutoBackup()
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
		expectedWalletID := ""
		if p.wallet != nil && p.wallet.GetPubKey() != nil {
			expectedWalletID = hex.EncodeToString(p.wallet.GetPubKey().SerializeCompressed())
		}
		head, decodeErr := rgb11wallet.DecodeWalletHead(encoded)
		if decodeErr != nil || head.Validate(expectedWalletID) != nil {
			p.rgbManager.dkvsStatus = "conflict"
		} else {
			p.rgbManager.head = head
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

func (p *rgb11Manager) rebuildRGB11Locks() error {
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

func (p *rgb11Manager) RebuildRGB11Locks() error {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	return p.rebuildRGB11Locks()
}

func (p *rgb11Manager) GetRGB11ConsistencyStatus() string {
	if p == nil || p.rgbManager == nil || p.rgbManager.consistencyStatus == "" {
		return "warning"
	}
	return p.rgbManager.consistencyStatus
}

func (p *rgb11Manager) ProjectRGB11Allocation(outpoint string, asset *indexer.AssetInfo, proof *rgb11wallet.AllocationProof) error {
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
func (p *rgb11Manager) getL1TxOutput(outpoint string) (*TxOutput, error) {
	base, err := p.l1IndexerClient.GetTxOutput(outpoint)
	if err != nil || base == nil || p.rgbManager.projectionStore == nil {
		return base, err
	}
	projected, err := p.rgbManager.projectionStore.LoadOutput(outpoint)
	if err != nil {

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

func (p *rgb11Manager) ListRGB11Outputs() ([]*TxOutput, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ListOutputs()
}

func (p *rgb11Manager) GetRGB11AssetBalance(name *indexer.AssetName) (*Decimal, error) {
	if name == nil || name.Protocol != rgb11wallet.Protocol || p.rgbManager.projectionStore == nil {
		return nil, rgb11wallet.ErrInvalidRGB11Asset
	}
	return p.rgbManager.projectionStore.Balance(*name)
}

func (p *rgb11Manager) RegisterRGB11TickerInfo(info *indexer.TickerInfo) error {
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

// RGB11Output is the serializable projection view exposed to UI clients.
// Internal TxOutput offset maps use structured keys and are intentionally not
// part of this API.

// RGB11State exposes existing SAT20 assets plus RGB-only proof sidecars.
// Assets is rebuilt from Outputs and is never a second writable balance ledger.

func (p *rgb11Manager) GetRGB11State() (*RGB11State, error) {
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
	stateOutputs := make([]*RGB11Output, 0, len(outputs))
	for _, output := range outputs {
		stateOutputs = append(stateOutputs, &RGB11Output{
			OutPointStr: output.OutPointStr,
			Assets:      output.Assets.Clone(),
		})
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
	tickers := make([]*RGB11TickerInfo, 0)
	for _, info := range p.tickerInfoMap {
		if info != nil && info.AssetName.Protocol == rgb11wallet.Protocol {
			tickers = append(tickers, &RGB11TickerInfo{
				TickerInfo: info,
				Ticker:     p.rgb11TickerSymbol(info),
			})
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
		Outputs:           stateOutputs,
		Proofs:            proofs,
		Transfers:         transfers,
	}, nil
}

func (p *rgb11Manager) rgb11CarrierBinding(allocation rgb11wallet.ValidatedAllocation,
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

func (p *rgb11Manager) ownsRGB11Carrier(binding *rgb11wallet.CarrierBinding, walletScript []byte) bool {
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

var (
	ErrRGB11IssueUTXOUnavailable = errors.New("not enough confirmed plain Bitcoin UTXOs for RGB11 issuance")
	ErrRGB11IFAMainnet           = errors.New("IFA issuance is disabled on Bitcoin mainnet by the frozen wallet API")
)

// UnmarshalJSON accepts atomic u64 amounts as either JSON numbers or decimal
// strings. PWA callers use strings so values above JavaScript's safe-integer
// range remain exact.

// IssueRGB11Asset selects wallet-owned confirmed plain UTXOs, creates a
// canonical standard-schema genesis, validates it against Bitcoin evidence,
// and imports its allocations into the native wallet projection.
func (p *rgb11Manager) IssueRGB11Asset(ctx context.Context, request RGB11IssueRequest) (*RGB11IssueResult, error) {
	started := time.Now()
	defer func() {
		Log.Infof("RGB11 issue finished in %v", time.Since(started))
	}()
	if p == nil || p.wallet == nil || p.l1IndexerClient == nil || p.rgbManager.evidence == nil || p.utxoLockerL1 == nil {
		return nil, ErrRGB11Inconsistent
	}
	kind, err := parseRGB11IssueSchema(request.Schema)
	if err != nil {
		return nil, err
	}
	params := GetChainParam()
	if kind == schemas.IFA && params.Net == chaincfg.MainNetParams.Net {
		return nil, ErrRGB11IFAMainnet
	}
	if request.MinConfirmations <= 0 {
		request.MinConfirmations = 1
	}
	amountCount := len(request.Amounts) + len(request.InflationAmounts)
	if kind == schemas.UDA {
		if len(request.Amounts) == 0 {
			request.Amounts = []uint64{1}
		}
		amountCount = len(request.Amounts)
	}
	phaseStarted := time.Now()
	selected, err := p.selectRGB11IssueOutpoints(amountCount, request.MinConfirmations)
	Log.Infof("RGB11 issue UTXO selection finished in %v (selected=%d, err=%v)", time.Since(phaseStarted), len(selected), err)
	if err != nil {
		return nil, err
	}
	phaseStarted = time.Now()
	allocations, err := rgb11IssueAllocations(selected[:len(request.Amounts)], request.Amounts)
	if err != nil {
		return nil, err
	}
	inflation, err := rgb11IssueAllocations(selected[len(request.Amounts):], request.InflationAmounts)
	if err != nil {
		return nil, err
	}
	Log.Infof("RGB11 issue allocation build finished in %v", time.Since(phaseStarted))
	phaseStarted = time.Now()
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: kind, Network: rgb11IssuanceNetwork(params),
		Ticker: request.Ticker, Name: request.Name, Details: request.Details,
		Precision: request.Precision, Terms: request.Terms,
		Allocations: allocations, InflationRights: inflation, RejectListURL: request.RejectListURL,
	})
	Log.Infof("RGB11 issue contract build finished in %v (err=%v)", time.Since(phaseStarted), err)
	if err != nil {
		return nil, err
	}
	phaseStarted = time.Now()
	imported, err := p.ImportRGB11Contract(ctx, []byte(issued.Armor))
	Log.Infof("RGB11 issue contract import finished in %v (err=%v)", time.Since(phaseStarted), err)
	if err != nil {
		return nil, err
	}
	if imported.Projected != len(selected) {
		return nil, fmt.Errorf("%w: issued %d allocations but projected %d", ErrRGB11Inconsistent, len(selected), imported.Projected)
	}
	return &RGB11IssueResult{
		ContractID: imported.ContractID, SchemaID: imported.SchemaID, AssetName: imported.AssetName,
		Armor: issued.Armor, OutPoints: selected, Receipt: imported.Receipt, Projected: imported.Projected,
	}, nil
}

func parseRGB11IssueSchema(value string) (schemas.Kind, error) {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "NIA":
		return schemas.NIA, nil
	case "IFA":
		return schemas.IFA, nil
	case "UDA":
		return schemas.UDA, nil
	default:
		return "", coreissuance.ErrUnsupportedSchema
	}
}

func (p *rgb11Manager) selectRGB11IssueOutpoints(count int, minConfirmations int64) ([]string, error) {
	if count <= 0 {
		return nil, coreissuance.ErrInvalidSpec
	}
	address := p.wallet.GetAddress()
	walletScript, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	candidates := p.l1IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].OutPoint < candidates[j].OutPoint })
	p.utxoLockerL1.Reload(address)
	selected := make([]string, 0, count)
	for _, candidate := range candidates {
		if candidate == nil || candidate.OutPoint == "" || p.utxoLockerL1.IsLocked(candidate.OutPoint) {
			continue
		}
		evidence, err := p.rgbManager.evidence.GetUTXO(candidate.OutPoint)
		if err != nil || evidence == nil {
			continue
		}
		if evidence.Confirmations < minConfirmations || !bytes.Equal(evidence.PkScript, walletScript) {
			continue
		}
		if _, err := wire.NewOutPointFromString(candidate.OutPoint); err != nil {
			continue
		}
		selected = append(selected, candidate.OutPoint)
		if len(selected) == count {
			return selected, nil
		}
	}
	return nil, fmt.Errorf("%w: need %d, found %d", ErrRGB11IssueUTXOUnavailable, count, len(selected))
}

func rgb11IssueAllocations(outpoints []string, amounts []uint64) ([]coreissuance.Allocation, error) {
	if len(outpoints) != len(amounts) {
		return nil, coreissuance.ErrInvalidSpec
	}
	allocations := make([]coreissuance.Allocation, 0, len(outpoints))
	for index, outpointText := range outpoints {
		outpoint, err := wire.NewOutPointFromString(outpointText)
		if err != nil {
			return nil, err
		}
		var entropy [8]byte
		if _, err := rand.Read(entropy[:]); err != nil {
			return nil, err
		}
		seal, err := seals.NewGraphBlindSeal(outpoint.Hash[:], outpoint.Index, binary.LittleEndian.Uint64(entropy[:]))
		if err != nil {
			return nil, err
		}
		allocations = append(allocations, coreissuance.Allocation{Seal: seal, Amount: amounts[index]})
	}
	return allocations, nil
}

func rgb11IssuanceNetwork(params *chaincfg.Params) coreissuance.ChainNet {
	if params != nil && params.Net == chaincfg.MainNetParams.Net {
		return coreissuance.BitcoinMainnet
	}
	if params != nil && params.Net == chaincfg.TestNet3Params.Net {
		return coreissuance.BitcoinTestnet3
	}
	if params != nil && params.Net == chaincfg.RegressionNetParams.Net {
		return coreissuance.BitcoinRegtest
	}
	if params != nil && params.Net == chaincfg.SigNetParams.Net {
		return coreissuance.BitcoinSignet
	}
	return coreissuance.BitcoinTestnet4
}

// ImportRGB11Contract validates a complete contract consignment and imports
// only revealed allocations whose Bitcoin output is controlled by the active
// wallet. The Indexer contributes UTXO facts only; it never contributes RGB
// balances or allocation state.
func (p *rgb11Manager) ImportRGB11Contract(ctx context.Context, raw []byte) (*RGB11ImportResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	if container.Armor == nil || container.Armor.Type != "contract" {
		return nil, fmt.Errorf("RGB11 import requires a contract consignment")
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(
		ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw,
	)
	if err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}

	projected := 0
	for _, allocation := range receipt.Allocations {
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		if utxo == nil {
			continue
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return nil, err
		}
		if !p.ownsRGB11Carrier(binding, walletScript) {
			continue
		}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		asset := &indexer.AssetInfo{
			Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0,
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: allocationOutpointTxID(allocation.OutPoint), Status: "valid",
			CarrierBinding: binding,
		}
		if err := p.ProjectRGB11Allocation(allocation.OutPoint, asset, proof); err != nil {
			return nil, err
		}
		projected++
	}

	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return nil, err
	}
	assetType := indexer.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexer.ASSET_TYPE_NFT
	}
	assetName, err := rgb11wallet.NewAssetName(container.ContractID, assetType)
	if err != nil {
		return nil, err
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return nil, err
	}
	ext := rgb11wallet.TickerExt{
		AssetName: assetName, Ticker: metadata.Ticker, OriginalAssetID: container.ContractID,
		SchemaID: container.SchemaID, ContractID: container.ContractID,
		ContractHash: receipt.ConsignmentHash, RejectListURL: metadata.RejectListURL,
		ControlMode: descriptor.DefaultControlMode,
		STPAllowed:  false, ValidationStatus: "valid",
	}
	extContent, err := json.Marshal(ext)
	if err != nil {
		return nil, err
	}
	info := &indexer.TickerInfo{
		AssetName: assetName, DisplayName: metadata.DisplayName, Divisibility: int(metadata.Precision),
		DeployTx:    allocationOutpointTxID(firstAllocationOutpoint(receipt.Allocations)),
		TotalMinted: fmt.Sprintf("%d", metadata.IssuedSupply), MaxSupply: fmt.Sprintf("%d", metadata.MaxSupply), Content: extContent,
	}
	if err := p.RegisterRGB11TickerInfo(info); err != nil {
		return nil, err
	}
	result := &RGB11ImportResult{
		ContractID: container.ContractID, SchemaID: container.SchemaID,
		AssetName: assetName, Receipt: receipt, Projected: projected,
	}
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func (p *rgb11Manager) rgb11TickerSymbol(info *indexer.TickerInfo) string {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || info == nil {
		return ""
	}
	var ext rgb11wallet.TickerExt
	if json.Unmarshal(info.Content, &ext) != nil {
		return ""
	}
	if ext.Ticker != "" {
		return ext.Ticker
	}
	if ext.ContractHash == "" {
		return ""
	}
	raw, err := p.rgbManager.projectionStore.LoadObject(ext.ContractHash)
	if err != nil {
		return ""
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return ""
	}
	schemaValue, _ := container.Value.Field("schema")
	typeSystem, _ := container.Value.Field("types")
	genesisValue, _ := container.Value.Field("genesis")
	metadata, err := schemas.ExtractGenesisAssetMetadata(schemaValue, typeSystem, genesisValue)
	if err != nil {
		return ""
	}
	return metadata.Ticker
}

func allocationOutpointTxID(outpoint string) string {
	for index := len(outpoint) - 1; index >= 0; index-- {
		if outpoint[index] == ':' {
			return outpoint[:index]
		}
	}
	return ""
}

func firstAllocationOutpoint(allocations []rgb11wallet.ValidatedAllocation) string {
	if len(allocations) == 0 {
		return ""
	}
	return allocations[0].OutPoint
}

const (
	RGB11RejectReasonList = "reject-list"
	RGB11RejectReasonUser = "user-rejected"
)

var (
	ErrRGB11RejectListUnavailable = errors.New("RGB11 reject list is unavailable")
	ErrRGB11Rejected              = rgb11wallet.ErrRGB11Rejected
)

// RGB11RejectListProvider makes the network policy injectable for deterministic
// wallet tests. The default implementation permits plain HTTP only on loopback
// while the wallet is configured for regtest.

func (p *rgb11Manager) rgb11RejectListProvider() RGB11RejectListProvider {
	if p != nil && p.rgbManager != nil && p.rgbManager.rejectLists != nil {
		return p.rgbManager.rejectLists
	}
	params := GetChainParam()
	allowLoopback := params != nil && params.Net == chaincfg.RegressionNetParams.Net
	return rejectlist.Client{AllowLoopbackHTTP: allowLoopback}
}

func rgb11ContainerRejectListURL(container *coreconsignment.Container) (string, error) {
	if container == nil {
		return "", coreconsignment.ErrContainerType
	}
	schema, okSchema := container.Value.Field("schema")
	types, okTypes := container.Value.Field("types")
	genesis, okGenesis := container.Value.Field("genesis")
	if !okSchema || !okTypes || !okGenesis {
		return "", coreconsignment.ErrContainerType
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return "", err
	}
	return metadata.RejectListURL, nil
}

func (p *rgb11Manager) checkRGB11RejectPolicy(container *coreconsignment.Container,
	checked []operations.Opout) error {
	url, err := rgb11ContainerRejectListURL(container)
	if err != nil || url == "" {
		return err
	}
	list, err := p.rgb11RejectListProvider().Fetch(url)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRGB11RejectListUnavailable, err)
	}
	dag, err := container.OperationDAG()
	if err != nil {
		return err
	}
	for _, opout := range checked {
		if rejected, ok := list.RejectedAncestor(opout, dag); ok {
			return &RGB11RejectListViolation{Checked: opout, Rejected: rejected}
		}
	}
	return nil
}

func rgb11ProofOpout(proof *rgb11wallet.AllocationProof) (operations.Opout, error) {
	if proof == nil || proof.AssignmentType > 0xffff || proof.AssignmentIndex > 0xffff {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	operationID, err := hex.DecodeString(proof.OperationID)
	if err != nil || len(operationID) != 32 {
		return operations.Opout{}, rgb11wallet.ErrInvalidProof
	}
	return operations.ParseOpout(fmt.Sprintf("%s/%d/%d", proof.OperationID, proof.AssignmentType, proof.AssignmentIndex))
}

var ErrRGB11WalletLocked = errors.New("RGB11 wallet must be unlocked")

func (p *rgb11Manager) CreateRGB11Invoice(request RGB11InvoiceRequest) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil || p.wallet == nil {
		return nil, ErrRGB11WalletLocked
	}
	if request.Expiry == 0 {
		request.Expiry = time.Now().Add(24 * time.Hour).Unix()
	}
	network := rgb11InvoiceNetwork(GetChainParam())
	pubkey := p.wallet.GetPubKey()
	if pubkey == nil {
		return nil, ErrRGB11WalletLocked
	}
	amount, err := strconv.ParseUint(request.AmountRaw, 10, 64)
	if err != nil {
		return nil, err
	}
	mode := corewallet.ReceiveMode(strings.ToLower(strings.TrimSpace(request.Mode)))
	if mode == "" {
		mode = corewallet.ReceiveBlind
	}
	var witnessScript []byte
	var internalXOnly *[32]byte
	if mode == corewallet.ReceiveWitness {
		derivationIndex := p.wallet.GetSubAccount()
		address := p.wallet.GetAddressByIndex(derivationIndex)
		if address == "" || address != p.wallet.GetAddress() {
			return nil, errors.New("RGB11 witness address is not the active BIP86 subaccount")
		}
		witnessScript, err = AddrToPkScript(address, GetChainParam())
		if err != nil {
			return nil, err
		}
		internal := p.wallet.GetPubKeyByIndex(derivationIndex)
		if internal == nil {
			return nil, ErrRGB11WalletLocked
		}
		compressed := internal.SerializeCompressed()
		var xonly [32]byte
		copy(xonly[:], compressed[1:])
		internalXOnly = &xonly
	}
	receive, err := p.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Mode: mode, ContractID: request.ContractID, SchemaID: request.SchemaID, Network: network,
		Amount: &amount, AssignmentName: request.AssignmentName,
		RecipientID: hex.EncodeToString(pubkey.SerializeCompressed()),
		WitnessVout: request.WitnessVout, WitnessScript: witnessScript,
		InternalXOnly: internalXOnly, Expiry: request.Expiry,
	})
	if err != nil {
		return nil, err
	}
	p.autoBackupRGB11AfterMutation()
	return receive, nil
}

func rgb11InvoiceNetwork(params *chaincfg.Params) invoicing.ChainNet {
	if params == nil {
		return invoicing.BitcoinTestnet4
	}
	switch params.Net {
	case chaincfg.MainNetParams.Net:
		return invoicing.BitcoinMainnet
	case chaincfg.TestNet3Params.Net:
		return invoicing.BitcoinTestnet3
	case chaincfg.RegressionNetParams.Net:
		return invoicing.BitcoinRegtest
	case chaincfg.SigNetParams.Net:
		return invoicing.BitcoinSignet
	default:
		return invoicing.BitcoinTestnet4
	}
}

func (p *rgb11Manager) GetRGB11ReceiveRequest(requestID string) (*corewallet.ReceiveRequest, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.engine.LoadReceive(requestID)
}

var (
	ErrRGB11InvoiceMismatch = errors.New("RGB11 consignment does not satisfy the wallet invoice")
	ErrRGB11NoAllocation    = errors.New("RGB11 consignment has no allocation for this wallet")
)

// ValidateRGB11Consignment validates and stores an immutable receipt without
// projecting any balance. It is useful for contract import and diagnostics.
func (p *rgb11Manager) ValidateRGB11Consignment(ctx context.Context, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	return p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, rgb11wallet.NewNativeConsensusValidator(), p.rgbManager.evidence, raw)
}

// AcceptRGB11Consignment validates the complete client-side history and then
// projects only the allocation matching the wallet's pre-persisted invoice
// seal. A valid consignment for another wallet never becomes local balance.
func (p *rgb11Manager) AcceptRGB11Consignment(ctx context.Context, requestID string, raw []byte) (*rgb11wallet.ValidationReceipt, error) {
	return p.acceptRGB11Consignment(ctx, requestID, raw, true)
}

func (p *rgb11Manager) acceptRGB11Consignment(ctx context.Context, requestID string, raw []byte,
	autoBackup bool) (*rgb11wallet.ValidationReceipt, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.engine == nil {
		return nil, ErrRGB11Inconsistent
	}
	request, err := p.rgbManager.engine.LoadReceive(requestID)
	if err != nil {
		return nil, err
	}
	invoice, err := invoicing.Parse(request.Invoice)
	if err != nil {
		return nil, err
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		return nil, err
	}
	var invoiceSeal [32]byte
	var validator rgb11wallet.ConsensusValidator
	var witnessScript []byte
	switch invoice.Beneficiary.Kind {
	case invoicing.BeneficiaryBlindedSeal:
		concealed, err := request.Seal.Conceal()
		if err != nil || !bytes.Equal(invoice.Beneficiary.BlindedSeal[:], concealed[:]) {
			return nil, ErrRGB11InvoiceMismatch
		}
		invoiceSeal = [32]byte(concealed)
		validator = rgb11wallet.NewNativeConsensusValidatorWithReveals(request.Seal)
	case invoicing.BeneficiaryWitnessVout:
		var err error
		witnessScript, err = invoice.Beneficiary.WitnessScript()
		if err != nil || !bytes.Equal(request.WitnessScript, witnessScript) {
			return nil, ErrRGB11InvoiceMismatch
		}
		validator = rgb11wallet.NewNativeConsensusValidator()
	default:
		return nil, ErrRGB11InvoiceMismatch
	}
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, raw)
	if err != nil {
		return nil, err
	}
	if invoice.Contract != nil && invoice.Contract.String() != receipt.ContractID {
		return nil, ErrRGB11InvoiceMismatch
	}
	if invoice.Schema != nil {
		decoded, decodeErr := decodeReceiptSchema(receipt.SchemaID)
		if decodeErr != nil || decoded != *invoice.Schema {
			return nil, ErrRGB11InvoiceMismatch
		}
	}
	container, err := coreconsignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	checked, err := p.rgb11ReceivedPolicyOpouts(receipt, request, invoice, invoiceSeal, witnessScript)
	if err != nil {
		return nil, err
	}
	if err := p.checkRGB11RejectPolicy(container, checked); err != nil {
		return nil, err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return nil, err
	}
	projected := 0
	var receivedAsset *indexer.AssetInfo
	var receivedOutpoint string
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	for _, allocation := range receipt.Allocations {
		if allocation.AssignmentType != 4000 {
			continue
		}
		vout, ok := outpointVout(allocation.OutPoint)
		if !ok || !allocation.WitnessTxPtr {
			continue
		}
		candidateSeal := seals.NewWitnessBlindSeal(vout, allocation.SealBlinding)
		candidateSecret, concealErr := candidateSeal.Conceal()
		if concealErr != nil {
			return nil, concealErr
		}
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal {
			if vout != request.Seal.Vout || allocation.SealBlinding != request.Seal.Blinding ||
				!bytes.Equal(candidateSecret[:], invoiceSeal[:]) {
				continue
			}
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil {
			return nil, err
		}
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil || !p.ownsRGB11Carrier(binding, walletScript) ||
			(invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout && !bytes.Equal(utxo.PkScript, witnessScript)) {
			if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
				continue
			}
			return nil, ErrRGB11InvoiceMismatch
		}
		if invoice.Assignment != nil {
			switch invoice.Assignment.Kind {
			case invoicing.StateAmount:
				if allocation.Amount.Value.Sign() < 0 || !allocation.Amount.Value.IsUint64() ||
					allocation.Amount.Value.Uint64() != uint64(invoice.Assignment.Amount) {
					return nil, ErrRGB11InvoiceMismatch
				}
			case invoicing.StateAllocation:
				if allocation.Amount.Value.Cmp(indexer.NewDefaultDecimal(1).Value) != 0 {
					return nil, ErrRGB11InvoiceMismatch
				}
			}
		}
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(candidateSecret[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: strings.SplitN(allocation.OutPoint, ":", 2)[0], Status: "valid",
			CarrierBinding: binding,
		}
		if err := p.ProjectRGB11Allocation(allocation.OutPoint, asset, proof); err != nil {
			return nil, err
		}
		receivedAsset = &indexer.AssetInfo{Name: asset.Name, Amount: *asset.Amount.Clone(), BindingSat: 0}
		receivedOutpoint = allocation.OutPoint
		projected++
	}
	if projected == 0 {
		return nil, ErrRGB11NoAllocation
	}
	transferID := receipt.TransferID
	if transferID == "" {
		transferID = receipt.ConsignmentHash
	}
	if err := p.rgbManager.engine.MarkRelayAccepted(requestID, transferID, receipt.ConsignmentHash); err != nil {
		return nil, fmt.Errorf("mark RGB11 receive accepted: %w", err)
	}
	expiry := int64(0)
	if invoice.Expiry != nil {
		expiry = *invoice.Expiry
	}
	state := &rgb11wallet.TransferState{
		TransferID: transferID, Direction: "receive", Asset: *receivedAsset,
		RecipientID: request.RecipientID, Invoice: request.Invoice,
		OutputOutPoints: []string{receivedOutpoint}, MinConfirmations: 1, Expiry: expiry,
		ConsignmentHash: receipt.ConsignmentHash, WitnessTxID: allocationOutpointTxID(receivedOutpoint),
		AckStatus: "accepted", Status: "pending", RelayRecordKey: request.RelayKey,
		AckRecordKey: request.AckKey, RelayDurability: "LOCAL_ONLY", RelayExpiry: expiry,
	}
	if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		return nil, err
	}
	if autoBackup {
		p.autoBackupRGB11AfterMutation()
	}
	return receipt, nil
}

func (p *rgb11Manager) rgb11ReceivedPolicyOpouts(receipt *rgb11wallet.ValidationReceipt,
	request *corewallet.ReceiveRequest, invoice *invoicing.Invoice, invoiceSeal [32]byte,
	witnessScript []byte) ([]operations.Opout, error) {
	checked := make([]operations.Opout, 0, 1)
	for _, allocation := range receipt.Allocations {
		if allocation.AssignmentType != 4000 || !allocation.WitnessTxPtr {
			continue
		}
		vout, ok := outpointVout(allocation.OutPoint)
		if !ok {
			continue
		}
		candidate := false
		switch invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			seal := seals.NewWitnessBlindSeal(vout, allocation.SealBlinding)
			secret, err := seal.Conceal()
			if err != nil {
				return nil, err
			}
			candidate = vout == request.Seal.Vout && allocation.SealBlinding == request.Seal.Blinding &&
				bytes.Equal(secret[:], invoiceSeal[:])
		case invoicing.BeneficiaryWitnessVout:
			utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
			if err != nil {
				return nil, err
			}
			candidate = utxo != nil && bytes.Equal(utxo.PkScript, witnessScript)
		}
		if !candidate {
			continue
		}
		opout, err := operations.ParseOpout(fmt.Sprintf("%s/%d/%d",
			allocation.OperationID, allocation.AssignmentType, allocation.AssignmentIndex))
		if err != nil {
			return nil, err
		}
		checked = append(checked, opout)
	}
	if len(checked) == 0 {
		return nil, ErrRGB11NoAllocation
	}
	return checked, nil
}

func outpointVout(outpoint string) (uint32, bool) {
	_, text, ok := strings.Cut(outpoint, ":")
	if !ok {
		return 0, false
	}
	value, err := strconv.ParseUint(text, 10, 32)
	return uint32(value), err == nil
}

func decodeReceiptSchema(schemaID string) ([32]byte, error) {
	return baid64.Decode32(schemaID, baid64.SchemaIDOptions())
}

const rgb11CarrierValue int64 = 330

var (
	ErrRGB11InsufficientBalance = errors.New("insufficient RGB11 balance")
	ErrRGB11HistoryMerge        = errors.New("selected RGB11 allocations require a history merge")
	ErrRGB11AckRequired         = errors.New("valid recipient ACK is required before broadcast")
	ErrRGB11BatchAckRequired    = errors.New("all RGB11 batch recipient ACKs are required before broadcast")
	ErrRGB11AssetPreservation   = errors.New("RGB11 input contains another asset that cannot be preserved")
)

type rgb11SendRecipient struct {
	raw         string
	invoice     *invoicing.Invoice
	amount      uint64
	recipientID string
	relayKey    string
	ackKey      string
	script      []byte
	vout        uint32
	transport   string
}

type rgb11SpendAllocation struct {
	proof  *rgb11wallet.AllocationProof
	asset  *indexer.AssetInfo
	target bool
}

// PrepareRGB11Transfer builds one client-side state transition for one asset.
// A request may contain multiple witness invoices; all recipients share one
// Bitcoin transaction and one official RGB consignment, while transport and
// ACK state remains recipient-specific. It intentionally does not relay or
// broadcast. RBF replacement is outside the first release scope.
func (p *rgb11Manager) PrepareRGB11Transfer(ctx context.Context, request RGB11SendRequest) (*RGB11PreparedTransfer, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil || p.wallet == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	invoiceTexts := append([]string(nil), request.Invoices...)
	if len(invoiceTexts) == 0 && strings.TrimSpace(request.Invoice) != "" {
		invoiceTexts = []string{request.Invoice}
	}
	if len(invoiceTexts) == 0 || len(invoiceTexts) > 32 {
		return nil, invoicing.ErrInvalidInvoice
	}
	recipients := make([]rgb11SendRecipient, 0, len(invoiceTexts))
	seenRecipient := make(map[string]struct{}, len(invoiceTexts))
	seenRelay := make(map[string]struct{}, len(invoiceTexts))
	seenAck := make(map[string]struct{}, len(invoiceTexts))
	var contractID consensus.ContractID
	var totalAmount uint64
	for index, raw := range invoiceTexts {
		trimmed := strings.TrimSpace(raw)
		invoice, err := invoicing.Parse(trimmed)
		if err != nil {
			return nil, err
		}
		if err := invoice.Validate(time.Now().Unix()); err != nil {
			return nil, err
		}
		currentContract, amount, recipientID, relayKey, ackKey, transport, err := validateRGB11SendInvoice(invoice)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			contractID = currentContract
		} else if currentContract != contractID {
			return nil, invoicing.ErrInvalidInvoice
		}
		if len(invoiceTexts) > 1 && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return nil, fmt.Errorf("RGB11 batch send requires witness invoices")
		}
		if _, ok := seenRecipient[recipientID]; ok {
			return nil, fmt.Errorf("RGB11 batch contains duplicate recipient")
		}
		if index > 0 && transport != recipients[0].transport {
			return nil, fmt.Errorf("RGB11 batch cannot mix transport modes")
		}
		if relayKey != "" {
			if _, ok := seenRelay[relayKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate relay key")
			}
			seenRelay[relayKey] = struct{}{}
		}
		if ackKey != "" {
			if _, ok := seenAck[ackKey]; ok {
				return nil, fmt.Errorf("RGB11 batch contains duplicate ACK key")
			}
			seenAck[ackKey] = struct{}{}
		}
		seenRecipient[recipientID] = struct{}{}
		if ^uint64(0)-totalAmount < amount {
			return nil, rgb11wallet.ErrInvalidProof
		}
		totalAmount += amount
		var script []byte
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
			script, err = invoice.Beneficiary.WitnessScript()
		} else {
			recipientPubKey, _ := hex.DecodeString(recipientID)
			script, err = HexPubKeyToP2TRPkScript(recipientPubKey)
		}
		if err != nil {
			return nil, err
		}
		recipients = append(recipients, rgb11SendRecipient{
			raw: trimmed, invoice: invoice, amount: amount, recipientID: recipientID,
			relayKey: relayKey, ackKey: ackKey, script: script, vout: uint32(index + 1), transport: transport,
		})
	}
	if request.MinConfirmations == 0 {
		request.MinConfirmations = 1
	}

	selected, _, targetAsset, targetClass, targetType, targetTotal, err :=
		p.selectRGB11Allocations(contractID.String(), totalAmount, request.MinConfirmations)
	if err != nil {
		return nil, err
	}
	if len(recipients) > 1 && targetClass != "fungible" {
		return nil, fmt.Errorf("RGB11 batch send supports fungible state only")
	}
	base, parentStateHash, err := p.mergeRGB11SpendHistories(selected)
	if err != nil {
		return nil, err
	}

	reveals := make([]seals.GraphBlindSeal, 0, len(selected))
	inputs := make([]operations.TransitionInput, 0, len(selected))
	for _, allocation := range selected {
		operationID, err := hex.DecodeString(allocation.proof.OperationID)
		if err != nil || len(operationID) != 32 || allocation.proof.AssignmentType > 0xffff || allocation.proof.AssignmentIndex > 0xffff {
			return nil, rgb11wallet.ErrInvalidProof
		}
		var operation [32]byte
		copy(operation[:], operationID)
		inputs = append(inputs, operations.TransitionInput{
			OperationID: operation, AssignmentType: uint16(allocation.proof.AssignmentType),
			Index: uint16(allocation.proof.AssignmentIndex),
		})
		graph, err := seals.DecodeGraphBlindSeal(allocation.proof.SealDisclosure)
		if err != nil {
			return nil, rgb11wallet.ErrInvalidProof
		}
		reveals = append(reveals, graph)
	}
	if len(reveals) > 0 {
		if _, err := base.RevealGraphSeals(reveals); err != nil {
			return nil, err
		}
	}

	changeSeals := make([]seals.GraphBlindSeal, 0)
	changeSecrets := make([][32]byte, 0)
	recipientSecrets := make([][32]byte, 0, 1)
	outputs := make([]operations.TransitionOutput, 0, len(recipients)+1)
	var structuredData []byte
	if targetClass == "structured" {
		for _, allocation := range selected {
			if allocation.target {
				structuredData = append([]byte(nil), allocation.proof.StateData...)
				break
			}
		}
	}
	for _, recipient := range recipients {
		output := operations.TransitionOutput{
			AssignmentType: uint16(targetType), Class: targetClass, Amount: recipient.amount,
			Data: append([]byte(nil), structuredData...),
		}
		switch recipient.invoice.Beneficiary.Kind {
		case invoicing.BeneficiaryBlindedSeal:
			copy(output.SecretSeal[:], recipient.invoice.Beneficiary.BlindedSeal[:])
			recipientSecrets = append(recipientSecrets, [32]byte(recipient.invoice.Beneficiary.BlindedSeal))
		case invoicing.BeneficiaryWitnessVout:
			seal, err := seals.RandomWitnessBlindSeal(recipient.vout)
			if err != nil {
				return nil, err
			}
			output.RevealedSeal = &seal
		default:
			return nil, invoicing.ErrInvalidInvoice
		}
		outputs = append(outputs, output)
	}
	changeVout := uint32(len(recipients) + 1)
	if targetTotal > totalAmount {
		if targetClass != "fungible" {
			return nil, ErrRGB11InsufficientBalance
		}
		change, secret, err := newRGB11ChangeOutput(changeVout, uint16(targetType), targetClass, targetTotal-totalAmount, nil)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}
	for _, allocation := range selected {
		if allocation.target {
			continue
		}
		stateAmount, err := decimalUint64(&allocation.asset.Amount)
		if err != nil {
			return nil, err
		}
		change, secret, err := newRGB11ChangeOutput(
			changeVout, uint16(allocation.proof.AssignmentType), allocation.proof.StateClass,
			stateAmount, allocation.proof.StateData,
		)
		if err != nil {
			return nil, err
		}
		outputs = append(outputs, change)
		changeSeals = append(changeSeals, secret.seal)
		changeSecrets = append(changeSecrets, secret.secret)
	}

	transition, transitionCommitment, err := operations.BuildTransition(operations.TransitionSpec{
		ContractID: [32]byte(contractID), Nonce: uint64(time.Now().UnixNano()),
		TransitionType: 10_000, Inputs: inputs, Outputs: outputs,
	})
	if err != nil {
		return nil, err
	}
	bundleValue, bundleCommitment, err := operations.BuildBundle(inputs, transition)
	if err != nil {
		return nil, err
	}
	mpcProof, err := anchors.NewMPCProof([32]byte(contractID), 3)
	if err != nil {
		return nil, err
	}
	mpcCommitment, err := anchors.ConvolveMPC(mpcProof, [32]byte(contractID), bundleCommitment.BundleID)
	if err != nil {
		return nil, err
	}

	recipientScripts := make([][]byte, 0, len(recipients))
	for _, recipient := range recipients {
		recipientScripts = append(recipientScripts, recipient.script)
	}
	changeScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, err
	}
	tx, prevFetcher, inputOutpoints, taprootRoots, _, err := p.buildRGB11WitnessTx(
		selected, recipientScripts, changeScript, anchors.OpretScript(mpcCommitment), request.FeeRate,
	)
	if err != nil {
		return nil, err
	}
	reserved := make([]string, 0, len(inputOutpoints))
	for _, outpoint := range inputOutpoints {
		if p.utxoLockerL1.IsLocked(outpoint) {
			continue
		}
		if err := p.utxoLockerL1.LockUtxo(outpoint, rgb11wallet.LockReasonPending); err != nil {
			for _, release := range reserved {
				_ = p.utxoLockerL1.UnlockUtxo(release)
			}
			return nil, err
		}
		reserved = append(reserved, outpoint)
	}
	reservationCommitted := false
	defer func() {
		if reservationCommitted {
			return
		}
		for _, outpoint := range reserved {
			_ = p.utxoLockerL1.UnlockUtxo(outpoint)
		}
	}()
	packet, signedTx, signedPSBT, err := p.signRGB11PSBT(
		tx, prevFetcher, [32]byte(contractID), transitionCommitment.OperationID,
		transition.Encoded, bundleCommitment.BundleID, mpcProof, mpcCommitment, inputs, taprootRoots,
	)
	if err != nil {
		return nil, err
	}
	_ = packet
	witnessBundle, err := operations.BuildOpretWitnessBundleWithTx(signedTx, bundleValue, mpcProof)
	if err != nil {
		return nil, err
	}
	recipientContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, recipientSecrets)
	if err != nil {
		return nil, err
	}
	localContainer, err := coreconsignment.BuildTransfer(base, witnessBundle, changeSecrets)
	if err != nil {
		return nil, err
	}
	recipientArmor, err := coreconsignment.EncodeArmor(recipientContainer.Value)
	if err != nil {
		return nil, err
	}
	localArmor, err := coreconsignment.EncodeArmor(localContainer.Value)
	if err != nil {
		return nil, err
	}
	recipientDecoded, err := coreconsignment.DecodeArmor(recipientArmor)
	if err != nil {
		return nil, err
	}

	var signedRaw bytes.Buffer
	if err := signedTx.Serialize(&signedRaw); err != nil {
		return nil, err
	}
	objectHash := sha256.Sum256([]byte(recipientArmor))
	targetPrecision := 0
	for _, allocation := range selected {
		if allocation.target {
			targetPrecision = allocation.asset.Amount.Precision
			break
		}
	}
	batchID := recipientDecoded.Armor.ID
	transferIDs := make([]string, len(recipients))
	for index := range recipients {
		if len(recipients) == 1 {
			transferIDs[index] = batchID
			continue
		}
		child := sha256.Sum256([]byte(fmt.Sprintf("SAT20-RGB11-BATCH-RECIPIENT-V1:%s:%d", batchID, index)))
		transferIDs[index] = hex.EncodeToString(child[:])
	}
	pendingList := make([]*rgb11wallet.PendingTransfer, 0, len(recipients))
	states := make([]*rgb11wallet.TransferState, 0, len(recipients))
	for index, recipient := range recipients {
		state := rgb11wallet.TransferState{
			TransferID: transferIDs[index], Direction: "send",
			Asset: indexer.AssetInfo{Name: targetAsset, Amount: indexer.Decimal{
				Precision: targetPrecision, Value: new(big.Int).SetUint64(recipient.amount),
			}, BindingSat: 0},
			RecipientID: recipient.recipientID, Invoice: recipient.raw, InputOutPoints: append([]string(nil), inputOutpoints...),
			OutputOutPoints: []string{
				fmt.Sprintf("%s:%d", signedTx.TxID(), recipient.vout),
				fmt.Sprintf("%s:%d", signedTx.TxID(), changeVout),
			},
			MinConfirmations: request.MinConfirmations, Expiry: *recipient.invoice.Expiry,
			ConsignmentHash: hex.EncodeToString(objectHash[:]), WitnessTxID: signedTx.TxID(),
			AckStatus: "awaiting", Status: "prepared", RelayRecordKey: recipient.relayKey, AckRecordKey: recipient.ackKey,
			RelayDurability: "LOCAL_ONLY", RelayExpiry: *recipient.invoice.Expiry,
			ParentStateHash: parentStateHash, RecipientVout: recipient.vout, BatchSize: len(recipients),
			BatchTransferIDs: append([]string(nil), transferIDs...), TransportMode: recipient.transport,
		}
		if len(recipients) > 1 {
			state.BatchID = batchID
		}
		pending := &rgb11wallet.PendingTransfer{
			State: state, RecipientConsignment: []byte(recipientArmor), LocalConsignment: []byte(localArmor),
			SignedTx: append([]byte(nil), signedRaw.Bytes()...), SignedPSBT: append([]byte(nil), signedPSBT...),
			ChangeSeals: append([]seals.GraphBlindSeal(nil), changeSeals...), CreatedAt: time.Now().Unix(),
		}
		pendingList = append(pendingList, pending)
		states = append(states, &pending.State)
	}
	if err := p.rgbManager.projectionStore.SavePendingTransfers(pendingList); err != nil {
		return nil, err
	}
	reservationCommitted = true
	p.autoBackupRGB11AfterMutation()
	return &RGB11PreparedTransfer{
		State: states[0], States: states, RecipientConsignment: recipientArmor,
		SignedPSBT: hex.EncodeToString(signedPSBT), TxID: signedTx.TxID(),
	}, nil
}

func validateRGB11SendInvoice(invoice *invoicing.Invoice) (consensus.ContractID, uint64, string, string, string, string, error) {
	if invoice == nil || invoice.Contract == nil || invoice.Assignment == nil || invoice.Expiry == nil ||
		(invoice.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal && invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout) ||
		invoice.Assignment.Kind != invoicing.StateAmount {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	wantNetwork := rgb11InvoiceNetwork(GetChainParam())
	if invoice.Beneficiary.Network != wantNetwork || invoice.Assignment.Amount == 0 {
		return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
	}
	values := make(map[string]string, len(invoice.UnknownQuery))
	for _, param := range invoice.UnknownQuery {
		values[param.Key] = param.Value
	}
	hasSAT20 := values["sat20_recipient"] != "" || values["sat20_relay"] != "" || values["sat20_ack"] != ""
	recipientID, relayKey, ackKey, transport := values["sat20_recipient"], values["sat20_relay"], values["sat20_ack"], "sat20-dkvs"
	if hasSAT20 {
		if recipientID == "" || relayKey == "" || ackKey == "" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryBlindedSeal && values["sat20_vout"] != "1" {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		pubkey, err := hex.DecodeString(recipientID)
		if err != nil || len(pubkey) != 33 {
			return consensus.ContractID{}, 0, "", "", "", "", invoicing.ErrInvalidInvoice
		}
		if _, err := HexPubKeyToP2TRPkScript(pubkey); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	} else {
		if len(invoice.Transports) != 0 || invoice.Beneficiary.Kind != invoicing.BeneficiaryWitnessVout {
			return consensus.ContractID{}, 0, "", "", "", "", fmt.Errorf("RGB11 external send requires an out-of-band witness invoice")
		}
		recipientID = invoice.Beneficiary.String()
		transport = "out-of-band"
	}
	if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
		if _, err := invoice.Beneficiary.WitnessScript(); err != nil {
			return consensus.ContractID{}, 0, "", "", "", "", err
		}
	}
	return *invoice.Contract, uint64(invoice.Assignment.Amount), recipientID, relayKey, ackKey, transport, nil
}

func (p *rgb11Manager) selectRGB11Allocations(contractID string, amount uint64, minConfirmations uint8) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	excluded := make(map[string]struct{})
	for {
		selected, baseHash, targetAsset, targetClass, targetType, total, err :=
			p.selectRGB11AllocationsOnce(contractID, amount, minConfirmations, excluded)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, total, err
		}
		base, _, err := p.mergeRGB11SpendHistories(selected)
		if err != nil {
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		checked := make([]operations.Opout, 0, len(selected))
		byOpout := make(map[operations.Opout]*rgb11wallet.AllocationProof)
		for _, allocation := range selected {
			if !allocation.target {
				continue
			}
			opout, err := rgb11ProofOpout(allocation.proof)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			checked = append(checked, opout)
			byOpout[opout] = allocation.proof
		}
		err = p.checkRGB11RejectPolicy(base, checked)
		var violation *RGB11RejectListViolation
		if errors.As(err, &violation) {
			proof := byOpout[violation.Checked]
			if proof == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			proof.PolicyStatus = "rejected"
			proof.PolicyReason = violation.Rejected.String()
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
			excluded[proof.OutPoint] = struct{}{}
			continue
		}
		if err != nil {
			for _, proof := range byOpout {
				proof.PolicyStatus = "unknown"
				proof.PolicyReason = err.Error()
				_ = p.rgbManager.projectionStore.SaveProofState(proof)
			}
			return nil, "", indexer.AssetName{}, "", 0, 0, err
		}
		for _, proof := range byOpout {
			proof.PolicyStatus = "allowed"
			proof.PolicyReason = ""
			_ = p.rgbManager.projectionStore.SaveProofState(proof)
		}
		return selected, baseHash, targetAsset, targetClass, targetType, total, nil
	}
}

func (p *rgb11Manager) selectRGB11AllocationsOnce(contractID string, amount uint64, minConfirmations uint8,
	excluded map[string]struct{}) (
	[]rgb11SpendAllocation, string, indexer.AssetName, string, uint32, uint64, error,
) {
	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, "", indexer.AssetName{}, "", 0, 0, err
	}
	byOutpoint := make(map[string][]*rgb11wallet.AllocationProof)
	var targets []*rgb11wallet.AllocationProof
	for _, proof := range proofs {
		if proof.Status != "valid" && proof.Status != "settled" {
			continue
		}
		byOutpoint[proof.OutPoint] = append(byOutpoint[proof.OutPoint], proof)
		official, err := rgb11wallet.OfficialAssetID(proof.AssetName)
		if err == nil && official == contractID && proof.AssignmentType == 4000 && proof.AssetName.Type != "control" {
			targets = append(targets, proof)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].OutPoint < targets[j].OutPoint })
	selectedOutpoints := make(map[string]bool)
	selected := make([]rgb11SpendAllocation, 0)
	var baseHash, targetClass string
	var targetAsset indexer.AssetName
	var targetType uint32
	var total uint64
	for _, target := range targets {
		if _, skip := excluded[target.OutPoint]; skip {
			continue
		}
		if selectedOutpoints[target.OutPoint] {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(target.OutPoint)
		if err != nil || utxo == nil || utxo.Confirmations < int64(minConfirmations) {
			continue
		}
		group := byOutpoint[target.OutPoint]
		for _, proof := range group {
			official, err := rgb11wallet.OfficialAssetID(proof.AssetName)
			if err != nil || official != contractID {
				return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
			}
			if baseHash == "" {
				baseHash = proof.ConsignmentHash
			}
			output, err := p.rgbManager.projectionStore.LoadOutput(proof.OutPoint)
			if err != nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, err
			}
			amount := output.GetAsset(&proof.AssetName)
			if amount == nil {
				return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
			}
			asset := &indexer.AssetInfo{Name: proof.AssetName, Amount: *amount.Clone(), BindingSat: 0}
			isTarget := proof.AssetName == target.AssetName && proof.AssignmentType == target.AssignmentType
			selected = append(selected, rgb11SpendAllocation{proof: proof, asset: asset, target: isTarget})
			if isTarget {
				value, err := decimalUint64(&asset.Amount)
				if err != nil || ^uint64(0)-total < value {
					return nil, "", indexer.AssetName{}, "", 0, 0, rgb11wallet.ErrInvalidProof
				}
				if targetClass == "" {
					targetClass, targetType, targetAsset = proof.StateClass, proof.AssignmentType, proof.AssetName
				}
				if proof.StateClass != targetClass || proof.AssignmentType != targetType || proof.AssetName != targetAsset {
					return nil, "", indexer.AssetName{}, "", 0, 0, ErrRGB11HistoryMerge
				}
				total += value
			}
		}
		selectedOutpoints[target.OutPoint] = true
		if total >= amount {
			break
		}
	}
	if total < amount || len(selected) == 0 {
		return nil, "", indexer.AssetName{}, "", 0, total, ErrRGB11InsufficientBalance
	}
	return selected, baseHash, targetAsset, targetClass, targetType, total, nil
}

func (p *rgb11Manager) mergeRGB11SpendHistories(selected []rgb11SpendAllocation) (*coreconsignment.Container, string, error) {
	hashes := make(map[string]struct{})
	for _, allocation := range selected {
		if allocation.proof == nil || allocation.proof.ConsignmentHash == "" {
			return nil, "", rgb11wallet.ErrInvalidProof
		}
		hashes[allocation.proof.ConsignmentHash] = struct{}{}
	}
	ordered := make([]string, 0, len(hashes))
	for hash := range hashes {
		ordered = append(ordered, hash)
	}
	sort.Strings(ordered)
	containers := make([]*coreconsignment.Container, 0, len(ordered))
	stateHasher := sha256.New()
	for _, hash := range ordered {
		raw, err := p.rgbManager.projectionStore.LoadObject(hash)
		if err != nil {
			return nil, "", err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil {
			return nil, "", err
		}
		receipt, err := p.rgbManager.projectionStore.LoadValidationReceipt(hash)
		if err != nil {
			return nil, "", err
		}
		containers = append(containers, container)
		stateHasher.Write(receipt.StateHash[:])
	}
	merged, err := coreconsignment.MergeHistories(containers...)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrRGB11HistoryMerge, err)
	}
	return merged, hex.EncodeToString(stateHasher.Sum(nil)), nil
}

type rgb11ChangeSecret struct {
	seal   seals.GraphBlindSeal
	secret [32]byte
}

func newRGB11ChangeOutput(vout uint32, assignmentType uint16, class string, amount uint64, data []byte) (operations.TransitionOutput, rgb11ChangeSecret, error) {
	seal, err := seals.RandomWitnessBlindSeal(vout)
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	secret, err := seal.Conceal()
	if err != nil {
		return operations.TransitionOutput{}, rgb11ChangeSecret{}, err
	}
	return operations.TransitionOutput{
		AssignmentType: assignmentType, Class: class, Amount: amount,
		Data: append([]byte(nil), data...), SecretSeal: [32]byte(secret),
	}, rgb11ChangeSecret{seal: seal, secret: [32]byte(secret)}, nil
}

func decimalUint64(value *indexer.Decimal) (uint64, error) {
	if value == nil || value.Value == nil || value.Value.Sign() < 0 || !value.Value.IsUint64() {
		return 0, rgb11wallet.ErrInvalidProof
	}
	return value.Value.Uint64(), nil
}

func (p *rgb11Manager) buildRGB11WitnessTx(selected []rgb11SpendAllocation, recipientScripts [][]byte, changeScript, opretScript []byte, feeRate int64) (
	*wire.MsgTx, *txscript.MultiPrevOutFetcher, []string, map[int][]byte, int64, error,
) {
	if len(recipientScripts) == 0 || len(recipientScripts) > 32 {
		return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
	}
	if feeRate <= 0 {
		feeRate = p.GetFeeRate()
	}
	if feeRate <= 0 {
		feeRate = 1
	}
	unique := make(map[string]int)
	inputOutpoints := make([]string, 0)
	taprootRoots := make(map[int][]byte)
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	tx := wire.NewMsgTx(2)
	var inputValue int64
	addInput := func(outpoint string, value int64, pkScript []byte) (int, error) {
		if index, ok := unique[outpoint]; ok {
			return index, nil
		}
		wireOutpoint, err := wire.NewOutPointFromString(outpoint)
		if err != nil {
			return 0, err
		}
		index := len(tx.TxIn)
		txIn := wire.NewTxIn(wireOutpoint, nil, nil)
		txIn.Sequence = wire.MaxTxInSequenceNum - 2
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*wireOutpoint, &wire.TxOut{Value: value, PkScript: append([]byte(nil), pkScript...)})
		unique[outpoint] = index
		inputOutpoints = append(inputOutpoints, outpoint)
		inputValue += value
		return index, nil
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return nil, nil, nil, nil, 0, err
	}
	for _, allocation := range selected {
		view, err := p.getL1TxOutput(allocation.proof.OutPoint)
		if err != nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, err)
		}
		if view == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve complete asset view for %s: %w", allocation.proof.OutPoint, ErrRGB11Inconsistent)
		}
		for index := range view.Assets {
			assetName := &view.Assets[index].Name
			if assetName.Protocol == rgb11wallet.Protocol || indexer.IsPlainAsset(assetName) {
				continue
			}
			return nil, nil, nil, nil, 0, fmt.Errorf("%w: %s carries %s", ErrRGB11AssetPreservation,
				allocation.proof.OutPoint, assetName.String())
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.proof.OutPoint)
		if err != nil || utxo == nil {
			return nil, nil, nil, nil, 0, fmt.Errorf("resolve RGB11 carrier %s: %w", allocation.proof.OutPoint, err)
		}
		var taprootRoot []byte
		if binding := allocation.proof.CarrierBinding; binding != nil && binding.CommitmentMethod == "tapret1st" {
			if len(binding.TapretRoot) != sha256.Size || !bytes.Equal(binding.ActualPkScript, utxo.PkScript) ||
				!p.ownsRGB11Carrier(binding, walletScript) {
				return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 Tapret carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
			}
			taprootRoot = append([]byte(nil), binding.TapretRoot...)
		} else if !bytes.Equal(utxo.PkScript, walletScript) {
			return nil, nil, nil, nil, 0, fmt.Errorf("RGB11 carrier %s is not controlled by active wallet", allocation.proof.OutPoint)
		}
		inputIndex, err := addInput(utxo.OutPoint, utxo.Value, utxo.PkScript)
		if err != nil {
			return nil, nil, nil, nil, 0, err
		}
		if len(taprootRoot) != 0 {
			if existing := taprootRoots[inputIndex]; len(existing) != 0 && !bytes.Equal(existing, taprootRoot) {
				return nil, nil, nil, nil, 0, rgb11wallet.ErrInvalidProof
			}
			taprootRoots[inputIndex] = taprootRoot
		}
	}
	tx.AddTxOut(wire.NewTxOut(0, opretScript))
	for _, recipientScript := range recipientScripts {
		if len(recipientScript) == 0 {
			return nil, nil, nil, nil, 0, invoicing.ErrInvalidInvoice
		}
		tx.AddTxOut(wire.NewTxOut(rgb11CarrierValue, recipientScript))
	}
	tx.AddTxOut(wire.NewTxOut(0, changeScript))
	changeIndex := len(tx.TxOut) - 1
	recipientValue := int64(len(recipientScripts)) * rgb11CarrierValue

	plain := p.l1IndexerClient.GetUtxoListWithTicker(p.wallet.GetAddress(), &indexer.ASSET_PLAIN_SAT)
	p.utxoLockerL1.Reload(p.wallet.GetAddress())
	plainIndex := len(plain) - 1
	for {
		var estimate utils.TxWeightEstimator
		for range tx.TxIn {
			estimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
		}
		for _, output := range tx.TxOut {
			estimate.AddTxOutput(output)
		}
		fee := estimate.Fee(feeRate)
		if inputValue >= recipientValue+rgb11CarrierValue+fee {
			tx.TxOut[changeIndex].Value = inputValue - recipientValue - fee
			return tx, prevFetcher, inputOutpoints, taprootRoots, fee, nil
		}
		var candidateFound bool
		for plainIndex >= 0 {
			candidate := plain[plainIndex]
			plainIndex--
			if _, used := unique[candidate.OutPoint]; used || p.utxoLockerL1.IsLocked(candidate.OutPoint) ||
				!bytes.Equal(candidate.PkScript, walletScript) {
				continue
			}
			if _, err := addInput(candidate.OutPoint, candidate.Value, candidate.PkScript); err != nil {
				return nil, nil, nil, nil, 0, err
			}
			candidateFound = true
			break
		}
		if !candidateFound {
			return nil, nil, nil, nil, 0, fmt.Errorf("insufficient plain sats for RGB11 fee")
		}
	}
}

func (p *rgb11Manager) signRGB11PSBT(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	contractID, transitionID [32]byte, transition []byte, bundleID [32]byte,
	mpcProof anchors.MPCProof, mpcCommitment [32]byte,
	inputs []operations.TransitionInput, taprootRoots map[int][]byte) (*psbt.Packet, *wire.MsgTx, []byte, error) {
	packet, err := CreatePsbt(tx, prevFetcher, nil)
	if err != nil {
		return nil, nil, nil, err
	}
	transitionKey, err := corepsbt.RGBTransition(transitionID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	closeKey, err := corepsbt.RGBCloseMethod().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	packet.Unknowns = append(packet.Unknowns,
		&psbt.Unknown{Key: transitionKey, Value: append([]byte(nil), transition...)},
		&psbt.Unknown{Key: closeKey, Value: []byte{2}},
	)
	consumedKey, err := corepsbt.RGBConsumedBy(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	consumed := make([]byte, 2+36*len(inputs))
	binary.LittleEndian.PutUint16(consumed[:2], uint16(len(inputs)))
	for index, input := range inputs {
		offset := 2 + index*36
		copy(consumed[offset:offset+32], input.OperationID[:])
		binary.LittleEndian.PutUint16(consumed[offset+32:offset+34], input.AssignmentType)
		binary.LittleEndian.PutUint16(consumed[offset+34:offset+36], input.Index)
	}
	packet.Unknowns = append(packet.Unknowns, &psbt.Unknown{Key: consumedKey, Value: consumed})
	if len(packet.Outputs) == 0 {
		return nil, nil, nil, fmt.Errorf("RGB11 PSBT has no commitment output")
	}
	messageKey, err := corepsbt.MPCMessage(contractID).RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	depthKey, err := corepsbt.MPCMinTreeDepth().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	commitmentKey, err := corepsbt.MPCCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofKey, err := corepsbt.MPCProof().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretHostKey, err := corepsbt.OpretHost().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	opretCommitmentKey, err := corepsbt.OpretCommitment().RawKey()
	if err != nil {
		return nil, nil, nil, err
	}
	proofValue := make([]byte, 7+32*len(mpcProof.Path))
	binary.LittleEndian.PutUint32(proofValue[:4], mpcProof.Position)
	binary.LittleEndian.PutUint16(proofValue[4:6], mpcProof.Cofactor)
	proofValue[6] = byte(len(mpcProof.Path))
	for index, node := range mpcProof.Path {
		copy(proofValue[7+index*32:], node[:])
	}
	packet.Outputs[0].Unknowns = append(packet.Outputs[0].Unknowns,
		&psbt.Unknown{Key: messageKey, Value: append([]byte(nil), bundleID[:]...)},
		&psbt.Unknown{Key: depthKey, Value: []byte{byte(len(mpcProof.Path))}},
		&psbt.Unknown{Key: commitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
		&psbt.Unknown{Key: proofKey, Value: proofValue},
		&psbt.Unknown{Key: opretHostKey, Value: []byte{1}},
		&psbt.Unknown{Key: opretCommitmentKey, Value: append([]byte(nil), mpcCommitment[:]...)},
	)
	if len(taprootRoots) == 0 {
		if err := p.wallet.SignPsbt(packet); err != nil {
			return nil, nil, nil, err
		}
	} else {
		signer, ok := p.wallet.(interface {
			SignPsbtWithTaprootMerkleRootsAtIndex(*psbt.Packet, map[int][]byte, uint32) error
		})
		if !ok {
			return nil, nil, nil, fmt.Errorf("active wallet does not support RGB11 Tapret signing")
		}
		if err := signer.SignPsbtWithTaprootMerkleRootsAtIndex(packet, taprootRoots, p.wallet.GetSubAccount()); err != nil {
			return nil, nil, nil, err
		}
	}
	if err := psbt.MaybeFinalizeAll(packet); err != nil {
		return nil, nil, nil, err
	}
	var encoded bytes.Buffer
	if err := packet.Serialize(&encoded); err != nil {
		return nil, nil, nil, err
	}
	finalTx, err := psbt.Extract(packet)
	if err != nil {
		return nil, nil, nil, err
	}
	if err := VerifySignedTx(finalTx, prevFetcher); err != nil {
		return nil, nil, nil, err
	}
	return packet, finalTx, encoded.Bytes(), nil
}

func queryValue(invoice *invoicing.Invoice, key string) string {
	if invoice == nil {
		return ""
	}
	for _, param := range invoice.UnknownQuery {
		if param.Key == key {
			return param.Value
		}
	}
	return ""
}

func parseOutpointVout(outpoint string) (uint32, error) {
	separator := strings.LastIndexByte(outpoint, ':')
	if separator < 0 {
		return 0, fmt.Errorf("invalid outpoint")
	}
	value, err := strconv.ParseUint(outpoint[separator+1:], 10, 32)
	return uint32(value), err
}

// RefreshRGB11State derives lifecycle changes only from Bitcoin facts and the
// wallet's validated local history. Unknown spends are fail-closed and remain
// locked; reorgs roll settled proofs back to valid without deleting history.
func (p *rgb11Manager) RefreshRGB11State(ctx context.Context) (*RGB11RefreshResult, error) {
	if p == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil || p.rgbManager.evidence == nil {
		return nil, ErrRGB11Inconsistent
	}
	result := &RGB11RefreshResult{}
	transfers, err := p.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		return nil, err
	}
	expectedSpends := make(map[string]string)
	for _, state := range transfers {
		if state.Direction == "receive" {
			status, err := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
			if err != nil {
				return nil, err
			}
			settled := status != nil && status.Confirmed && status.Confirmations >= int64(state.MinConfirmations)
			if settled {
				if state.Status != "settled" {
					result.Settled++
				}
				state.Status = "settled"
			} else {
				if state.Status == "settled" {
					result.Reorged++
				}
				state.Status = "pending"
				result.Pending++
			}
			lockReason := rgb11wallet.LockReasonPending
			if settled {
				lockReason = rgb11wallet.LockReasonRGB
			}
			for _, outpoint := range state.OutputOutPoints {
				if err := p.utxoLockerL1.SetLockReason(outpoint, lockReason); err != nil {
					return nil, err
				}
			}
			if err := p.rgbManager.projectionStore.SaveTransferState(state); err != nil {
				return nil, err
			}
			continue
		}
		if state.Status == "broadcast" || state.Status == "pending" || state.Status == "settled" {
			for _, outpoint := range state.InputOutPoints {
				expectedSpends[outpoint] = state.WitnessTxID
			}
		}
		if state.Status != "broadcast" && state.Status != "pending" && state.Status != "settled" {
			continue
		}
		pending, err := p.rgbManager.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return nil, err
		}
		status, err := p.rgbManager.evidence.GetTxStatus(state.WitnessTxID)
		if err != nil {
			return nil, err
		}
		if status != nil && (status.InMempool || status.Confirmed) {
			if err := p.applyRGB11LocalChange(ctx, pending, status); err != nil {

				pending.State.Status = "pending"
				_ = p.rgbManager.projectionStore.SavePendingTransferState(pending)
				result.Pending++
				continue
			}
			if status.Confirmed && status.Confirmations >= int64(state.MinConfirmations) {
				if pending.State.Status != "settled" {
					result.Settled++
				}
				pending.State.Status = "settled"
			} else {
				if pending.State.Status == "settled" {
					result.Reorged++
				}
				pending.State.Status = "pending"
				result.Pending++
			}
			if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
				return nil, err
			}
			if pending.State.Status == "settled" &&
				(!pending.State.AddressMode || pending.State.DeliveryAcknowledged) {
				transferIDs := pending.State.BatchTransferIDs
				if len(transferIDs) == 0 {
					transferIDs = []string{pending.State.TransferID}
				}
				if pending.State.AddressMode {
					pending.State.DeliveryCacheCompacted = true
					if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
						return nil, err
					}
				}
				if err := p.rgbManager.projectionStore.CompactSettledRecipientConsignments(transferIDs); err != nil {
					return nil, err
				}
			}
			continue
		}
		conflicted := false
		for _, outpoint := range state.InputOutPoints {
			outspend, err := p.rgbManager.evidence.GetOutspend(outpoint)
			if err != nil {
				return nil, err
			}
			if outspend != nil && outspend.Spent && outspend.SpendingTx != state.WitnessTxID {
				conflicted = true
				result.Inconsistent = append(result.Inconsistent, outpoint)
			}
		}
		if conflicted {
			pending.State.Status = "conflicted"
			pending.State.AckStatus = "invalidated"
			result.Conflicted++
		} else if pending.State.Status == "settled" {
			pending.State.Status = "broadcast"
			result.Reorged++
		}
		if err := p.rgbManager.projectionStore.SavePendingTransferState(pending); err != nil {
			return nil, err
		}
	}

	proofs, err := p.rgbManager.projectionStore.ListProofs()
	if err != nil {
		return nil, err
	}
	for _, proof := range proofs {
		outspend, err := p.rgbManager.evidence.GetOutspend(proof.OutPoint)
		if err != nil {
			return nil, err
		}
		if outspend != nil && outspend.Spent {
			if expectedSpends[proof.OutPoint] == outspend.SpendingTx {
				proof.Status = "pending"
			} else {
				proof.Status = "inconsistent"
				result.Inconsistent = append(result.Inconsistent, proof.OutPoint)
				_ = p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonRGB)
			}
			if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
				return nil, err
			}
			continue
		}
		status, err := p.rgbManager.evidence.GetTxStatus(proof.WitnessTxID)
		if err != nil {
			return nil, err
		}
		wasSettled := proof.Status == "settled"
		if status != nil && status.Confirmed {
			proof.Status = "settled"
			proof.Confirmations = status.Confirmations
			if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonRGB); err != nil {
				return nil, err
			}
		} else {
			proof.Status = "valid"
			proof.Confirmations = 0
			if err := p.utxoLockerL1.SetLockReason(proof.OutPoint, rgb11wallet.LockReasonPending); err != nil {
				return nil, err
			}
			if wasSettled {
				result.Reorged++
			}
		}
		if err := p.rgbManager.projectionStore.SaveProofState(proof); err != nil {
			return nil, err
		}
	}
	if len(result.Inconsistent) > 0 {
		p.rgbManager.consistencyStatus = "broken"
		return result, fmt.Errorf("%w: unknown or conflicting RGB11 spend", ErrRGB11Inconsistent)
	}
	p.rgbManager.consistencyStatus = "ok"
	p.autoBackupRGB11AfterMutation()
	return result, nil
}

func (p *rgb11Manager) applyRGB11LocalChange(ctx context.Context, pending *rgb11wallet.PendingTransfer,
	status *rgb11wallet.BitcoinTxStatus) error {
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(pending.ChangeSeals...)
	receipt, err := p.rgbManager.projectionStore.ValidateAndStoreConsignment(ctx, validator, p.rgbManager.evidence, pending.LocalConsignment)
	if err != nil {
		return err
	}
	receiptHash, err := receipt.Hash()
	if err != nil {
		return err
	}
	replacements := make([]rgb11wallet.ProjectionReplacement, 0)
	wantPrefix := pending.State.WitnessTxID + ":"
	for _, allocation := range receipt.Allocations {
		if !strings.HasPrefix(allocation.OutPoint, wantPrefix) || !allocation.WitnessTxPtr {
			continue
		}
		matched := false
		for _, changeSeal := range pending.ChangeSeals {
			if changeSeal.Vout == outpointVoutMust(allocation.OutPoint) && changeSeal.Blinding == allocation.SealBlinding {
				strict, strictErr := changeSeal.StrictBytes()
				matched = strictErr == nil && bytes.Equal(strict, allocation.SealDisclosure)
				if matched {
					break
				}
			}
		}
		if !matched {
			continue
		}
		utxo, err := p.rgbManager.evidence.GetUTXO(allocation.OutPoint)
		if err != nil || utxo == nil {
			return fmt.Errorf("resolve RGB11 change %s: %w", allocation.OutPoint, err)
		}
		output := indexer.NewTxOutput(utxo.Value)
		output.OutPointStr = allocation.OutPoint
		output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
		binding, err := p.rgb11CarrierBinding(allocation, utxo)
		if err != nil {
			return err
		}
		asset := &indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone(), BindingSat: 0}
		commitment := consensus.TaggedHash(consensus.SecretSealCommitmentTag, allocation.SealDisclosure)
		proofStatus := "valid"
		confirmations := int64(0)
		if status != nil && status.Confirmed {
			proofStatus, confirmations = "settled", status.Confirmations
		}
		proof := &rgb11wallet.AllocationProof{
			OutPoint: allocation.OutPoint, AssetName: allocation.AssetName,
			OperationID: allocation.OperationID, AssignmentType: allocation.AssignmentType,
			AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
			StateData:       append([]byte(nil), allocation.StateData...),
			SealCommitment:  hex.EncodeToString(commitment[:]),
			SealDisclosure:  append([]byte(nil), allocation.SealDisclosure...),
			ConsignmentHash: receipt.ConsignmentHash, ValidationHash: receiptHash,
			WitnessTxID: pending.State.WitnessTxID, Status: proofStatus, Confirmations: confirmations,
			CarrierBinding: binding,
		}
		replacements = append(replacements, rgb11wallet.ProjectionReplacement{Output: output, Asset: asset, Proof: proof})
	}
	return p.rgbManager.projectionStore.ReplaceProjections(pending.State.InputOutPoints, replacements)
}

func outpointVoutMust(outpoint string) uint32 {
	vout, _ := outpointVout(outpoint)
	return vout
}
