package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// ValidateNativeContractDepositAnchor checks the actual native source and the
// customer's signed destination before a second pool owner signs an anchor.
// The public Bitcoin indexer is deliberately never asked for an RGB balance.
func (p *Manager) ValidateNativeContractDepositAnchor(ctx context.Context, contract ContractRuntime, tx *swire.MsgTx) error {
	return p.validateNativeContractDepositAnchor(ctx, contract, tx, nativeproof.Current)
}

func (p *Manager) validateNativeContractDepositAnchor(ctx context.Context, contract ContractRuntime, tx *swire.MsgTx, mode nativeproof.Mode) error {
	if contract == nil || tx == nil || len(tx.TxIn) != 1 || len(tx.TxOut) == 0 {
		return fmt.Errorf("invalid native contract deposit anchor")
	}
	bind, err := p.AnchorOutputsActive()
	if err != nil {
		return err
	}
	if mode == nativeproof.Historical {
		bind = true
	}
	data, _, err := CheckAnchorPkScript(tx.TxIn[0].SignatureScript, tx.TxOut, bind)
	if err != nil {
		return err
	}
	if !bytes.Equal(data.WitnessScript, contract.GetRedeemScript()) {
		return fmt.Errorf("native anchor uses another public channel")
	}
	material, present, err := sindexer.NativeAssetProof(tx)
	if err != nil || !present || len(material) == 0 {
		return fmt.Errorf("native anchor proof unavailable: %w", err)
	}
	source, err := p.loadNativeChannelSource(contract.Address(), data.Utxo)
	if err != nil {
		return err
	}
	if !bytes.Equal(source.PublicProof, material) {
		return fmt.Errorf("native anchor changed the authenticated source proof")
	}
	result, err := p.validateNativeChannelSource(ctx, contract.GetRuntimeBase().GetLocalWallet(), contract.Address(), source, mode)
	if err != nil {
		return err
	}
	if len(data.Assets) != 1 || data.Assets[0].Name != result.Asset.Name ||
		data.Assets[0].BindingSat != 0 || data.Assets[0].Amount.Precision != result.Asset.Amount.Precision ||
		data.Assets[0].Amount.Cmp(&result.Asset.Amount) != 0 || data.Value != result.Value {
		return fmt.Errorf("native anchor invoice must map the full canonical source")
	}
	raw, err := p.GetDB().Read(nativeDepositKey("receive", data.Utxo))
	if err != nil {
		return err
	}
	var deposit NativeContractDeposit
	if json.Unmarshal(raw, &deposit) != nil || deposit.Intent.ContractURL != contract.URL() ||
		deposit.Source == nil || deposit.Source.Output == nil || deposit.Source.Output.OutPointStr != data.Utxo {
		return fmt.Errorf("native anchor has no matching durable customer authorization")
	}
	script, err := GetPkScriptFromAddress(deposit.Intent.Recipient)
	if err != nil {
		return err
	}
	output := tx.TxOut[0]
	if !bytes.Equal(output.PkScript, script) || output.Value != result.Value || len(output.Assets) != 1 ||
		output.Assets[0].Name != result.Asset.Name || output.Assets[0].BindingSat != 0 ||
		output.Assets[0].Amount.Precision != result.Asset.Amount.Precision || output.Assets[0].Amount.Cmp(&result.Asset.Amount) != 0 {
		return fmt.Errorf("native deposit anchor changed the customer's signed recipient or quantity")
	}
	for _, output := range tx.TxOut[1:] {
		if !sindexer.IsOpReturn(output.PkScript) || output.Value != 0 || len(output.Assets) != 0 {
			return fmt.Errorf("native deposit anchor has an unauthorized additional output")
		}
	}
	return nil
}

// A block replay may encounter a deposit whose RGB carrier has already been
// spent by a later withdrawal. Recover the earlier mapping only from its
// actual canonical anchor, including its signed outputs and native proof.
func (p *Manager) validateHistoricalNativeContractDeposit(ctx context.Context, contract ContractRuntime, source *NativeChannelSource) error {
	stored, err := p.loadNativeChannelSource(contract.Address(), source.Output.OutPointStr)
	if err != nil {
		return err
	}
	if !bytes.Equal(stored.PublicProof, source.PublicProof) {
		return fmt.Errorf("historical native deposit source changed")
	}
	locator, err := p.l2IndexerClient.GetAscendData(source.Output.OutPointStr)
	if err != nil || locator == nil || locator.AnchorTxId == "" {
		return fmt.Errorf("historical native deposit has no mapped anchor: %w", err)
	}
	chains, err := p.newNativeSettlementChains(ctx, contract.GetRuntimeBase().GetLocalWallet())
	if err != nil {
		return err
	}
	anchor, err := chains.satoshiTx(locator.AnchorTxId)
	if err != nil {
		return err
	}
	if err := p.validateNativeContractDepositAnchor(ctx, contract, anchor, nativeproof.Historical); err != nil {
		return err
	}
	data, _, err := CheckAnchorPkScript(anchor.TxIn[0].SignatureScript, anchor.TxOut, true)
	if err != nil || data.Utxo != source.Output.OutPointStr {
		return fmt.Errorf("historical native deposit locator references another source")
	}
	return chains.stable()
}

