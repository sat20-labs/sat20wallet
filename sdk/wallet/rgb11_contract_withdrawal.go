package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const rgbMailboxCommitted = "MAILBOX_COMMITTED"

var errRGBMailboxPending = errors.New("recipient Core has not confirmed the RGB proof in the mailbox")

func isRGBPublicPayout(item *rgb11wallet.PendingTransfer) bool {
	if item == nil || item.ChannelSend == nil || item.ChannelSend.Reason != "contract" || !item.State.AddressMode {
		return false
	}
	var more wwire.RemoteSignMoreData_Contract
	if json.Unmarshal(item.ChannelSend.MoreData, &more) != nil || !more.RGB11Withdrawal ||
		item.ChannelSend.ChannelID == "" || ExtractChannelId(more.ContractURL) != item.ChannelSend.ChannelID {
		return false
	}
	kind, invoice, err := sindexer.ReadDataFromNullDataScript(more.MoreData)
	if err != nil || kind != sindexer.CONTENT_TYPE_INVOKERESULT {
		return false
	}
	path, reason, _, err := ParseContractResultInvoice(invoice)
	return err == nil && reason == INVOKE_RESULT_WITHDRAW && path == ExtractRelativePath(more.ContractURL)
}

func rgbDeliveryReady(item *rgb11wallet.PendingTransfer) bool {
	if isRGBPublicPayout(item) {
		return item.State.DeliveryMode == "direct" && item.State.RelayDurability == rgbMailboxCommitted &&
			item.State.DeliveryRecordKey != "" && item.State.DeliveryRecordHash != ""
	}
	return item.State.DeliveryAcknowledged && item.State.AckStatus == "accepted"
}

func needsRGBRedelivery(item *rgb11wallet.PendingTransfer) bool {
	return item.State.Status == "delivered" && isRGBPublicPayout(item) && !rgbDeliveryReady(item)
}

type rgb11ContractWithdrawPlan struct {
	Version     uint32              `json:"version"`
	ContractURL string              `json:"contract_url"`
	BitcoinTxID string              `json:"bitcoin_txid"`
	Descend     []byte              `json:"descend"`
	PrevOutputs []*TxOutput_SatsNet `json:"prev_outputs"`
}

type rgb11ContractBroadcastPair struct {
	Version     uint32                   `json:"version"`
	ContractURL string                   `json:"contract_url"`
	Bitcoin     []byte                   `json:"bitcoin"`
	Descend     []byte                   `json:"descend"`
	Proof       *wwire.RGB11SigningProof `json:"proof"`
}

func rgb11ContractWithdrawKey(kind, txid string) []byte {
	return []byte(GetDBKeyPrefix() + "rgb11-contract-withdraw/" + _chain + "/" + kind + "/" + txid)
}

