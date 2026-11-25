package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func (p *LaunchPoolContract) launchAssetAmount(value int64) *Decimal {
	if p.AssetName.Protocol == rgb11wallet.Protocol {
		return indexer.NewDecimal(value, p.RGBPrecision)
	}
	return indexer.NewDefaultDecimal(value)
}

func (p *LaunchPoolContract) checkRGB11LaunchPoolContent() error {
	if p.AssetName.Protocol != rgb11wallet.Protocol {
		if p.RGBPrecision != 0 || p.RGBGenesisHash != "" || p.RGBSourcePoint != "" {
			return fmt.Errorf("RGB genesis fields require an RGB launchpool")
		}
		return nil
	}
	if _, err := dkvs.RGB11NameKey(p.AssetName.Ticker); p.AssetName.Type != indexer.ASSET_TYPE_FT || err != nil {
		return fmt.Errorf("RGB launchpool requires a canonical asset name")
	}
	if p.BindingSat != 0 || p.MaxSupply <= 0 || p.MaxSupply > math.MaxInt64/100 || p.MintAmtPerSat <= 0 ||
		p.LaunchRatio < LAUNCH_POOL_MIN_RATION || p.LaunchRatio > LAUNCH_POOL_MAX_RATION ||
		p.ReserveRatio < 0 || p.ReserveRatio > LAUNCH_POOL_MAX_RATION-p.LaunchRatio ||
		p.RGBPrecision < 0 || p.RGBPrecision > MAX_ASSET_DIVISIBILITY ||
		!p.launchAssetAmount(p.MaxSupply).Value.IsUint64() {
		return fmt.Errorf("invalid RGB11 launchpool precision or supply")
	}
	hash, err := hex.DecodeString(p.RGBGenesisHash)
	if err != nil || len(hash) != sha256.Size || hex.EncodeToString(hash) != p.RGBGenesisHash {
		return fmt.Errorf("RGB launchpool requires a fixed genesis hash before signing")
	}
	point, err := wire.NewOutPointFromString(p.RGBSourcePoint)
	if err != nil || point.String() != p.RGBSourcePoint {
		return fmt.Errorf("RGB launchpool requires a fixed issuance source")
	}
	return nil
}

// MintAmtPerSat is expressed in whole tokens. RGB quantities keep genesis
// precision, so the price calculation rounds once to a spendable satoshi.
func (p *LaunchPoolContract) rgb11LaunchMintValue(amount *Decimal) (int64, error) {
	if amount == nil || amount.Value == nil || amount.Sign() < 0 || amount.Precision != p.RGBPrecision || p.MintAmtPerSat <= 0 {
		return 0, fmt.Errorf("invalid RGB launchpool mint quantity")
	}
	rate := p.launchAssetAmount(int64(p.MintAmtPerSat))
	value, remainder := new(big.Int), new(big.Int)
	value.QuoRem(amount.Value, rate.Value, remainder)
	if remainder.Sign() != 0 {
		value.Add(value, big.NewInt(1))
	}
	if !value.IsInt64() {
		return 0, fmt.Errorf("RGB launchpool payment exceeds supported bounds")
	}
	return value.Int64(), nil
}

func (p *LaunchPoolContract) rgb11LaunchRatioAmount(ratio int) *Decimal {
	amount := p.launchAssetAmount(p.MaxSupply)
	amount.Value.Mul(amount.Value, big.NewInt(int64(ratio)))
	amount.Value.Quo(amount.Value, big.NewInt(100))
	return amount
}

func (p *LaunchPoolContract) rgb11LaunchMintAmount(value int64) (*Decimal, error) {
	if value <= 0 || p.MintAmtPerSat <= 0 {
		return nil, fmt.Errorf("invalid RGB launchpool payment")
	}
	amount := p.launchAssetAmount(int64(p.MintAmtPerSat))
	amount.Value.Mul(amount.Value, big.NewInt(value))
	return amount, nil
}

