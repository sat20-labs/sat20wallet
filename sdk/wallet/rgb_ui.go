package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/schemas"
	rgb "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// UI business methods deliberately have RGB names. The RGB11 protocol methods
// remain available to native SDK callers; they are not exported to the PWA.
func rgbAssetInfo(state *RGB11State, key string) (*RGB11TickerInfo, error) {
	for _, t := range state.TickerInfos {
		if t.AssetName.String() == key || t.ContractID == key {
			return t, nil
		}
	}
	return nil, fmt.Errorf("RGB asset is not available in the current wallet")
}
func rgbAmount(text string, precision uint8) (string, error) {
	amount, err := indexer.NewDecimalFromString(text, int(precision))
	if err != nil {
		return "", err
	}
	if amount.Value.Sign() <= 0 || amount.Value.BitLen() > 64 {
		return "", fmt.Errorf("invalid RGB amount")
	}
	return amount.Value.String(), nil
}
func rgbDisplayAmount(a indexer.AssetInfo) AssetAmount {
	return AssetAmount{Asset: a.Name.String(), Amount: a.Amount.String(), Precision: uint8(a.Amount.Precision)}
}
func rgbAssetView(t *RGB11TickerInfo, state *RGB11State) RGBAssetView {
	v := RGBAssetView{Asset: t.AssetName.String(), ContractID: t.ContractID, Ticker: t.Ticker, Name: t.DisplayName, Precision: uint8(t.Divisibility), Available: "0", Pending: "0", CanExport: true, CanInvoice: true, CanonicalName: t.CanonicalName, GenesisAddress: t.GenesisAddress, NamingStatus: t.NamingStatus, Verified: t.Verified}
	var ext rgb.TickerExt
	if json.Unmarshal(t.Content, &ext) == nil {
		if d, err := schemas.ByID(ext.SchemaID); err == nil {
			v.Kind = string(d.Kind)
		}
	}
	for _, a := range state.AvailableAssets {
		if a.Name == t.AssetName {
			v.Available = a.Amount.String()
			v.CanSend = a.Amount.Sign() > 0
		}
	}
	for _, a := range state.PendingAssets {
		if a.Name == t.AssetName {
			v.Pending = a.Amount.String()
		}
	}
	if v.Kind == "IFA" && t.AssetName.Type != "control" {
		for _, a := range state.AvailableAssets {
			if a.Name.Type == "control" && a.Name.Ticker == t.AssetName.Ticker && a.Amount.Sign() > 0 {
				v.CanInflate = true
				break
			}
		}
	}
	return v
}
func rgbBatch(state *RGB11State, id string) ([]*rgb.TransferState, error) {
	var first *rgb.TransferState
	for _, t := range state.Transfers {
		if t.Direction == "send" && (t.TransferID == id || t.BatchID == id) {
			first = t
			break
		}
	}
	if first == nil {
		return nil, fmt.Errorf("RGB transfer not found in current wallet")
	}
	ids := first.BatchTransferIDs
	if len(ids) == 0 {
		ids = []string{first.TransferID}
	}
	result := make([]*rgb.TransferState, 0, len(ids))
	for _, member := range ids {
		found := false
		for _, t := range state.Transfers {
			if t.Direction == "send" && t.TransferID == member {
				result = append(result, t)
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("RGB transfer batch is incomplete")
		}
	}
	return result, nil
}
func (p *Manager) rgbTransferView(state *RGB11State, id string) (*RGBTransferView, error) {
	members, err := rgbBatch(state, id)
	if err != nil {
		return nil, err
	}
	first := members[0]
	view := &RGBTransferView{ID: first.TransferID, Status: first.Status, TxID: first.WitnessTxID, ErrorCode: first.RejectReason}
	for _, m := range members {
		destination := m.Invoice
		if m.AddressMode {
			destination = m.ReceiverAddress
		}
		view.Recipients = append(view.Recipients, RGBRecipientView{Destination: destination, Amount: rgbDisplayAmount(m.Asset)})
	}
	switch first.Status {
	case "prepared", "delivered", "relayed", rgb11StatusBroadcastAttempted:
		view.CanConfirm, view.CanCancel = true, first.TransportMode == "out-of-band" && first.Status != rgb11StatusBroadcastAttempted
		if first.TransportMode == RGB11ProxyTransport && !first.AddressMode {
			view.CanCancel = true
			for _, item := range members {
				if item.Status != "prepared" && item.Status != "relayed" || item.AckStatus == "accepted" || item.DeliveryAcknowledged {
					view.CanCancel = false
				}
			}
		}
		if first.TransportMode == "out-of-band" {
			view.NeedsReceipts = first.Status != rgb11StatusBroadcastAttempted
			view.Status = "awaiting_receipts"
		} else if first.Status != "prepared" && first.AckStatus != "accepted" {
			view.Status = "awaiting_ack"
			if first.AckStatus == "awaiting-persistence" {
				view.Status = "awaiting_persistence"
			}
		}
	case "broadcast", "broadcasted", "confirmed", "pending":
		view.Status = "pending_chain"
	case "settled":
		view.Status = "completed"
	case "rejected":
		if first.RejectReason == RGB11RejectReasonUser {
			view.Status = "cancelled"
			view.ErrorCode = ""
		}
	}
	view.CanExport = !first.AddressMode && (view.CanConfirm || view.Status == "completed")
	scoped, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	pending, err := scoped.rgbManager.projectionStore.LoadPendingTransfer(first.TransferID)
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	if pending != nil && len(pending.SignedPSBT) > 0 {
		packet, err := psbt.NewFromRawBytes(bytes.NewReader(pending.SignedPSBT), false)
		if err != nil {
			return nil, err
		}
		fee, err := packet.GetTxFee()
		if err != nil {
			return nil, err
		}
		view.FeeSats = int64(fee)
	}
	if first.AddressMode {
		view.WarningCodes = []string{"rgb_address_carrier"}
	}
	return view, nil
}
func (p *Manager) GetRGBView() (*RGBView, error) {
	state, lockScope, locks, err := p.captureRGBLockState()
	if err != nil {
		return nil, err
	}
	view := &RGBView{LockScope: lockScope, Locks: locks, Assets: []RGBAssetView{}, Transfers: []RGBTransferView{}, Receives: []RGBReceiveView{}, Syncing: state.SyncStatus == "syncing", CanWrite: state.ConsistencyStatus != "broken" && p.rgbModuleView(p.GetAccountManagementStatus()).CanWrite}
	for _, t := range state.TickerInfos {
		view.Assets = append(view.Assets, rgbAssetView(t, state))
	}
	seen := make(map[string]bool)
	for _, t := range state.Transfers {
		if t.Direction != "send" {
			continue
		}
		key := t.BatchID
		if key == "" {
			key = t.TransferID
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		v, err := p.rgbTransferView(state, t.TransferID)
		if err != nil {
			return nil, err
		}
		view.Transfers = append(view.Transfers, *v)
	}
	receiveIDs := make(map[string]bool)
	for _, r := range state.Reservations {
		if r.Direction == "receive" && r.RequestID != "" {
			receiveIDs[r.RequestID] = true
		}
	}
	// Settled reservations leave the active list; their existing transfers
	// retain the receive history. Direct messages are not invoice requests.
	for _, t := range state.Transfers {
		if t.Direction == "receive" && !t.AddressMode && t.ReceiveRequestID != "" {
			receiveIDs[t.ReceiveRequestID] = true
		}
	}
	for id := range receiveIDs {
		v, err := p.rgbReceiveView(state, id)
		if err != nil {
			return nil, err
		}
		view.Receives = append(view.Receives, *v)
	}
	sort.Slice(view.Receives, func(i, j int) bool { return view.Receives[i].ID < view.Receives[j].ID })
	if state.ConsistencyStatus == "broken" {
		view.ErrorCode = "RGB11_INCONSISTENT"
	}
	return view, nil
}
func (p *Manager) RefreshRGBState(ctx context.Context) (*RGBView, error) {
	if _, err := p.syncRootRGB11AddressMailbox(ctx, dkvsindexer.RecordVerificationOptions{}); err != nil {
		return nil, err
	}
	if _, err := p.RefreshRGB11State(ctx); err != nil {
		return nil, err
	}
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	for _, r := range state.Reservations {
		if r.Direction == "receive" && r.TransportMode == "rgb-json-rpc" && r.Status != "closed" {
			if _, err := p.ReceiveRGB11ProxyConsignment(ctx, r.RequestID); err != nil && !errors.Is(err, ErrRGB11ProxyNoConsignment) {
				return nil, err
			}
		}
	}
	return p.GetRGBView()
}
func (p *Manager) PrepareRGBTransfer(ctx context.Context, req RGBSendRequest) (opValue0 *RGBTransferView, opErr error) {
	if len(req.Recipients) == 0 {
		return nil, fmt.Errorf("recipient is required")
	}
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	first := req.Recipients[0]
	var prepared *RGB11PreparedTransfer
	if _, parseErr := invoicing.Parse(first.Destination); parseErr == nil {
		opLogID := p.beginWalletLog("rgb", "rgb_send", "Send RGB asset")
		defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()
		invoices := make([]string, 0, len(req.Recipients))
		for _, r := range req.Recipients {
			if r.Asset != "" || r.Amount != "" {
				return nil, fmt.Errorf("invoice already specifies its asset and amount")
			}
			invoices = append(invoices, r.Destination)
		}
		prepared, err = p.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoices: invoices, FeeRate: req.FeeRate, MinConfirmations: 1})
	} else {
		if len(req.Recipients) != 1 {
			return nil, fmt.Errorf("address transfer requires one recipient")
		}
		asset, e := rgbAssetInfo(state, first.Asset)
		if e != nil {
			return nil, e
		}
		amount, e := rgbAmount(first.Amount, uint8(asset.Divisibility))
		if e != nil {
			return nil, e
		}
		return p.previewRGBDirectTransfer(ctx, RGB11AddressSendRequest{ReceiverAddress: first.Destination, AssetName: asset.AssetName, AmountRaw: amount, FeeRate: req.FeeRate, MinConfirmations: 1})
	}
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentTransfer(prepared.State.TransferID)
}
func (p *Manager) rgbCurrentTransfer(id string) (*RGBTransferView, error) {
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	return p.rgbTransferView(state, id)
}
func (p *Manager) PrepareRGBInflation(ctx context.Context, invoice string, feeRate int64) (opValue0 *RGBTransferView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_inflate", "Inflate RGB asset")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	result, err := p.PrepareRGB11Inflation(ctx, RGB11InflationRequest{Invoice: invoice, FeeRate: feeRate, MinConfirmations: 1})
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentTransfer(result.State.TransferID)
}
func (p *Manager) ConfirmRGBTransfer(ctx context.Context, id string, receipts []string) (view *RGBTransferView, opErr error) {
	if value, ok := p.rgbDirectPreviews.Load(id); ok {
		return p.confirmRGBDirectPreview(ctx, id, value.(*rgb11DirectPreview))
	}
	defer func() { p.finishRGBLog(id, opErr, view) }()
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	members, err := rgbBatch(state, id)
	if err != nil {
		return nil, err
	}
	first := members[0]
	ids := make([]string, len(members))
	for i, t := range members {
		ids[i] = t.TransferID
	}
	switch {
	case first.AddressMode:
		_, err = p.DeliverAndBroadcastConfiguredRGB11AddressTransfer(first.TransferID)
	case first.TransportMode == "rgb-json-rpc":
		_, err = p.DeliverAndBroadcastRGB11ProxyTransfer(ctx, ids)
	case first.TransportMode == "out-of-band":
		if first.Status != rgb11StatusBroadcastAttempted {
			err = p.checkRGBReceipts(state, members, receipts)
		}
		if err == nil {
			_, err = p.BroadcastRGB11OutOfBand(ids)
		}
	default:
		err = fmt.Errorf("unsupported RGB delivery mode")
	}
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentTransfer(first.TransferID)
}
func (p *Manager) CancelRGBTransfer(ctx context.Context, id string) (view *RGBTransferView, opErr error) {
	if value, ok := p.rgbDirectPreviews.Load(id); ok {
		preview := value.(*rgb11DirectPreview)
		preview.mu.Lock()
		defer preview.mu.Unlock()
		if !preview.saved {
			p.rgbDirectPreviews.Delete(id)
			preview.cancelled = true
			return &RGBTransferView{ID: id, Status: "cancelled"}, nil
		}
	}
	defer func() { p.finishRGBLog(id, opErr, view) }()
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	members, err := rgbBatch(state, id)
	if err != nil {
		return nil, err
	}
	if !members[0].AddressMode && members[0].TransportMode == RGB11ProxyTransport {
		_, err = runRGB11ManagedOperation(p, ctx, rgb11ManagedOperationContinue,
			func(manager *rgb11Manager) (struct{}, error) {
				return struct{}{}, manager.cancelRGB11Proxy(ctx, members[0].TransferID, RGB11RejectReasonUser)
			})
		if err != nil {
			return nil, err
		}
		return p.rgbCurrentTransfer(members[0].TransferID)
	}
	if err := p.CancelRGB11OutOfBandTransfer(id); err != nil {
		return nil, err
	}
	return p.rgbCurrentTransfer(id)
}
func (p *Manager) ExportRGBTransfer(id string) ([]RGBTransferFile, error) {
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	members, err := rgbBatch(state, id)
	if err != nil {
		return nil, err
	}
	var files []RGBTransferFile
	for i, t := range members {
		var pkg *RGB11PreparedTransferPackage
		if t.Status == "settled" {
			pkg, err = p.ExportRGB11SettledConsignment(t.TransferID)
		} else {
			pkg, err = p.ResumeRGB11PreparedTransfer(t.TransferID)
		}
		if err != nil {
			return nil, err
		}
		files = append(files, RGBTransferFile{RecipientIndex: i, File: *recoveryFile("rgb-"+t.TransferID+".asc", pkg.RecipientConsignment)})
		data, err := base64.StdEncoding.DecodeString(pkg.RecipientConsignmentBase64)
		if err != nil {
			return nil, err
		}
		files = append(files, RGBTransferFile{RecipientIndex: i, File: ExportFile{Name: "rgb-" + t.TransferID + ".rgb", MIME: "application/octet-stream", Data: data}})
	}
	return files, nil
}
func (p *Manager) CreateRGBInvoice(ctx context.Context, req RGBInvoiceRequest) (opValue0 *RGBReceiveView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_invoice", "Create RGB invoice")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	asset, err := rgbAssetInfo(state, req.Asset)
	if err != nil {
		return nil, err
	}
	amount, err := rgbAmount(req.Amount, uint8(asset.Divisibility))
	if err != nil {
		return nil, err
	}
	input := RGB11InvoiceRequest{Mode: req.ReceiveMode, ContractID: asset.ContractID, AmountRaw: amount, AssignmentName: "assetOwner", Expiry: time.Now().Add(24 * time.Hour).Unix(), WitnessVout: 1}
	switch req.Transport {
	case "proxy":
		input.TransportMode = "rgb-json-rpc"
		input.TransportEndpoints = []string{req.ProxyEndpoint}
	case "manual":
		input.TransportMode = "out-of-band"
	default:
		return nil, fmt.Errorf("unsupported RGB receive transport")
	}
	result, err := p.CreateRGB11Invoice(input)
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentReceive(result.RequestID)
}
func (p *Manager) rgbCurrentReceive(id string) (*RGBReceiveView, error) {
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	return p.rgbReceiveView(state, id)
}
func (p *Manager) PrepareRGBConsignment(ctx context.Context, id string, data []byte) (opValue0 *RGBReceiveView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_receive_prepare", "Validate RGB transfer")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		var pkg struct {
			Transport   string `json:"transport_mode"`
			Consignment string `json:"consignment"`
		}
		if json.Unmarshal(data, &pkg) != nil || pkg.Transport != "out-of-band" || pkg.Consignment == "" {
			return nil, fmt.Errorf("invalid RGB transfer package")
		}
		data = []byte(pkg.Consignment)
	}
	if _, err := p.PrepareRGB11Consignment(ctx, id, data); err != nil {
		return nil, err
	}
	return p.rgbCurrentReceive(id)
}
func (p *Manager) AcceptRGBConsignment(ctx context.Context, id string) (opValue0 *RGBReceiveView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_receive_accept", "Accept RGB transfer")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	scoped, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, err
	}
	request, err := scoped.GetRGB11ReceiveRequest(id)
	if err != nil {
		return nil, err
	}
	raw, err := scoped.rgbManager.projectionStore.LoadObject(request.ObjectHash)
	if err != nil {
		return nil, err
	}
	if _, err := p.AcceptRGB11Consignment(ctx, id, raw); err != nil {
		return nil, err
	}
	return p.rgbCurrentReceive(id)
}
func (p *Manager) IssueRGBAsset(ctx context.Context, req RGBIssueRequest) (opValue0 *RGBAssetView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_issue", "Issue RGB asset")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	convert := func(values []string) ([]uint64, error) {
		result := make([]uint64, len(values))
		for i, v := range values {
			raw, err := rgbAmount(v, req.Precision)
			if err != nil {
				return nil, err
			}
			result[i], err = strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return nil, err
			}
		}
		return result, nil
	}
	amounts, err := convert(req.Amounts)
	if err != nil {
		return nil, err
	}
	inflation, err := convert(req.Inflation)
	if err != nil {
		return nil, err
	}
	result, err := p.IssueRGB11Asset(ctx, RGB11IssueRequest{Schema: req.Kind, Ticker: req.Ticker, Name: req.Name, Details: req.Details, Precision: req.Precision, Amounts: amounts, InflationAmounts: inflation, RejectListURL: req.RejectURL, MinConfirmations: 1})
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentAsset(result.ContractID)
}
func (p *Manager) ImportRGBContract(ctx context.Context, data []byte) (opValue0 *RGBAssetView, opErr error) {
	opLogID := p.beginWalletLog("rgb", "rgb_import", "Import RGB contract")
	defer func() { p.finishWalletLog(opLogID, opErr, opValue0) }()

	container, err := coreconsignment.Decode(data)
	if err != nil {
		return nil, err
	}
	file, err := coreconsignment.EncodeFile(container)
	if err != nil {
		return nil, err
	}
	result, err := p.ImportRGB11ContractFile(ctx, file)
	if err != nil {
		return nil, err
	}
	return p.rgbCurrentAsset(result.ContractID)
}
func (p *Manager) rgbCurrentAsset(id string) (*RGBAssetView, error) {
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	asset, err := rgbAssetInfo(state, id)
	if err != nil {
		return nil, err
	}
	view := rgbAssetView(asset, state)
	return &view, nil
}
func (p *Manager) ExportRGBContract(asset string) ([]ExportFile, error) {
	state, err := p.GetRGB11State()
	if err != nil {
		return nil, err
	}
	info, err := rgbAssetInfo(state, asset)
	if err != nil {
		return nil, err
	}
	result, err := p.ExportRGB11Contract(info.ContractID)
	if err != nil {
		return nil, err
	}
	data, err := base64.StdEncoding.DecodeString(result.ContractConsignmentBase64)
	if err != nil {
		return nil, err
	}
	return []ExportFile{*recoveryFile("rgb-contract.asc", result.Armor), {Name: "rgb-contract.rgb", MIME: "application/octet-stream", Data: data}}, nil
}

