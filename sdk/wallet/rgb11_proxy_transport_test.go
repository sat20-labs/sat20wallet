package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/consensus"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11ProxyTestServer struct {
	mu                     sync.Mutex
	recipientID            string
	txID                   string
	vout                   *uint32
	consignment            []byte
	ack                    *bool
	postError              *rgb11ProxyError
	ackPostHTTPStatus      int
	ackPostFailBeforeStore bool
	ackGetHTTPStatus       int
	ackPostCount           int
	ackGetCount            int
}

const testRGB11ProxyTransport = "rpcs://proxy.iriswallet.com/0.2/json-rpc"

func (s *rgb11ProxyTestServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		s.handleMultipart(w, r)
		return
	}
	var request struct {
		Method string          `json:"method"`
		ID     string          `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if request.ID == "" {
		http.Error(w, "missing JSON-RPC id", http.StatusBadRequest)
		return
	}
	switch request.Method {
	case "consignment.get":
		s.mu.Lock()
		defer s.mu.Unlock()
		if len(s.consignment) == 0 {
			_, _ = io.WriteString(w, `{"result":null}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": rgb11ProxyConsignment{
			Consignment: base64.StdEncoding.EncodeToString(s.consignment),
			TxID:        s.txID, Vout: s.vout,
		}})
	case "ack.post":
		var params rgb11ProxyAckParam
		if err := json.Unmarshal(request.Params, &params); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.ackPostCount++
		status := s.ackPostHTTPStatus
		failBeforeStore := s.ackPostFailBeforeStore
		if failBeforeStore {
			s.mu.Unlock()
			http.Error(w, "ack post failed", status)
			return
		}
		if s.ack != nil {
			matches := *s.ack == params.Ack
			s.mu.Unlock()
			if matches {
				_, _ = io.WriteString(w, `{"result":false}`)
			} else {
				_, _ = io.WriteString(w, `{"error":{"code":-100,"message":"Cannot change ACK"}}`)
			}
			return
		}
		ack := params.Ack
		s.ack = &ack
		s.mu.Unlock()
		if status != 0 {
			http.Error(w, "ack post response failed", status)
			return
		}
		_, _ = io.WriteString(w, `{"result":true}`)
	case "ack.get":
		s.mu.Lock()
		s.ackGetCount++
		status := s.ackGetHTTPStatus
		var ack *bool
		if s.ack != nil {
			value := *s.ack
			ack = &value
		}
		s.mu.Unlock()
		if status != 0 {
			http.Error(w, "ack get failed", status)
			return
		}
		if ack == nil {
			_, _ = io.WriteString(w, `{"result":null}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": *ack})
	default:
		http.Error(w, "unknown method", http.StatusBadRequest)
	}
}

func (s *rgb11ProxyTestServer) handleMultipart(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(rgb11ProxyMaxConsignmentLen + 1024); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var params rgb11ProxyPostConsignmentParam
	if err := json.Unmarshal([]byte(r.FormValue("params")), &params); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	postError := s.postError
	s.mu.Unlock()
	if postError != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": postError})
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.recipientID = params.RecipientID
	s.txID = params.TxID
	s.vout = params.Vout
	s.consignment = raw
	s.mu.Unlock()
	_, _ = io.WriteString(w, `{"result":true}`)
}

func TestRGB11ReceiveTransportsBuildsStandardInvoiceEndpoint(t *testing.T) {
	transports, standard, err := rgb11ReceiveTransports(RGB11InvoiceRequest{
		TransportMode: "out-of-band",
	})
	if err != nil || !standard || len(transports) != 0 {
		t.Fatalf("unexpected out-of-band transport: standard=%v transports=%+v err=%v", standard, transports, err)
	}
	if _, _, err := rgb11ReceiveTransports(RGB11InvoiceRequest{
		TransportMode: "out-of-band", TransportEndpoints: []string{testRGB11ProxyTransport},
	}); err == nil {
		t.Fatal("out-of-band transport accepted a proxy endpoint")
	}
	if _, _, err := rgb11ReceiveTransports(RGB11InvoiceRequest{
		TransportMode: RGB11ProxyTransport,
	}); !errors.Is(err, ErrRGB11ProxyNoEndpoint) {
		t.Fatalf("missing endpoint error = %v", err)
	}
	transports, standard, err = rgb11ReceiveTransports(RGB11InvoiceRequest{
		TransportMode: RGB11ProxyTransport, TransportEndpoints: []string{testRGB11ProxyTransport},
	})
	if err != nil || !standard || len(transports) != 1 || transports[0].String() != testRGB11ProxyTransport {
		t.Fatalf("unexpected standard transports: standard=%v transports=%+v", standard, transports)
	}
	if _, _, err := rgb11ReceiveTransports(RGB11InvoiceRequest{
		TransportMode:      RGB11ProxyTransport,
		TransportEndpoints: []string{"rpc://proxy.example.com/0.2/json-rpc"},
	}); err == nil {
		t.Fatal("accepted insecure non-loopback JSON-RPC transport")
	}
}

func TestRGB11ProxyProtocolRoundTrip(t *testing.T) {
	state := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()
	endpoint := server.URL
	recipientID := "tb4:recipient"
	txID := strings.Repeat("ab", 32)
	vout := uint32(1)
	consignment := []byte{1, 2, 3, 4}

	if err := rgb11ProxyPostConsignment(
		context.Background(), endpoint, recipientID, txID, &vout, consignment,
	); err != nil {
		t.Fatal(err)
	}
	received, err := rgb11ProxyJSON[rgb11ProxyConsignment](
		context.Background(), endpoint, "consignment.get",
		rgb11ProxyRecipientParam{RecipientID: recipientID},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(received.Consignment)
	if err != nil || string(raw) != string(consignment) || received.TxID != txID ||
		received.Vout == nil || *received.Vout != vout {
		t.Fatalf("unexpected consignment response: %+v raw=%x err=%v", received, raw, err)
	}
	if err := rgb11ProxyPostConsignment(
		context.Background(), endpoint, recipientID, txID, nil, consignment,
	); err != nil {
		t.Fatal(err)
	}
	received, err = rgb11ProxyJSON[rgb11ProxyConsignment](
		context.Background(), endpoint, "consignment.get",
		rgb11ProxyRecipientParam{RecipientID: recipientID},
	)
	if err != nil || received.Vout != nil {
		t.Fatalf("blinded consignment must omit witness vout: %+v err=%v", received, err)
	}
	if err := rgb11ProxyPostAck(context.Background(), endpoint, recipientID, true); err != nil {
		t.Fatal(err)
	}
	ack, err := rgb11ProxyJSON[bool](
		context.Background(), endpoint, "ack.get",
		rgb11ProxyRecipientParam{RecipientID: recipientID},
	)
	if err != nil || ack == nil || !*ack {
		t.Fatalf("unexpected ACK: %v, %v", ack, err)
	}
}

func TestRGB11ProxyEnsureAck(t *testing.T) {
	t.Run("first post succeeds", func(t *testing.T) {
		state := &rgb11ProxyTestServer{}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		if err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true); err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.ack == nil || !*state.ack || state.ackPostCount != 1 || state.ackGetCount != 0 {
			t.Fatalf("unexpected ACK state: %+v", state)
		}
	})

	t.Run("repeated true is idempotent", func(t *testing.T) {
		value := true
		state := &rgb11ProxyTestServer{ack: &value}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		if err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true); err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.ackPostCount != 1 || state.ackGetCount != 1 {
			t.Fatalf("unexpected ACK attempts: post=%d get=%d", state.ackPostCount, state.ackGetCount)
		}
	})

	t.Run("failed post response verifies stored true", func(t *testing.T) {
		state := &rgb11ProxyTestServer{ackPostHTTPStatus: http.StatusBadGateway}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		if err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true); err != nil {
			t.Fatal(err)
		}
		state.mu.Lock()
		defer state.mu.Unlock()
		if state.ack == nil || !*state.ack || state.ackPostCount != 1 || state.ackGetCount != 1 {
			t.Fatalf("unexpected ACK recovery state: %+v", state)
		}
	})

	t.Run("opposite remote decision conflicts", func(t *testing.T) {
		value := false
		state := &rgb11ProxyTestServer{ack: &value}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true)
		if err == nil || !strings.Contains(err.Error(), "conflicts with requested decision") {
			t.Fatalf("unexpected conflict error: %v", err)
		}
	})

	t.Run("missing remote decision preserves post error", func(t *testing.T) {
		state := &rgb11ProxyTestServer{
			ackPostHTTPStatus: http.StatusBadGateway, ackPostFailBeforeStore: true,
		}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true)
		if err == nil || !strings.Contains(err.Error(), "HTTP 502") ||
			!strings.Contains(err.Error(), "acknowledgment is not available") {
			t.Fatalf("unexpected missing ACK error: %v", err)
		}
	})

	t.Run("post and get failures are both retained", func(t *testing.T) {
		state := &rgb11ProxyTestServer{
			ackPostHTTPStatus: http.StatusBadGateway, ackPostFailBeforeStore: true,
			ackGetHTTPStatus: http.StatusServiceUnavailable,
		}
		server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
		defer server.Close()
		err := rgb11ProxyEnsureAck(context.Background(), server.URL, "recipient", true)
		if err == nil || !strings.Contains(err.Error(), "HTTP 502") ||
			!strings.Contains(err.Error(), "HTTP 503") {
			t.Fatalf("unexpected combined ACK error: %v", err)
		}
	})
}

func TestRGB11ProxyEndpointsAcceptLoopbackRPC(t *testing.T) {
	transport, err := invoicing.ParseTransport("rpc://127.0.0.1:3000/0.2/json-rpc")
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := rgb11ProxyEndpointURL(transport)
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:3000/0.2/json-rpc" {
		t.Fatalf("endpoint = %q", endpoint)
	}
}

func TestRGB11ProxyInvoiceAcceptsBlindedBeneficiary(t *testing.T) {
	invoice := "rgb:~/~/~/tb4:utxob:0p7Vez5g-71fjxwc-cM1U8q8-aPdDoj5-rhHwhlc-7lHZlZy-YcXFX" +
		"?expiry=1785485187&endpoints=rpcs://proxy.iriswallet.com/0.2/json-rpc"
	parsed, endpoints, err := rgb11ProxyInvoice(invoice)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Beneficiary.Kind != invoicing.BeneficiaryBlindedSeal || len(endpoints) != 1 ||
		endpoints[0].invoice != testRGB11ProxyTransport {
		t.Fatalf("unexpected blinded proxy invoice: invoice=%+v endpoints=%+v", parsed, endpoints)
	}
}

func TestRGB11WitnessReceiveMatchesExpectedRecipientAfterChange(t *testing.T) {
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	if recipientWallet == nil {
		t.Fatal("create recipient wallet")
	}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	manager := newRGB11FlowManager(t, recipientWallet, &rgb11FlowIndexer{
		outputs: make(map[string]*TxOutput),
	}, evidence, 94)
	receive, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{testRGB11ProxyTransport},
		AmountRaw:          "10", WitnessVout: 2, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := manager.rgbManager.engine.LoadReceive(receive.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := invoicing.Parse(receive.Invoice)
	if err != nil {
		t.Fatal(err)
	}
	txID := strings.Repeat("91", 32)
	changeOutpoint := txID + ":1"
	recipientOutpoint := txID + ":2"
	assetName := indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: "recipient-match"}
	receipt := &rgb11wallet.ValidationReceipt{Allocations: []rgb11wallet.ValidatedAllocation{
		{
			OutPoint: changeOutpoint, AssetName: assetName,
			Amount: *indexer.NewDefaultDecimal(90), AssignmentType: 4000,
			WitnessTxPtr: true, WitnessTxID: txID, CommitmentMethod: "genesis",
		},
		{
			OutPoint: recipientOutpoint, AssetName: assetName,
			Amount: *indexer.NewDefaultDecimal(10), AssignmentType: 4000,
			WitnessTxPtr: true, WitnessTxID: txID, CommitmentMethod: "genesis",
		},
	}}
	preparedOutputs := map[string]*rgb11wallet.BitcoinUTXO{
		changeOutpoint: {
			OutPoint: changeOutpoint, Value: 1_000, PkScript: []byte{0x51},
		},
		recipientOutpoint: {
			OutPoint: recipientOutpoint, Value: 1_000,
			PkScript: append([]byte(nil), request.WitnessScript...),
		},
	}
	expectedVout := uint32(2)
	matched, err := manager.rgbManager.matchRGB11ReceiveAllocation(
		receipt, request, invoice, preparedOutputs, txID, &expectedVout,
	)
	if err != nil {
		t.Fatal(err)
	}
	if matched.Allocation.OutPoint != recipientOutpoint || matched.Vout == nil ||
		*matched.Vout != expectedVout || matched.SealCommitment != "" {
		t.Fatalf("unexpected witness allocation match: %+v", matched)
	}
	wrongVout := uint32(1)
	if _, err := manager.rgbManager.matchRGB11ReceiveAllocation(
		receipt, request, invoice, preparedOutputs, txID, &wrongVout,
	); !errors.Is(err, ErrRGB11NoAllocation) {
		t.Fatalf("wrong proxy vout was accepted: %v", err)
	}
}

func TestRGB11ProxyDefersNackUntilBitcoinEvidenceIsAvailable(t *testing.T) {
	state := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()

	for _, err := range []error{
		coreconsignment.ErrWitnessUnresolved,
		coreconsignment.ErrOutpointUnknown,
	} {
		rgb11ProxyPostNackIfTerminal(context.Background(), server.URL, "recipient", err)
		state.mu.Lock()
		ack := state.ack
		state.mu.Unlock()
		if ack != nil {
			t.Fatalf("temporary Bitcoin evidence error %v posted ACK=%v", err, *ack)
		}
	}

	rgb11ProxyPostNackIfTerminal(
		context.Background(), server.URL, "recipient", coreconsignment.ErrWitnessMismatch,
	)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.ack == nil || *state.ack {
		t.Fatalf("terminal validation error did not post NACK: %v", state.ack)
	}
}

func TestRGB11ProxyDeliveryBroadcastsBeforeAck(t *testing.T) {
	state := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(state.serveHTTP))
	defer server.Close()

	testWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	if testWallet == nil {
		t.Fatal("create test wallet")
	}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	manager := newRGB11FlowManager(t, testWallet, &rgb11FlowIndexer{
		outputs: make(map[string]*TxOutput),
	}, evidence, 91)

	script, err := AddrToPkScript(testWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	beneficiary, err := invoicing.NewWitnessBeneficiary(invoicing.BitcoinTestnet4, script, nil)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := consensus.ParseContractID("rgb:eIbQx5Am-XRDjj01-RM~5eo7-rv2nluD-OnBJRAy-S9~Yfts")
	if err != nil {
		t.Fatal(err)
	}
	transport, err := invoicing.ParseTransport("rpc://" + strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).Unix()
	invoice := invoicing.Invoice{
		Transports:     []invoicing.Transport{transport},
		Contract:       &contract,
		AssignmentName: "assetOwner",
		Assignment:     &invoicing.InvoiceState{Kind: invoicing.StateAmount, Amount: 10},
		Beneficiary:    beneficiary,
		Expiry:         &expiry,
	}
	if err := invoice.Validate(time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	tx := wire.NewMsgTx(wire.TxVersion)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(330, script))
	var signed bytes.Buffer
	if err := tx.Serialize(&signed); err != nil {
		t.Fatal(err)
	}
	consignment, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	if err != nil {
		t.Fatal(err)
	}
	consignmentHash := sha256.Sum256(consignment)
	transferID := "proxy-transfer"
	pending := &rgb11wallet.PendingTransfer{
		State: rgb11wallet.TransferState{
			TransferID: transferID, BatchTransferIDs: []string{transferID}, BatchSize: 1,
			RecipientVout: 1, TransportMode: RGB11ProxyTransport, Direction: "send",
			Asset: indexer.AssetInfo{
				Name:   indexer.AssetName{Protocol: "rgb11", Type: "f", Ticker: "proxy"},
				Amount: *indexer.NewDefaultDecimal(10),
			},
			RecipientID: beneficiary.String(), Invoice: invoice.String(),
			Expiry: expiry, ConsignmentHash: hexString(consignmentHash[:]),
			WitnessTxID: tx.TxHash().String(), AckStatus: "awaiting", Status: "prepared",
		},
		RecipientConsignment: consignment,
		LocalConsignment:     append([]byte(nil), consignment...),
		SignedTx:             signed.Bytes(), SignedPSBT: []byte{1}, CreatedAt: time.Now().Unix(),
	}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}

	delivered, err := manager.rgbManager.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{transferID},
	)
	if err != nil {
		t.Fatal(err)
	}
	if delivered.TxID != tx.TxHash().String() || len(evidence.broadcasted) == 0 {
		t.Fatalf("delivery did not broadcast: result=%+v", delivered)
	}
	state.mu.Lock()
	postedTxID, postedRecipient := state.txID, state.recipientID
	postedConsignment := append([]byte(nil), state.consignment...)
	state.mu.Unlock()
	if postedTxID != delivered.TxID || postedRecipient != beneficiary.String() {
		t.Fatalf("unexpected proxy upload txid=%s recipient=%s", postedTxID, postedRecipient)
	}
	if !bytes.HasPrefix(postedConsignment, []byte("RGB\x00TFR")) {
		t.Fatalf("proxy upload is not a standard RGB transfer file: %x", postedConsignment[:min(7, len(postedConsignment))])
	}
	if _, err := coreconsignment.DecodeFile(postedConsignment); err != nil {
		t.Fatalf("proxy upload is not decodable as a standard RGB transfer file: %v", err)
	}
	stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != "broadcast" || stored.State.AckStatus != "awaiting" {
		t.Fatalf("unexpected post-broadcast state: %+v", stored.State)
	}

	ack, err := manager.rgbManager.FetchRGB11ProxyAck(context.Background(), transferID)
	if err != nil || ack.Available {
		t.Fatalf("ACK should still be pending: ack=%+v err=%v", ack, err)
	}
	state.mu.Lock()
	state.ack = new(bool)
	*state.ack = true
	state.mu.Unlock()
	ack, err = manager.rgbManager.FetchRGB11ProxyAck(context.Background(), transferID)
	if err != nil || !ack.Available || !ack.Accepted {
		t.Fatalf("accepted ACK not recorded: ack=%+v err=%v", ack, err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != "broadcast" || stored.State.AckStatus != "accepted" {
		t.Fatalf("ACK changed broadcast lifecycle incorrectly: %+v", stored.State)
	}

	conflictID := "proxy-recipient-conflict"
	inputOutpoint := strings.Repeat("44", 32) + ":0"
	conflict := *pending
	conflict.State = pending.State
	conflict.State.TransferID = conflictID
	conflict.State.BatchTransferIDs = []string{conflictID}
	conflict.State.Status = "prepared"
	conflict.State.AckStatus = "awaiting"
	conflict.State.InputOutPoints = []string{inputOutpoint}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(&conflict); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.SetLockReason(inputOutpoint, rgb11wallet.LockReasonPending); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.postError = &rgb11ProxyError{Code: -101, Message: "Cannot change uploaded file"}
	state.mu.Unlock()
	broadcastCount := len(evidence.broadcasted)
	_, err = manager.rgbManager.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{conflictID},
	)
	var proxyErr *rgb11ProxyRPCError
	if !errors.As(err, &proxyErr) || proxyErr.Code != -101 {
		t.Fatalf("terminal proxy error = %v", err)
	}
	if len(evidence.broadcasted) != broadcastCount {
		t.Fatal("terminal proxy conflict broadcast the witness transaction")
	}
	rejected, err := manager.rgbManager.projectionStore.LoadPendingTransfer(conflictID)
	if err != nil || rejected.State.Status != "rejected" ||
		rejected.State.RejectReason != "proxy-recipient-conflict" {
		t.Fatalf("terminal proxy conflict state = %+v, err=%v", rejected, err)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[inputOutpoint]; lock != nil {
		t.Fatalf("terminal proxy conflict left input locked: %+v", lock)
	}
}

func TestRGB11ProxyBlindReceiveAcknowledgesBeforeBroadcast(t *testing.T) {
	proxy := &rgb11ProxyTestServer{}
	server := httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
	defer server.Close()

	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		"", &chaincfg.TestNet4Params,
	)
	if senderWallet == nil || recipientWallet == nil {
		t.Fatal("create test wallets")
	}
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	recipientScript, err := AddrToPkScript(recipientWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	const feeOutpoint = "8181818181818181818181818181818181818181818181818181818181818181:0"
	const receiveOutpoint = "8282828282828282828282828282828282828282828282828282828282828282:0"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {
				OutPoint: sourceOutpoint, Value: 10_000,
				PkScript: append([]byte(nil), senderScript...), Confirmations: 6,
			},
			feeOutpoint: {
				OutPoint: feeOutpoint, Value: 100_000,
				PkScript: append([]byte(nil), senderScript...), Confirmations: 6,
			},
			receiveOutpoint: {
				OutPoint: receiveOutpoint, Value: 100_000,
				PkScript: append([]byte(nil), recipientScript...), Confirmations: 6,
			},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	for _, item := range []struct {
		outpoint string
		value    int64
		script   []byte
	}{
		{sourceOutpoint, 10_000, senderScript},
		{feeOutpoint, 100_000, senderScript},
		{receiveOutpoint, 100_000, recipientScript},
	} {
		output := indexer.NewTxOutput(item.value)
		output.OutPointStr = item.outpoint
		output.OutValue.PkScript = append([]byte(nil), item.script...)
		rpc.outputs[item.outpoint] = output
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
			OutPoint: item.outpoint, Value: item.value,
			PkScript: append([]byte(nil), item.script...),
		})
	}
	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 92)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 93)
	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := sender.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "rpc://" + strings.TrimPrefix(server.URL, "http://")
	receive, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "blind", TransportMode: RGB11ProxyTransport,
		TransportEndpoints: []string{endpoint}, ContractID: imported.ContractID,
		AmountRaw: "20000", Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receive.Seal.TxID == nil {
		t.Fatal("standard blind invoice did not use an absolute receiver UTXO seal")
	}
	prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: receive.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sender.rgbManager.publishRGB11ProxyConsignment(
		context.Background(), prepared.State.TransferID,
	); err != nil {
		t.Fatal(err)
	}
	received, err := recipient.ReceiveRGB11ProxyConsignment(
		context.Background(), receive.RequestID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !received.AckPosted || !received.AwaitingBroadcast ||
		received.TxID != prepared.State.WitnessTxID || received.Vout != nil {
		t.Fatalf("unexpected prepared receive result: %+v", received)
	}
	proofs, err := recipient.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 0 {
		t.Fatalf("prepared receive projected spendable RGB state: %+v", proofs)
	}
	proxy.mu.Lock()
	ack := proxy.ack
	proxy.mu.Unlock()
	if ack == nil || !*ack {
		t.Fatalf("receiver did not ACK prepared consignment: %v", ack)
	}

	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	evidence.mu.Lock()
	evidence.rawTx[pending.State.WitnessTxID] = append([]byte(nil), pending.SignedTx...)
	for _, outpoint := range pending.State.InputOutPoints {
		evidence.spendingTx[outpoint] = pending.State.WitnessTxID
	}
	evidence.mu.Unlock()
	received, err = recipient.ReceiveRGB11ProxyConsignment(
		context.Background(), receive.RequestID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if received.AwaitingBroadcast || received.Receipt == nil ||
		received.TxID != pending.State.WitnessTxID {
		t.Fatalf("unexpected accepted receive result: %+v", received)
	}
	proofs, err = recipient.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 || proofs[0].OutPoint != receiveOutpoint ||
		proofs[0].WitnessTxID != pending.State.WitnessTxID ||
		proofs[0].Status != "valid" || proofs[0].SealCommitment == "" {
		t.Fatalf("unexpected accepted blind allocation: %+v", proofs)
	}
	if lock := recipient.utxoLockerL1.GetLockedUtxoList()[receiveOutpoint]; lock == nil ||
		lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("accepted blind carrier lock=%+v", lock)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	received, err = recipient.ReceiveRGB11ProxyConsignment(
		context.Background(), receive.RequestID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !received.AckPosted || received.TxID != pending.State.WitnessTxID {
		t.Fatalf("unexpected repeated receive result: %+v", received)
	}
	repeatedBalance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	if repeatedBalance.Value.Cmp(balance.Value) != 0 {
		t.Fatalf("repeated receive changed balance: before=%s after=%s", balance.Value, repeatedBalance.Value)
	}
	proofs, err = recipient.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 {
		t.Fatalf("repeated receive duplicated proofs: %+v", proofs)
	}
}

func hexString(value []byte) string {
	const digits = "0123456789abcdef"
	result := make([]byte, len(value)*2)
	for index, item := range value {
		result[index*2] = digits[item>>4]
		result[index*2+1] = digits[item&0x0f]
	}
	return string(result)
}