func (p *LaunchPoolContractRunTime) handleRGB11LaunchReserve() {
	total := p.rgb11LaunchRatioAmount(p.ReserveRatio)
	if p.AssetAmtInPool.Cmp(total) < 0 ||
		p.AssetAmtInPool.Cmp(p.rgb11LaunchRatioAmount(100-LAUNCH_POOL_MAX_RATION)) < 0 {
		return
	}
	foundationPart := total.Clone()
	foundationPart.Value.Mul(foundationPart.Value, big.NewInt(int64(RESERVE_TO_FOUNDATION)))
	foundationPart.Value.Quo(foundationPart.Value, big.NewInt(1000))
	// The deployer receives the indivisible remainder so the two allocations
	// consume exactly the signed reserve, including its fractional tokens.
	deployerPart := total.Sub(foundationPart)
	deployer := addMintInfo(p.Deployer, p.mintInfoMap)
	deployer.TotalAmt = deployer.TotalAmt.Add(deployerPart)
	if foundationPart.Sign() > 0 {
		foundation := addMintInfo(p.GetFoundationAddress(), p.mintInfoMap)
		foundation.TotalAmt = foundation.TotalAmt.Add(foundationPart)
	}
}

func (p *LaunchPoolContract) encodeRGB11LaunchPoolIdentity() ([]byte, error) {
	if err := p.checkRGB11LaunchPoolContent(); err != nil {
		return nil, err
	}
	result := make([]byte, 70)
	result[0], result[1] = 1, byte(p.RGBPrecision)
	hash, _ := hex.DecodeString(p.RGBGenesisHash)
	copy(result[2:34], hash)
	point, _ := wire.NewOutPointFromString(p.RGBSourcePoint)
	copy(result[34:66], point.Hash[:])
	binary.LittleEndian.PutUint32(result[66:], point.Index)
	return result, nil
}

func (p *LaunchPoolContract) decodeRGB11LaunchPoolIdentity(raw []byte) error {
	if len(raw) != 70 || raw[0] != 1 {
		return fmt.Errorf("invalid fixed RGB launchpool identity")
	}
	p.RGBPrecision, p.RGBGenesisHash = int(raw[1]), hex.EncodeToString(raw[2:34])
	point := wire.OutPoint{Index: binary.LittleEndian.Uint32(raw[66:])}
	copy(point.Hash[:], raw[34:66])
	p.RGBSourcePoint = point.String()
	return p.checkRGB11LaunchPoolContent()
}

type rgb11LaunchPoolGenesis struct {
	Version     uint32                  `json:"version"`
	Owner       string                  `json:"owner"`
	RequestHash string                  `json:"request_hash"`
	Contract    LaunchPoolContract      `json:"contract"`
	Genesis     []byte                  `json:"genesis"`
	Source      *rgb11ChannelOutputJSON `json:"source"`
}