func nativeContractTicker(source *NativeChannelSource, validated *nativeproof.Result) (*indexer.TickerInfo, error) {
	proof, err := nativeproof.Decode(source.PublicProof)
	if err != nil {
		return nil, err
	}
	container, err := coreconsignment.Decode(proof.Consignment)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(proof.Consignment)
	info, err := rgb11TickerInfoFromValidatedContract(container, &rgb11wallet.ValidationReceipt{
		ContractID: validated.ContractID, SchemaID: validated.SchemaID,
		ConsignmentHash: hex.EncodeToString(h[:]),
	})
	if err != nil {
		return nil, err
	}
	if info.AssetName != validated.Asset.Name || info.Divisibility != validated.Asset.Amount.Precision {
		return nil, fmt.Errorf("native genesis metadata does not match the verified source")
	}
	return info, nil
}

// CreateNativeContractDepositAnchorTx is shared by all public-channel
// templates. The source is recovered from authenticated native inventory,
// independently revalidated, and mapped in full before the invoice is signed.
func (p *Manager) CreateNativeContractDepositAnchorTx(ctx context.Context, contract ContractRuntime,
	destAddr string, output *TxOutput, assetName *AssetName, memo []byte) (*swire.MsgTx, error) {
	if contract == nil || output == nil || assetName == nil ||
		assetName.Protocol != rgb11wallet.Protocol || *contract.GetAssetName() != assetName.AssetName {
		return nil, fmt.Errorf("invalid native contract anchor request")
	}
	source, err := p.loadNativeChannelSource(contract.Address(), output.OutPointStr)
	if err != nil {
		return nil, err
	}
	validated, err := p.validateNativeChannelSource(ctx, contract.GetRuntimeBase().GetLocalWallet(),
		contract.Address(), source, nativeproof.Current)
	if err != nil {
		return nil, err
	}
	if len(output.Assets) != 1 || output.Assets[0].Name != validated.Asset.Name ||
		output.Assets[0].Amount.Cmp(&validated.Asset.Amount) != 0 || output.Value() != validated.Value {
		return nil, fmt.Errorf("native anchor must map its exact complete source")
	}
	ticker, err := nativeContractTicker(source, validated)
	if err != nil {
		return nil, err
	}
	destScript, err := GetPkScriptFromAddress(destAddr)
	if err != nil {
		return nil, err
	}
	script, err := sindexer.StandardAnchorScriptWithSig(output.OutPointStr, contract.GetRedeemScript(),
		validated.Value, swire.TxAssets{validated.Asset}, nil)
	if err != nil {
		return nil, err
	}
	tx := swire.NewMsgTx(swire.TxVersion)
	tx.AddTxIn(&swire.TxIn{PreviousOutPoint: *swire.NewOutPoint(&chainhash.Hash{}, swire.AnchorTxOutIndex),
		Sequence: swire.AnchorTxOutIndex, SignatureScript: script})
	tx.AddTxOut(swire.NewTxOut(validated.Value, swire.TxAssets{validated.Asset}, destScript))
	metadata := fmt.Sprintf("%s-%s-%d-0", ticker.AssetName.String(), ticker.MaxSupply, ticker.Divisibility)
	for _, marker := range []struct {
		kind int
		data []byte
	}{
		{int(sindexer.CONTENT_TYPE_ASCENDING), []byte(metadata)},
		{int(sindexer.CONTENT_TYPE_CHANNELID), []byte(contract.Address())},
	} {
		// Content tags are byte-valued in the common script encoding.
		data, err := sindexer.NullDataScript(byte(marker.kind), marker.data)
		if err != nil {
			return nil, err
		}
		tx.AddTxOut(swire.NewTxOut(0, nil, data))
	}
	if len(memo) != 0 {
		tx.AddTxOut(swire.NewTxOut(0, nil, memo))
	}
	if err := sindexer.SetNativeAssetProof(tx, source.PublicProof); err != nil {
		return nil, err
	}
	if _, err := p.SignAnchorTx(tx, contract.GetRuntimeBase().GetLocalWallet().SignMessage); err != nil {
		return nil, err
	}
	return tx, nil
}
