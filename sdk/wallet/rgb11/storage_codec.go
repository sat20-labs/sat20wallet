package rgb11wallet

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/seals"
	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const (
	rgb11StoreMagic        = "R11R"
	rgb11StoreCodecVersion = uint8(1)
	rgb11StoreMaxText      = 64 * 1024
	rgb11StoreMaxBytes     = 4 * 1024 * 1024
	rgb11StoreMaxRecords   = 16 * 1024

	rgb11RecordReceipt    = uint8(1)
	rgb11RecordOutput     = uint8(2)
	rgb11RecordProof      = uint8(3)
	rgb11RecordPending    = uint8(4)
	rgb11RecordTransfer   = uint8(5)
	rgb11RecordReceiveKey = uint8(6)
)

func encode(value any) ([]byte, error) {
	var kind uint8
	var write func(*strict.Encoder) error
	switch item := value.(type) {
	case *ValidationReceipt:
		kind, write = rgb11RecordReceipt, func(e *strict.Encoder) error { return encodeValidationReceipt(e, item) }
	case *indexer.TxOutput:
		kind, write = rgb11RecordOutput, func(e *strict.Encoder) error { return encodeProjectionOutput(e, item) }
	case *AllocationProof:
		kind, write = rgb11RecordProof, func(e *strict.Encoder) error { return encodeAllocationProof(e, item) }
	case *PendingTransfer:
		kind, write = rgb11RecordPending, func(e *strict.Encoder) error { return encodePendingTransfer(e, item) }
	case *TransferState:
		kind, write = rgb11RecordTransfer, func(e *strict.Encoder) error { return encodeTransferState(e, item) }
	case *ReceiveKey:
		kind, write = rgb11RecordReceiveKey, func(e *strict.Encoder) error { return encodeReceiveKey(e, item) }
	default:
		return nil, fmt.Errorf("unsupported RGB11 storage record %T", value)
	}
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	if err := e.Raw([]byte(rgb11StoreMagic)); err != nil {
		return nil, err
	}
	if err := e.U8(rgb11StoreCodecVersion); err != nil {
		return nil, err
	}
	if err := e.U8(kind); err != nil {
		return nil, err
	}
	if err := write(e); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decode(data []byte, target any) error {
	if len(data) == 0 || len(data) > rgb11StoreMaxBytes {
		return ErrRGB11Inconsistent
	}
	r := bytes.NewReader(data)
	d := strict.NewDecoder(r)
	magic, err := d.Raw(uint64(len(rgb11StoreMagic)))
	if err != nil || string(magic) != rgb11StoreMagic {
		return ErrRGB11Inconsistent
	}
	version, err := d.U8()
	if err != nil || version != rgb11StoreCodecVersion {
		return ErrRGB11Inconsistent
	}
	kind, err := d.U8()
	if err != nil {
		return ErrRGB11Inconsistent
	}
	var read func(*strict.Decoder) error
	switch item := target.(type) {
	case *ValidationReceipt:
		if kind != rgb11RecordReceipt {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodeValidationReceipt(d, item) }
	case *indexer.TxOutput:
		if kind != rgb11RecordOutput {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodeProjectionOutput(d, item) }
	case *AllocationProof:
		if kind != rgb11RecordProof {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodeAllocationProof(d, item) }
	case *PendingTransfer:
		if kind != rgb11RecordPending {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodePendingTransfer(d, item) }
	case *TransferState:
		if kind != rgb11RecordTransfer {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodeTransferState(d, item) }
	case *ReceiveKey:
		if kind != rgb11RecordReceiveKey {
			return ErrRGB11Inconsistent
		}
		read = func(d *strict.Decoder) error { return decodeReceiveKey(d, item) }
	default:
		return fmt.Errorf("unsupported RGB11 storage target %T", target)
	}
	if err := read(d); err != nil || r.Len() != 0 {
		return ErrRGB11Inconsistent
	}
	return nil
}

func encodeText(e *strict.Encoder, value string) error { return e.String(value, 0, rgb11StoreMaxText) }
func decodeText(d *strict.Decoder) (string, error)     { return d.String(0, rgb11StoreMaxText) }
func encodeBlob(e *strict.Encoder, value []byte) error { return e.Bytes(value, 0, rgb11StoreMaxBytes) }
func decodeBlob(d *strict.Decoder) ([]byte, error)     { return d.Bytes(0, rgb11StoreMaxBytes) }

func encodeAssetName(e *strict.Encoder, name indexer.AssetName) error {
	for _, value := range []string{name.Protocol, name.Type, name.Ticker} {
		if err := encodeText(e, value); err != nil {
			return err
		}
	}
	return nil
}

func decodeAssetName(d *strict.Decoder) (indexer.AssetName, error) {
	var name indexer.AssetName
	var err error
	if name.Protocol, err = decodeText(d); err != nil {
		return name, err
	}
	if name.Type, err = decodeText(d); err != nil {
		return name, err
	}
	name.Ticker, err = decodeText(d)
	return name, err
}

func encodeDecimal(e *strict.Encoder, value *indexer.Decimal) error {
	if value == nil || value.Value == nil || indexer.ValidateDecimalPrecision(value.Precision) != nil {
		return ErrRGB11Inconsistent
	}
	if err := e.U8(uint8(value.Precision)); err != nil {
		return err
	}
	if err := e.Bool(value.Value.Sign() < 0); err != nil {
		return err
	}
	return e.Bytes(value.Value.Bytes(), 0, 128)
}

func decodeDecimal(d *strict.Decoder) (indexer.Decimal, error) {
	precision, err := d.U8()
	if err != nil || indexer.ValidateDecimalPrecision(int(precision)) != nil {
		return indexer.Decimal{}, ErrRGB11Inconsistent
	}
	negative, err := d.Bool()
	if err != nil {
		return indexer.Decimal{}, err
	}
	magnitude, err := d.Bytes(0, 128)
	if err != nil || (len(magnitude) > 0 && magnitude[0] == 0) || (negative && len(magnitude) == 0) {
		return indexer.Decimal{}, ErrRGB11Inconsistent
	}
	value := new(big.Int).SetBytes(magnitude)
	if negative {
		value.Neg(value)
	}
	return indexer.Decimal{Precision: int(precision), Value: value}, nil
}

func encodeAssetInfo(e *strict.Encoder, asset *indexer.AssetInfo) error {
	if asset == nil {
		return ErrRGB11Inconsistent
	}
	if err := encodeAssetName(e, asset.Name); err != nil {
		return err
	}
	if err := encodeDecimal(e, &asset.Amount); err != nil {
		return err
	}
	return e.U32(asset.BindingSat)
}

func decodeAssetInfo(d *strict.Decoder) (indexer.AssetInfo, error) {
	name, err := decodeAssetName(d)
	if err != nil {
		return indexer.AssetInfo{}, err
	}
	amount, err := decodeDecimal(d)
	if err != nil {
		return indexer.AssetInfo{}, err
	}
	binding, err := d.U32()
	if err != nil {
		return indexer.AssetInfo{}, err
	}
	return indexer.AssetInfo{Name: name, Amount: amount, BindingSat: binding}, nil
}

func encodeOptionalAssetInfo(e *strict.Encoder, asset *indexer.AssetInfo) error {
	present := asset != nil && (asset.Name.Protocol != "" || asset.Name.Type != "" || asset.Name.Ticker != "" || asset.Amount.Value != nil || asset.BindingSat != 0)
	return e.Option(present, func(e *strict.Encoder) error { return encodeAssetInfo(e, asset) })
}

func decodeOptionalAssetInfo(d *strict.Decoder) (indexer.AssetInfo, error) {
	asset := indexer.AssetInfo{}
	present, err := d.Option(func(d *strict.Decoder) error {
		decoded, err := decodeAssetInfo(d)
		if err != nil {
			return err
		}
		asset = decoded
		return nil
	})
	if err != nil {
		return indexer.AssetInfo{}, err
	}
	if !present {
		return indexer.AssetInfo{}, nil
	}
	return asset, nil
}

func encodeAssetList(e *strict.Encoder, assets indexer.TxAssets) error {
	if err := e.Length(uint64(len(assets)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for index := range assets {
		if err := encodeAssetInfo(e, &assets[index]); err != nil {
			return err
		}
	}
	return nil
}

func decodeAssetList(d *strict.Decoder) (indexer.TxAssets, error) {
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, err
	}
	assets := make(indexer.TxAssets, 0, count)
	for index := uint64(0); index < count; index++ {
		asset, err := decodeAssetInfo(d)
		if err != nil {
			return nil, err
		}
		if err := (&assets).Add(&asset); err != nil {
			return nil, err
		}
	}
	return assets, nil
}

func encodeValidationReceipt(e *strict.Encoder, receipt *ValidationReceipt) error {
	if receipt == nil {
		return ErrValidationReceipt
	}
	for _, write := range []func() error{
		func() error { return e.U32(receipt.Version) },
		func() error { return encodeText(e, receipt.EngineBuildID) },
		func() error { return encodeText(e, receipt.ConsignmentHash) },
		func() error { return encodeText(e, receipt.ContractID) },
		func() error { return encodeText(e, receipt.SchemaID) },
		func() error { return encodeText(e, receipt.TransferID) },
		func() error { return e.Raw(receipt.StateHash[:]) },
		func() error { return e.U64(uint64(receipt.ValidatedAt)) },
		func() error { return encodeText(e, receipt.Status) },
		func() error { return e.Length(uint64(len(receipt.Allocations)), rgb11StoreMaxRecords) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	for index := range receipt.Allocations {
		if err := encodeValidatedAllocation(e, &receipt.Allocations[index]); err != nil {
			return err
		}
	}
	return nil
}

func decodeValidationReceipt(d *strict.Decoder, receipt *ValidationReceipt) error {
	if receipt == nil {
		return ErrValidationReceipt
	}
	var err error
	if receipt.Version, err = d.U32(); err != nil {
		return err
	}
	for _, target := range []*string{&receipt.EngineBuildID, &receipt.ConsignmentHash, &receipt.ContractID, &receipt.SchemaID, &receipt.TransferID} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	stateHash, err := d.Raw(32)
	if err != nil {
		return err
	}
	copy(receipt.StateHash[:], stateHash)
	validatedAt, err := d.U64()
	if err != nil {
		return err
	}
	receipt.ValidatedAt = int64(validatedAt)
	if receipt.Status, err = decodeText(d); err != nil {
		return err
	}
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return err
	}
	receipt.Allocations = make([]ValidatedAllocation, count)
	for index := range receipt.Allocations {
		if err := decodeValidatedAllocation(d, &receipt.Allocations[index]); err != nil {
			return err
		}
	}
	return nil
}

func encodeValidatedAllocation(e *strict.Encoder, allocation *ValidatedAllocation) error {
	if allocation == nil {
		return ErrValidationReceipt
	}
	for _, write := range []func() error{
		func() error { return encodeText(e, allocation.OutPoint) },
		func() error { return encodeAssetName(e, allocation.AssetName) },
		func() error { return encodeDecimal(e, &allocation.Amount) },
		func() error { return encodeText(e, allocation.OperationID) },
		func() error { return e.U32(allocation.AssignmentType) },
		func() error { return e.U32(allocation.AssignmentIndex) },
		func() error { return encodeText(e, allocation.StateClass) },
		func() error { return encodeBlob(e, allocation.StateData) },
		func() error { return encodeBlob(e, allocation.SealDisclosure) },
		func() error { return e.U64(allocation.SealBlinding) },
		func() error { return e.Bool(allocation.WitnessTxPtr) },
		func() error { return encodeText(e, allocation.CommitmentMethod) },
		func() error { return encodeBlob(e, allocation.CarrierInternalKey) },
		func() error { return encodeBlob(e, allocation.TapretRoot) },
		func() error { return encodeBlob(e, allocation.TapretProof) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

func decodeValidatedAllocation(d *strict.Decoder, allocation *ValidatedAllocation) error {
	if allocation == nil {
		return ErrValidationReceipt
	}
	var err error
	if allocation.OutPoint, err = decodeText(d); err != nil {
		return err
	}
	if allocation.AssetName, err = decodeAssetName(d); err != nil {
		return err
	}
	if allocation.Amount, err = decodeDecimal(d); err != nil {
		return err
	}
	if allocation.OperationID, err = decodeText(d); err != nil {
		return err
	}
	if allocation.AssignmentType, err = d.U32(); err != nil {
		return err
	}
	if allocation.AssignmentIndex, err = d.U32(); err != nil {
		return err
	}
	if allocation.StateClass, err = decodeText(d); err != nil {
		return err
	}
	if allocation.StateData, err = decodeBlob(d); err != nil {
		return err
	}
	if allocation.SealDisclosure, err = decodeBlob(d); err != nil {
		return err
	}
	if allocation.SealBlinding, err = d.U64(); err != nil {
		return err
	}
	if allocation.WitnessTxPtr, err = d.Bool(); err != nil {
		return err
	}
	if allocation.CommitmentMethod, err = decodeText(d); err != nil {
		return err
	}
	if allocation.CarrierInternalKey, err = decodeBlob(d); err != nil {
		return err
	}
	if allocation.TapretRoot, err = decodeBlob(d); err != nil {
		return err
	}
	allocation.TapretProof, err = decodeBlob(d)
	return err
}

func encodeProjectionOutput(e *strict.Encoder, output *indexer.TxOutput) error {
	if output == nil {
		return ErrProjectionMismatch
	}
	if output.OutValue.Value < 0 {
		return ErrProjectionMismatch
	}
	if err := e.U64(output.UtxoId); err != nil {
		return err
	}
	if err := encodeText(e, output.OutPointStr); err != nil {
		return err
	}
	if err := e.U64(uint64(output.OutValue.Value)); err != nil {
		return err
	}
	if err := encodeBlob(e, output.OutValue.PkScript); err != nil {
		return err
	}
	if err := encodeAssetList(e, output.Assets); err != nil {
		return err
	}
	if err := encodeOutputOffsets(e, output.Offsets); err != nil {
		return err
	}
	if err := encodeSatBindingMap(e, output.SatBindingMap); err != nil {
		return err
	}
	return encodeInvalidAssets(e, output.Invalids)
}

func decodeProjectionOutput(d *strict.Decoder, output *indexer.TxOutput) error {
	if output == nil {
		return ErrProjectionMismatch
	}
	utxoID, err := d.U64()
	if err != nil {
		return err
	}
	outpoint, err := decodeText(d)
	if err != nil {
		return err
	}
	value, err := d.U64()
	if err != nil || value > math.MaxInt64 {
		return err
	}
	pkScript, err := decodeBlob(d)
	if err != nil {
		return err
	}
	assets, err := decodeAssetList(d)
	if err != nil {
		return err
	}
	offsets, err := decodeOutputOffsets(d)
	if err != nil {
		return err
	}
	bindings, err := decodeSatBindingMap(d)
	if err != nil {
		return err
	}
	invalids, err := decodeInvalidAssets(d)
	if err != nil {
		return err
	}
	*output = *indexer.NewTxOutput(int64(value))
	output.UtxoId, output.OutPointStr = utxoID, outpoint
	output.OutValue.PkScript = pkScript
	output.Assets, output.Offsets, output.SatBindingMap, output.Invalids = assets, offsets, bindings, invalids
	return nil
}

func sortedAssetNames[V any](values map[indexer.AssetName]V) []indexer.AssetName {
	names := make([]indexer.AssetName, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return names[i].String() < names[j].String() })
	return names
}

func encodeOutputOffsets(e *strict.Encoder, values map[indexer.AssetName]indexer.AssetOffsets) error {
	if err := e.Length(uint64(len(values)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for _, name := range sortedAssetNames(values) {
		if err := encodeAssetName(e, name); err != nil {
			return err
		}
		ranges := values[name]
		if err := e.Length(uint64(len(ranges)), rgb11StoreMaxRecords); err != nil {
			return err
		}
		for _, item := range ranges {
			if item == nil {
				return ErrProjectionMismatch
			}
			if err := e.U64(uint64(item.Start)); err != nil {
				return err
			}
			if err := e.U64(uint64(item.End)); err != nil {
				return err
			}
		}
	}
	return nil
}

func decodeOutputOffsets(d *strict.Decoder) (map[indexer.AssetName]indexer.AssetOffsets, error) {
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, err
	}
	values := make(map[indexer.AssetName]indexer.AssetOffsets, count)
	for index := uint64(0); index < count; index++ {
		name, err := decodeAssetName(d)
		if err != nil {
			return nil, err
		}
		if _, exists := values[name]; exists {
			return nil, ErrProjectionMismatch
		}
		rangeCount, err := d.Length(rgb11StoreMaxRecords)
		if err != nil {
			return nil, err
		}
		ranges := make(indexer.AssetOffsets, 0, rangeCount)
		for item := uint64(0); item < rangeCount; item++ {
			start, err := d.U64()
			if err != nil {
				return nil, err
			}
			end, err := d.U64()
			if err != nil || start > math.MaxInt64 || end > math.MaxInt64 || end < start {
				return nil, ErrProjectionMismatch
			}
			ranges = append(ranges, &indexer.OffsetRange{Start: int64(start), End: int64(end)})
		}
		values[name] = ranges
	}
	return values, nil
}

func encodeSatBindingMap(e *strict.Encoder, values map[int64]*indexer.AssetInfo) error {
	keys := make([]int64, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	if err := e.Length(uint64(len(keys)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for _, key := range keys {
		if key < 0 {
			return ErrProjectionMismatch
		}
		if err := e.U64(uint64(key)); err != nil {
			return err
		}
		if err := encodeAssetInfo(e, values[key]); err != nil {
			return err
		}
	}
	return nil
}

func decodeSatBindingMap(d *strict.Decoder) (map[int64]*indexer.AssetInfo, error) {
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, err
	}
	values := make(map[int64]*indexer.AssetInfo, count)
	for index := uint64(0); index < count; index++ {
		key, err := d.U64()
		if err != nil || key > math.MaxInt64 {
			return nil, err
		}
		asset, err := decodeAssetInfo(d)
		if err != nil {
			return nil, err
		}
		if _, exists := values[int64(key)]; exists {
			return nil, ErrProjectionMismatch
		}
		values[int64(key)] = &asset
	}
	return values, nil
}

func encodeInvalidAssets(e *strict.Encoder, values map[indexer.AssetName]bool) error {
	if err := e.Length(uint64(len(values)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for _, name := range sortedAssetNames(values) {
		if err := encodeAssetName(e, name); err != nil {
			return err
		}
		if err := e.Bool(values[name]); err != nil {
			return err
		}
	}
	return nil
}

func decodeInvalidAssets(d *strict.Decoder) (map[indexer.AssetName]bool, error) {
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, err
	}
	values := make(map[indexer.AssetName]bool, count)
	for index := uint64(0); index < count; index++ {
		name, err := decodeAssetName(d)
		if err != nil {
			return nil, err
		}
		value, err := d.Bool()
		if err != nil {
			return nil, ErrProjectionMismatch
		}
		if _, exists := values[name]; exists {
			return nil, ErrProjectionMismatch
		}
		values[name] = value
	}
	return values, nil
}

func encodeAllocationProof(e *strict.Encoder, proof *AllocationProof) error {
	if proof == nil {
		return ErrInvalidProof
	}
	for _, write := range []func() error{
		func() error { return encodeText(e, proof.OutPoint) },
		func() error { return encodeAssetName(e, proof.AssetName) },
		func() error { return encodeText(e, proof.OperationID) },
		func() error { return e.U32(proof.AssignmentType) },
		func() error { return e.U32(proof.AssignmentIndex) },
		func() error { return encodeText(e, proof.StateClass) },
		func() error { return encodeBlob(e, proof.StateData) },
		func() error { return encodeText(e, proof.SealCommitment) },
		func() error { return encodeBlob(e, proof.SealDisclosure) },
		func() error { return encodeText(e, proof.ConsignmentHash) },
		func() error { return encodeText(e, proof.ValidationHash) },
		func() error { return encodeText(e, proof.WitnessTxID) },
		func() error { return encodeCarrierBinding(e, proof.CarrierBinding) },
		func() error { return encodeText(e, proof.Status) },
		func() error { return e.U64(uint64(proof.Confirmations)) },
		func() error { return encodeText(e, proof.PolicyStatus) },
		func() error { return encodeText(e, proof.PolicyReason) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

func decodeAllocationProof(d *strict.Decoder, proof *AllocationProof) error {
	if proof == nil {
		return ErrInvalidProof
	}
	var err error
	if proof.OutPoint, err = decodeText(d); err != nil {
		return err
	}
	if proof.AssetName, err = decodeAssetName(d); err != nil {
		return err
	}
	if proof.OperationID, err = decodeText(d); err != nil {
		return err
	}
	if proof.AssignmentType, err = d.U32(); err != nil {
		return err
	}
	if proof.AssignmentIndex, err = d.U32(); err != nil {
		return err
	}
	if proof.StateClass, err = decodeText(d); err != nil {
		return err
	}
	if proof.StateData, err = decodeBlob(d); err != nil {
		return err
	}
	if proof.SealCommitment, err = decodeText(d); err != nil {
		return err
	}
	if proof.SealDisclosure, err = decodeBlob(d); err != nil {
		return err
	}
	for _, target := range []*string{&proof.ConsignmentHash, &proof.ValidationHash, &proof.WitnessTxID} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if proof.CarrierBinding, err = decodeCarrierBinding(d); err != nil {
		return err
	}
	if proof.Status, err = decodeText(d); err != nil {
		return err
	}
	confirmations, err := d.U64()
	if err != nil {
		return err
	}
	proof.Confirmations = int64(confirmations)
	if proof.PolicyStatus, err = decodeText(d); err != nil {
		return err
	}
	proof.PolicyReason, err = decodeText(d)
	return err
}

func encodeCarrierBinding(e *strict.Encoder, binding *CarrierBinding) error {
	return e.Option(binding != nil, func(e *strict.Encoder) error {
		for _, write := range []func() error{
			func() error { return e.U32(binding.DerivationIndex) },
			func() error { return encodeText(e, binding.LogicalAddress) },
			func() error { return encodeText(e, binding.OutPoint) },
			func() error { return encodeBlob(e, binding.ActualPkScript) },
			func() error { return encodeBlob(e, binding.ActualOutputKey) },
			func() error { return encodeBlob(e, binding.InternalPubKey) },
			func() error { return encodeBlob(e, binding.TapretRoot) },
			func() error { return encodeBlob(e, binding.TapretProof) },
			func() error { return encodeText(e, binding.CommitmentMethod) },
		} {
			if err := write(); err != nil {
				return err
			}
		}
		return nil
	})
}

func decodeCarrierBinding(d *strict.Decoder) (*CarrierBinding, error) {
	binding := &CarrierBinding{}
	present, err := d.Option(func(d *strict.Decoder) error {
		var err error
		if binding.DerivationIndex, err = d.U32(); err != nil {
			return err
		}
		for _, target := range []*string{&binding.LogicalAddress, &binding.OutPoint} {
			if *target, err = decodeText(d); err != nil {
				return err
			}
		}
		for _, target := range []*[]byte{&binding.ActualPkScript, &binding.ActualOutputKey, &binding.InternalPubKey, &binding.TapretRoot, &binding.TapretProof} {
			if *target, err = decodeBlob(d); err != nil {
				return err
			}
		}
		binding.CommitmentMethod, err = decodeText(d)
		return err
	})
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	return binding, nil
}

func encodeReceiveKey(e *strict.Encoder, key *ReceiveKey) error {
	if key == nil || key.Version != 1 || key.RequestID == "" ||
		key.Change != 1 || key.Index >= 1<<31 || key.LogicalAddress == "" ||
		len(key.WitnessScript) == 0 || len(key.InternalPubKey) != 32 {
		return ErrRGB11Inconsistent
	}
	for _, write := range []func() error{
		func() error { return e.U8(key.Version) },
		func() error { return encodeText(e, key.RequestID) },
		func() error { return e.U32(key.ScopeIndex) },
		func() error { return e.U32(key.Change) },
		func() error { return e.U32(key.Index) },
		func() error { return encodeText(e, key.LogicalAddress) },
		func() error { return encodeBlob(e, key.WitnessScript) },
		func() error { return encodeBlob(e, key.InternalPubKey) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

func decodeReceiveKey(d *strict.Decoder, key *ReceiveKey) error {
	var err error
	if key.Version, err = d.U8(); err != nil {
		return err
	}
	if key.RequestID, err = decodeText(d); err != nil {
		return err
	}
	if key.ScopeIndex, err = d.U32(); err != nil {
		return err
	}
	if key.Change, err = d.U32(); err != nil {
		return err
	}
	if key.Index, err = d.U32(); err != nil {
		return err
	}
	if key.LogicalAddress, err = decodeText(d); err != nil {
		return err
	}
	if key.WitnessScript, err = decodeBlob(d); err != nil {
		return err
	}
	if key.InternalPubKey, err = decodeBlob(d); err != nil {
		return err
	}
	if key.Version != 1 || key.RequestID == "" || key.Change != 1 ||
		key.Index >= 1<<31 || key.LogicalAddress == "" ||
		len(key.WitnessScript) == 0 || len(key.InternalPubKey) != 32 {
		return ErrRGB11Inconsistent
	}
	return nil
}

func encodeStringList(e *strict.Encoder, values []string) error {
	if err := e.Length(uint64(len(values)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for _, value := range values {
		if err := encodeText(e, value); err != nil {
			return err
		}
	}
	return nil
}

func decodeStringList(d *strict.Decoder) ([]string, error) {
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return nil, err
	}
	values := make([]string, count)
	for index := range values {
		if values[index], err = decodeText(d); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func encodeTransferState(e *strict.Encoder, state *TransferState) error {
	if state == nil {
		return ErrValidationReceipt
	}
	for _, write := range []func() error{
		func() error { return encodeText(e, state.TransferID) },
		func() error { return encodeText(e, state.BatchID) },
		func() error { return encodeStringList(e, state.BatchTransferIDs) },
		func() error { return e.U64(uint64(state.BatchSize)) },
		func() error { return e.U32(state.RecipientVout) },
		func() error { return encodeText(e, state.TransportMode) },
		func() error { return encodeText(e, state.Direction) },
		func() error { return encodeOptionalAssetInfo(e, &state.Asset) },
		func() error { return encodeText(e, state.RecipientID) },
		func() error { return encodeText(e, state.Invoice) },
		func() error { return encodeStringList(e, state.InputOutPoints) },
		func() error { return encodeStringList(e, state.OutputOutPoints) },
		func() error { return e.U8(state.MinConfirmations) },
		func() error { return e.U64(uint64(state.Expiry)) },
		func() error { return encodeText(e, state.ConsignmentHash) },
		func() error { return encodeText(e, state.WitnessTxID) },
		func() error { return encodeText(e, state.AckStatus) },
		func() error { return encodeText(e, state.Status) },
		func() error { return encodeText(e, state.RejectReason) },
		func() error { return encodeStringList(e, state.RejectedOpouts) },
		func() error { return encodeText(e, state.RelayRecordKey) },
		func() error { return encodeText(e, state.AckRecordKey) },
		func() error { return encodeText(e, state.RelayDurability) },
		func() error { return e.U64(uint64(state.RelayExpiry)) },
		func() error { return encodeText(e, state.NetworkBackupRef) },
		func() error { return encodeText(e, state.ParentStateHash) },
		func() error { return encodeText(e, state.DKVSOperationID) },
		func() error { return e.Bool(state.AddressMode) },
		func() error { return encodeText(e, state.AddressMessageID) },
		func() error { return encodeText(e, state.SenderAccountID) },
		func() error { return encodeText(e, state.ReceiverAccountID) },
		func() error { return encodeText(e, state.ReceiverAddress) },
		func() error { return encodeText(e, state.ReceiveCapabilityKey) },
		func() error { return encodeText(e, state.ReceiveCapabilityHash) },
		func() error { return encodeText(e, state.DeliveryMode) },
		func() error { return encodeText(e, state.DeliveryObjectID) },
		func() error { return encodeText(e, state.DeliveryRecordKey) },
		func() error { return encodeText(e, state.DeliveryRecordHash) },
		func() error { return e.Bool(state.DeliveryTemporary) },
		func() error { return e.U64(state.DeliveryExpiryHeight) },
		func() error { return e.U64(state.DeliveryTTL) },
		func() error { return e.Bool(state.DeliveryAcknowledged) },
		func() error { return e.Bool(state.DeliveryCacheCompacted) },
		func() error { return e.Bool(state.SyntheticInvoiceRemoved) },
	} {
		if err := write(); err != nil {
			return err
		}
	}
	return nil
}

func decodeTransferState(d *strict.Decoder, state *TransferState) error {
	if state == nil {
		return ErrValidationReceipt
	}
	var err error
	for _, target := range []*string{&state.TransferID, &state.BatchID} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.BatchTransferIDs, err = decodeStringList(d); err != nil {
		return err
	}
	batchSize, err := d.U64()
	if err != nil || batchSize > uint64(math.MaxInt) {
		return err
	}
	state.BatchSize = int(batchSize)
	if state.RecipientVout, err = d.U32(); err != nil {
		return err
	}
	for _, target := range []*string{&state.TransportMode, &state.Direction} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.Asset, err = decodeOptionalAssetInfo(d); err != nil {
		return err
	}
	for _, target := range []*string{&state.RecipientID, &state.Invoice} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.InputOutPoints, err = decodeStringList(d); err != nil {
		return err
	}
	if state.OutputOutPoints, err = decodeStringList(d); err != nil {
		return err
	}
	if state.MinConfirmations, err = d.U8(); err != nil {
		return err
	}
	expiry, err := d.U64()
	if err != nil {
		return err
	}
	state.Expiry = int64(expiry)
	for _, target := range []*string{&state.ConsignmentHash, &state.WitnessTxID, &state.AckStatus, &state.Status, &state.RejectReason} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.RejectedOpouts, err = decodeStringList(d); err != nil {
		return err
	}
	for _, target := range []*string{&state.RelayRecordKey, &state.AckRecordKey, &state.RelayDurability} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	relayExpiry, err := d.U64()
	if err != nil {
		return err
	}
	state.RelayExpiry = int64(relayExpiry)
	for _, target := range []*string{&state.NetworkBackupRef, &state.ParentStateHash, &state.DKVSOperationID} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.AddressMode, err = d.Bool(); err != nil {
		return err
	}
	for _, target := range []*string{&state.AddressMessageID, &state.SenderAccountID, &state.ReceiverAccountID, &state.ReceiverAddress, &state.ReceiveCapabilityKey, &state.ReceiveCapabilityHash, &state.DeliveryMode, &state.DeliveryObjectID, &state.DeliveryRecordKey, &state.DeliveryRecordHash} {
		if *target, err = decodeText(d); err != nil {
			return err
		}
	}
	if state.DeliveryTemporary, err = d.Bool(); err != nil {
		return err
	}
	if state.DeliveryExpiryHeight, err = d.U64(); err != nil {
		return err
	}
	if state.DeliveryTTL, err = d.U64(); err != nil {
		return err
	}
	if state.DeliveryAcknowledged, err = d.Bool(); err != nil {
		return err
	}
	if state.DeliveryCacheCompacted, err = d.Bool(); err != nil {
		return err
	}
	state.SyntheticInvoiceRemoved, err = d.Bool()
	return err
}

func encodePendingTransfer(e *strict.Encoder, pending *PendingTransfer) error {
	if pending == nil {
		return ErrValidationReceipt
	}
	if err := encodeTransferState(e, &pending.State); err != nil {
		return err
	}
	for _, value := range [][]byte{pending.RecipientConsignment, pending.LocalConsignment, pending.SignedTx, pending.SignedPSBT} {
		if err := encodeBlob(e, value); err != nil {
			return err
		}
	}
	if err := e.Length(uint64(len(pending.ChangeSeals)), rgb11StoreMaxRecords); err != nil {
		return err
	}
	for _, seal := range pending.ChangeSeals {
		encoded, err := seal.StrictBytes()
		if err != nil {
			return err
		}
		if err := e.Bytes(encoded, 13, 45); err != nil {
			return err
		}
	}
	return e.U64(uint64(pending.CreatedAt))
}

func decodePendingTransfer(d *strict.Decoder, pending *PendingTransfer) error {
	if pending == nil {
		return ErrValidationReceipt
	}
	if err := decodeTransferState(d, &pending.State); err != nil {
		return err
	}
	var err error
	for _, target := range []*[]byte{&pending.RecipientConsignment, &pending.LocalConsignment, &pending.SignedTx, &pending.SignedPSBT} {
		if *target, err = decodeBlob(d); err != nil {
			return err
		}
	}
	count, err := d.Length(rgb11StoreMaxRecords)
	if err != nil {
		return err
	}
	pending.ChangeSeals = make([]seals.GraphBlindSeal, count)
	for index := range pending.ChangeSeals {
		encoded, err := d.Bytes(13, 45)
		if err != nil {
			return err
		}
		if pending.ChangeSeals[index], err = seals.DecodeGraphBlindSeal(encoded); err != nil {
			return err
		}
	}
	createdAt, err := d.U64()
	if err != nil {
		return err
	}
	pending.CreatedAt = int64(createdAt)
	return nil
}