func (r *rgb11LaunchPoolGenesis) validate() error {
	if r.Version != 1 || r.Owner == "" || r.RequestHash == "" || r.Source == nil ||
		r.Source.Value <= 0 || len(r.Source.PkScript) == 0 || r.Source.Outpoint != r.Contract.RGBSourcePoint ||
		r.Contract.AssetName.Protocol != rgb11wallet.Protocol || len(r.Source.Assets) != 1 ||
		r.Source.Assets[0].Amount.Value == nil ||
		r.Source.Assets[0].BindingSat != 0 || r.Source.Assets[0].Amount.Precision != r.Contract.RGBPrecision ||
		r.Contract.RGBPrecision < 0 || r.Contract.RGBPrecision > MAX_ASSET_DIVISIBILITY {
		return fmt.Errorf("invalid fixed RGB11 launchpool issuance")
	}
	if err := r.Contract.CheckContent(); err != nil {
		return err
	}
	if r.Source.Assets[0].Amount.Cmp(r.Contract.launchAssetAmount(r.Contract.MaxSupply)) != 0 {
		return fmt.Errorf("RGB11 issuance source does not hold the complete fixed supply")
	}
	hash := sha256.Sum256(r.Genesis)
	if hex.EncodeToString(hash[:]) != r.Contract.RGBGenesisHash {
		return fmt.Errorf("RGB11 launchpool genesis differs from its durable identity")
	}
	container, err := coreconsignment.DecodeFile(r.Genesis)
	if err != nil {
		return err
	}
	_, registration, err := rgb11wallet.NewRGB11ContractValue(r.Genesis)
	if err != nil {
		return err
	}
	ticker, err := dkvs.RGB11NameTicker(registration.BaseTicker, registration.ContractID)
	source := r.Source.Assets[0].Name
	if err != nil || ticker != r.Contract.AssetName.Ticker || registration.AssetType != r.Contract.AssetName.Type ||
		source.Protocol != r.Contract.AssetName.Protocol || source.Type != registration.AssetType || source.Ticker != registration.ContractID ||
		container.GenesisReport.Kind != schemas.NIA ||
		container.GenesisReport.FungibleAmounts[4000] != r.Contract.launchAssetAmount(r.Contract.MaxSupply).Value.Uint64() {
		return fmt.Errorf("RGB11 launchpool requires its exact fixed-supply NIA genesis")
	}
	return nil
}

func rgb11LaunchPoolKey(kind, identity string) []byte {
	h := sha256.Sum256([]byte(identity))
	return []byte(GetDBKeyPrefix() + "rgb11-launchpool/" + _chain + "/" + kind + "/" + hex.EncodeToString(h[:]))
}

// PrepareRGB11LaunchPool runs on the issuing core before DeployContract. The
// request ID and full genesis are committed together before wallet import or
// any deployment signature. A retry imports the same genesis; it cannot issue
// a fresh contract and silently change the signed asset name.
func (p *Manager) PrepareRGB11LaunchPool(ctx context.Context, requestID string, content *LaunchPoolContract,
	request RGB11IssueRequest) (*LaunchPoolContract, error) {
	if strings.TrimSpace(requestID) == "" || content == nil || content.MaxSupply <= 0 ||
		!strings.EqualFold(request.Schema, "NIA") || len(request.InflationAmounts) != 0 {
		return nil, fmt.Errorf("RGB11 launchpool requires a request ID and a fixed-supply RGB NIA")
	}
	request.Schema = "NIA"
	static := *content
	static.AssetName, static.RGBGenesisHash, static.RGBSourcePoint = indexer.AssetName{}, "", ""
	static.RGBPrecision, static.BindingSat = int(request.Precision), 0
	if static.RGBPrecision > MAX_ASSET_DIVISIBILITY || static.MaxSupply > math.MaxInt64/100 {
		return nil, fmt.Errorf("RGB11 launchpool precision or supply exceeds supported bounds")
	}
	if static.DisplayName == "" {
		static.DisplayName = request.Name
	}
	request.MinConfirmations = max(request.MinConfirmations, 1)
	supply := indexer.NewDecimal(static.MaxSupply, static.RGBPrecision)
	if !supply.Value.IsUint64() {
		return nil, fmt.Errorf("RGB11 launchpool supply exceeds u64")
	}
	if len(request.Amounts) != 0 && (len(request.Amounts) != 1 || request.Amounts[0] != supply.Value.Uint64()) {
		return nil, fmt.Errorf("RGB11 launchpool issuance must contain its full fixed supply")
	}
	request.Amounts = []uint64{supply.Value.Uint64()}
	fingerprint, err := json.Marshal(struct {
		Contract LaunchPoolContract
		Issue    RGB11IssueRequest
	}{static, request})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(fingerprint)
	return runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationNew, func(rgb *rgb11Manager) (*LaunchPoolContract, error) {
		owner := hex.EncodeToString(rgb.wallet.GetPaymentPubKey().SerializeCompressed())
		key := rgb11LaunchPoolKey("request", owner+"/"+requestID)
		requestHash := hex.EncodeToString(digest[:])
		raw, err := p.GetDB().Read(key)
		if errors.Is(err, indexer.ErrKeyNotFound) {
			candidate, prepareErr := rgb.makeRGB11LaunchGenesis(static, request, supply, owner, requestHash)
			raw, err = p.reserveRGB11LaunchGenesis(rgb, key, candidate, prepareErr)
		}
		if err != nil {
			return nil, err
		}
		var record rgb11LaunchPoolGenesis
		if json.Unmarshal(raw, &record) != nil || record.Version != 1 || record.Owner != owner ||
			record.RequestHash != requestHash || record.Source == nil {
			return nil, fmt.Errorf("RGB11 launchpool request ID is immutable")
		}
		if err := record.validate(); err != nil {
			return nil, err
		}
		if _, err := rgb.ImportRGB11Contract(ctx, record.Genesis); err != nil {
			return nil, err
		}
		result := record.Contract
		return &result, nil
	})
}

