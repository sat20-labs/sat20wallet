package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// A core peer persists the complete signed pair before broadcasting either
// transaction. In particular, a successful descend followed by a temporary
// Bitcoin broadcast failure cannot be recovered by signing again: the mapped
// inputs have already been spent. Replay the durable bytes from the ordinary
// contract reconciliation loop, including after restart, without new signatures
// or a new input/fee selection. Canonical settlement remains a separate step.
func (p *Manager) resumeNativeContractBroadcast(ctx context.Context, owner common.Wallet, contractURL, txid string) error {
	raw, err := p.GetDB().Read(nativeContractWithdrawKey("signed", txid))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil // The selection can be durable before co-signing completes.
	}
	if err != nil {
		return err
	}
	var pair nativeContractBroadcastPair
	if json.Unmarshal(raw, &pair) != nil || pair.Version != 1 || pair.ContractURL != contractURL ||
		pair.Proof == nil || len(pair.Proof.Consignment) == 0 {
		return fmt.Errorf("invalid durable native broadcast pair")
	}
	channel, assetText, _, err := ParseContractURL(contractURL)
	if err != nil {
		return err
	}
	name := indexer.NewAssetNameFromString(assetText)
	if name == nil || name.Protocol != rgb11wallet.Protocol {
		return fmt.Errorf("invalid native broadcast contract asset")
	}
	if _, _, err := p.nativeChannelWitness(owner, channel); err != nil {
		return err
	}
	bitcoin, descend := new(wire.MsgTx), new(swire.MsgTx)
	reader := bytes.NewReader(pair.Bitcoin)
	if bitcoin.Deserialize(reader) != nil || reader.Len() != 0 || bitcoin.TxID() != txid {
		return fmt.Errorf("durable native Bitcoin transaction identity changed")
	}
	reader = bytes.NewReader(pair.Descend)
	if descend.Deserialize(reader) != nil || reader.Len() != 0 {
		return fmt.Errorf("invalid durable native descend transaction")
	}
	commitments := 0
	for _, output := range descend.TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
			continue
		}
		if string(data) != txid || output.Value != 0 || len(output.Assets) != 1 || output.Assets[0].Name != *name ||
			output.Assets[0].BindingSat != 0 || output.Assets[0].Amount.Sign() <= 0 {
			return fmt.Errorf("durable native descend does not bind its Bitcoin transaction and asset")
		}
		commitments++
	}
	if commitments != 1 {
		return fmt.Errorf("durable native pair requires one descend commitment")
	}
	scope, err := p.stpRGB11Scope(owner)
	if err != nil {
		return err
	}
	status, err := scope.evidence.GetTxStatus(txid)
	if err != nil || status == nil || status.TxID != txid {
		return fmt.Errorf("native Bitcoin broadcast status unavailable: %w", errRGB11ProjectionEvidence(err))
	}
	bitcoinKnown := status.Confirmed || status.InMempool
	if bitcoinKnown {
		observed, err := scope.evidence.GetRawTx(txid)
		if err != nil || !bytes.Equal(observed, pair.Bitcoin) {
			return fmt.Errorf("observed native Bitcoin transaction differs from durable signed bytes")
		}
	}
	descendKnown := false
	if observed, err := p.l2IndexerClient.GetRawTx(descend.TxID()); err == nil && observed != "" {
		decoded, err := hex.DecodeString(observed)
		if err != nil || !bytes.Equal(decoded, pair.Descend) {
			return fmt.Errorf("observed native descend differs from durable signed bytes")
		}
		descendKnown = true
	}
	if bitcoinKnown && descendKnown {
		return nil
	}
	chains, err := p.newNativeSettlementChains(ctx, owner)
	if err != nil {
		return err
	}
	if err := chains.verifyBitcoinSpend(bitcoin); err != nil {
		return err
	}
	previous, err := chains.satoshiPrevious(descend)
	if err != nil {
		return err
	}
	if err := VerifySignedTx_SatsNet(descend, previous); err != nil {
		return err
	}
	if !bitcoinKnown {
		if _, _, err := p.RebuildRGB11TxOutput(bitcoin, pair.Proof); err != nil {
			return err
		}
		if err := p.TestAcceptance([]*wire.MsgTx{bitcoin}); err != nil {
			return err
		}
	}
	if !descendKnown {
		if err := p.TestAcceptance_SatsNet([]*swire.MsgTx{descend}); err != nil {
			return err
		}
	}
	if err := chains.stable(); err != nil {
		return err
	}
	if !descendKnown {
		if err := p.BroadcastTxs_SatsNet([]*swire.MsgTx{descend}); err != nil {
			return err
		}
	}
	if !bitcoinKnown {
		if _, err := p.BroadcastTx(bitcoin); err != nil {
			return err
		}
	}
	return nil
}