func rgb11ContractDealID(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func rgb11WithdrawalRequestIdentity(raw []byte) (string, error) {
	var request wwire.RemoteSignMoreData_Contract
	if json.Unmarshal(raw, &request) != nil || !request.RGB11Withdrawal || request.ContractURL == "" || len(request.MoreData) == 0 {
		return "", fmt.Errorf("invalid RGB11 withdrawal request")
	}
	identity := append(append([]byte(request.ContractURL), 0), request.MoreData...)
	return rgb11ContractDealID(identity), nil
}

func (p *Manager) resumeRGB11ContractWithdrawal(ctx context.Context, more []byte) (string, int64, bool, error) {
	rgb, err := p.captureRGB11NetworkScope(true)
	if err != nil {
		return "", 0, false, err
	}
	states, err := rgb.projectionStore.ListTransfers()
	if err != nil {
		return "", 0, false, err
	}
	wanted, err := rgb11WithdrawalRequestIdentity(more)
	if err != nil {
		return "", 0, false, err
	}
	for _, state := range states {
		if state.Direction != "send" {
			continue
		}
		pending, err := rgb.projectionStore.LoadPendingTransfer(state.TransferID)
		if err != nil {
			return "", 0, false, err
		}
		if pending.ChannelSend == nil {
			continue
		}
		identity, err := rgb11WithdrawalRequestIdentity(pending.ChannelSend.MoreData)
		if err != nil || identity != wanted {
			continue
		}
		txid, fee, err := p.ResumeRGB11Send(ctx, state.TransferID)
		return txid, fee, true, err
	}
	return "", 0, false, nil
}

func (p *rgb11Manager) rgb11ContractDescendPlan(first *rgb11wallet.PendingTransfer, batch []*rgb11wallet.PendingTransfer) (*swire.MsgTx, *stxscript.MultiPrevOutFetcher, error) {
	var more wwire.RemoteSignMoreData_Contract
	if first.ChannelSend == nil || json.Unmarshal(first.ChannelSend.MoreData, &more) != nil || !more.RGB11Withdrawal {
		return nil, nil, nil
	}
	if more.ContractURL == "" || first.State.Asset.Name.Protocol != rgb11wallet.Protocol {
		return nil, nil, fmt.Errorf("invalid RGB11 contract withdrawal")
	}
	owner := p.accountManagementOwner()
	key := rgb11ContractWithdrawKey("plan", first.State.WitnessTxID)
	raw, err := owner.GetDB().Read(key)
	var plan rgb11ContractWithdrawPlan
	if err == nil {
		if json.Unmarshal(raw, &plan) != nil || plan.Version != 1 || plan.ContractURL != more.ContractURL || plan.BitcoinTxID != first.State.WitnessTxID {
			return nil, nil, fmt.Errorf("invalid RGB11 withdrawal plan")
		}
	} else if errors.Is(err, indexer.ErrKeyNotFound) {
		_, asset, _, err := ParseContractURL(more.ContractURL)
		if err != nil {
			return nil, nil, err
		}
		name := ParseAssetString(asset)
		local, err := owner.RGB11LocalAsset(name)
		if err != nil || *local != first.State.Asset.Name {
			return nil, nil, ErrRGB11PublicIdentityMismatch
		}
		var amount *Decimal
		for _, item := range batch {
			amount = amount.Add(&item.State.Asset.Amount)
		}
		tx, fetcher, err := owner.CreateDeAnchorTx(first.ChannelSend.ChannelID, first.State.WitnessTxID,
			&AssetName{AssetName: *name}, amount, 0, nil)
		if err != nil {
			return nil, nil, err
		}
		var encoded bytes.Buffer
		if err := tx.Serialize(&encoded); err != nil {
			return nil, nil, err
		}
		plan = rgb11ContractWithdrawPlan{Version: 1, ContractURL: more.ContractURL,
			BitcoinTxID: first.State.WitnessTxID, Descend: encoded.Bytes()}
		for _, input := range tx.TxIn {
			out := fetcher.FetchPrevOutput(input.PreviousOutPoint)
			if out == nil {
				return nil, nil, fmt.Errorf("RGB11 descend prevout unavailable")
			}
			plan.PrevOutputs = append(plan.PrevOutputs, &TxOutput_SatsNet{OutPointStr: input.PreviousOutPoint.String(), OutValue: *out})
		}
		raw, err := json.Marshal(plan)
		if err != nil {
			return nil, nil, err
		}
		if err := owner.GetDB().Write(key, raw); err != nil {
			return nil, nil, err
		}
	} else {
		return nil, nil, err
	}
	tx := new(swire.MsgTx)
	if err := tx.Deserialize(bytes.NewReader(plan.Descend)); err != nil {
		return nil, nil, err
	}
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
	points := make([]string, 0, len(plan.PrevOutputs))
	for _, out := range plan.PrevOutputs {
		if out == nil || out.OutPoint() == nil {
			return nil, nil, fmt.Errorf("invalid RGB11 descend prevout")
		}
		fetcher.AddPrevOut(*out.OutPoint(), &out.OutValue)
		points = append(points, out.OutPointStr)
	}
	if err := owner.utxoLockerL2.TryReserve(points, common.UtxoLockRGBWithdrawal, "rgb11-contract/"+first.State.WitnessTxID); err != nil {
		return nil, nil, err
	}
	return tx, fetcher, nil
}

// RecordRGB11ContractBroadcast persists a complete signed pair before either
// chain can see it. The same function is used by both pool owners. Retries may
// only replay the identical bytes; a fresh fee/input selection is forbidden.
func (p *Manager) RecordRGB11ContractBroadcast(contractURL string, bitcoin []*wire.MsgTx, descend []*swire.MsgTx, proofs ...*wwire.RGB11SigningProof) error {
	if len(bitcoin) != 1 || len(descend) != 1 || bitcoin[0] == nil || descend[0] == nil {
		return fmt.Errorf("RGB11 withdrawal requires exactly one Bitcoin transaction and one descend")
	}
	if len(proofs) != 1 || proofs[0] == nil || len(proofs[0].Consignment) == 0 {
		return fmt.Errorf("RGB11 withdrawal requires recoverable RGB11 change proof")
	}
	matched := 0
	for _, output := range descend[0].TxOut {
		kind, data, err := sindexer.ReadDataFromNullDataScript(output.PkScript)
		if err == nil && kind == sindexer.CONTENT_TYPE_DESCENDING {
			if string(data) != bitcoin[0].TxID() {
				return fmt.Errorf("RGB11 descend does not commit its Bitcoin transaction")
			}
			matched++
		}
	}
	if matched != 1 {
		return fmt.Errorf("RGB11 withdrawal requires one descend commitment")
	}
	batch, err := NewAtomicWriteBatch(p.GetDB())
	if err != nil {
		return err
	}
	defer batch.Close()
	var btc, l2 bytes.Buffer
	if err := bitcoin[0].Serialize(&btc); err != nil {
		return err
	}
	if err := descend[0].Serialize(&l2); err != nil {
		return err
	}
	pair := rgb11ContractBroadcastPair{Version: 1, ContractURL: contractURL, Bitcoin: btc.Bytes(), Descend: l2.Bytes(), Proof: proofs[0]}
	raw, err := json.Marshal(pair)
	if err != nil {
		return err
	}
	key := rgb11ContractWithdrawKey("signed", bitcoin[0].TxID())
	rgb11ChannelInventoryMu.Lock()
	defer rgb11ChannelInventoryMu.Unlock()
	old, err := p.GetDB().Read(key)
	if err == nil && !bytes.Equal(old, raw) {
		return fmt.Errorf("RGB11 broadcast pair is immutable")
	}
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	if err := batch.Put(key, raw); err != nil {
		return err
	}
	return batch.Flush()
}

func (p *Manager) SendRGB11ContractWithdrawal(local common.Wallet, dest []*SendAssetInfo, asset string,
	feeRate int64, channelID, reason string, more []byte, excludeRecent bool, maxHeight int) (string, int64, error) {
	if len(dest) == 0 || len(dest) > 9 {
		return "", 0, fmt.Errorf("RGB11 contract withdrawal requires one to nine recipients")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if txid, fee, found, err := p.resumeRGB11ContractWithdrawal(ctx, more); found || err != nil {
		return txid, fee, err
	}
	return p.sendRGB11FromChannelAtHeight(local, dest, asset, feeRate, channelID, reason, more, true, excludeRecent, maxHeight)
}

func (p *rgb11Manager) broadcastRGB11ContractDescend(first *rgb11wallet.PendingTransfer) error {
	var more wwire.RemoteSignMoreData_Contract
	if first.ChannelSend == nil || json.Unmarshal(first.ChannelSend.MoreData, &more) != nil || !more.RGB11Withdrawal {
		return nil
	}
	raw, err := p.accountManagementOwner().GetDB().Read(rgb11ContractWithdrawKey("signed", first.State.WitnessTxID))
	if err != nil {
		return err
	}
	var pair rgb11ContractBroadcastPair
	if json.Unmarshal(raw, &pair) != nil || pair.Version != 1 || pair.ContractURL != more.ContractURL {
		return fmt.Errorf("invalid RGB11 broadcast pair")
	}
	btc, l2 := new(wire.MsgTx), new(swire.MsgTx)
	if err := btc.Deserialize(bytes.NewReader(pair.Bitcoin)); err != nil {
		return err
	}
	if err := l2.Deserialize(bytes.NewReader(pair.Descend)); err != nil {
		return err
	}
	if btc.TxID() != first.State.WitnessTxID {
		return fmt.Errorf("RGB11 broadcast pair changed Bitcoin identity")
	}
	observed := false
	if status, err := p.evidence.GetTxStatus(btc.TxID()); err == nil && status != nil && (status.Confirmed || status.InMempool) {
		raw, err := p.evidence.GetRawTx(btc.TxID())
		if err != nil || !bytes.Equal(raw, pair.Bitcoin) {
			return fmt.Errorf("observed RGB11 withdrawal differs from fixed bytes: %w", err)
		}
		observed = true
	}
	if !observed {
		if err := p.accountManagementOwner().TestAcceptance([]*wire.MsgTx{btc}); err != nil {
			return err
		}
	}
	manager := p.accountManagementOwner()
	if raw, err := manager.l2IndexerClient.GetRawTx(l2.TxID()); err == nil && raw != "" {
		decoded, err := hex.DecodeString(raw)
		if err != nil || !bytes.Equal(decoded, pair.Descend) {
			return fmt.Errorf("observed RGB11 descend differs from fixed signed pair")
		}
		return nil
	}
	return manager.BroadcastTxs_SatsNet([]*swire.MsgTx{l2})
}

// A peer can finish both broadcasts and lose the signing response. Recover the
// signed descend using the already durable plan's transaction ID, then persist
// the same complete pair before marking the local RGB transfer signed. Never
// synthesize a new descend or infer settlement from the Bitcoin witness alone.
func (p *rgb11Manager) recoverRGB11ContractBroadcast(first *rgb11wallet.PendingTransfer, bitcoin *wire.MsgTx) error {
	var more wwire.RemoteSignMoreData_Contract
	if first.ChannelSend == nil || json.Unmarshal(first.ChannelSend.MoreData, &more) != nil || !more.RGB11Withdrawal {
		return nil
	}
	owner := p.accountManagementOwner()
	raw, err := owner.GetDB().Read(rgb11ContractWithdrawKey("plan", first.State.WitnessTxID))
	if err != nil {
		return err
	}
	var plan rgb11ContractWithdrawPlan
	if json.Unmarshal(raw, &plan) != nil || plan.Version != 1 || plan.ContractURL != more.ContractURL || plan.BitcoinTxID != bitcoin.TxID() {
		return fmt.Errorf("RGB11 response recovery has no matching durable plan")
	}
	declared := new(swire.MsgTx)
	reader := bytes.NewReader(plan.Descend)
	if declared.Deserialize(reader) != nil || reader.Len() != 0 {
		return fmt.Errorf("invalid durable RGB11 response-recovery plan")
	}
	encoded, err := owner.l2IndexerClient.GetRawTx(declared.TxID())
	if err != nil {
		return err
	}
	observed, err := DecodeMsgTx_SatsNet(encoded)
	if err != nil || observed.TxID() != declared.TxID() {
		return fmt.Errorf("RGB11 response recovery cannot locate the fixed descend")
	}
	previous := stxscript.NewMultiPrevOutFetcher(nil)
	for _, output := range plan.PrevOutputs {
		if output == nil || output.OutPoint() == nil {
			return fmt.Errorf("RGB11 recovery plan has an invalid previous output")
		}
		point := *output.OutPoint()
		if previous.FetchPrevOutput(point) != nil {
			return fmt.Errorf("RGB11 recovery plan repeats a previous output")
		}
		previous.AddPrevOut(point, &output.OutValue)
	}
	if len(plan.PrevOutputs) != len(observed.TxIn) {
		return fmt.Errorf("RGB11 response recovery has an incomplete input snapshot")
	}
	for _, input := range observed.TxIn {
		if previous.FetchPrevOutput(input.PreviousOutPoint) == nil {
			return fmt.Errorf("RGB11 response recovery changed a planned input")
		}
	}
	if err := VerifySignedTx_SatsNet(observed, previous); err != nil {
		return err
	}
	proof := &wwire.RGB11SigningProof{Consignment: append([]byte(nil), first.LocalConsignment...)}
	for _, seal := range first.ChangeSeals {
		raw, err := seal.StrictBytes()
		if err != nil {
			return err
		}
		proof.ChangeSeals = append(proof.ChangeSeals, raw)
	}
	return owner.RecordRGB11ContractBroadcast(more.ContractURL, []*wire.MsgTx{bitcoin}, []*swire.MsgTx{observed}, proof)
}