func (rgb *rgb11Manager) makeRGB11LaunchGenesis(static LaunchPoolContract, request RGB11IssueRequest,
	supply *Decimal, owner, requestHash string) (rgb11LaunchPoolGenesis, error) {
	selected, err := rgb.selectRGB11IssueOutpoints(1, request.MinConfirmations)
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	allocations, err := rgb11IssueAllocations(selected, request.Amounts)
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
		Ticker: request.Ticker, Name: request.Name, Details: request.Details, Precision: request.Precision,
		Terms: request.Terms, Allocations: allocations, RejectListURL: request.RejectListURL})
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	genesis, err := coreconsignment.EncodeFile(issued.Container)
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	asset, err := rgb11wallet.NewContractAssetKey(issued.Container.ContractID, indexer.ASSET_TYPE_FT)
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	ticker, err := dkvs.RGB11NameTicker(request.Ticker, asset.Ticker)
	if err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	utxo, err := rgb.evidence.GetUTXO(selected[0])
	if err != nil {
		return rgb11LaunchPoolGenesis{}, fmt.Errorf("RGB11 issuance source unavailable: %w", err)
	}
	if utxo == nil || utxo.Confirmations < request.MinConfirmations {
		return rgb11LaunchPoolGenesis{}, ErrRGB11IssueUTXOUnavailable
	}
	source := indexer.NewTxOutput(utxo.Value)
	source.OutPointStr, source.OutValue.PkScript = selected[0], append([]byte(nil), utxo.PkScript...)
	source.Assets = indexer.TxAssets{{Name: asset, Amount: *supply.Clone()}}
	static.AssetName, static.RGBSourcePoint = asset, selected[0]
	static.AssetName.Ticker = ticker
	genesisHash := sha256.Sum256(genesis)
	static.RGBGenesisHash = hex.EncodeToString(genesisHash[:])
	if err := static.CheckContent(); err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	record := rgb11LaunchPoolGenesis{Version: 1, Owner: owner, RequestHash: requestHash,
		Contract: static, Genesis: genesis, Source: &rgb11ChannelOutputJSON{
			UtxoID: source.UtxoId, Outpoint: source.OutPointStr, Value: source.Value(),
			PkScript: source.OutValue.PkScript, Assets: source.Assets,
		}}
	if err := record.validate(); err != nil {
		return rgb11LaunchPoolGenesis{}, err
	}
	return record, nil
}

