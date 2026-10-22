package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/btcsuite/btcd/txscript"
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
	corewallet "github.com/sat20-labs/rgb11/wallet"
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
	postHTTPStatus         int
	postFailBeforeStore    bool
	ackPostHTTPStatus      int
	ackPostFailBeforeStore bool
	ackGetHTTPStatus       int
	ackPostCount           int
	ackGetCount            int
	ackHistory             []bool
}

type rgb11ProxyFaultEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	mu        sync.Mutex
	txid      string
	raw       []byte
	err       error
	triggered bool
}

func (e *rgb11ProxyFaultEvidence) failNextRawTx(txid string, err error) {
	e.mu.Lock()
	e.txid, e.raw, e.err, e.triggered = txid, nil, err, false
	e.mu.Unlock()
}

func (e *rgb11ProxyFaultEvidence) returnWrongRawTxOnce(txid string, raw []byte) {
	e.mu.Lock()
	e.txid, e.raw, e.err, e.triggered = txid, append([]byte(nil), raw...), nil, false
	e.mu.Unlock()
}

func (e *rgb11ProxyFaultEvidence) rawTxFaultTriggered() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.triggered
}

func (e *rgb11ProxyFaultEvidence) GetRawTx(txid string) ([]byte, error) {
	e.mu.Lock()
	if e.err != nil && e.txid == txid {
		err := e.err
		e.txid, e.raw, e.err, e.triggered = "", nil, nil, true
		e.mu.Unlock()
		return nil, err
	}
	if e.raw != nil && e.txid == txid {
		raw := append([]byte(nil), e.raw...)
		e.txid, e.raw, e.err, e.triggered = "", nil, nil, true
		e.mu.Unlock()
		return raw, nil
	}
	e.mu.Unlock()
	return e.BitcoinEvidenceProvider.GetRawTx(txid)
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
		s.ackHistory = append(s.ackHistory, params.Ack)
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
	status := s.postHTTPStatus
	failBeforeStore := s.postFailBeforeStore
	s.mu.Unlock()
	if postError != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": postError})
		return
	}
	if failBeforeStore {
		http.Error(w, "consignment post failed", status)
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
	if status != 0 {
		http.Error(w, "consignment post response failed", status)
		return
	}
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
	state.mu.Lock()
	state.postHTTPStatus = http.StatusBadGateway
	state.mu.Unlock()
	if err := rgb11ProxyPostConsignment(
		context.Background(), endpoint, recipientID, txID, nil, consignment,
	); err != nil {
		t.Fatalf("stored consignment was not recovered after response loss: %v", err)
	}
	state.mu.Lock()
	state.postHTTPStatus = 0
	state.postError = &rgb11ProxyError{Code: -101, Message: "Cannot change uploaded file"}
	state.mu.Unlock()
	if err := rgb11ProxyPostConsignment(
		context.Background(), endpoint, recipientID, txID, nil, consignment,
	); err != nil {
		t.Fatalf("identical existing consignment was treated as conflict: %v", err)
	}
	state.mu.Lock()
	state.consignment = []byte("different")
	state.mu.Unlock()
	if err := rgb11ProxyPostConsignment(
		context.Background(), endpoint, recipientID, txID, nil, consignment,
	); err == nil {
		t.Fatal("different existing consignment was accepted")
	}
	state.mu.Lock()
	state.postError = nil
	state.mu.Unlock()
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

func TestRGB11ProxyAckSkipsMissing(t *testing.T) {
	first := &rgb11ProxyTestServer{}
	firstServer := httptest.NewServer(http.HandlerFunc(first.serveHTTP))
	defer firstServer.Close()
	accepted := true
	second := &rgb11ProxyTestServer{ack: &accepted}
	secondServer := httptest.NewServer(http.HandlerFunc(second.serveHTTP))
	defer secondServer.Close()

	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", &chaincfg.TestNet4Params,
	)
	manager := newRGB11FlowManager(t, wallet, &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)},
		&rgb11FlowEvidence{utxos: make(map[string]*rgb11wallet.BitcoinUTXO),
			rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}, 93)
	script, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}
	beneficiary, err := invoicing.NewWitnessBeneficiary(invoicing.BitcoinTestnet4, script, nil)
	if err != nil {
		t.Fatal(err)
	}
	firstTransport, err := invoicing.ParseTransport("rpc://" + strings.TrimPrefix(firstServer.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	secondTransport, err := invoicing.ParseTransport("rpc://" + strings.TrimPrefix(secondServer.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).Unix()
	invoice := invoicing.Invoice{
		Transports:  []invoicing.Transport{firstTransport, secondTransport},
		Beneficiary: beneficiary, Expiry: &expiry,
	}
	const transferID = "proxy-multi-endpoint"
	consignment := []byte("proxy-multi-endpoint-consignment")
	consignmentHash := sha256.Sum256(consignment)
	pending := &rgb11wallet.PendingTransfer{State: rgb11wallet.TransferState{
		TransferID: transferID, TransportMode: RGB11ProxyTransport, Direction: "send",
		RecipientID: beneficiary.String(), Invoice: invoice.String(), Expiry: expiry,
		Status: "relayed", ConsignmentHash: hexString(consignmentHash[:]),
	}, RecipientConsignment: consignment, LocalConsignment: append([]byte(nil), consignment...),
		SignedTx: []byte{1}, SignedPSBT: []byte{1}}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(pending); err != nil {
		t.Fatal(err)
	}
	result, err := manager.rgbManager.FetchRGB11ProxyAck(context.Background(), transferID)
	if err != nil || result == nil || !result.Available || !result.Accepted ||
		result.Endpoint != secondTransport.String() {
		t.Fatalf("multi endpoint ACK=%+v err=%v", result, err)
	}
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
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
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
	rpc := &rgb11FlowIndexer{
		outputs: make(map[string]*TxOutput),
	}
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	manager := newRGB11ActivatedFlowAccount(t,
		"comfort very add tuition senior run eight snap burst appear exile dutch",
		remote, client, evidence, rpc)
	if manager.wallet.GetAddress() != recipientWallet.GetAddress() {
		t.Fatal("activated fixture changed the original recipient identity")
	}
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
		*matched.Vout != expectedVout {
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
		rgb11wallet.ErrWalletScope,
	} {
		rgb11ProxyPostNackIfTerminal(context.Background(), server.URL, "recipient", err)
		state.mu.Lock()
		ack := state.ack
		state.mu.Unlock()
		if ack != nil {
			t.Fatalf("retryable receive error %v posted ACK=%v", err, *ack)
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

func TestRGB11ProxyDeliveryWaitsForAckBeforeBroadcast(t *testing.T) {
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
	packet, err := CreatePsbt(tx, txscript.NewCannedPrevOutputFetcher(script, 10_000), nil)
	if err != nil {
		t.Fatal(err)
	}
	var preparedPSBT bytes.Buffer
	if err := packet.Serialize(&preparedPSBT); err != nil {
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
			InputOutPoints:  []string{tx.TxIn[0].PreviousOutPoint.String()},
			OutputOutPoints: []string{tx.TxHash().String() + ":0"},
			Expiry:          expiry, ConsignmentHash: hexString(consignmentHash[:]),
			WitnessTxID: tx.TxHash().String(), AckStatus: "awaiting", Status: "prepared",
		},
		RecipientConsignment: consignment,
		LocalConsignment:     append([]byte(nil), consignment...),
		SignedTx:             signed.Bytes(), SignedPSBT: preparedPSBT.Bytes(), CreatedAt: time.Now().Unix(),
		ReservationID: "proxy-transfer-reservation",
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
	if !delivered.AwaitingACK || delivered.Broadcast || delivered.TxID != "" || len(evidence.broadcasted) != 0 {
		t.Fatalf("delivery crossed the ACK boundary: result=%+v", delivered)
	}
	state.mu.Lock()
	postedTxID, postedRecipient := state.txID, state.recipientID
	postedConsignment := append([]byte(nil), state.consignment...)
	state.mu.Unlock()
	if postedTxID != tx.TxHash().String() || postedRecipient != beneficiary.String() {
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
	if stored.State.Status != "relayed" || stored.State.AckStatus != "awaiting" {
		t.Fatalf("unexpected pre-ACK state: %+v", stored.State)
	}

	ack, err := manager.rgbManager.FetchRGB11ProxyAck(context.Background(), transferID)
	if err != nil || ack.Available {
		t.Fatalf("ACK should still be pending: ack=%+v err=%v", ack, err)
	}
	state.mu.Lock()
	state.ack = new(bool)
	*state.ack = true
	state.mu.Unlock()
	delivered, err = manager.rgbManager.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{transferID},
	)
	if err != nil || !delivered.Broadcast || delivered.AwaitingACK ||
		delivered.TxID != tx.TxHash().String() || len(evidence.broadcasted) == 0 {
		t.Fatalf("accepted transfer was not broadcast: result=%+v err=%v", delivered, err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State.Status != "broadcast" || stored.State.AckStatus != "accepted" {
		t.Fatalf("ACK-before-broadcast lifecycle is incorrect: %+v", stored.State)
	}
	stored.State.Expiry = time.Now().Add(-time.Hour).Unix()
	if err := manager.rgbManager.projectionStore.SavePendingTransferState(stored); err != nil {
		t.Fatal(err)
	}
	broadcastCount := len(evidence.broadcasted)
	repeated, err := manager.rgbManager.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{transferID},
	)
	if err != nil || repeated == nil || !repeated.Broadcast ||
		repeated.TxID != tx.TxHash().String() || len(evidence.broadcasted) != broadcastCount {
		t.Fatalf("expired completed transfer was not idempotent: result=%+v err=%v", repeated, err)
	}

	nackID := "proxy-recipient-nack"
	nackInput := strings.Repeat("33", 32) + ":0"
	nack := *pending
	nack.State = pending.State
	nack.State.TransferID = nackID
	nack.State.BatchTransferIDs = []string{nackID}
	nack.State.Status = "prepared"
	nack.State.AckStatus = "awaiting"
	nack.State.InputOutPoints = []string{nackInput}
	nack.ReservationID = "proxy-nack-reservation"
	// The rejected operation must own the inputs of its own immutable witness,
	// rather than reusing a transaction which spends the preceding fixture.
	nackTx := tx.Copy()
	nackPoint, err := wire.NewOutPointFromString(nackInput)
	if err != nil {
		t.Fatal(err)
	}
	nackTx.TxIn[0].PreviousOutPoint = *nackPoint
	var nackRaw bytes.Buffer
	if err := nackTx.Serialize(&nackRaw); err != nil {
		t.Fatal(err)
	}
	nack.SignedTx = nackRaw.Bytes()
	nack.State.WitnessTxID = nackTx.TxHash().String()
	nack.State.OutputOutPoints = []string{nack.State.WitnessTxID + ":0"}
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(&nack); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.TryReserve([]string{nackInput},
		rgb11wallet.LockReasonPending, nack.ReservationID); err != nil {
		t.Fatal(err)
	}
	rejectedDecision := false
	state.mu.Lock()
	state.consignment = nil
	state.ack = &rejectedDecision
	state.mu.Unlock()
	broadcastCount = len(evidence.broadcasted)
	rejectedDelivery, err := manager.rgbManager.DeliverAndBroadcastRGB11ProxyTransfer(
		context.Background(), []string{nackID},
	)
	if err != nil || rejectedDelivery == nil || !rejectedDelivery.Rejected ||
		rejectedDelivery.Broadcast || len(evidence.broadcasted) != broadcastCount {
		t.Fatalf("NACK crossed the broadcast boundary: result=%+v err=%v", rejectedDelivery, err)
	}
	rejected, err := manager.rgbManager.projectionStore.LoadPendingTransfer(nackID)
	if err != nil || rejected.State.Status != "rejected" ||
		rejected.State.RejectReason != "recipient-rejected" {
		t.Fatalf("NACK terminal state=%+v err=%v", rejected, err)
	}
	if lock := manager.utxoLockerL1.GetLockedUtxoList()[nackInput]; lock != nil {
		t.Fatalf("NACK left input locked: %+v", lock)
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
	conflict.ReservationID = "proxy-conflict-reservation"
	if err := manager.rgbManager.projectionStore.SavePendingTransfer(&conflict); err != nil {
		t.Fatal(err)
	}
	if err := manager.utxoLockerL1.TryReserve([]string{inputOutpoint},
		rgb11wallet.LockReasonPending, conflict.ReservationID); err != nil {
		t.Fatal(err)
	}
	state.mu.Lock()
	state.postError = &rgb11ProxyError{Code: -101, Message: "Cannot change uploaded file"}
	state.consignment = []byte("different-existing-consignment")
	state.ack = nil
	state.mu.Unlock()
	broadcastCount = len(evidence.broadcasted)
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
	rejected, err = manager.rgbManager.projectionStore.LoadPendingTransfer(conflictID)
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
	seed := func(script []byte, value int64, marker byte) (string, []byte) {
		t.Helper()
		tx := wire.NewMsgTx(wire.TxVersion)
		tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: wire.MaxPrevOutIndex}, []byte{1, marker}, nil))
		tx.AddTxOut(wire.NewTxOut(value, append([]byte(nil), script...)))
		var raw bytes.Buffer
		if err := tx.Serialize(&raw); err != nil {
			t.Fatal(err)
		}
		return tx.TxHash().String() + ":0", raw.Bytes()
	}
	sourceOutpoint, sourceRaw := seed(senderScript, 10_000, 1)
	feeOutpoint, feeRaw := seed(senderScript, 100_000, 2)
	receiveOutpoint, receiveRaw := seed(recipientScript, 100_000, 3)
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
		rawTx: map[string][]byte{
			strings.SplitN(sourceOutpoint, ":", 2)[0]:  sourceRaw,
			strings.SplitN(feeOutpoint, ":", 2)[0]:     feeRaw,
			strings.SplitN(receiveOutpoint, ":", 2)[0]: receiveRaw,
		},
		txStatuses: make(map[string]*rgb11wallet.BitcoinTxStatus),
		spendingTx: make(map[string]string),
	}
	// Each fixture UTXO is backed by a matching raw transaction and an explicit
	// confirmed status. The test issues its contract against this evidence
	// instead of relying on the static vector's unrelated genesis outpoint.
	for _, outpoint := range []string{sourceOutpoint, feeOutpoint, receiveOutpoint} {
		txid := strings.SplitN(outpoint, ":", 2)[0]
		evidence.txStatuses[txid] = &rgb11wallet.BitcoinTxStatus{
			TxID: txid, Confirmed: true, BlockHeight: 1, BlockHash: "test-tip", Confirmations: 6,
		}
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
	recipientEvidence := &rgb11ProxyFaultEvidence{BitcoinEvidenceProvider: evidence}
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, recipientEvidence, 93)
	// This native fixture intentionally has no account-root identity. Exercise
	// the RGB protocol/ACK state machine at its owning manager boundary; public
	// SDK synchronization barriers are covered by the managed-gate and
	// CoreModules account-recovery tests.
	imported, err := sender.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "PROXY", Name: "Proxy transport test asset",
		Precision: 0, Amounts: []uint64{20_000},
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "rpc://" + strings.TrimPrefix(server.URL, "http://")
	receive, err := recipient.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
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
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
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
	received, err := recipient.rgbManager.ReceiveRGB11ProxyConsignment(
		context.Background(), receive.RequestID,
	)
	if err != nil {
		requestAfter, requestErr := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
		requestStatus, transferSet, objectSet := corewallet.ReceiveStatus(""), false, false
		if requestAfter != nil {
			requestStatus, transferSet, objectSet = requestAfter.Status, requestAfter.TransferID != "", requestAfter.ObjectHash != ""
		}
		proxy.mu.Lock()
		ackHistory := append([]bool(nil), proxy.ackHistory...)
		proxy.mu.Unlock()
		t.Fatalf("proxy receive failed before ACK assertions: err=%v request_error=%v status=%q transfer_set=%t object_set=%t fault_triggered=%t ack_history=%v",
			err, requestErr, requestStatus, transferSet, objectSet,
			recipientEvidence.rawTxFaultTriggered(), ackHistory)
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
	state, err := recipient.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available := rgb11AssetAmount(state.AvailableAssets, imported.AssetName); available != 0 {
		t.Fatalf("ACKed receive exposed RGB before the sender's transition was available: %d", available)
	}
	proxy.mu.Lock()
	ack := proxy.ack
	proxy.mu.Unlock()
	if ack == nil || !*ack {
		t.Fatalf("receiver did not ACK prepared consignment: %v", ack)
	}
	if !t.Run("transient_evidence_error_after_positive_ACK_preserves_receive_responsibility", func(t *testing.T) {
		requestBefore, err := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		if requestBefore.Status != corewallet.ReceiveAcknowledged || requestBefore.TransferID == "" ||
			requestBefore.ObjectHash == "" {
			t.Fatalf("positive ACK did not persist the receive identity: status=%q transfer=%q object=%q",
				requestBefore.Status, requestBefore.TransferID, requestBefore.ObjectHash)
		}
		stateBefore, err := recipient.rgbManager.projectionStore.LoadTransferState(requestBefore.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		reservationBefore, err := recipient.rgbManager.projectionStore.LoadReceiveReservation(receive.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		preparedBefore, err := recipient.rgbManager.projectionStore.LoadPreparedReceive(requestBefore.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		objectBefore, err := recipient.rgbManager.projectionStore.LoadObject(requestBefore.ObjectHash)
		if err != nil {
			t.Fatal(err)
		}
		lockBefore := recipient.utxoLockerL1.GetLockedUtxoList()[reservationBefore.OutPoint]
		if lockBefore == nil || lockBefore.Reason != rgb11wallet.LockReasonPending ||
			lockBefore.ReservationID != reservationBefore.ReservationID {
			t.Fatalf("positive ACK receive carrier was not held by its reservation: present=%t reason_pending=%t reservation_match=%t",
				lockBefore != nil, lockBefore != nil && lockBefore.Reason == rgb11wallet.LockReasonPending,
				lockBefore != nil && lockBefore.ReservationID == reservationBefore.ReservationID)
		}

		transientErr := errors.New("temporary test witness evidence failure")
		recipientEvidence.failNextRawTx(requestBefore.WitnessTxID, transientErr)
		retried, retryErr := recipient.rgbManager.ReceiveRGB11ProxyConsignment(context.Background(), receive.RequestID)
		if !recipientEvidence.rawTxFaultTriggered() {
			t.Errorf("retry did not reach the injected nonterminal witness evidence failure: %v", retryErr)
		}
		if retryErr != nil || retried == nil || !retried.AckPosted || !retried.AwaitingBroadcast ||
			retried.RequestID != requestBefore.RequestID || retried.TxID != requestBefore.WitnessTxID {
			var resultID, resultTxID string
			var ackPosted, awaiting bool
			if retried != nil {
				resultID, resultTxID = retried.RequestID, retried.TxID
				ackPosted, awaiting = retried.AckPosted, retried.AwaitingBroadcast
			}
			t.Errorf("unavailable witness evidence did not preserve the ACKed receive: request_match=%t tx_match=%t ack_posted=%t awaiting=%t err=%v",
				resultID == requestBefore.RequestID, resultTxID == requestBefore.WitnessTxID,
				ackPosted, awaiting, retryErr)
		}

		requestAfter, requestErr := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
		if requestErr != nil {
			t.Errorf("load ACKed request after transient error: %v", requestErr)
		} else if requestAfter == nil || requestAfter.Status != corewallet.ReceiveAcknowledged ||
			requestAfter.TransferID != requestBefore.TransferID ||
			requestAfter.ObjectHash != requestBefore.ObjectHash ||
			requestAfter.WitnessTxID != requestBefore.WitnessTxID {
			t.Errorf("transient error changed ACKed request: status=%q transfer_match=%t object_match=%t witness_match=%t",
				requestAfter.Status, requestAfter.TransferID == requestBefore.TransferID,
				requestAfter.ObjectHash == requestBefore.ObjectHash,
				requestAfter.WitnessTxID == requestBefore.WitnessTxID)
		}
		stateAfter, stateErr := recipient.rgbManager.projectionStore.LoadTransferState(requestBefore.TransferID)
		if stateErr != nil {
			t.Errorf("load prepared receive state after transient error: %v", stateErr)
		} else if stateAfter.Status != stateBefore.Status || stateAfter.AckStatus != stateBefore.AckStatus ||
			stateAfter.TransferID != stateBefore.TransferID || stateAfter.ConsignmentHash != stateBefore.ConsignmentHash ||
			stateAfter.WitnessTxID != stateBefore.WitnessTxID || stateAfter.RejectReason != stateBefore.RejectReason {
			t.Errorf("transient error changed prepared receive state: status=%q/%q ack=%q/%q transfer_match=%t object_match=%t witness_match=%t",
				stateBefore.Status, stateAfter.Status, stateBefore.AckStatus, stateAfter.AckStatus,
				stateAfter.TransferID == stateBefore.TransferID,
				stateAfter.ConsignmentHash == stateBefore.ConsignmentHash,
				stateAfter.WitnessTxID == stateBefore.WitnessTxID)
		}
		reservationAfter, reservationErr := recipient.rgbManager.projectionStore.LoadReceiveReservation(receive.RequestID)
		if reservationErr != nil || reservationAfter == nil || *reservationAfter != *reservationBefore {
			t.Errorf("transient error changed receive reservation: before=%+v after=%+v err=%v",
				reservationBefore, reservationAfter, reservationErr)
		}
		preparedAfter, preparedErr := recipient.rgbManager.projectionStore.LoadPreparedReceive(requestBefore.TransferID)
		if preparedErr != nil || preparedAfter != preparedBefore {
			t.Errorf("transient error changed prepared-receive mapping: before=%q after=%q err=%v",
				preparedBefore, preparedAfter, preparedErr)
		}
		objectAfter, objectErr := recipient.rgbManager.projectionStore.LoadObject(requestBefore.ObjectHash)
		if objectErr != nil || !bytes.Equal(objectAfter, objectBefore) {
			t.Errorf("transient error changed the immutable consignment object: before=%d bytes after=%d bytes err=%v",
				len(objectBefore), len(objectAfter), objectErr)
		}
		lockAfter := recipient.utxoLockerL1.GetLockedUtxoList()[reservationBefore.OutPoint]
		if lockAfter == nil || lockAfter.Reason != lockBefore.Reason ||
			lockAfter.ReservationID != lockBefore.ReservationID {
			t.Errorf("transient error released or changed the receive carrier lock: before_present=%t after_present=%t reason_match=%t reservation_match=%t",
				lockBefore != nil, lockAfter != nil,
				lockAfter != nil && lockBefore != nil && lockAfter.Reason == lockBefore.Reason,
				lockAfter != nil && lockBefore != nil && lockAfter.ReservationID == lockBefore.ReservationID)
		}
		proxy.mu.Lock()
		ackHistory := append([]bool(nil), proxy.ackHistory...)
		ackAfter := proxy.ack
		proxy.mu.Unlock()
		if ackAfter == nil || !*ackAfter {
			t.Errorf("transient error changed the proxy's positive ACK: %v", ackAfter)
		}
		if len(ackHistory) == 0 || !ackHistory[0] {
			t.Errorf("proxy ACK history lost the original positive ACK: %v", ackHistory)
		}
		for _, posted := range ackHistory {
			if !posted {
				t.Errorf("receiver attempted a negative ACK after positive ACK: %v", ackHistory)
				break
			}
		}
		if t.Failed() {
			return
		}

		// The injected lookup error is one-shot. A subsequent retry must still
		// find the same accepted responsibility and remain resumable while the
		// witness transaction is not yet visible.
		retried, err = recipient.rgbManager.ReceiveRGB11ProxyConsignment(context.Background(), receive.RequestID)
		if err != nil || retried == nil || !retried.AckPosted || !retried.AwaitingBroadcast ||
			retried.RequestID != requestBefore.RequestID || retried.TxID != requestBefore.WitnessTxID {
			var resultID, resultTxID string
			var ackPosted, awaiting bool
			if retried != nil {
				resultID, resultTxID = retried.RequestID, retried.TxID
				ackPosted, awaiting = retried.AckPosted, retried.AwaitingBroadcast
			}
			t.Fatalf("same ACKed receive was not resumable after transient failure: request_match=%t tx_match=%t ack_posted=%t awaiting=%t err=%v",
				resultID == requestBefore.RequestID, resultTxID == requestBefore.WitnessTxID,
				ackPosted, awaiting, err)
		}
		proxy.mu.Lock()
		defer proxy.mu.Unlock()
		for _, posted := range proxy.ackHistory {
			if !posted {
				t.Errorf("resuming ACKed receive wrote a negative ACK: %v", proxy.ackHistory)
				break
			}
		}
	}) {
		return
	}
	if !t.Run("temporary_witness_mismatch_after_positive_ACK_preserves_receive_responsibility", func(t *testing.T) {
		requestBefore, err := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
		if err != nil || requestBefore == nil || requestBefore.Status != corewallet.ReceiveAcknowledged {
			t.Fatalf("receive is not still positively acknowledged: request=%+v err=%v", requestBefore, err)
		}
		stateBefore, err := recipient.rgbManager.projectionStore.LoadTransferState(requestBefore.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		reservationBefore, err := recipient.rgbManager.projectionStore.LoadReceiveReservation(receive.RequestID)
		if err != nil {
			t.Fatal(err)
		}
		preparedBefore, err := recipient.rgbManager.projectionStore.LoadPreparedReceive(requestBefore.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		objectBefore, err := recipient.rgbManager.projectionStore.LoadObject(requestBefore.ObjectHash)
		if err != nil {
			t.Fatal(err)
		}
		lockBefore := recipient.utxoLockerL1.GetLockedUtxoList()[reservationBefore.OutPoint]
		if lockBefore == nil || lockBefore.Reason != rgb11wallet.LockReasonPending ||
			lockBefore.ReservationID != reservationBefore.ReservationID {
			t.Fatalf("ACKed receive carrier is not held: present=%t pending=%t reservation_match=%t",
				lockBefore != nil, lockBefore != nil && lockBefore.Reason == rgb11wallet.LockReasonPending,
				lockBefore != nil && lockBefore.ReservationID == reservationBefore.ReservationID)
		}

		// A same-key, in-mempool but mismatching raw transaction is a temporary
		// chain-evidence inconsistency. The core reports ErrWitnessMismatch,
		// which is not one of the two explicitly resumable unknown-state errors.
		evidence.mu.Lock()
		evidence.txStatuses[requestBefore.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{
			TxID: requestBefore.WitnessTxID, InMempool: true,
		}
		evidence.mu.Unlock()
		recipientEvidence.returnWrongRawTxOnce(requestBefore.WitnessTxID, feeRaw)
		_, retryErr := recipient.rgbManager.ReceiveRGB11ProxyConsignment(context.Background(), receive.RequestID)
		if !recipientEvidence.rawTxFaultTriggered() {
			t.Errorf("retry did not reach the injected mismatching witness transaction: %v", retryErr)
		}
		if !errors.Is(retryErr, coreconsignment.ErrWitnessMismatch) {
			t.Errorf("retry error = %v, want the core witness mismatch class", retryErr)
		}

		requestAfter, requestErr := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
		if requestErr != nil || requestAfter == nil || requestAfter.Status != corewallet.ReceiveAcknowledged ||
			requestAfter.TransferID != requestBefore.TransferID || requestAfter.ObjectHash != requestBefore.ObjectHash ||
			requestAfter.WitnessTxID != requestBefore.WitnessTxID {
			var status corewallet.ReceiveStatus
			if requestAfter != nil {
				status = requestAfter.Status
			}
			t.Errorf("mismatching evidence changed ACKed request: status=%q identity_match=%t err=%v",
				status, requestAfter != nil && requestAfter.TransferID == requestBefore.TransferID &&
					requestAfter.ObjectHash == requestBefore.ObjectHash && requestAfter.WitnessTxID == requestBefore.WitnessTxID,
				requestErr)
		}
		stateAfter, stateErr := recipient.rgbManager.projectionStore.LoadTransferState(requestBefore.TransferID)
		if stateErr != nil || stateAfter == nil || stateAfter.Status != stateBefore.Status || stateAfter.AckStatus != stateBefore.AckStatus ||
			stateAfter.TransferID != stateBefore.TransferID || stateAfter.ConsignmentHash != stateBefore.ConsignmentHash ||
			stateAfter.WitnessTxID != stateBefore.WitnessTxID {
			var afterStatus, afterAck string
			if stateAfter != nil {
				afterStatus, afterAck = stateAfter.Status, stateAfter.AckStatus
			}
			identityMatch := stateAfter != nil && stateAfter.TransferID == stateBefore.TransferID &&
				stateAfter.ConsignmentHash == stateBefore.ConsignmentHash && stateAfter.WitnessTxID == stateBefore.WitnessTxID
			t.Errorf("mismatching evidence changed receive state: status=%q/%q ack=%q/%q same_identity=%t err=%v",
				stateBefore.Status, afterStatus, stateBefore.AckStatus, afterAck,
				identityMatch, stateErr)
		}
		reservationAfter, reservationErr := recipient.rgbManager.projectionStore.LoadReceiveReservation(receive.RequestID)
		if reservationErr != nil || reservationAfter == nil || *reservationAfter != *reservationBefore {
			t.Errorf("mismatching evidence changed receive reservation: retained=%t err=%v",
				reservationAfter != nil && reservationBefore != nil && *reservationAfter == *reservationBefore, reservationErr)
		}
		preparedAfter, preparedErr := recipient.rgbManager.projectionStore.LoadPreparedReceive(requestBefore.TransferID)
		if preparedErr != nil || preparedAfter != preparedBefore {
			t.Errorf("mismatching evidence changed prepared-receive mapping: retained=%t err=%v",
				preparedAfter == preparedBefore, preparedErr)
		}
		objectAfter, objectErr := recipient.rgbManager.projectionStore.LoadObject(requestBefore.ObjectHash)
		if objectErr != nil || !bytes.Equal(objectAfter, objectBefore) {
			t.Errorf("mismatching evidence changed immutable consignment: retained=%t err=%v",
				bytes.Equal(objectAfter, objectBefore), objectErr)
		}
		lockAfter := recipient.utxoLockerL1.GetLockedUtxoList()[reservationBefore.OutPoint]
		if lockAfter == nil || lockAfter.Reason != lockBefore.Reason || lockAfter.ReservationID != lockBefore.ReservationID {
			t.Errorf("mismatching evidence released receive carrier lock: retained=%t",
				lockAfter != nil && lockAfter.Reason == lockBefore.Reason && lockAfter.ReservationID == lockBefore.ReservationID)
		}
		proxy.mu.Lock()
		ackHistory := append([]bool(nil), proxy.ackHistory...)
		ackAfter := proxy.ack
		proxy.mu.Unlock()
		if ackAfter == nil || !*ackAfter {
			t.Errorf("mismatching evidence changed positive proxy ACK: %v", ackAfter)
		}
		for _, posted := range ackHistory {
			if !posted {
				t.Errorf("receiver attempted a negative ACK after positive ACK: %v", ackHistory)
				break
			}
		}
		if t.Failed() {
			return
		}
		retried, err := recipient.rgbManager.ReceiveRGB11ProxyConsignment(context.Background(), receive.RequestID)
		if err != nil || retried == nil || !retried.AckPosted || !retried.AwaitingBroadcast ||
			retried.RequestID != requestBefore.RequestID || retried.TxID != requestBefore.WitnessTxID {
			t.Fatalf("same ACKed receive did not resume after evidence mismatch: result=%+v err=%v", retried, err)
		}
	}) {
		return
	}
	t.Run("expired_ACK_receive_reservation_stays_locked_after_cold_restore", func(t *testing.T) {
		reservations, err := recipient.rgbManager.projectionStore.ListReceiveReservations()
		if err != nil {
			t.Fatal(err)
		}
		matched := false
		for _, reservation := range reservations {
			if reservation == nil || reservation.RequestID != receive.RequestID {
				continue
			}
			reservation.Expiry = time.Now().Add(-time.Hour).Unix()
			if err := recipient.rgbManager.projectionStore.SaveReceiveReservation(reservation); err != nil {
				t.Fatal(err)
			}
			matched = true
		}
		if !matched {
			t.Fatal("ACKed blind invoice has no persisted receive reservation")
		}
		cold := newRGB11FlowManager(t, recipientWallet.Clone(), rpc, evidence, 94)
		importRGB11RecoveryForTest(t, cold, exportRGB11RecoveryForTest(t, recipient))
		lock := cold.utxoLockerL1.GetLockedUtxoList()[receiveOutpoint]
		if lock == nil || lock.Reason != rgb11wallet.LockReasonPending || lock.ReservationID == "" {
			t.Fatalf("expired ACKed receive reservation was released during recovery: present=%t reason_pending=%t has_reservation=%t",
				lock != nil, lock != nil && lock.Reason == rgb11wallet.LockReasonPending,
				lock != nil && lock.ReservationID != "")
		}
		if !cold.isL1SendInputProtected(receiveOutpoint) {
			t.Fatal("ordinary input selection does not protect the cold-recovered ACKed carrier")
		}
		if err := cold.utxoLockerL1.TryReserve([]string{receiveOutpoint}, "fee", "expired-ack-fee-selection"); !errors.Is(err, ErrUtxoReserved) {
			t.Fatalf("ordinary fee selection reserved the ACKed receive carrier: %v", err)
		}
	})

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
	received, err = recipient.rgbManager.ReceiveRGB11ProxyConsignment(
		context.Background(), receive.RequestID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if received.AwaitingBroadcast || received.Receipt == nil ||
		received.TxID != pending.State.WitnessTxID {
		t.Fatalf("unexpected accepted receive result: %+v", received)
	}
	acceptedRequest, err := recipient.rgbManager.engine.LoadReceive(receive.RequestID)
	if err != nil || acceptedRequest == nil || acceptedRequest.TransferID == "" {
		t.Fatalf("accepted proxy receive lost its transfer identity: present=%t err=%v",
			acceptedRequest != nil, err)
	}
	acceptedState, err := recipient.rgbManager.projectionStore.LoadTransferState(acceptedRequest.TransferID)
	if err != nil || acceptedState == nil || acceptedState.RelayDurability != "STANDARD_PROXY" ||
		acceptedState.AckStatus != "accepted" {
		var durability string
		var ackStatus string
		if acceptedState != nil {
			durability = acceptedState.RelayDurability
			ackStatus = acceptedState.AckStatus
		}
		t.Fatalf("accepted proxy transfer lost lifecycle identity: durability=%q ack=%q err=%v",
			durability, ackStatus, err)
	}
	proofs, err = recipient.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 1 || proofs[0].OutPoint != receiveOutpoint ||
		proofs[0].WitnessTxID != allocationOutpointTxID(receiveOutpoint) ||
		proofs[0].Status != "valid" || proofs[0].SealCommitment == "" {
		t.Fatalf("unexpected accepted blind allocation proof producer: %+v", proofs)
	}
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = recipient.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available := rgb11AssetAmount(state.AvailableAssets, imported.AssetName); available != 0 {
		t.Fatalf("unconfirmed transition made blind allocation available: %d", available)
	}
	if lock := recipient.utxoLockerL1.GetLockedUtxoList()[receiveOutpoint]; lock == nil ||
		lock.Reason != rgb11wallet.LockReasonPending {
		t.Fatalf("accepted blind carrier lock=%+v", lock)
	}
	evidence.mu.Lock()
	evidence.txStatuses[pending.State.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{
		TxID: pending.State.WitnessTxID, Confirmed: true, BlockHeight: 1,
		BlockHash: "test-tip", Confirmations: 1,
	}
	evidence.mu.Unlock()
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err = recipient.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if available := rgb11AssetAmount(state.AvailableAssets, imported.AssetName); available != 20_000 {
		t.Fatalf("confirmed blind transition available amount=%d, want 20000", available)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	received, err = recipient.rgbManager.ReceiveRGB11ProxyConsignment(
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
	assertRGB11FrameworkRecovery(t, sender, recipient)
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