type rgbOOBReceipt struct {
	Version           int    `json:"version"`
	Stage             string `json:"stage"`
	ContractID        string `json:"contract_id"`
	SchemaID          string `json:"schema_id"`
	ConsignmentHash   string `json:"consignment_hash"`
	TransferID        string `json:"transfer_id"`
	WitnessTxID       string `json:"witness_txid"`
	InvoiceHash       string `json:"invoice_hash"`
	AmountRaw         string `json:"amount_raw"`
	Precision         uint8  `json:"precision"`
	RecipientOutpoint string `json:"recipient_outpoint"`
}

func rgbTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Receipts bind the canonical proof bytes, independent of the export's armor.
func rgbReceiptHash(container *coreconsignment.Container) (string, error) {
	raw, err := coreconsignment.EncodeFile(container)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (p *Manager) checkRGBReceipts(state *RGB11State, members []*rgb.TransferState, texts []string) error {
	if len(texts) != len(members) {
		return fmt.Errorf("all recipient receipts are required")
	}
	scoped, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	used := make(map[int]bool)
	for _, t := range members {
		info, err := rgbAssetInfo(state, t.Asset.Name.String())
		if err != nil {
			return err
		}
		var ext rgb.TickerExt
		if err := json.Unmarshal(info.Content, &ext); err != nil {
			return err
		}
		raw, err := scoped.rgbManager.projectionStore.LoadObject(t.ConsignmentHash)
		if err != nil {
			return err
		}
		container, err := coreconsignment.Decode(raw)
		if err != nil {
			return err
		}
		proofHash, err := rgbReceiptHash(container)
		if err != nil {
			return err
		}
		matched := false
		for i, text := range texts {
			if used[i] {
				continue
			}
			var r rgbOOBReceipt
			if json.Unmarshal([]byte(text), &r) != nil {
				continue
			}
			same := r.TransferID == t.TransferID || (t.BatchID != "" && r.TransferID == t.BatchID && t.RecipientVout > 0)
			if r.Version == 1 && r.Stage == "validated-awaiting-broadcast" && same && r.ContractID == info.ContractID && r.SchemaID == ext.SchemaID && r.ConsignmentHash == proofHash && r.WitnessTxID == t.WitnessTxID && r.InvoiceHash == rgbTextHash(t.Invoice) && r.AmountRaw == t.Asset.Amount.Value.String() && r.Precision == uint8(t.Asset.Amount.Precision) && (t.RecipientVout == 0 || r.RecipientOutpoint == fmt.Sprintf("%s:%d", t.WitnessTxID, t.RecipientVout)) {
				matched = true
				used[i] = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("RGB recipient receipt does not match the transfer")
		}
	}
	return nil
}
func (p *Manager) rgbReceiveView(state *RGB11State, id string) (*RGBReceiveView, error) {
	var resv *rgb.RGB11Reservation
	for _, r := range state.Reservations {
		if r.RequestID == id {
			resv = r
			break
		}
	}
	var transfer *rgb.TransferState
	for _, t := range state.Transfers {
		if t.Direction == "receive" && !t.AddressMode && t.ReceiveRequestID == id {
			transfer = t
			break
		}
	}
	if resv == nil && transfer == nil {
		return nil, fmt.Errorf("RGB receive request not found")
	}
	view := &RGBReceiveView{ID: id, Status: "waiting", Transport: "manual"}
	var info *RGB11TickerInfo
	var err error
	var transport string
	if transfer != nil {
		info, err = rgbAssetInfo(state, transfer.Asset.Name.String())
		if err != nil {
			return nil, err
		}
		invoice, err := invoicing.Parse(transfer.Invoice)
		if err != nil {
			return nil, err
		}
		view.Amount = rgbDisplayAmount(transfer.Asset)
		view.Invoice, view.ExpiresAt = transfer.Invoice, transfer.Expiry
		view.ReceiveMode = "blind"
		if invoice.Beneficiary.Kind == invoicing.BeneficiaryWitnessVout {
			view.ReceiveMode = "witness"
		}
		transport = transfer.TransportMode
	} else {
		info, err = rgbAssetInfo(state, resv.ContractID)
		if err != nil {
			return nil, err
		}
		amount, err := indexer.NewDecimalFromString(resv.AmountRaw, 0)
		if err != nil {
			return nil, err
		}
		amount.Precision = info.Divisibility
		view.Amount = AssetAmount{Asset: info.AssetName.String(), Amount: amount.String(), Precision: uint8(info.Divisibility)}
		view.Invoice, view.ReceiveMode, view.ExpiresAt = resv.Invoice, resv.Mode, resv.Expiry
		transport = resv.TransportMode
	}
	if transport == "rgb-json-rpc" {
		view.Transport = "proxy"
	}
	view.CanImport = view.Transport == "manual" && (view.ExpiresAt == 0 || view.ExpiresAt > time.Now().Unix())
	if view.ExpiresAt != 0 && view.ExpiresAt <= time.Now().Unix() {
		view.Status = "expired"
	}
	if t := transfer; t != nil {
		view.TxID = t.WitnessTxID
		view.ErrorCode = t.RejectReason
		view.Status = t.Status
		if t.Status == "awaiting_broadcast" {
			view.Status = "awaiting_confirmation"
			view.CanImport = false
			view.CanAccept = true
		}
		if t.Status == "pending" || t.Status == "broadcast" {
			view.Status = "pending_chain"
			view.CanImport = false
		}
		if t.Status == "settled" {
			view.Status = "completed"
			view.CanImport = false
		}
		if t.Status == "awaiting_broadcast" && t.TransportMode == "out-of-band" && len(t.OutputOutPoints) == 1 {
			var ext rgb.TickerExt
			if err := json.Unmarshal(info.Content, &ext); err != nil {
				return nil, err
			}
			scoped, err := p.synchronizedRGB11Manager()
			if err != nil {
				return nil, err
			}
			// Prepared receive material is already validated and persisted, but
			// has no settled receipt before broadcast. Read its canonical ID
			// from that same stored proof; do not create another state record.
			raw, err := scoped.rgbManager.projectionStore.LoadObject(t.ConsignmentHash)
			if err != nil {
				return nil, err
			}
			container, err := coreconsignment.Decode(raw)
			if err != nil {
				return nil, err
			}
			proofHash, err := rgbReceiptHash(container)
			if err != nil {
				return nil, err
			}
			receipt := rgbOOBReceipt{Version: 1, Stage: "validated-awaiting-broadcast", ContractID: info.ContractID, SchemaID: ext.SchemaID, ConsignmentHash: proofHash, TransferID: container.Armor.ID, WitnessTxID: t.WitnessTxID, InvoiceHash: rgbTextHash(t.Invoice), AmountRaw: t.Asset.Amount.Value.String(), Precision: uint8(t.Asset.Amount.Precision), RecipientOutpoint: t.OutputOutPoints[0]}
			data, err := json.Marshal(receipt)
			if err != nil {
				return nil, err
			}
			view.Receipt = string(data)
		}
	}
	return view, nil
}

var _ RGBAPI = (*Manager)(nil)