func (p *Manager) reserveRGB11LaunchGenesis(rgb *rgb11Manager, key []byte, record rgb11LaunchPoolGenesis,
	prepareErr error) ([]byte, error) {
	p.l1SendMu.Lock()
	defer p.l1SendMu.Unlock()

	// Another caller may have reserved this request while we queried Bitcoin.
	raw, err := p.GetDB().Read(key)
	if !errors.Is(err, indexer.ErrKeyNotFound) {
		return raw, err
	}
	if prepareErr != nil {
		return nil, prepareErr
	}
	point := record.Source.Outpoint
	changes, err := rgb.rgb11ExpectedChangeOutpoints()
	if err != nil {
		return nil, err
	}
	rgb.utxoLockerL1.Reload(rgb.wallet.GetAddress())
	free, err := rgb.rgb11IssuePointFree(point, changes)
	if err != nil {
		return nil, err
	}
	if !free {
		return nil, fmt.Errorf("%w: RGB11 launchpool issuance source is already reserved", ErrRGB11IssueUTXOUnavailable)
	}
	raw, err = json.Marshal(record)
	if err != nil {
		return nil, err
	}
	batch, err := NewAtomicWriteBatch(p.GetDB())
	if err != nil {
		return nil, err
	}
	defer batch.Close()
	for _, k := range [][]byte{key, rgb11LaunchPoolKey("asset", record.Source.Assets[0].Name.String()), rgb11LaunchPoolKey("source", point)} {
		if err := batch.Put(k, raw); err != nil {
			return nil, err
		}
	}
	if err := batch.Flush(); err != nil {
		return nil, err
	}
	return raw, nil
}

// RGB11LaunchPublicGenesis returns the public registration payload for a prepared launch.
func (p *Manager) RGB11LaunchPublicGenesis(pool *LaunchPoolContract) ([]byte, error) {
	if pool == nil || pool.AssetName.Protocol != rgb11wallet.Protocol {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	raw, err := p.GetDB().Read(rgb11LaunchPoolKey("source", pool.RGBSourcePoint))
	if err != nil {
		return nil, err
	}
	var record rgb11LaunchPoolGenesis
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	if err := record.validate(); err != nil {
		return nil, err
	}
	if record.Contract.Content() != pool.Content() {
		return nil, ErrRGB11PublicIdentityMismatch
	}
	return rgb11wallet.PublicRGB11Contract(record.Genesis)
}

func (p *LaunchPoolContractRunTime) allowRGB11LaunchPoolDeploy() error {
	if err := p.checkRGB11LaunchPoolContent(); err != nil {
		return err
	}
	manager := p.stp.GetWalletMgr()
	asset, err := manager.RGB11LocalAsset(&p.AssetName)
	if err != nil {
		return err
	}
	if p.isInitiator {
		raw, err := p.db.Read(rgb11LaunchPoolKey("asset", asset.String()))
		if err != nil {
			return fmt.Errorf("prepare the RGB genesis on the issuing core before deployment: %w", err)
		}
		var record rgb11LaunchPoolGenesis
		if json.Unmarshal(raw, &record) != nil || record.Contract.AssetName != p.AssetName || record.Contract.RGBGenesisHash != p.RGBGenesisHash ||
			record.Contract.RGBSourcePoint != p.RGBSourcePoint || record.Contract.MaxSupply != p.MaxSupply ||
			record.Contract.RGBPrecision != p.RGBPrecision || record.Owner != hex.EncodeToString(p.LocalPubKey) {
			return fmt.Errorf("deployment does not match the issuing core's fixed genesis")
		}
		if err := record.validate(); err != nil {
			return err
		}
	}
	return p.ContractRuntimeBase.AllowDeploy()
}

func (p *LaunchPoolContractRunTime) validateRGB11LaunchDeposit(ctx context.Context, deposit *RGB11ContractDeposit) error {
	if deposit == nil || deposit.Source == nil || deposit.Source.Output == nil || deposit.Intent.Recipient != p.Address() ||
		deposit.Intent.ContractURL != p.URL() || deposit.Intent.AssetName != p.AssetName.String() ||
		deposit.Intent.Amount != p.launchAssetAmount(p.MaxSupply).Value.String() {
		return fmt.Errorf("RGB launchpool installation changed fixed supply or destination")
	}
	proof, err := nativeproof.Decode(deposit.Source.PublicProof)
	if err != nil {
		return err
	}
	container, err := coreconsignment.Decode(proof.Consignment)
	if err != nil {
		return err
	}
	genesis, _, err := rgb11ContractFromValidatedHistory(proof.Consignment, container.ContractID)
	if err != nil {
		return err
	}
	h := sha256.Sum256(genesis)
	if hex.EncodeToString(h[:]) != p.RGBGenesisHash {
		return fmt.Errorf("RGB launchpool installation changed its signed genesis")
	}
	if container.GenesisReport.Kind != schemas.NIA ||
		container.GenesisReport.FungibleAmounts[4000] != p.launchAssetAmount(p.MaxSupply).Value.Uint64() {
		return fmt.Errorf("RGB launchpool requires the complete fixed NIA supply")
	}
	if err := p.stp.GetWalletMgr().registerRGB11ContractBinding(p); err != nil {
		return err
	}
	result, err := p.stp.GetWalletMgr().validateRGB11ChannelSource(ctx, p.localWallet, p.Address(), deposit.Source, nativeproof.Prepared)
	if err != nil {
		return err
	}
	if result.Asset.Amount.Precision != p.RGBPrecision || result.Asset.Amount.Cmp(p.launchAssetAmount(p.MaxSupply)) != 0 {
		return fmt.Errorf("RGB launchpool RGB11 supply differs from its static contract")
	}
	initial, err := wire.NewOutPointFromString(p.RGBSourcePoint)
	if err != nil {
		return err
	}
	var root *schemas.InputRef
	for _, allocation := range result.History.HistoricalStates {
		if allocation.Outpoint.TxID == [32]byte(initial.Hash) && allocation.Outpoint.Vout == initial.Index &&
			hex.EncodeToString(allocation.Reference.OperationID[:]) == result.Asset.Name.Ticker &&
			allocation.Reference.AssignmentType == 4000 && allocation.State.Class == "fungible" &&
			allocation.State.Amount == p.launchAssetAmount(p.MaxSupply).Value.Uint64() {
			if root != nil {
				return fmt.Errorf("RGB launchpool has multiple full-supply issuance allocations")
			}
			ref := allocation.Reference
			root = &ref
		}
	}
	if root == nil {
		return fmt.Errorf("RGB launchpool transition does not consume its fixed issuance source")
	}
	return validateRGB11LaunchConsumption(result.History, *root, deposit.Source.Output.OutPointStr)
}

// A full-supply historical state is not by itself a consumption proof. Follow
// only the exact, core-validated input edges leading to the installing output.
func validateRGB11LaunchConsumption(history coreconsignment.HistoryReport, root schemas.InputRef, terminal string) error {
	point, err := wire.NewOutPointFromString(terminal)
	if err != nil {
		return err
	}
	inputs := make(map[[32]byte][]schemas.InputRef)
	consumers := make(map[schemas.InputRef][32]byte)
	for _, edge := range history.ValidatedConsumers {
		if old, exists := consumers[edge.Input]; exists {
			if old != edge.ConsumerOperationID {
				return fmt.Errorf("RGB launchpool source has conflicting consumers")
			}
			continue
		}
		consumers[edge.Input] = edge.ConsumerOperationID
		inputs[edge.ConsumerOperationID] = append(inputs[edge.ConsumerOperationID], edge.Input)
	}
	if _, consumed := consumers[root]; !consumed {
		return fmt.Errorf("RGB launchpool issuance source was not consumed")
	}
	visiting, reached := make(map[schemas.InputRef]bool), make(map[schemas.InputRef]bool)
	var visit func(schemas.InputRef) error
	visit = func(ref schemas.InputRef) error {
		if ref == root || reached[ref] {
			return nil
		}
		if visiting[ref] || len(inputs[ref.OperationID]) == 0 {
			return fmt.Errorf("RGB launchpool output is disconnected from its fixed issuance")
		}
		visiting[ref] = true
		for _, input := range inputs[ref.OperationID] {
			if err := visit(input); err != nil {
				return err
			}
		}
		delete(visiting, ref)
		reached[ref] = true
		return nil
	}
	found := false
	for _, allocation := range history.HistoricalStates {
		if allocation.Outpoint.TxID == [32]byte(point.Hash) && allocation.Outpoint.Vout == point.Index {
			if allocation.Reference == root {
				return fmt.Errorf("RGB launchpool did not transfer the issuance to its owners")
			}
			if err := visit(allocation.Reference); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("RGB launchpool installation output is absent from validated history")
	}
	return nil
}

// RGB11LaunchPoolInstallation permits only the fixed installing supply. It
// is also used by the authenticated action-result handler before activation.
func RGB11LaunchPoolInstallation(contract ContractRuntime, deposit *RGB11ContractDeposit) error {
	pool, ok := contract.(*LaunchPoolContractRunTime)
	if !ok || pool.AssetName.Protocol != rgb11wallet.Protocol || pool.HasRun != 0 {
		return fmt.Errorf("not an installing RGB launchpool")
	}
	return pool.validateRGB11LaunchDeposit(context.Background(), deposit)
}

func (p *Manager) prepareRGB11LaunchPoolDeposit(ctx context.Context, pool *LaunchPoolContractRunTime, feeRate int64) (*RGB11ContractDeposit, error) {
	var deposit *RGB11ContractDeposit
	prefix := []byte(GetDBKeyPrefix() + "rgb11-contract-deposit/" + _chain + "/send/")
	if err := p.GetDB().BatchRead(prefix, false, func(_, raw []byte) error {
		var record rgb11ContractDepositJournal
		if json.Unmarshal(raw, &record) != nil {
			return fmt.Errorf("invalid RGB11 deposit recovery record")
		}
		if record.Deposit.Intent.ContractURL != pool.URL() {
			return nil
		}
		if !record.DirectPeer || !bytes.Equal(record.OwnerPublicKey, pool.LocalPubKey) {
			return fmt.Errorf("launchpool installation has another issuer")
		}
		if deposit != nil {
			return fmt.Errorf("multiple issuance transfers claim the same launchpool")
		}
		copy := record.Deposit
		deposit = &copy
		return nil
	}); err != nil {
		return nil, err
	}
	if deposit == nil {
		var err error
		deposit, err = p.prepareRGB11ContractDeposit(ctx, pool, pool.Address(), pool.AssetName.String(),
			pool.launchAssetAmount(pool.MaxSupply).String(), feeRate, pool.localWallet, true)
		if err != nil {
			return nil, err
		}
	}
	if err := p.ReceiveRGB11ContractDeposit(ctx, pool, deposit); err != nil {
		return nil, err
	}
	return deposit, nil
}

func installRGB11LaunchPool(stp ContractManager, resv ContractDeployResvIF, _ any) (any, error) {
	pool, ok := resv.GetContract().(*LaunchPoolContractRunTime)
	if !ok || pool.AssetName.Protocol != rgb11wallet.Protocol {
		return nil, fmt.Errorf("not a RGB11 launchpool")
	}
	if !resv.LocalIsInitiator() {
		return pool.InstallStatus(), nil
	}
	manager := stp.GetWalletMgr()
	ctx := context.Background()
	deposit, err := manager.prepareRGB11LaunchPoolDeposit(ctx, pool, resv.GetFeeRate())
	if err != nil {
		return nil, err
	}
	if _, err := manager.ResumeRGB11ContractDeposit(ctx, deposit.TransferID); err != nil {
		return nil, err
	}
	pool.DeployTickerTxId = strings.Split(pool.RGBSourcePoint, ":")[0]
	pool.MintTxId = strings.Split(deposit.Source.Output.OutPointStr, ":")[0]
	pool.HasDeployed, pool.HasMinted = 2, 1
	pool.Status = CONTRACT_STATUS_INIT + 2
	if err := stp.SaveReservation(resv); err != nil {
		return nil, err
	}
	if _, err := manager.MaterializeRGB11ContractInput(ctx, pool, deposit.Source.Output.OutPointStr); err != nil {
		return nil, err
	}
	signed, err := resv.SignedDeployContractInvoice()
	if err != nil {
		return nil, err
	}
	memo, err := sindexer.NullDataScript(sindexer.CONTENT_TYPE_DEPLOYCONTRACT, signed)
	if err != nil {
		return nil, err
	}
	anchor, err := pool.loadRGB11LaunchAnchor()
	if err != nil {
		return nil, err
	}
	if anchor == nil {
		anchor, err = manager.CreateRGB11DepositAnchor(ctx, pool, pool.Address(), deposit.Source.Output, pool.GetAssetNameV2(), memo)
		if err != nil {
			return nil, err
		}
		if err := pool.saveRGB11LaunchAnchor(anchor); err != nil {
			return nil, err
		}
	}
	if err := pool.sendRGB11LaunchAnchor(ctx, anchor); err != nil {
		return nil, err
	}
	pool.HasMinted, pool.HasExpanded, pool.Status, pool.AnchorTxId = 2, 1, CONTRACT_STATUS_INIT+3, anchor.TxID()
	resv.SetDeployContractTx(anchor)
	resv.SetDeployContractTxId(anchor.TxID())
	resv.SetHasSentDeployTx(1)
	resv.SetStatus(RS_DEPLOY_CONTRACT_INSTALL_STARTED + ResvStatus(pool.Status))
	if err := stp.SaveReservation(resv); err != nil {
		return nil, err
	}
	return pool.InstallStatus(), nil
}

func (p *LaunchPoolContractRunTime) verifyRGB11LaunchAnchor(req *wwire.RemoteSignMoreData_Contract) (*DealInfo, error) {
	p.mutex.RLock()
	installed := p.HasRun != 0
	p.mutex.RUnlock()
	if len(req.Tx) != 1 || req.Tx[0].L1Tx || !req.Tx[0].NotSign || req.Tx[0].Reason != "ascend" || installed {
		return nil, fmt.Errorf("invalid RGB11 launchpool anchor request")
	}
	tx, err := DecodeMsgTx_SatsNet(req.Tx[0].Tx)
	if err != nil {
		return nil, err
	}
	manager := p.stp.GetWalletMgr()
	bind, err := manager.AnchorOutputsActive()
	if err != nil || len(tx.TxIn) != 1 {
		return nil, fmt.Errorf("invalid RGB11 launchpool anchor input: %w", err)
	}
	data, _, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, bind)
	if err != nil {
		return nil, err
	}
	if _, err := manager.MaterializeRGB11ContractInput(context.Background(), p, data.Utxo); err != nil {
		return nil, err
	}
	if err := manager.ValidateRGB11DepositAnchor(context.Background(), p, tx); err != nil {
		return nil, err
	}
	signed, err := p.resv.SignedDeployContractInvoice()
	if err != nil {
		return nil, err
	}
	expected, err := sindexer.ParseSignedDeployContractInvoice(signed)
	if err != nil {
		return nil, err
	}
	count := 0
	for _, output := range tx.TxOut {
		kind, _, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err == nil && kind == sindexer.CONTENT_TYPE_DEPLOYCONTRACT {
			count++
		}
	}
	if count != 1 {
		return nil, fmt.Errorf("RGB11 launchpool anchor requires one signed deployment invoice")
	}
	// Local/remote signature fields are reversed in the peer's reservation.
	// CheckDeployTx authenticates both signatures in initiator order; compare
	// the unsigned payload with this reservation's already agreed identity.
	_, observed, err := p.CheckDeployTx(tx)
	if err != nil {
		return nil, err
	}
	if observed.ContractPath != expected.ContractPath || observed.DeployTime != expected.DeployTime ||
		!bytes.Equal(observed.ContractContent, expected.ContractContent) {
		return nil, fmt.Errorf("RGB11 launchpool anchor changed the signed deployment identity")
	}
	if err := p.saveRGB11LaunchAnchor(tx); err != nil {
		return nil, err
	}
	return &DealInfo{Reason: "rgb11-launchpool-install", TxId: tx.TxID(), AssetName: p.GetAssetName(), TotalAmt: p.launchAssetAmount(p.MaxSupply)}, nil
}
