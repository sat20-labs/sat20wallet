package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	btcwire "github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	walletutils "github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

const (
	rgb11RegtestWalletPassword = "regtest-acceptance-only"
	rgb11RegtestTicker         = "R11E2E"
	rgb11RegtestAsset          = "brc20:f:sgas"
	rgb11RegtestNIAAmount      = uint64(300)
	rgb11RegtestStandardAmount = uint64(100)
	rgb11RegtestReturnAmount   = uint64(50)
	rgb11RegtestGasSettlements = int64(32)
)

type rgb11RegtestNIA struct {
	ContractID string
	AssetName  indexercommon.AssetName
}

type rgb11RegtestTransferEvidence struct {
	TransferID      string
	TxID            string
	ConsignmentHash string
}

type rgb11RegtestProofIdentity struct {
	Amount             uint64
	OutPoint           string
	AssetName          indexercommon.AssetName
	OperationID        string
	AssignmentType     uint32
	AssignmentIndex    uint32
	StateClass         string
	StateDataHash      string
	SealCommitment     string
	SealDisclosureHash string
	CarrierBindingHash string
	ConsignmentHash    string
	ValidationHash     string
	WitnessTxID        string
}

type rgb11RegtestAutopayBudget struct {
	StartingGasBalance *big.Rat
	FundingAmount      *big.Rat
	StartingPaidBlocks int64
	PerSettlementGas   int64
	MaximumSettlements int64
}

type rgb11RegtestProxyObject struct {
	RecipientID string
	TxID        string
	Vout        *uint32
	Raw         []byte
	Ack         *bool
}

// This loopback service implements only the ordinary RGB JSON-RPC blob and
// positive-ACK exchange. Every blob is the real public SDK consignment; the
// fixture never supplies Bitcoin evidence, balances, receipts, or chain state.
type rgb11RegtestProxy struct {
	mu      sync.Mutex
	objects map[string]*rgb11RegtestProxyObject
}

func newRGB11RegtestProxy() *rgb11RegtestProxy {
	return &rgb11RegtestProxy{objects: make(map[string]*rgb11RegtestProxyObject)}
}

func (p *rgb11RegtestProxy) serveHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		p.serveConsignmentPost(w, r)
		return
	}
	var request struct {
		Method string          `json:"method"`
		ID     string          `json:"id"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request); err != nil {
		http.Error(w, "invalid JSON-RPC request", http.StatusBadRequest)
		return
	}
	if request.ID == "" {
		http.Error(w, "missing JSON-RPC id", http.StatusBadRequest)
		return
	}
	switch request.Method {
	case "consignment.get":
		var params struct {
			RecipientID string `json:"recipient_id"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.RecipientID == "" {
			http.Error(w, "invalid recipient id", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		object := p.objects[params.RecipientID]
		if object == nil {
			p.mu.Unlock()
			_, _ = io.WriteString(w, `{"result":null}`)
			return
		}
		response := struct {
			Consignment string  `json:"consignment"`
			TxID        string  `json:"txid"`
			Vout        *uint32 `json:"vout,omitempty"`
		}{base64.StdEncoding.EncodeToString(object.Raw), object.TxID, object.Vout}
		p.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"result": response})
	case "ack.post":
		var params struct {
			RecipientID string `json:"recipient_id"`
			Ack         bool   `json:"ack"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.RecipientID == "" {
			http.Error(w, "invalid ACK", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		object := p.objects[params.RecipientID]
		if object == nil {
			p.mu.Unlock()
			http.Error(w, "consignment not found", http.StatusNotFound)
			return
		}
		if object.Ack != nil && *object.Ack != params.Ack {
			p.mu.Unlock()
			_, _ = io.WriteString(w, `{"error":{"code":-100,"message":"Cannot change ACK"}}`)
			return
		}
		ack := params.Ack
		object.Ack = &ack
		p.mu.Unlock()
		_, _ = io.WriteString(w, `{"result":true}`)
	case "ack.get":
		var params struct {
			RecipientID string `json:"recipient_id"`
		}
		if json.Unmarshal(request.Params, &params) != nil || params.RecipientID == "" {
			http.Error(w, "invalid recipient id", http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		object := p.objects[params.RecipientID]
		var ack *bool
		if object != nil && object.Ack != nil {
			value := *object.Ack
			ack = &value
		}
		p.mu.Unlock()
		if ack == nil {
			_, _ = io.WriteString(w, `{"result":null}`)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": *ack})
	default:
		http.Error(w, "unsupported JSON-RPC method", http.StatusBadRequest)
	}
}

func (p *rgb11RegtestProxy) serveConsignmentPost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "invalid consignment form", http.StatusBadRequest)
		return
	}
	if r.FormValue("method") != "consignment.post" {
		http.Error(w, "unsupported method", http.StatusBadRequest)
		return
	}
	var params struct {
		RecipientID string  `json:"recipient_id"`
		TxID        string  `json:"txid"`
		Vout        *uint32 `json:"vout"`
	}
	if json.Unmarshal([]byte(r.FormValue("params")), &params) != nil ||
		params.RecipientID == "" || params.TxID == "" {
		http.Error(w, "invalid consignment metadata", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing consignment object", http.StatusBadRequest)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 4<<20+1))
	if err != nil || len(raw) == 0 || len(raw) > 4<<20 {
		http.Error(w, "invalid consignment object", http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	object := p.objects[params.RecipientID]
	if object == nil {
		object = &rgb11RegtestProxyObject{RecipientID: params.RecipientID}
		p.objects[params.RecipientID] = object
	}
	object.TxID = params.TxID
	object.Vout = params.Vout
	object.Raw = append([]byte(nil), raw...)
	p.mu.Unlock()
	_, _ = io.WriteString(w, `{"result":true}`)
}

func (p *rgb11RegtestProxy) object(recipientID string) (rgb11RegtestProxyObject, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	object := p.objects[recipientID]
	if object == nil {
		return rgb11RegtestProxyObject{}, false
	}
	result := *object
	result.Raw = append([]byte(nil), object.Raw...)
	if object.Vout != nil {
		vout := *object.Vout
		result.Vout = &vout
	}
	if object.Ack != nil {
		ack := *object.Ack
		result.Ack = &ack
	}
	return result, true
}

func TestSDKPWARealRegtestRGB11StandardRecoveryDirectReturn(t *testing.T) {
	network := prepareSDKPWARegtestNodes(t)
	ordinaryA, ordinaryB, mineL1 := prepareSDKPWARegtestAssets(t, true)
	attachRGB11RegtestWallet(t, ordinaryA, network)
	attachRGB11RegtestWallet(t, ordinaryB, network)

	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.RegressionNetParams)
	require.True(t, defaults.Enabled)
	require.Equal(t, uint64(8888), defaults.AutopayDeployNonce)
	require.Equal(t, defaults.AutopayDeployer, ordinaryA.GetWallet().GetAddress(),
		"ordinary-1 is the fixed regtest AUTOPAY deployer")
	triggerFee, err := contractcommon.GasFeeAtHeight(contractcommon.TriggerBaseGas,
		rgb11RegtestCoreHeight(t, network))
	require.NoError(t, err)
	resultFee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas,
		rgb11RegtestCoreHeight(t, network))
	require.NoError(t, err)
	gasQuote := int64(rgb11RegtestGasSettlements) * (triggerFee + resultFee)
	require.Positive(t, gasQuote)
	invokeFee, err := contractcommon.GasFeeAtHeight(contractcommon.InvokeBaseGas,
		rgb11RegtestCoreHeight(t, network))
	require.NoError(t, err)
	accountResultFee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas,
		rgb11RegtestCoreHeight(t, network))
	require.NoError(t, err)
	sdkOperationGas := invokeFee + accountResultFee
	require.Positive(t, sdkOperationGas)
	deployFee, err := contractcommon.GasFeeAtHeight(contractcommon.DeployBaseGas,
		rgb11RegtestCoreHeight(t, network))
	require.NoError(t, err)
	deployOperationGas := deployFee + accountResultFee
	require.Positive(t, deployOperationGas)

	channel := ensureRGB11RegtestChannel(t, ordinaryA, network, mineL1)
	aCost := rgb11RegtestPaidStorageCost(t, ordinaryA)
	bCost := rgb11RegtestPaidStorageCost(t, ordinaryB)
	ensureRGB11RegtestAccountManagement(t, ordinaryA)
	ensureRGB11RegtestAccountManagement(t, ordinaryB)
	aAuth, aNeedsPayment := reusableRGB11RegtestPaidStorage(t, ordinaryA)
	bAuth, bNeedsPayment := reusableRGB11RegtestPaidStorage(t, ordinaryB)
	bTarget := bCost + sdkOperationGas
	bBalanceBeforeFunding := rgb11RegtestSgasBalance(t, ordinaryB)
	bFundingShortfall := int64(0)
	if bNeedsPayment && bBalanceBeforeFunding < bTarget {
		bFundingShortfall = bTarget - bBalanceBeforeFunding
	}
	// Budget only unfinished real operations. An existing operating reserve is
	// reused at its actual remaining size, not refilled to the first-run quote.
	stateJSON, err := ordinaryA.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: defaults.AutopayContract,
	})
	require.NoError(t, err)
	var existence struct {
		Details struct {
			Exists *bool `json:"exists"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal([]byte(stateJSON), &existence))
	needsDeploy := existence.Details.Exists != nil && !*existence.Details.Exists
	gasFunding := gasQuote
	if !needsDeploy {
		state, decodeErr := dkvsindexer.DecodeAutopayContractState([]byte(stateJSON), defaults.AutopayContract)
		require.NoError(t, decodeErr)
		require.NotNil(t, state)
		currentGas := new(big.Rat)
		if state.GasBalance != "" {
			_, ok := currentGas.SetString(state.GasBalance)
			require.True(t, ok)
		}
		require.True(t, currentGas.IsInt() && currentGas.Num().IsInt64())
		if currentGas.Sign() > 0 {
			gasQuote = currentGas.Num().Int64()
			require.GreaterOrEqual(t, gasQuote, triggerFee+resultFee,
				"existing AUTOPAY reserve cannot fund even one settlement; stop with real remaining-budget evidence")
			gasFunding = 0
		}
	}
	sgasTarget := bFundingShortfall
	if aNeedsPayment {
		sgasTarget += aCost + sdkOperationGas
	}
	if needsDeploy {
		sgasTarget += deployOperationGas
	}
	if gasFunding > 0 {
		sgasTarget += gasFunding + sdkOperationGas
	}
	t.Logf("remaining real budget: deploy=%t paid_A=%t paid_B=%t B_shortfall=%d gas_funding=%d operating_reserve=%d A_SGAS_target=%d",
		needsDeploy, aNeedsPayment, bNeedsPayment, bFundingShortfall, gasFunding, gasQuote, sgasTarget)
	if sgasTarget > 0 {
		ensureRGB11RegtestSgas(t, ordinaryA, channel, network, mineL1, sgasTarget)
	}
	autopayBudget := ensureRGB11RegtestAutopay(t, ordinaryA, network, defaults,
		gasQuote, triggerFee+resultFee, !needsDeploy && gasFunding == 0)
	if aNeedsPayment {
		aAuth, err = ordinaryA.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		require.NoError(t, err)
	}
	if bNeedsPayment {
		if bFundingShortfall > 0 {
			fundRGB11RegtestAccountFromSgas(t, ordinaryA, ordinaryB, network, bFundingShortfall)
		}
		require.GreaterOrEqual(t, rgb11RegtestSgasBalance(t, ordinaryB), bTarget,
			"B must have its paid-storage principal and the current invoke/result gas before confirming paid storage")
		bAuth, err = ordinaryB.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		require.NoError(t, err)
	}
	require.NotNil(t, aAuth)
	require.NotNil(t, bAuth)
	assertRGB11RegtestAutopayReady(t, ordinaryA)
	assertRGB11RegtestAutopayReady(t, ordinaryB)

	aPackage, _, _ := ensureRGB11RegtestRecoveryPackage(t, ordinaryA, aAuth)
	bPackage, bUserSharePath, bPublicLocator := ensureRGB11RegtestRecoveryPackage(t, ordinaryB, bAuth)
	aRootID, err := ordinaryA.RootAccountID()
	require.NoError(t, err)
	bRootID, err := ordinaryB.RootAccountID()
	require.NoError(t, err)
	require.Equal(t, aRootID, aPackage.Envelope.Locator.AccountID)
	require.Equal(t, bRootID, bPackage.Envelope.Locator.AccountID)
	location, err := ordinaryB.AccountIndexerLocation()
	require.NoError(t, err)
	// A prior run may have completed on the independently restored B device.
	// Converge the original devices from real DKVS before choosing a resume path.
	for _, manager := range []*wallet.Manager{ordinaryA, ordinaryB} {
		require.NoError(t, manager.SyncAccountManagementState(context.Background()))
		// Start also runs background synchronization. A rejected stale local
		// candidate is expected to be rebuilt; all other errors remain fatal.
		refreshCtx, cancelRefresh := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancelRefresh()
		for {
			_, refreshErr := manager.RefreshRGB11State(refreshCtx)
			if refreshErr == nil {
				break
			}
			require.Equal(t, "apply RGB11 refresh candidate: publish RGB11 candidate projection: account management snapshot changed",
				refreshErr.Error(), "unexpected RGB refresh failure during fixture convergence")
			require.NoError(t, refreshCtx.Err(), "local RGB snapshot did not converge before preparation")
			time.Sleep(100 * time.Millisecond)
		}
		cancelRefresh()
	}
	asset, canonicalContractFile := ensureRGB11RegtestNIA(t, ordinaryA, ordinaryB)
	// The only unfinished operation this case can resume is its original
	// standard pre-delivery send. All other incomplete work remains fail-closed.
	var resumePrepared *wallet.RGB11PreparedTransferPackage
	var resumeReceive *corewallet.ReceiveRequest
	aState, err := ordinaryA.GetRGB11State()
	require.NoError(t, err)
	for _, transfer := range aState.Transfers {
		if transfer == nil || transfer.Asset.Name != asset.AssetName || transfer.Status == "settled" {
			continue
		}
		require.Nil(t, resumePrepared, "more than one unfinished RGB transfer")
		require.Equal(t, "send", transfer.Direction)
		require.Equal(t, "prepared", transfer.Status, "only the retained pre-delivery transfer may resume")
		require.Equal(t, wallet.RGB11ProxyTransport, transfer.TransportMode)
		require.False(t, transfer.AddressMode)
		require.Equal(t, "awaiting", transfer.AckStatus)
		require.NotNil(t, transfer.Asset.Amount.Value)
		require.Equal(t, uint64(100), transfer.Asset.Amount.Value.Uint64())
		resumePrepared, err = ordinaryA.ResumeRGB11PreparedTransfer(transfer.TransferID)
		require.NoError(t, err)
		require.Equal(t, transfer, resumePrepared.State)
		require.Equal(t, transfer.WitnessTxID, resumePrepared.TxID)
		require.Greater(t, transfer.Expiry, time.Now().Unix(), "original invoice expired; never rewrite its expiry or prepare a replacement")
	}
	for _, manager := range []*wallet.Manager{ordinaryA, ordinaryB} {
		state, stateErr := manager.GetRGB11State()
		require.NoError(t, stateErr)
		for _, transfer := range state.Transfers {
			if transfer == nil || transfer.Asset.Name != asset.AssetName || transfer.Status == "settled" {
				continue
			}
			require.True(t, manager == ordinaryA && resumePrepared != nil && transfer.TransferID == resumePrepared.State.TransferID,
				"unrelated unfinished transfer must keep its original operation")
		}
		for _, reservation := range state.Reservations {
			if reservation == nil || reservation.ContractID != asset.ContractID {
				continue
			}
			require.NotNil(t, resumePrepared, "unfinished invoice cannot be replaced")
			require.True(t, manager == ordinaryB, "unexpected sender reservation")
			require.Nil(t, resumeReceive, "ambiguous original receive request")
			require.Equal(t, resumePrepared.State.Invoice, reservation.Invoice)
			resumeReceive, err = ordinaryB.GetRGB11ReceiveRequest(reservation.RequestID)
			require.NoError(t, err)
			require.NotNil(t, resumeReceive)
			require.Equal(t, reservation.RequestID, resumeReceive.RequestID)
			require.Equal(t, resumePrepared.State.Invoice, resumeReceive.Invoice)
			require.Equal(t, corewallet.ReceivePrepared, resumeReceive.Status)
			require.Empty(t, resumeReceive.ObjectHash)
			require.Empty(t, resumeReceive.TransferID)
		}
	}
	if resumePrepared != nil {
		require.NotNil(t, resumeReceive, "original B receive request is required")
	}

	aBalance := rgb11RegtestBalance(t, ordinaryA, asset.AssetName)
	bBalance := rgb11RegtestBalance(t, ordinaryB, asset.AssetName)
	require.True(t, (aBalance == 300 && bBalance == 0) ||
		(aBalance == 200 && bBalance == 100) || (aBalance == 250 && bBalance == 50),
		"resume only from the unstarted, post-standard, or completed real state; got A=%d B=%d", aBalance, bBalance)

	var standard, standardSender, direct rgb11RegtestTransferEvidence
	var standardHash string
	var standardRequestID string
	var directValidationHash string
	if aBalance == 300 && bBalance == 0 {
		proxy := newRGB11RegtestProxy()
		var proxyServer *httptest.Server
		var invoice *corewallet.ReceiveRequest
		var prepared *wallet.RGB11PreparedTransferPackage
		if resumePrepared != nil {
			invoice, prepared = resumeReceive, resumePrepared
			parsedInvoice, parseErr := invoicing.Parse(invoice.Invoice)
			require.NoError(t, parseErr)
			require.Len(t, parsedInvoice.Transports, 1)
			originalTransport := parsedInvoice.Transports[0]
			require.Equal(t, invoicing.TransportJSONRPC, originalTransport.Kind)
			require.False(t, originalTransport.TLS)
			originalURL, parseErr := url.Parse("http://" + originalTransport.Host)
			require.NoError(t, parseErr)
			ip := net.ParseIP(originalURL.Hostname())
			require.True(t, ip != nil && ip.IsLoopback() && originalURL.Port() != "" && originalURL.User == nil)
			require.Equal(t, "/0.2/json-rpc", originalURL.Path)
			require.Empty(t, originalURL.RawQuery)
			require.Empty(t, originalURL.Fragment)
			listener, listenErr := net.Listen("tcp", originalURL.Host)
			require.NoError(t, listenErr, "restore the original loopback proxy port; never change invoice endpoints")
			proxyServer = httptest.NewUnstartedServer(http.HandlerFunc(proxy.serveHTTP))
			require.NoError(t, proxyServer.Listener.Close())
			proxyServer.Listener = listener
			proxyServer.Start()
			require.Equal(t, originalTransport.String(), "rpc://"+strings.TrimPrefix(proxyServer.URL, "http://")+"/0.2/json-rpc")
			t.Logf("resume original standard transfer=%s txid=%s request=%s proxy=%s", prepared.State.TransferID, prepared.TxID, invoice.RequestID, originalURL.Host)
		} else {
			proxyServer = httptest.NewServer(http.HandlerFunc(proxy.serveHTTP))
			endpoint := "rpc://" + strings.TrimPrefix(proxyServer.URL, "http://") + "/0.2/json-rpc"
			invoice, err = ordinaryB.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
				Mode: "witness", TransportMode: wallet.RGB11ProxyTransport,
				TransportEndpoints: []string{endpoint}, ContractID: asset.ContractID,
				AmountRaw: "100", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
			})
			require.NoError(t, err)
			created, prepareErr := ordinaryA.PrepareRGB11Transfer(context.Background(), wallet.RGB11SendRequest{
				Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
			})
			require.NoError(t, prepareErr)
			require.NotNil(t, created.State)
			prepared, err = ordinaryA.ResumeRGB11PreparedTransfer(created.State.TransferID)
			require.NoError(t, err)
			require.Equal(t, created.TxID, prepared.TxID)
		}
		defer proxyServer.Close()
		require.NotEmpty(t, invoice.RequestID)
		standardRequestID = invoice.RequestID
		require.NotNil(t, prepared.State)
		require.Equal(t, invoice.Invoice, prepared.State.Invoice)
		require.Equal(t, asset.AssetName, prepared.State.Asset.Name)
		require.NotEmpty(t, prepared.State.InputOutPoints)
		for _, outpoint := range prepared.State.InputOutPoints {
			require.True(t, ordinaryA.IsLocked(ordinaryA.GetWallet().GetAddress(), outpoint),
				"prepared RGB input must remain reserved before ACK and broadcast")
		}
		_, rawConsignment, fileHash := rgb11RegtestPreparedMaterial(t, ordinaryA, prepared.State,
			prepared.RecipientConsignment, prepared.RecipientConsignmentBase64, prepared.TxID)
		standardHash = fileHash
		standardSender = rgb11RegtestTransferEvidence{prepared.State.TransferID, prepared.TxID, prepared.State.ConsignmentHash}
		delivery, deliveryErr := ordinaryA.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		require.NoError(t, deliveryErr)
		require.True(t, delivery.AwaitingACK)
		require.False(t, delivery.Broadcast, "the sender must wait for the receiver's positive ACK")
		posted, found := proxy.object(prepared.State.RecipientID)
		require.True(t, found)
		require.Equal(t, prepared.TxID, posted.TxID)
		require.Equal(t, rawConsignment, posted.Raw, "proxy stores the exact SDK-produced consignment")
		received, receiveErr := ordinaryB.ReceiveRGB11ProxyConsignment(
			context.Background(), invoice.RequestID)
		require.NoError(t, receiveErr)
		require.True(t, received.AckPosted)
		require.True(t, received.AwaitingBroadcast)
		require.NotNil(t, received.Receipt)
		require.Equal(t, "prepared", received.Receipt.Status)
		require.Equal(t, asset.ContractID, received.Receipt.ContractID)
		require.Equal(t, prepared.State.TransferID, received.Receipt.TransferID)
		require.Equal(t, standardHash, received.Receipt.ConsignmentHash)
		posted, found = proxy.object(prepared.State.RecipientID)
		require.True(t, found && posted.Ack != nil && *posted.Ack)
		delivery, deliveryErr = ordinaryA.DeliverAndBroadcastRGB11ProxyTransfer(
			context.Background(), []string{prepared.State.TransferID})
		require.NoError(t, deliveryErr)
		require.True(t, delivery.Broadcast)
		require.Equal(t, prepared.TxID, delivery.TxID)
		confirmRGB11RegtestL1Tx(t, ordinaryA, prepared.TxID,
			ordinaryA.GetWallet().GetAddressByIndex(1), mineL1)
		_, err = ordinaryA.RefreshRGB11State(context.Background())
		require.NoError(t, err)
		received, receiveErr = ordinaryB.ReceiveRGB11ProxyConsignment(
			context.Background(), invoice.RequestID)
		require.NoError(t, receiveErr)
		require.NotNil(t, received)
		require.Equal(t, invoice.RequestID, received.RequestID)
		require.Equal(t, prepared.TxID, received.TxID)
		require.True(t, received.AckPosted)
		t.Logf("original proxy receive retry: request=%s txid=%s ack=%t awaiting_broadcast=%t receipt_present=%t",
			received.RequestID, received.TxID, received.AckPosted, received.AwaitingBroadcast, received.Receipt != nil)
		// ACCEPTED retries only return ACK metadata. The common check below
		// validates the actual persisted receipt after this original tx settles.
		require.NoError(t, ordinaryA.SyncAccountManagementState(context.Background()))
		standard = rgb11RegtestTransferEvidence{prepared.State.TransferID, prepared.TxID, standardHash}
	} else {
		standardSender = rgb11RegtestFindSettledTransfer(t, ordinaryA, asset, "send", false)
		standard, _ = rgb11RegtestProxyReceiveEvidence(t, ordinaryA, standardSender)
		rgb11RegtestAssertSettledTransfer(t, ordinaryA, asset, standardSender, "send", false)
		rgb11RegtestAssertSettledTransfer(t, ordinaryB, asset, standard, "receive", false)
		assertRGB11RegtestCoreConfirmation(t, standard.TxID)
		if aBalance == 250 {
			direct = rgb11RegtestFindSettledTransfer(t, ordinaryA, asset, "receive", true)
			rgb11RegtestAssertSettledTransfer(t, ordinaryA, asset, direct, "receive", true)
			rgb11RegtestAssertSettledTransfer(t, ordinaryB, asset, direct, "send", true)
			assertRGB11RegtestCoreConfirmation(t, direct.TxID)
		}
	}

	// Derive the expected wire file only from A's original persisted recipient
	// Armor; never use B's receipt/object to choose its own expected hash.
	expectedStandard, expectedStandardFile := rgb11RegtestProxyReceiveEvidence(t, ordinaryA, standardSender)
	require.Equal(t, expectedStandard, standard)
	rgb11RegtestAssertAcceptedProxyMaterial(t, ordinaryA, ordinaryB, asset, standard,
		expectedStandardFile, standardRequestID)
	rgb11RegtestAssertSettledTransfer(t, ordinaryA, asset, standardSender, "send", false)
	if aBalance == 300 {
		require.Equal(t, uint64(200), rgb11RegtestBalance(t, ordinaryA, asset.AssetName))
		require.Equal(t, uint64(100), rgb11RegtestBalance(t, ordinaryB, asset.AssetName))
	}

	// Close the original B writer before opening the fixed identity on an empty,
	// independent local database. Its recoverable account state must come from
	// the live DKVS package and the confirmed managed-data record.
	ordinaryB.Stop()
	balanceBeforeRestore := rgb11RegtestBalance(t, ordinaryB, asset.AssetName)
	canonicalRecoveryPackageBefore := rgb11RegtestCanonicalRecoveryPackage(t, ordinaryB)
	proofsBeforeRestore := snapshotRGB11RegtestOwnedProofs(t, ordinaryB, asset, canonicalRecoveryPackageBefore)
	require.NotEmpty(t, proofsBeforeRestore, "B must hold at least one locked, validated allocation")
	recoveredB := restoreRGB11RegtestAccount(t, network, bPackage.Envelope.Locator, bUserSharePath,
		location, defaults.AutopayContract, bPublicLocator)
	_, err = recoveredB.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	require.NoError(t, recoveredB.SyncAccountManagementState(context.Background()))
	require.Equal(t, bRootID, mustRGB11RegtestRootID(t, recoveredB))
	require.Equal(t, balanceBeforeRestore, rgb11RegtestBalance(t, recoveredB, asset.AssetName),
		"fresh DB recovery must restore the exact confirmed RGB allocation amount")
	recoveredB.Stop()
	canonicalRecoveryPackageAfter := rgb11RegtestCanonicalRecoveryPackage(t, recoveredB)
	proofsAfterRestore := snapshotRGB11RegtestOwnedProofs(t, recoveredB, asset, canonicalRecoveryPackageAfter)
	require.Equal(t, proofsBeforeRestore, proofsAfterRestore,
		"cold recovery must preserve each owned output's immutable allocation identity")
	require.Equal(t, canonicalRecoveryPackageBefore, canonicalRecoveryPackageAfter,
		"cold recovery must preserve the complete canonical RGB recovery closure at the same pre-return checkpoint")
	rgb11RegtestAssertSettledTransfer(t, recoveredB, asset, standard, "receive", false)
	if direct.TransferID != "" {
		rgb11RegtestAssertSettledTransfer(t, recoveredB, asset, direct, "send", true)
	}

	// Re-open the original device one writer at a time and verify it converges
	// from the same DKVS state before the recovered device sends the return.
	attachRGB11RegtestWallet(t, ordinaryB, network)
	_, err = ordinaryB.UnlockWallet(rgb11RegtestWalletPassword)
	require.NoError(t, err)
	require.NoError(t, ordinaryB.SyncAccountManagementState(context.Background()))
	_, err = ordinaryB.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	require.Equal(t, balanceBeforeRestore, rgb11RegtestBalance(t, ordinaryB, asset.AssetName))
	ordinaryB.Stop()
	canonicalRecoveryPackageOriginal := rgb11RegtestCanonicalRecoveryPackage(t, ordinaryB)
	require.Equal(t, proofsBeforeRestore, snapshotRGB11RegtestOwnedProofs(t, ordinaryB, asset, canonicalRecoveryPackageOriginal))
	require.Equal(t, canonicalRecoveryPackageBefore, canonicalRecoveryPackageOriginal,
		"the original device must converge to the restored state before any return transfer")
	attachRGB11RegtestWallet(t, recoveredB, network)
	_, err = recoveredB.UnlockWallet(rgb11RegtestWalletPassword)
	require.NoError(t, err)
	require.NoError(t, recoveredB.SyncAccountManagementState(context.Background()))
	_, err = recoveredB.RefreshRGB11State(context.Background())
	require.NoError(t, err)

	if balanceBeforeRestore == 100 {
		direct, directValidationHash = sendRGB11RegtestDirectReturn(t, recoveredB,
			ordinaryA, network, mineL1, asset)
	} else {
		require.Equal(t, uint64(50), balanceBeforeRestore,
			"only an already-confirmed completed direct return may resume with B=50")
	}
	_, err = ordinaryA.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	_, err = recoveredB.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	require.NoError(t, ordinaryA.SyncAccountManagementState(context.Background()))
	require.NoError(t, recoveredB.SyncAccountManagementState(context.Background()))
	rgb11RegtestAssertSettledTransfer(t, ordinaryA, asset, direct, "receive", true)
	rgb11RegtestAssertSettledTransfer(t, recoveredB, asset, direct, "send", true)
	if directValidationHash != "" {
		rgb11RegtestAssertReceiptProof(t, ordinaryA, asset,
			direct, directValidationHash, 50)
	}

	require.Equal(t, uint64(250), rgb11RegtestBalance(t, ordinaryA, asset.AssetName))
	require.Equal(t, uint64(50), rgb11RegtestBalance(t, recoveredB, asset.AssetName))
	assertRGB11RegtestExportRoundTrip(t, ordinaryA, recoveredB, asset.ContractID, canonicalContractFile)
	assertRGB11RegtestAutopayBudget(t, ordinaryA, defaults.AutopayContract, autopayBudget)
	t.Logf("RGB11 real regtest closed: contract=%s A=250 B=50 standard_tx=%s direct_return_tx=%s",
		asset.ContractID, standard.TxID, direct.TxID)
}

func attachRGB11RegtestWallet(t *testing.T, manager *wallet.Manager, network *realSatoshiNet) {
	t.Helper()
	client := &wallet.NetClient{Client: &http.Client{Timeout: 10 * time.Second}}
	coreKey, err := walletutils.ParsePubkey(network.Core.nodePubKey)
	require.NoError(t, err)
	bootstrapKey, err := walletutils.ParsePubkey(network.Bootstrap.nodePubKey)
	require.NoError(t, err)
	coreClient := wallet.NewNodeClient("http", network.Core.stpAddr, "regtest", client)
	manager.SetServerNode(wallet.NewNode(coreClient, network.Core.stpAddr,
		wallet.SERVER_NODE, coreKey, coreKey))
	wantURL := coreClient.GetUrl(wwire.STP_SPLICING_IN_REQ)
	for _, channel := range manager.GetAllChannels() {
		if channel == nil || !bytes.Equal(channel.PeerNodeId, coreKey.SerializeCompressed()) {
			continue
		}
		channel.Mutex.RLock()
		beforeRPC := channel.PeerRPC
		channel.Mutex.RUnlock()
		beforeClient, ok := beforeRPC.(*wallet.NodeClient)
		require.True(t, ok, "existing Core channel must retain a real SDK NodeClient")
		require.NotNil(t, beforeClient)
		beforeURL := beforeClient.GetUrl(wwire.STP_SPLICING_IN_REQ)
		t.Logf("channel peer route before: scheme=%s host=%s path=%s", beforeURL.Scheme, beforeURL.Host, beforeURL.Path)
		channel.Mutex.Lock()
		channel.PeerRPC = manager.GetPeerNodeClient(&channel.ChannelInDB)
		afterRPC := channel.PeerRPC
		channel.Mutex.Unlock()
		afterClient, ok := afterRPC.(*wallet.NodeClient)
		require.True(t, ok)
		require.NotNil(t, afterClient)
		afterURL := afterClient.GetUrl(wwire.STP_SPLICING_IN_REQ)
		t.Logf("channel peer route after: scheme=%s host=%s path=%s", afterURL.Scheme, afterURL.Host, afterURL.Path)
		require.Equal(t, wantURL, afterURL, "existing Core channel must use the exact local regtest RPC before Start")
	}
	bootstrap := manager.GetBootstrapNodes()
	require.NotEmpty(t, bootstrap)
	bootstrap[0] = wallet.NewNode(wallet.NewNodeClient("http", network.Bootstrap.stpAddr,
		"regtest", client), network.Bootstrap.stpAddr, wallet.BOOTSTRAP_NODE,
		bootstrapKey, bootstrapKey)
	manager.Start()
	require.Eventually(t, manager.IsReady, rpcReadyTimeout, 200*time.Millisecond,
		"ordinary wallet manager must initialize against the real regtest pair")
	require.True(t, manager.CheckSuperNodeStatus(), "wallet must be connected to the real Core node")
}

func ensureRGB11RegtestAccountManagement(t *testing.T, manager *wallet.Manager) {
	t.Helper()
	if !manager.GetAccountManagementStatus().Active {
		require.NoError(t, manager.InitializeAccountManagement(rgb11RegtestWalletPassword))
	}
	require.NoError(t, manager.BindAccountToCurrentCoreNode())
	rootID, err := manager.RootAccountID()
	require.NoError(t, err)
	status := manager.GetAccountManagementStatus()
	require.True(t, status.Active)
	require.Equal(t, rootID, status.AccountID,
		"account management identity must match the stable root account key")
}

func reusableRGB11RegtestPaidStorage(t *testing.T, manager *wallet.Manager) (*wallet.AccountStorageAuthorization, bool) {
	t.Helper()
	// A fresh network has no AUTOPAY state to decode yet. Only an explicit
	// successful info response may establish absence; fee-proof errors from
	// an existing contract must remain failures.
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.RegressionNetParams)
	raw, err := manager.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: defaults.AutopayContract,
	})
	require.NoError(t, err)
	var info struct {
		Details struct {
			Exists *bool `json:"exists"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &info))
	if info.Details.Exists != nil && !*info.Details.Exists {
		require.NotEqual(t, wallet.AccountStoragePaid, manager.GetAccountManagementStatus().StorageMode,
			"an existing paid profile must not lose its AUTOPAY contract")
		return nil, true
	}
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
	require.NoError(t, err, "an existing AUTOPAY contract must have valid fee state")
	require.NotNil(t, state)
	authorization, err := manager.ReusePaidAccountStorage(100)
	if err == nil {
		return authorization, false
	}
	require.ErrorIs(t, err, wallet.ErrAccountPaidStorageNotReusable)
	require.NotEqual(t, wallet.AccountStoragePaid, manager.GetAccountManagementStatus().StorageMode,
		"never recharge an account that already records paid storage but cannot currently reuse it")
	return nil, true
}

func ensureRGB11RegtestPaidStorage(t *testing.T, manager *wallet.Manager) *wallet.AccountStorageAuthorization {
	t.Helper()
	if authorization, needsPayment := reusableRGB11RegtestPaidStorage(t, manager); !needsPayment {
		return authorization
	}
	authorization, err := manager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
	require.NoError(t, err)
	require.Equal(t, wallet.AccountStoragePaid, authorization.Mode)
	return authorization
}

func rgb11RegtestPaidStorageCost(t *testing.T, manager *wallet.Manager) int64 {
	t.Helper()
	options, err := manager.GetAccountStorageOptions()
	require.NoError(t, err)
	for _, option := range options {
		if option.Mode != wallet.AccountStoragePaid {
			continue
		}
		amount, ok := new(big.Rat).SetString(option.EstimatedCost)
		require.True(t, ok && amount.IsInt(), "paid-storage quote must be an integer SGAS amount")
		require.True(t, amount.Num().IsInt64())
		return amount.Num().Int64()
	}
	require.FailNow(t, "wallet did not return a paid-storage SGAS quote")
	return 0
}

func ensureRGB11RegtestAutopay(t *testing.T, manager *wallet.Manager, network *realSatoshiNet,
	defaults dkvsindexer.NetworkDefaults, gasQuote, perSettlementGas int64, reuseOperatingReserve bool) rgb11RegtestAutopayBudget {
	t.Helper()
	infoJSON, err := manager.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: defaults.AutopayContract,
	})
	require.NoError(t, err, "AUTOPAY info lookup must succeed before deciding whether to deploy")
	var info struct {
		Details struct {
			Exists *bool `json:"exists"`
		} `json:"details"`
	}
	require.NoError(t, json.Unmarshal([]byte(infoJSON), &info))
	if info.Details.Exists != nil && !*info.Details.Exists {
		content, contentErr := defaults.AutopayContent()
		require.NoError(t, contentErr)
		deployed, deployErr := manager.DeployUnifiedContract(&wallet.ContractDeployRequest{
			ContractType: wallet.ContractTypeTemplate, SubType: contractcommon.TemplateAutopay,
			ContractContent: base64.StdEncoding.EncodeToString(content), ContentEncoding: "base64",
			DeployNonce: defaults.AutopayDeployNonce,
		})
		require.NoError(t, deployErr)
		require.Equal(t, defaults.AutopayContract, deployed.ContractAddress)
		waitRGB11RegtestL2Transaction(t, manager, network, deployed.TxID)
	}
	raw, err := manager.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: defaults.AutopayContract,
	})
	require.NoError(t, err)
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, contractcommon.TemplateAutopay, contractcommon.NormalizeTemplateName(state.TemplateName))
	startingPaidBlocks := state.PaidBlocks
	currentGas := new(big.Rat)
	if state.GasBalance != "" {
		parsed, ok := currentGas.SetString(state.GasBalance)
		require.True(t, ok)
		currentGas = parsed
	}
	if reuseOperatingReserve {
		require.True(t, currentGas.IsInt() && currentGas.Num().IsInt64())
		gasQuote = currentGas.Num().Int64()
		require.GreaterOrEqual(t, gasQuote, perSettlementGas, "existing reserve exhausted; do not implicitly refinance on resume")
	}
	fundingAmount := new(big.Rat)
	if currentGas.Cmp(new(big.Rat).SetInt64(gasQuote)) < 0 {
		fundingAmount.Sub(new(big.Rat).SetInt64(gasQuote), currentGas)
		require.True(t, fundingAmount.IsInt() && fundingAmount.Num().IsInt64())
		amount := fundingAmount.Num().String()
		param, encodeErr := (&contractcommon.TemplateAutopayConfigInvokeParam{
			GasFundingAmount: amount,
		}).Encode()
		require.NoError(t, encodeErr)
		funded, fundErr := manager.InvokeUnifiedContract(&wallet.ContractInvokeRequest{
			ContractType: wallet.ContractTypeTemplate, SubType: contractcommon.TemplateAutopay,
			ContractAddress: defaults.AutopayContract, Action: contractcommon.TemplateInvokeAPIFunding,
			Param: base64.StdEncoding.EncodeToString(param), ParamEncoding: "base64",
			Assets: []wallet.ContractFundingAsset{{AssetName: defaults.AutopayFeeAssetName, Amount: amount}},
		})
		require.NoError(t, fundErr)
		waitRGB11RegtestL2Transaction(t, manager, network, funded.TxID)
		raw, err = manager.QueryContract(&wallet.ContractQueryRequest{
			Query: wallet.ContractQueryState, Contract: defaults.AutopayContract,
		})
		require.NoError(t, err)
		state, err = dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
		require.NoError(t, err)
	}
	maximumSettlements := gasQuote / perSettlementGas
	require.Positive(t, maximumSettlements)
	paidDelta := state.PaidBlocks - startingPaidBlocks
	require.GreaterOrEqual(t, paidDelta, int64(0))
	require.LessOrEqual(t, paidDelta, maximumSettlements)
	verifiedGas, ok := new(big.Rat).SetString(state.GasBalance)
	require.True(t, ok, "AUTOPAY must report its actual remaining gas balance")
	expectedGas := new(big.Rat).Add(currentGas, fundingAmount)
	expectedGas.Sub(expectedGas, new(big.Rat).SetInt64(paidDelta*perSettlementGas))
	require.Zero(t, expectedGas.Cmp(verifiedGas),
		"funding confirmation must account for actual intervening paid-block settlements")
	remainingBudget := maximumSettlements - paidDelta
	require.Greater(t, remainingBudget, int64(0), "operating budget exhausted during funding")
	require.GreaterOrEqual(t, verifiedGas.Cmp(new(big.Rat).SetInt64(remainingBudget*perSettlementGas)), 0,
		"remaining gas must cover the unconsumed portion of the explicit settlement budget")
	return rgb11RegtestAutopayBudget{
		StartingGasBalance: new(big.Rat).Set(currentGas),
		FundingAmount:      new(big.Rat).Set(fundingAmount),
		StartingPaidBlocks: startingPaidBlocks,
		PerSettlementGas:   perSettlementGas,
		MaximumSettlements: maximumSettlements,
	}
}

func assertRGB11RegtestAutopayBudget(t *testing.T, manager *wallet.Manager,
	contract string, budget rgb11RegtestAutopayBudget) {
	t.Helper()
	require.NotNil(t, budget.StartingGasBalance)
	require.NotNil(t, budget.FundingAmount)
	require.Positive(t, budget.PerSettlementGas)
	raw, err := manager.QueryContract(&wallet.ContractQueryRequest{
		Query: wallet.ContractQueryState, Contract: contract,
	})
	require.NoError(t, err)
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), contract)
	require.NoError(t, err)
	require.NotNil(t, state)
	paidDelta := state.PaidBlocks - budget.StartingPaidBlocks
	require.GreaterOrEqual(t, paidDelta, int64(0), "AUTOPAY paid-block count must not move backwards")
	require.LessOrEqual(t, paidDelta, budget.MaximumSettlements,
		"the real acceptance path must stay within its explicitly funded settlement budget")
	expected := new(big.Rat).Add(budget.StartingGasBalance, budget.FundingAmount)
	expected.Sub(expected, new(big.Rat).SetInt64(paidDelta*budget.PerSettlementGas))
	actual := new(big.Rat)
	if state.GasBalance != "" {
		_, ok := actual.SetString(state.GasBalance)
		require.True(t, ok, "AUTOPAY gas balance must remain a decimal integer")
	}
	require.Zero(t, expected.Cmp(actual),
		"AUTOPAY gas must equal starting balance plus explicit funding minus actual paid-block settlements")
	remainingBudget := budget.MaximumSettlements - paidDelta
	require.GreaterOrEqual(t, actual.Cmp(new(big.Rat).SetInt64(remainingBudget*budget.PerSettlementGas)), 0,
		"remaining gas must cover every unconsumed budgeted settlement")
	t.Logf("AUTOPAY budget used: paid_blocks=%d/%d gas_balance=%s", paidDelta,
		budget.MaximumSettlements, actual.RatString())
}

func waitRGB11RegtestL2Transaction(t *testing.T, manager *wallet.Manager,
	network *realSatoshiNet, txid string) {
	t.Helper()
	require.NotEmpty(t, txid)
	require.Eventually(t, func() bool {
		confirmedAt, err := manager.GetIndexerRPCClient_SatsNet().GetTxHeight(txid)
		return err == nil && confirmedAt > 0
	}, rpcReadyTimeout, 200*time.Millisecond, "scheduled regtest miner/indexer did not confirm %s", txid)
	require.NoError(t, joinBlocks(network.Nodes))
	waitSDKPWARegtestIndexes(t, network)
}

func ensureRGB11RegtestChannel(t *testing.T, manager *wallet.Manager, network *realSatoshiNet,
	mineL1 func(address string, blocks int)) *wallet.Channel {
	t.Helper()
	coreKey, err := walletutils.ParsePubkey(network.Core.nodePubKey)
	require.NoError(t, err)
	expectedID, err := manager.GetChannelAddress()
	require.NoError(t, err)
	find := func() *wallet.Channel {
		for _, channel := range manager.GetAllChannels() {
			if channel != nil && bytes.Equal(channel.PeerNodeId, coreKey.SerializeCompressed()) {
				return channel
			}
		}
		// Funding reservations precede the confirmed channel map. Reuse the
		// saved reservation so a resumed run never opens a second channel.
		if channel := manager.GetActiveChannelWithId(expectedID); channel != nil &&
			bytes.Equal(channel.PeerNodeId, coreKey.SerializeCompressed()) {
			return channel
		}
		return nil
	}
	channel := find()
	if channel == nil {
		channelID, openErr := manager.OpenChannel(2, 50_000, nil, "RGB11 real regtest recovery")
		require.NoError(t, openErr)
		require.Equal(t, expectedID, channelID)
		require.Eventually(t, func() bool { channel = find(); return channel != nil },
			rpcReadyTimeout, 200*time.Millisecond, "SDK did not persist the real channel funding transaction")
	}
	require.NotNil(t, channel.ChanPoint)
	require.NotEmpty(t, channel.ChanPoint.OutPointStr)
	parts := strings.Split(channel.ChanPoint.OutPointStr, ":")
	require.Len(t, parts, 2)
	confirmRGB11RegtestL1Tx(t, manager, parts[0], manager.GetWallet().GetAddressByIndex(1), mineL1)
	require.Eventually(t, func() bool {
		channel = find()
		return channel != nil && channel.Status == wallet.CS_READY
	}, rpcReadyTimeout, 200*time.Millisecond, "real L1 funding must make the channel ready")
	waitRGB11RegtestPeerChannelReady(t, manager, network, channel.ChannelId)
	return channel
}

func waitRGB11RegtestPeerChannelReady(t *testing.T, manager *wallet.Manager,
	network *realSatoshiNet, channelID string) {
	t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	endpoint := "http://" + network.Core.stpAddr + "/regtest/info/channel/" + url.PathEscape(channelID)
	lastStatus, lastCode := -1, -1
	lastID, lastPoint := "", ""
	defer func() {
		t.Logf("local Core channel readiness: status=%d code=%d channel=%s point=%s", lastStatus, lastCode, lastID, lastPoint)
	}()
	require.Eventually(t, func() bool {
		local := manager.GetChannel(channelID)
		if local == nil || local.Status != wallet.CS_READY || local.ChanPoint == nil {
			return false
		}
		response, err := client.Get(endpoint)
		if err != nil {
			return false
		}
		defer response.Body.Close()
		var remote struct {
			Code    int `json:"code"`
			Channel *struct {
				ID     string `json:"channelId"`
				Point  string `json:"chanPoint"`
				Status int    `json:"status"`
			} `json:"channel"`
		}
		if response.StatusCode != http.StatusOK || json.NewDecoder(response.Body).Decode(&remote) != nil {
			return false
		}
		lastCode = remote.Code
		if remote.Channel == nil {
			return false
		}
		lastStatus, lastID, lastPoint = remote.Channel.Status, remote.Channel.ID, remote.Channel.Point
		return remote.Code == 0 && lastStatus == int(wallet.CS_READY) &&
			lastID == channelID && lastPoint == local.ChanPoint.OutPointStr
	}, rpcReadyTimeout, 200*time.Millisecond,
		"both peers must confirm the same original channel before the next operation")
}

func ensureRGB11RegtestSgas(t *testing.T, manager *wallet.Manager, channel *wallet.Channel,
	network *realSatoshiNet, mineL1 func(address string, blocks int), target int64) {
	t.Helper()
	asset := wallet.ParseAssetString(rgb11RegtestAsset)
	require.NotNil(t, asset)
	address := wallet.PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	current := int64(0)
	if balance := manager.GetAssetBalance_SatsNet(address, asset); balance != nil && balance.Value != nil {
		current = rgb11RegtestWholeSgas(t, balance)
	}
	if current >= target {
		return
	}
	need := target - current
	ticker := manager.GetTickerInfoV2(rgb11RegtestAsset)
	require.NotNil(t, ticker)
	name := wallet.GetAssetName(ticker)
	channel.Mutex.RLock()
	existing := channel.GetCommitLocalValue(name)
	channel.Mutex.RUnlock()
	inChannel := int64(0)
	if existing != nil {
		inChannel = rgb11RegtestWholeSgas(t, existing)
	}
	t.Logf("SGAS preparation: wallet=%d target=%d required_unlock=%d existing_channel=%d", current, target, need, inChannel)
	// A previous run may already have confirmed the splice before its unlock
	// became spendable. Reuse that committed balance instead of splicing again.
	if inChannel < need {
		additional := need - inChannel
		l1Balance := manager.GetAssetBalance("", asset)
		require.NotNil(t, l1Balance, "existing-only ordinary-1 fixture must own real L1 SGAS")
		require.NotNil(t, l1Balance.Value)
		require.GreaterOrEqual(t, rgb11RegtestWholeSgas(t, l1Balance), additional)
		txid, _, err := manager.SplicingIn(channel.ChannelId, rgb11RegtestAsset,
			strconv.FormatInt(additional, 10), nil, nil, nil, nil, 2, wallet.SPLICING_REASON_LOCAL)
		require.NoError(t, err)
		require.NotEmpty(t, txid)
		confirmRGB11RegtestL1Tx(t, manager, txid, manager.GetWallet().GetAddressByIndex(1), mineL1)
	}
	require.Eventually(t, func() bool {
		channel = manager.GetChannel(channel.ChannelId)
		return channel != nil && channel.Status == wallet.CS_READY
	}, rpcReadyTimeout, 200*time.Millisecond, "confirmed SDK splice-in must restore the channel")
	waitRGB11RegtestPeerChannelReady(t, manager, network, channel.ChannelId)
	require.Eventually(t, func() bool {
		channel = manager.GetChannel(channel.ChannelId)
		channel.Mutex.RLock()
		outputs, err := channel.GetChannelUtxosWithAsset_SatsNet(indexercommon.NewDefaultDecimal(need), name)
		channel.Mutex.RUnlock()
		if err != nil {
			return false
		}
		for _, output := range outputs {
			point := strings.Split(output.OutPointStr, ":")
			height, err := manager.GetIndexerRPCClient_SatsNet().GetTxHeight(point[0])
			if err != nil || height <= 0 {
				return false
			}
		}
		return len(outputs) > 0
	}, rpcReadyTimeout, 200*time.Millisecond, "the exact SGAS channel outputs must be confirmed and no longer reserved before unlock")
	unlockTxID, _, err := manager.UnlockFromChannel(channel.ChannelId, address,
		rgb11RegtestAsset, strconv.FormatInt(need, 10), nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, unlockTxID)
	waitRGB11RegtestL2Transaction(t, manager, network, unlockTxID)
	require.Eventually(t, func() bool {
		balance := manager.GetAssetBalance_SatsNet(address, asset)
		return balance != nil && balance.Value != nil && rgb11RegtestWholeSgas(t, balance) >= target
	}, rpcReadyTimeout, 200*time.Millisecond, "SDK-unlocked SGAS did not reach the real wallet output")
}

func rgb11RegtestSgasBalance(t *testing.T, manager *wallet.Manager) int64 {
	t.Helper()
	asset := wallet.ParseAssetString(rgb11RegtestAsset)
	require.NotNil(t, asset)
	address := wallet.PublicKeyToP2TRAddress_SatsNet(manager.GetWallet().GetPubKey())
	balance := manager.GetAssetBalance_SatsNet(address, asset)
	if balance == nil || balance.Value == nil {
		return 0
	}
	return rgb11RegtestWholeSgas(t, balance)
}

func rgb11RegtestWholeSgas(t *testing.T, amount *indexercommon.Decimal) int64 {
	t.Helper()
	whole, err := amount.FloorInt64()
	require.NoError(t, err, "SGAS test budget must fit whole asset units")
	return whole
}

func TestSDKPWARegtestSgasBudgetUsesWholeAssetUnits(t *testing.T) {
	for _, precision := range []int{0, 10} {
		t.Run(strconv.Itoa(precision), func(t *testing.T) {
			amount := indexercommon.NewDecimal(3300, precision)
			require.Equal(t, int64(3300), rgb11RegtestWholeSgas(t, amount))
			require.Less(t, rgb11RegtestWholeSgas(t, amount), int64(10150),
				"the real 3300-SGAS balance must enter the funding branch")
		})
	}
}

func fundRGB11RegtestAccountFromSgas(t *testing.T, sender, receiver *wallet.Manager,
	network *realSatoshiNet, amount int64) {
	t.Helper()
	require.Positive(t, amount)
	asset := wallet.ParseAssetString(rgb11RegtestAsset)
	require.NotNil(t, asset)
	senderAddress := wallet.PublicKeyToP2TRAddress_SatsNet(sender.GetWallet().GetPubKey())
	receiverAddress := wallet.PublicKeyToP2TRAddress_SatsNet(receiver.GetWallet().GetPubKey())
	balance := sender.GetAssetBalance_SatsNet(senderAddress, asset)
	require.NotNil(t, balance)
	require.NotNil(t, balance.Value)
	require.GreaterOrEqual(t, rgb11RegtestWholeSgas(t, balance), amount)
	tx, err := sender.SendAssets_SatsNet(receiverAddress, rgb11RegtestAsset,
		strconv.FormatInt(amount, 10), nil)
	require.NoError(t, err)
	require.NotNil(t, tx)
	waitRGB11RegtestL2Transaction(t, sender, network, tx.TxID())
	require.Eventually(t, func() bool {
		balance := receiver.GetAssetBalance_SatsNet(receiverAddress, asset)
		return balance != nil && balance.Value != nil && rgb11RegtestWholeSgas(t, balance) >= amount
	}, rpcReadyTimeout, 200*time.Millisecond, "real SGAS transfer did not reach the receiving account")
}

func assertRGB11RegtestAutopayReady(t *testing.T, manager *wallet.Manager) {
	t.Helper()
	status, err := manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Required)
	require.True(t, status.Ready)
}

func ensureRGB11RegtestRecoveryPackage(t *testing.T, manager *wallet.Manager,
	authorization *wallet.AccountStorageAuthorization) (*account.RecoveryPackage, string, string) {
	t.Helper()
	status := manager.GetAccountManagementStatus()
	accountID, err := manager.RootAccountID()
	require.NoError(t, err)
	require.Equal(t, status.AccountID, accountID)
	backup, err := manager.ExportAccountBackupForPWA(rgb11RegtestWalletPassword, nil)
	require.NoError(t, err)
	rootBackup, err := account.RootBootstrapBackup(backup)
	require.NoError(t, err)
	require.NotEmpty(t, rootBackup.Wallets)
	rootMnemonic := rootBackup.Wallets[0].Mnemonic
	require.NotEmpty(t, rootMnemonic)
	userSharePath := rgb11RegtestUserSharePath(t, accountID)
	if status.RecoveryConfigured {
		mode := account.RecoveryMode(status.RecoveryMode)
		require.Contains(t, []account.RecoveryMode{account.RecoveryMode2Of2, account.RecoveryMode2Of3}, mode)
		locator := account.Locator{Version: account.Version, AccountID: accountID,
			PackageID: status.PackageID, RecoveryMode: mode}
		if material := sdkRegtestRecoveryMaterialFor(t, locator); material != nil {
			userSharePath = material.UserSharePath
		}
		pkg, loadErr := manager.LoadAccountRecoveryPackage(mustRGB11RegtestLocation(t, manager), locator)
		require.NoError(t, loadErr)
		userShare := loadRGB11RegtestUserShare(t, userSharePath)
		backup, secret, recoveryErr := recoverRGB11RegtestPackage(t, pkg, userShare)
		require.NoError(t, recoveryErr)
		defer clearBytes(secret)
		require.NotEmpty(t, backup.Wallets)
		require.True(t, rootMnemonic == backup.Wallets[0].Mnemonic,
			"stored recovery package must reproduce this wallet's current root mnemonic")
		status = manager.GetAccountManagementStatus()
		require.True(t, status.RecoveryConfigured)
		require.Equal(t, accountID, status.AccountID)
		return pkg, userSharePath, sdkRegtestSignedRecoveryLocator(t, manager, authorization, locator)
	}
	packageValue, err := manager.CreateAccountRecoveryPackage(account.CreateOptions{
		AccountID: accountID, Backup: rootBackup, RecoveryMode: account.RecoveryMode2Of2,
		Questions: e2eKnowledgeQuestions(),
	})
	require.NoError(t, err)
	storeRGB11RegtestUserShare(t, userSharePath, packageValue.UserShare)
	publicLocator := sdkRegtestSignedRecoveryLocator(t, manager, authorization, packageValue.Envelope.Locator)
	var recoveredSecret []byte
	err = manager.UseAccountStorageAuthorization(wallet.AccountStoragePurposeRecovery,
		func(auth *wallet.AccountStorageAuthorization) error {
			repository, repositoryErr := manager.NewAccountRepositoryForStorage(*auth)
			if repositoryErr != nil {
				return repositoryErr
			}
			if publishErr := account.NewManager(repository).Publish(context.Background(), *packageValue); publishErr != nil {
				return publishErr
			}
			loaded, loadErr := manager.LoadAccountRecoveryPackage(auth.Location, packageValue.Envelope.Locator)
			if loadErr != nil {
				return loadErr
			}
			userShare := loadRGB11RegtestUserShare(t, userSharePath)
			recoveredBackup, secret, loadErr := recoverRGB11RegtestPackage(t, loaded, userShare)
			if loadErr != nil {
				return loadErr
			}
			defer clearBytes(secret)
			if len(recoveredBackup.Wallets) == 0 || recoveredBackup.Wallets[0].Mnemonic != rootMnemonic {
				return errors.New("recovery package did not reproduce the root wallet mnemonic")
			}
			recoveredSecret = append([]byte(nil), secret...)
			return manager.ActivateAccountManagement(recoveredSecret, rgb11RegtestWalletPassword,
				*auth, packageValue.Envelope.Locator,
				publicLocator)
		})
	require.NoError(t, err)
	defer clearBytes(recoveredSecret)
	require.True(t, rootMnemonic == rootBackup.Wallets[0].Mnemonic,
		"root mnemonic must remain unchanged during recovery package activation")
	status = manager.GetAccountManagementStatus()
	require.True(t, status.RecoveryConfigured)
	require.Equal(t, accountID, status.AccountID)
	require.Equal(t, string(account.RecoveryMode2Of2), status.RecoveryMode)
	return packageValue, userSharePath, publicLocator
}

func mustRGB11RegtestLocation(t *testing.T, manager *wallet.Manager) wallet.AccountIndexerLocation {
	t.Helper()
	location, err := manager.AccountIndexerLocation()
	require.NoError(t, err)
	return location
}

func recoverRGB11RegtestPackage(t *testing.T, pkg *account.RecoveryPackage,
	userShare account.RecoveryShare) (account.Backup, []byte, error) {
	if pkg == nil {
		return account.Backup{}, nil, errors.New("recovery package is missing")
	}
	share, err := account.RecoverDKVSShare(pkg.DKVSShareCapsule, pkg.KnowledgeBundle, sdkRegtestRecoveryAnswers(t, pkg))
	if err != nil {
		return account.Backup{}, nil, err
	}
	return account.RecoverAccount(pkg.Envelope, userShare, share)
}

func rgb11RegtestUserSharePath(t *testing.T, accountID string) string {
	t.Helper()
	root := filepath.Join(os.TempDir(), "sat20wallet-rgb11-recovery-user-shares")
	nameDigest := sha256.Sum256([]byte(accountID))
	dir := filepath.Join(root, hex.EncodeToString(nameDigest[:]))
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.Chmod(root, 0o700))
	require.NoError(t, os.Chmod(dir, 0o700))
	return filepath.Join(dir, "user-share")
}

func storeRGB11RegtestUserShare(t *testing.T, path string, share account.RecoveryShare) {
	t.Helper()
	encoded, err := account.EncodeRecoveryShare(share)
	require.NoError(t, err)
	if existing, readErr := os.ReadFile(path); readErr == nil {
		require.True(t, encoded == string(existing),
			"an existing private user share must be reused verbatim, never replaced")
		return
	} else {
		require.ErrorIs(t, readErr, os.ErrNotExist)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	require.NoError(t, err)
	if _, err := io.WriteString(file, encoded); err != nil {
		_ = file.Close()
		require.NoError(t, err)
	}
	require.NoError(t, file.Close())
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func loadRGB11RegtestUserShare(t *testing.T, path string) account.RecoveryShare {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err, "recovery requires the original locally held user share")
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "user share file must be private")
	encoded, err := os.ReadFile(path)
	require.NoError(t, err)
	share, err := account.DecodeRecoveryShare(string(encoded))
	require.NoError(t, err)
	return share
}

func restoreRGB11RegtestAccount(t *testing.T, network *realSatoshiNet,
	locator account.Locator, userSharePath string, location wallet.AccountIndexerLocation,
	autopayContract, publicLocator string) *wallet.Manager {
	t.Helper()
	l1, err := urlForRGB11RegtestIndexer(os.Getenv("SAT20_REAL_L1_INDEXER"))
	require.NoError(t, err)
	require.NotEmpty(t, location.Scheme)
	require.NotEmpty(t, location.Host)
	config := &sdkcommon.Config{
		Env: "dev", Chain: "regtest",
		IndexerL1: &sdkcommon.Indexer{Scheme: l1.Scheme, Host: l1.Host, Proxy: strings.Trim(l1.Path, "/")},
		IndexerL2: &sdkcommon.Indexer{Scheme: location.Scheme, Host: location.Host, Proxy: strings.Trim(location.Proxy, "/")},
		Peers: []string{
			"b@" + network.Bootstrap.nodePubKey + "@http://" + network.Bootstrap.stpAddr + "/regtest",
			"s@" + network.Core.nodePubKey + "@http://" + network.Core.stpAddr + "/regtest",
		},
	}
	database := indexerdb.NewKVDB(t.TempDir())
	require.NotNil(t, database)
	manager := wallet.NewManager(config, database)
	require.NotNil(t, manager)
	t.Cleanup(func() { manager.Close(); database.Close() })
	loaded, err := manager.LoadAccountRecoveryPackage(location, locator)
	require.NoError(t, err)
	require.Equal(t, locator, loaded.Envelope.Locator)
	userShare := loadRGB11RegtestUserShare(t, userSharePath)
	recoveredBackup, secret, err := recoverRGB11RegtestPackage(t, loaded, userShare)
	require.NoError(t, err)
	defer clearBytes(secret)
	require.NotEmpty(t, recoveredBackup.Wallets)
	recoveredState, err := manager.LoadAccountManagementStateForRecovery(
		location, locator, secret, recoveredBackup.Wallets[0].Mnemonic)
	require.NoError(t, err)
	_, err = manager.RestoreAccountManagementState(*recoveredState, secret,
		rgb11RegtestWalletPassword, locator, wallet.AccountManagementRestoreOptions{
			Location: location, StorageMode: wallet.AccountStoragePaid,
			AutopayContract: autopayContract, PublicLocator: publicLocator,
		})
	require.NoError(t, err)
	attachRGB11RegtestWallet(t, manager, network)
	require.NoError(t, manager.SyncAccountManagementState(context.Background()))
	status := manager.GetAccountManagementStatus()
	require.True(t, status.Active)
	require.True(t, status.RecoveryConfigured)
	require.Equal(t, locator.AccountID, status.AccountID)
	require.Equal(t, wallet.AccountStoragePaid, status.StorageMode)
	rootID, err := manager.RootAccountID()
	require.NoError(t, err)
	require.Equal(t, status.AccountID, rootID)
	return manager
}

func urlForRGB11RegtestIndexer(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid regtest indexer URL %q", raw)
	}
	return parsed, nil
}

func sendRGB11RegtestDirectReturn(t *testing.T, sender, receiver *wallet.Manager,
	network *realSatoshiNet, mineL1 func(address string, blocks int), asset rgb11RegtestNIA) (
	rgb11RegtestTransferEvidence, string) {
	t.Helper()
	receiverEndpoint, err := receiver.EnableConfiguredRGB11AddressReceive(wallet.RGB11ReceiveCapabilityOptions{})
	require.NoError(t, err)
	require.NotNil(t, receiverEndpoint)
	receiverRootID, err := receiver.RootAccountID()
	require.NoError(t, err)
	require.Equal(t, receiverRootID, receiverEndpoint.AccountID)

	prepared, resolvedEndpoint, err := sender.PrepareConfiguredRGB11AddressTransfer(
		context.Background(), wallet.RGB11AddressSendRequest{
			ReceiverAddress: receiver.GetWallet().GetAddress(), AssetName: asset.AssetName,
			AmountRaw: "50", FeeRate: 2, MinConfirmations: 1,
			Expiry: time.Now().Add(time.Hour).Unix(),
		}, dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	require.NotNil(t, prepared)
	require.NotNil(t, prepared.State)
	require.NotNil(t, resolvedEndpoint)
	require.Equal(t, receiverEndpoint.AccountID, resolvedEndpoint.AccountID)
	require.Equal(t, receiverEndpoint.Address, resolvedEndpoint.Address)
	require.Equal(t, asset.AssetName, prepared.State.Asset.Name)
	require.True(t, prepared.State.AddressMode)
	require.NotEmpty(t, prepared.State.TransferID)
	require.NotEmpty(t, prepared.TxID)
	require.NotEmpty(t, prepared.State.InputOutPoints)
	for _, outpoint := range prepared.State.InputOutPoints {
		require.True(t, sender.IsLocked(sender.GetWallet().GetAddress(), outpoint),
			"direct transfer inputs remain reserved until mailbox ACK and real confirmation")
	}
	// DKVS direct delivery transports the original Armor object; the standard
	// proxy above transports EncodeFile bytes of that same consignment.
	rawConsignment, _, _ := rgb11RegtestPreparedMaterial(t, sender, prepared.State,
		prepared.RecipientConsignment, prepared.RecipientConsignmentBase64, prepared.TxID)
	consignmentHash := prepared.State.ConsignmentHash

	delivery, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(
		prepared.State.TransferID, wallet.RGB11AddressDeliveryOptions{})
	require.NoError(t, err)
	if delivery.Broadcast {
		require.Equal(t, prepared.TxID, delivery.TxID,
			"an automatic mailbox ACK may only broadcast the original prepared transaction")
	} else {
		require.True(t, delivery.AwaitingACK)
	}
	// In both branches prove the exact durable receiver ACK and sender accepted
	// ACK/broadcast below; a successful return alone never proves delivery.

	require.Eventually(t, func() bool {
		_, receiveSyncErr := receiver.SyncConfiguredRGB11AddressMailbox(context.Background(),
			dkvsindexer.RecordVerificationOptions{}, wallet.RGB11AddressDeliveryOptions{})
		if receiveSyncErr != nil {
			return false
		}
		receiveState, receiveStateErr := rgb11RegtestFindTransferState(receiver, prepared.State.TransferID)
		if receiveStateErr != nil || receiveState == nil ||
			receiveState.Direction != "receive" || receiveState.AckStatus != "ack-sent" ||
			!receiveState.DeliveryAcknowledged || receiveState.WitnessTxID != prepared.TxID ||
			receiveState.ConsignmentHash != consignmentHash {
			return false
		}
		_, sendSyncErr := sender.SyncConfiguredRGB11AddressMailbox(context.Background(),
			dkvsindexer.RecordVerificationOptions{}, wallet.RGB11AddressDeliveryOptions{})
		if sendSyncErr != nil {
			return false
		}
		sendState, sendStateErr := rgb11RegtestFindTransferState(sender, prepared.State.TransferID)
		if sendStateErr != nil || sendState == nil || sendState.Direction != "send" ||
			sendState.Status != "broadcast" || sendState.AckStatus != "accepted" ||
			!sendState.DeliveryAcknowledged || sendState.WitnessTxID != prepared.TxID ||
			sendState.ConsignmentHash != consignmentHash {
			return false
		}
		var mempool []string
		if json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &mempool) != nil {
			return false
		}
		return containsString(mempool, prepared.TxID)
	}, rpcReadyTimeout, 200*time.Millisecond,
		"the exact receiver ACK, sender broadcast state, and original txid did not converge")

	confirmRGB11RegtestL1Tx(t, sender, prepared.TxID,
		sender.GetWallet().GetAddressByIndex(1), mineL1)
	receipt, err := receiver.ValidateRGB11Consignment(context.Background(), rawConsignment)
	require.NoError(t, err)
	validationHash := rgb11RegtestAssertReceipt(t, receipt, asset.AssetName, asset.ContractID,
		prepared.State.TransferID, consignmentHash, rgb11RegtestReturnAmount)
	_, err = sender.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	_, err = receiver.RefreshRGB11State(context.Background())
	require.NoError(t, err)
	require.NoError(t, sender.SyncAccountManagementState(context.Background()))
	require.NoError(t, receiver.SyncAccountManagementState(context.Background()))
	evidence := rgb11RegtestTransferEvidence{
		TransferID: prepared.State.TransferID, TxID: prepared.TxID,
		ConsignmentHash: consignmentHash,
	}
	rgb11RegtestAssertSettledTransfer(t, sender, asset, evidence, "send", true)
	rgb11RegtestAssertSettledTransfer(t, receiver, asset, evidence, "receive", true)
	rgb11RegtestAssertReceiptProof(t, receiver, asset, evidence,
		validationHash, rgb11RegtestReturnAmount)
	return evidence, validationHash
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func rgb11RegtestPreparedMaterial(t *testing.T, manager *wallet.Manager,
	state *rgb11wallet.TransferState, armor, fileBase64, txid string) ([]byte, []byte, string) {
	t.Helper()
	require.NotNil(t, state)
	require.Equal(t, state.WitnessTxID, txid)
	armorBytes := []byte(armor)
	armorDigest := sha256.Sum256(armorBytes)
	require.Equal(t, state.ConsignmentHash, hex.EncodeToString(armorDigest[:]), "sender identity binds the persisted recipient Armor bytes")
	store := rgb11wallet.NewProjectionStore(manager.GetDB(), nil)
	require.NoError(t, store.SetScope(fmt.Sprintf("wallet-%d-account-%d-rgb11v2", manager.GetCurrentWalletId(), manager.GetCurrentAccountId())))
	pending, err := store.LoadPendingTransfer(state.TransferID)
	require.NoError(t, err)
	require.Equal(t, state.TransferID, pending.State.TransferID)
	require.Equal(t, txid, pending.State.WitnessTxID)
	require.NotEmpty(t, pending.SignedTx)
	var signed btcwire.MsgTx
	require.NoError(t, signed.Deserialize(bytes.NewReader(pending.SignedTx)))
	require.Equal(t, txid, signed.TxHash().String(), "persisted signed transaction must retain the original txid")
	for _, outpoint := range state.InputOutPoints {
		found := false
		for _, input := range signed.TxIn {
			found = found || input.PreviousOutPoint.String() == outpoint
		}
		require.True(t, found, "retained RGB input must belong to the original signed transaction")
	}
	require.Equal(t, state.ConsignmentHash, pending.RecipientObjectHash)
	require.Equal(t, armorBytes, pending.RecipientConsignment)
	stored, err := store.LoadObject(state.ConsignmentHash)
	require.NoError(t, err)
	require.Equal(t, armorBytes, stored)
	container, err := coreconsignment.DecodeArmor(armor)
	require.NoError(t, err)
	require.NotNil(t, container.Armor)
	require.Equal(t, "transfer", container.Armor.Type)
	require.True(t, rgb11wallet.ContractAssetKeyMatches(state.Asset.Name, container.ContractID))
	require.Equal(t, state.TransferID, container.Armor.ID)
	fileBytes, err := base64.StdEncoding.DecodeString(fileBase64)
	require.NoError(t, err)
	wantFile, err := coreconsignment.EncodeFile(container)
	require.NoError(t, err)
	require.Equal(t, wantFile, fileBytes, "official transfer file must be the exact encoding of the persisted recipient Armor")
	decodedFile, err := coreconsignment.Decode(fileBytes)
	require.NoError(t, err)
	require.NotNil(t, decodedFile.Armor)
	require.Equal(t, container.ContractID, decodedFile.ContractID)
	require.Equal(t, container.Armor.ID, decodedFile.Armor.ID)
	fileDigest := sha256.Sum256(fileBytes)
	return armorBytes, fileBytes, hex.EncodeToString(fileDigest[:])
}

func rgb11RegtestProxyReceiveEvidence(t *testing.T, sender *wallet.Manager,
	send rgb11RegtestTransferEvidence) (rgb11RegtestTransferEvidence, []byte) {
	t.Helper()
	store := rgb11wallet.NewProjectionStore(sender.GetDB(), nil)
	require.NoError(t, store.SetScope(fmt.Sprintf("wallet-%d-account-%d-rgb11v2", sender.GetCurrentWalletId(), sender.GetCurrentAccountId())))
	raw, err := store.LoadObject(send.ConsignmentHash)
	require.NoError(t, err)
	digest := sha256.Sum256(raw)
	require.Equal(t, send.ConsignmentHash, hex.EncodeToString(digest[:]))
	container, err := coreconsignment.DecodeArmor(string(raw))
	require.NoError(t, err)
	require.NotNil(t, container.Armor)
	require.Equal(t, send.TransferID, container.Armor.ID)
	file, err := coreconsignment.EncodeFile(container)
	require.NoError(t, err)
	digest = sha256.Sum256(file)
	send.ConsignmentHash = hex.EncodeToString(digest[:])
	return send, file
}

func rgb11RegtestAssertAcceptedProxyMaterial(t *testing.T, sender, receiver *wallet.Manager,
	asset rgb11RegtestNIA, expected rgb11RegtestTransferEvidence, expectedFile []byte, originalRequestID string) {
	t.Helper()
	sendState, err := rgb11RegtestFindTransferState(sender, expected.TransferID)
	require.NoError(t, err)
	require.NotNil(t, sendState)
	require.Equal(t, expected.TxID, sendState.WitnessTxID)
	require.NotEmpty(t, sendState.Invoice)
	scope := fmt.Sprintf("wallet-%d-account-%d-rgb11v2", receiver.GetCurrentWalletId(), receiver.GetCurrentAccountId())
	engineStore := rgb11wallet.NewEngineStore(receiver.GetDB())
	require.NoError(t, engineStore.SetScope(scope))
	records, err := engineStore.ExportSnapshot()
	require.NoError(t, err)
	requestID := ""
	for _, record := range records {
		if !strings.HasPrefix(record.Key, "wallet/receive/") {
			continue
		}
		request, decodeErr := corewallet.DecodeReceiveRequest(record.Value)
		require.NoError(t, decodeErr)
		if request.Invoice != sendState.Invoice {
			continue
		}
		require.Empty(t, requestID, "original invoice must identify exactly one persisted B request")
		requestID = request.RequestID
	}
	require.NotEmpty(t, requestID, "original B receive request must survive the confirmed standard transfer")
	if originalRequestID != "" {
		require.Equal(t, originalRequestID, requestID)
	}
	var request *corewallet.ReceiveRequest
	var refresh *wallet.RGB11RefreshResult
	var lastErr error
	defer func() {
		status := ""
		if request != nil {
			status = string(request.Status)
		}
		t.Logf("original standard convergence: request=%s status=%s txid=%s refresh=%+v error=%v",
			requestID, status, expected.TxID, refresh, lastErr)
	}()
	require.Eventually(t, func() bool {
		refresh, lastErr = receiver.RefreshRGB11State(context.Background())
		if lastErr != nil || refresh == nil || refresh.Unresolved != 0 || len(refresh.Inconsistent) != 0 {
			return false
		}
		request, lastErr = receiver.GetRGB11ReceiveRequest(requestID)
		if lastErr != nil || request == nil || request.Status != corewallet.ReceiveAccepted {
			return false
		}
		state, stateErr := rgb11RegtestFindTransferState(receiver, expected.TransferID)
		lastErr = stateErr
		return stateErr == nil && state != nil && state.Status == "settled"
	}, rpcReadyTimeout, 200*time.Millisecond, "original confirmed standard transfer must settle without replacement or redelivery")
	require.Equal(t, sendState.Invoice, request.Invoice)
	require.Equal(t, expected.TransferID, request.TransferID)
	require.Equal(t, expected.TxID, request.WitnessTxID)
	require.Equal(t, expected.ConsignmentHash, request.ObjectHash)
	require.NoError(t, receiver.SyncAccountManagementState(context.Background()))
	store := rgb11wallet.NewProjectionStore(receiver.GetDB(), nil)
	require.NoError(t, store.SetScope(scope))
	object, err := store.LoadObject(expected.ConsignmentHash)
	require.NoError(t, err)
	require.Equal(t, expectedFile, object, "B must retain the exact official file independently encoded from A's original Armor")
	digest := sha256.Sum256(object)
	require.Equal(t, expected.ConsignmentHash, hex.EncodeToString(digest[:]))
	receipt, err := store.LoadValidationReceipt(expected.ConsignmentHash)
	require.NoError(t, err)
	validationHash := rgb11RegtestAssertReceipt(t, receipt, asset.AssetName, asset.ContractID,
		expected.TransferID, expected.ConsignmentHash, 100)
	rgb11RegtestAssertSettledTransfer(t, receiver, asset, expected, "receive", false)
	balance := rgb11RegtestBalance(t, receiver, asset.AssetName)
	require.True(t, balance == 100 || balance == 50, "only original post-standard or completed-return balances may proceed")
	if balance == 100 {
		rgb11RegtestAssertReceiptProof(t, receiver, asset, expected, validationHash, 100)
	}
}

func rgb11RegtestFindTransferState(manager *wallet.Manager,
	transferID string) (*rgb11wallet.TransferState, error) {
	state, err := manager.GetRGB11State()
	if err != nil {
		return nil, err
	}
	for _, transfer := range state.Transfers {
		if transfer != nil && transfer.TransferID == transferID {
			return transfer, nil
		}
	}
	return nil, nil
}

func rgb11RegtestCoreHeight(t *testing.T, network *realSatoshiNet) uint64 {
	t.Helper()
	_, height, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, height, int32(0))
	return uint64(height)
}

func mustRGB11RegtestRootID(t *testing.T, manager *wallet.Manager) string {
	t.Helper()
	rootID, err := manager.RootAccountID()
	require.NoError(t, err)
	return rootID
}

func ensureRGB11RegtestNIA(t *testing.T, issuer, receiver *wallet.Manager) (rgb11RegtestNIA, []byte) {
	t.Helper()
	var result rgb11RegtestNIA
	var matches int
	result.ContractID, result.AssetName, matches = rgb11RegtestFindNIAContract(t, issuer, "")
	require.LessOrEqual(t, matches, 1, "the original ticker must resolve to at most one RGB contract")
	if matches == 0 {
		issued, issueErr := issuer.IssueRGB11Asset(context.Background(), wallet.RGB11IssueRequest{
			Schema: "NIA", Ticker: rgb11RegtestTicker, Name: "Real regtest SDK recovery acceptance",
			Precision: 0, Amounts: []uint64{rgb11RegtestNIAAmount}, MinConfirmations: 1,
		})
		require.NoError(t, issueErr)
		require.NotNil(t, issued.Receipt)
		armor, armorErr := coreconsignment.ParseArmor(issued.Armor)
		require.NoError(t, armorErr)
		require.NotNil(t, armor)
		require.Equal(t, issued.ContractID, armor.Contract)
		expectedAssetName, keyErr := rgb11wallet.NewContractAssetKey(
			issued.ContractID, indexercommon.ASSET_TYPE_FT)
		require.NoError(t, keyErr)
		require.Equal(t, expectedAssetName, issued.AssetName)
		require.True(t, rgb11wallet.ContractAssetKeyMatches(expectedAssetName, issued.ContractID))
		rgb11RegtestAssertReceipt(t, issued.Receipt, expectedAssetName, issued.ContractID, armor.ID,
			issued.Receipt.ConsignmentHash, rgb11RegtestNIAAmount)
		_, refreshErr := issuer.RefreshRGB11State(context.Background())
		require.NoError(t, refreshErr)
		result.ContractID, result.AssetName, matches = rgb11RegtestFindNIAContract(t, issuer, issued.ContractID)
		require.Equal(t, 1, matches, "issued contract must appear once in validated TickerExt metadata")
		require.Equal(t, issued.ContractID, result.ContractID)
		require.Equal(t, expectedAssetName, result.AssetName)
	}
	require.True(t, rgb11wallet.ContractAssetKeyMatches(result.AssetName, result.ContractID))
	issuerBalance := rgb11RegtestBalance(t, issuer, result.AssetName)

	exported, err := issuer.ExportRGB11Contract(result.ContractID)
	require.NoError(t, err)
	contractFile, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	require.NoError(t, err)
	require.NotEmpty(t, contractFile)
	require.True(t, bytes.HasPrefix(contractFile, []byte("RGB\x00CON")),
		"contract import/export must retain the official canonical RGB CON file")
	digest := sha256.Sum256(contractFile)
	require.Equal(t, hex.EncodeToString(digest[:]), exported.SHA256)
	_, _, receiverMatches := rgb11RegtestFindNIAContract(t, receiver, result.ContractID)
	require.LessOrEqual(t, receiverMatches, 1)
	if receiverMatches == 0 {
		importResult, importErr := receiver.ImportRGB11ContractFile(context.Background(), contractFile)
		require.NoError(t, importErr)
		require.Equal(t, result.ContractID, importResult.ContractID)
		require.Zero(t, importResult.Projected, "contract import alone never creates RGB ownership")
	}
	_, receiverAssetName, receiverMatches := rgb11RegtestFindNIAContract(t, receiver, result.ContractID)
	require.Equal(t, 1, receiverMatches, "receiver must retain the imported contract's TickerExt identity")
	require.Equal(t, result.AssetName, receiverAssetName)
	require.True(t, rgb11wallet.ContractAssetKeyMatches(receiverAssetName, result.ContractID))
	receiverBalance := rgb11RegtestBalance(t, receiver, result.AssetName)
	require.Equal(t, rgb11RegtestNIAAmount, issuerBalance+receiverBalance,
		"issuer and receiver balances must conserve the real NIA issuance")
	require.Contains(t, []uint64{0, 50, 100}, receiverBalance,
		"only the initial, post-standard, or completed direct-return state is resumable")
	require.Contains(t, []uint64{200, 250, 300}, issuerBalance)
	return result, contractFile
}

func rgb11RegtestFindNIAContract(t *testing.T, manager *wallet.Manager,
	contractID string) (string, indexercommon.AssetName, int) {
	t.Helper()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	var foundContract string
	var foundAsset indexercommon.AssetName
	matches := 0
	for _, ticker := range state.TickerInfos {
		if ticker == nil || ticker.TickerInfo == nil || ticker.ContractID == "" {
			continue
		}
		var ext rgb11wallet.TickerExt
		require.NoError(t, json.Unmarshal(ticker.TickerInfo.Content, &ext))
		if ext.ContractID == "" {
			continue
		}
		require.Equal(t, ticker.ContractID, ext.ContractID,
			"ticker presentation must retain the original RGB ContractID")
		if contractID != "" {
			if ext.ContractID != contractID {
				continue
			}
		} else if !strings.EqualFold(ext.Ticker, rgb11RegtestTicker) &&
			!strings.EqualFold(ext.NormalizedTicker, rgb11wallet.NormalizeTicker(rgb11RegtestTicker)) {
			continue
		}
		expected, keyErr := rgb11wallet.NewContractAssetKey(ext.ContractID, indexercommon.ASSET_TYPE_FT)
		require.NoError(t, keyErr)
		require.Equal(t, expected, ext.AssetName,
			"TickerExt must carry the complete immutable contract identity")
		require.Equal(t, expected, ticker.TickerInfo.AssetName,
			"wallet presentation must retain the complete immutable contract identity")
		require.True(t, rgb11wallet.ContractAssetKeyMatches(ticker.TickerInfo.AssetName, ext.ContractID))
		matches++
		foundContract, foundAsset = ext.ContractID, expected
	}
	return foundContract, foundAsset, matches
}

func rgb11RegtestBalance(t *testing.T, manager *wallet.Manager, asset indexercommon.AssetName) uint64 {
	t.Helper()
	balance, err := manager.GetRGB11AssetBalance(&asset)
	require.NoError(t, err)
	if balance == nil || balance.Value == nil {
		return 0
	}
	return balance.Value.Uint64()
}

func snapshotRGB11RegtestOwnedProofs(t *testing.T, manager *wallet.Manager,
	asset rgb11RegtestNIA, canonicalPackage []byte) map[string]rgb11RegtestProofIdentity {
	t.Helper()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	localWalletID := manager.GetCurrentWalletId()
	accountIndex := manager.GetCurrentAccountId()
	require.Positive(t, localWalletID, "scope uses the local SDK wallet ID")
	scope := fmt.Sprintf("wallet-%d-account-%d-rgb11v2", localWalletID, accountIndex)
	projectionStore := rgb11wallet.NewProjectionStore(manager.GetDB(), nil)
	require.NoError(t, projectionStore.SetScope(scope))
	canonicalByOutpoint := make(map[string]*rgb11wallet.AllocationProof)
	if len(canonicalPackage) != 0 {
		packageValue, decodeErr := rgb11wallet.DecodeRecoveryPackage(canonicalPackage)
		require.NoError(t, decodeErr)
		canonicalProofs, decodeErr := rgb11wallet.ProofsFromProjectionSnapshot(packageValue.ProjectionRecords)
		require.NoError(t, decodeErr)
		for _, proof := range canonicalProofs {
			if proof != nil && proof.AssetName == asset.AssetName {
				canonicalByOutpoint[proof.OutPoint] = proof
			}
		}
	}
	proofByOutpoint := make(map[string]*rgb11wallet.AllocationProof)
	for _, proof := range state.Proofs {
		if proof != nil && proof.AssetName == asset.AssetName {
			proofByOutpoint[proof.OutPoint] = proof
		}
	}
	owned := make(map[string]rgb11RegtestProofIdentity)
	var total uint64
	address := manager.GetWallet().GetAddress()
	for _, output := range state.Outputs {
		if output == nil {
			continue
		}
		for _, outputAsset := range output.Assets {
			if outputAsset.Name != asset.AssetName {
				continue
			}
			proof := proofByOutpoint[output.OutPointStr]
			require.NotNil(t, proof, "every owned RGB allocation output must have its proof row")
			require.True(t, rgb11wallet.ContractAssetKeyMatches(proof.AssetName, asset.ContractID))
			storedProof, loadErr := projectionStore.LoadProof(proof.OutPoint, proof.AssetName)
			require.NoError(t, loadErr)
			require.Equal(t, proof, storedProof, "the public state must reflect the persisted proof sidecar")
			receipt, loadErr := projectionStore.LoadValidationReceipt(proof.ConsignmentHash)
			require.NoError(t, loadErr)
			require.Equal(t, proof.ConsignmentHash, receipt.ConsignmentHash)
			require.Equal(t, asset.ContractID, receipt.ContractID)
			validationHash, hashErr := receipt.Hash()
			require.NoError(t, hashErr)
			require.Equal(t, validationHash, proof.ValidationHash,
				"proof identity must retain the canonical ValidationReceipt.Hash")
			rawObject, loadErr := projectionStore.LoadObject(proof.ConsignmentHash)
			require.NoError(t, loadErr)
			objectDigest := sha256.Sum256(rawObject)
			require.Equal(t, proof.ConsignmentHash, hex.EncodeToString(objectDigest[:]),
				"persisted consignment object must match its immutable SHA-256 key")
			require.NotEmpty(t, proof.WitnessTxID)
			require.NotEmpty(t, proof.SealDisclosure)
			if proof.Status == "spending" {
				// GetRGB11State retains historical outputs, whereas its balance
				// excludes them. Prove the original confirmed consumer before
				// excluding this row from current ownership; keep the complete
				// receipt/object checks above and canonical backup comparison.
				point := strings.Split(proof.OutPoint, ":")
				require.Len(t, point, 2)
				vout, parseErr := strconv.ParseUint(point[1], 10, 32)
				require.NoError(t, parseErr)
				response, lookupErr := (&http.Client{Timeout: 10 * time.Second}).Get(
					strings.TrimRight(os.Getenv("SAT20_REAL_ESPLORA"), "/") + "/tx/" + point[0] + "/outspend/" + point[1])
				require.NoError(t, lookupErr)
				var spend struct {
					Spent  bool   `json:"spent"`
					TxID   string `json:"txid"`
					Status struct {
						Confirmed bool `json:"confirmed"`
					} `json:"status"`
				}
				lookupErr = json.NewDecoder(response.Body).Decode(&spend)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusOK, response.StatusCode)
				require.NoError(t, lookupErr)
				require.True(t, spend.Spent && spend.Status.Confirmed && spend.TxID != "")
				knownConsumer := false
				for _, transfer := range state.Transfers {
					if transfer.Direction == "send" && transfer.Status == "settled" && transfer.WitnessTxID == spend.TxID {
						for _, input := range transfer.InputOutPoints {
							knownConsumer = knownConsumer || input == proof.OutPoint
						}
					}
				}
				require.True(t, knownConsumer, "historical proof requires its original settled sender consumer")
				assertRGB11RegtestCoreConfirmation(t, spend.TxID)
				var raw struct {
					Vin []struct {
						TxID string `json:"txid"`
						Vout uint32 `json:"vout"`
					} `json:"vin"`
				}
				require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawtransaction", spend.TxID, "true"), &raw))
				exactInput := false
				for _, input := range raw.Vin {
					exactInput = exactInput || (input.TxID == point[0] && input.Vout == uint32(vout))
				}
				require.True(t, exactInput, "Core raw transaction must consume this exact historical outpoint")
				continue
			}
			require.Contains(t, []string{"valid", "settled"}, proof.Status)
			require.True(t, manager.IsLocked(address, output.OutPointStr),
				"owned RGB allocation output remains protected by its proof lock")
			require.NotNil(t, outputAsset.Amount.Value)
			amount := outputAsset.Amount.Value.Uint64()
			total += amount
			var receiptAmount uint64
			for _, allocation := range receipt.Allocations {
				if allocation.OutPoint == proof.OutPoint && allocation.AssetName == asset.AssetName {
					require.NotNil(t, allocation.Amount.Value)
					receiptAmount = allocation.Amount.Value.Uint64()
				}
			}
			require.Equal(t, amount, receiptAmount,
				"each owned output must match its exact receipt allocation")
			stateDigest := sha256.Sum256(proof.StateData)
			sealDigest := sha256.Sum256(proof.SealDisclosure)
			carrierBytes, marshalErr := json.Marshal(proof.CarrierBinding)
			require.NoError(t, marshalErr)
			carrierDigest := sha256.Sum256(carrierBytes)
			comparisonHash := proof.ValidationHash
			if len(canonicalPackage) != 0 {
				canonicalProof := canonicalByOutpoint[proof.OutPoint]
				require.NotNil(t, canonicalProof)
				require.Equal(t, proof.ConsignmentHash, canonicalProof.ConsignmentHash)
				require.NotEmpty(t, canonicalProof.ValidationHash)
				comparisonHash = canonicalProof.ValidationHash
			}
			owned[output.OutPointStr+"|"+asset.AssetName.String()] = rgb11RegtestProofIdentity{
				Amount: amount, OutPoint: proof.OutPoint, AssetName: proof.AssetName,
				OperationID: proof.OperationID, AssignmentType: proof.AssignmentType,
				AssignmentIndex: proof.AssignmentIndex, StateClass: proof.StateClass,
				StateDataHash:      hex.EncodeToString(stateDigest[:]),
				SealCommitment:     proof.SealCommitment,
				SealDisclosureHash: hex.EncodeToString(sealDigest[:]),
				CarrierBindingHash: hex.EncodeToString(carrierDigest[:]),
				ConsignmentHash:    proof.ConsignmentHash, ValidationHash: comparisonHash,
				WitnessTxID: proof.WitnessTxID,
			}
		}
	}
	require.NotEmpty(t, owned)
	require.Equal(t, rgb11RegtestBalance(t, manager, asset.AssetName), total,
		"the sum of proof-backed current outputs must equal the wallet RGB balance")
	return owned
}

// Call only after Stop has joined the runtime writers; retain the original DB
// handle so projection and engine exports describe the same durable checkpoint.
func rgb11RegtestCanonicalRecoveryPackage(t *testing.T, manager *wallet.Manager) []byte {
	t.Helper()
	walletID, err := manager.RGB11WalletID()
	require.NoError(t, err)
	localWalletID := manager.GetCurrentWalletId()
	accountIndex := manager.GetCurrentAccountId()
	require.Positive(t, localWalletID, "scope uses the local SDK wallet ID")
	scope := fmt.Sprintf("wallet-%d-account-%d-rgb11v2", localWalletID, accountIndex)
	projectionStore := rgb11wallet.NewProjectionStore(manager.GetDB(), nil)
	engineStore := rgb11wallet.NewEngineStore(manager.GetDB())
	require.NoError(t, projectionStore.SetScope(scope))
	require.NoError(t, engineStore.SetScope(scope))
	projectionRecords, err := projectionStore.ExportSnapshot()
	require.NoError(t, err)
	engineRecords, err := engineStore.ExportSnapshot()
	require.NoError(t, err)
	snapshot := &rgb11wallet.RGB11WalletSnapshot{
		Version: rgb11wallet.WalletSnapshotVersion, WalletID: walletID,
		AccountIndex: accountIndex, EngineBuildID: rgb11wallet.NativeEngineBuildID,
		ProjectionRecords: projectionRecords, EngineRecords: engineRecords,
	}
	require.NoError(t, rgb11wallet.ValidateWalletSnapshot(snapshot))
	recoveryPackage, err := rgb11wallet.RecoveryPackageFromSnapshot(snapshot, 0)
	require.NoError(t, err)
	encoded, err := rgb11wallet.EncodeRecoveryPackage(recoveryPackage)
	require.NoError(t, err)
	return encoded
}

func rgb11RegtestAssertReceipt(t *testing.T, receipt *rgb11wallet.ValidationReceipt,
	assetName indexercommon.AssetName, contractID, transferID, consignmentHash string, amount uint64) string {
	t.Helper()
	require.NotNil(t, receipt)
	require.Equal(t, "valid", receipt.Status)
	require.Equal(t, contractID, receipt.ContractID)
	require.Equal(t, transferID, receipt.TransferID)
	require.Equal(t, consignmentHash, receipt.ConsignmentHash)
	require.NotEmpty(t, receipt.SchemaID)
	require.NotEmpty(t, receipt.EngineBuildID)
	var total uint64
	for _, allocation := range receipt.Allocations {
		require.NotEmpty(t, allocation.OutPoint)
		require.Equal(t, assetName, allocation.AssetName)
		require.True(t, rgb11wallet.ContractAssetKeyMatches(allocation.AssetName, contractID))
		require.NotNil(t, allocation.Amount.Value)
		total += allocation.Amount.Value.Uint64()
	}
	require.Equal(t, amount, total, "consensus receipt must validate the exact expected allocation")
	hash, err := receipt.Hash()
	require.NoError(t, err)
	require.Len(t, hash, 64)
	return hash
}

func rgb11RegtestAssertReceiptProof(t *testing.T, manager *wallet.Manager,
	asset rgb11RegtestNIA, evidence rgb11RegtestTransferEvidence,
	validationHash string, expectedAmount uint64) {
	t.Helper()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	proofs := make(map[string]*rgb11wallet.AllocationProof)
	for _, proof := range state.Proofs {
		if proof == nil || proof.AssetName != asset.AssetName ||
			proof.WitnessTxID != evidence.TxID || proof.ConsignmentHash != evidence.ConsignmentHash {
			continue
		}
		require.Equal(t, validationHash, proof.ValidationHash,
			"proof identity is the deterministic hash of the accepted validation receipt")
		require.NotEmpty(t, proof.SealDisclosure)
		proofs[proof.OutPoint] = proof
	}
	require.NotEmpty(t, proofs, "confirmed transfer must persist its receipt-bound allocation proof")
	var amount uint64
	for _, output := range state.Outputs {
		if output == nil || proofs[output.OutPointStr] == nil {
			continue
		}
		for _, outputAsset := range output.Assets {
			if outputAsset.Name == asset.AssetName {
				require.Equal(t, "settled", proofs[output.OutPointStr].Status)
				require.True(t, manager.IsLocked(manager.GetWallet().GetAddress(), output.OutPointStr))
				require.NotNil(t, outputAsset.Amount.Value)
				amount += outputAsset.Amount.Value.Uint64()
			}
		}
	}
	require.Equal(t, expectedAmount, amount,
		"receipt-bound outputs must preserve the exact transfer amount")
}

func rgb11RegtestAssertSettledTransfer(t *testing.T, manager *wallet.Manager,
	asset rgb11RegtestNIA, evidence rgb11RegtestTransferEvidence,
	direction string, addressMode bool) {
	t.Helper()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	var transferFound bool
	for _, transfer := range state.Transfers {
		if transfer == nil || transfer.TransferID != evidence.TransferID {
			continue
		}
		require.Equal(t, direction, transfer.Direction)
		require.Equal(t, addressMode, transfer.AddressMode)
		require.Equal(t, asset.AssetName, transfer.Asset.Name)
		require.Equal(t, evidence.TxID, transfer.WitnessTxID)
		require.Equal(t, evidence.ConsignmentHash, transfer.ConsignmentHash)
		require.Equal(t, "settled", transfer.Status)
		if direction == "send" {
			require.NotEmpty(t, transfer.InputOutPoints,
				"send transfers must record their spent input outpoints")
		} else {
			require.Empty(t, transfer.InputOutPoints,
				"receive transfers identify owned outputs and must not claim sender inputs")
		}
		require.NotEmpty(t, transfer.OutputOutPoints)
		transferFound = true
	}
	require.True(t, transferFound, "wallet state must project the confirmed transfer, txid, and raw object hash")
	proofs := snapshotRGB11RegtestOwnedProofs(t, manager, asset, nil)
	require.NotEmpty(t, proofs, "owned RGB outputs must retain validated, locked allocation proofs")
}

func rgb11RegtestFindSettledTransfer(t *testing.T, manager *wallet.Manager,
	asset rgb11RegtestNIA, direction string, addressMode bool) rgb11RegtestTransferEvidence {
	t.Helper()
	state, err := manager.GetRGB11State()
	require.NoError(t, err)
	var found rgb11RegtestTransferEvidence
	for _, transfer := range state.Transfers {
		if transfer != nil && transfer.Status == "settled" && transfer.Direction == direction &&
			transfer.AddressMode == addressMode && transfer.Asset.Name == asset.AssetName {
			require.Empty(t, found.TransferID, "expected one settled transfer for the selected transport stage")
			found = rgb11RegtestTransferEvidence{
				TransferID: transfer.TransferID, TxID: transfer.WitnessTxID,
				ConsignmentHash: transfer.ConsignmentHash,
			}
		}
	}
	require.NotEmpty(t, found.TransferID, "completed transfer evidence was not found")
	require.NotEmpty(t, found.TxID)
	require.NotEmpty(t, found.ConsignmentHash)
	return found
}

func assertRGB11RegtestExportRoundTrip(t *testing.T, first, second *wallet.Manager,
	contractID string, canonical []byte) {
	t.Helper()
	left, err := first.ExportRGB11Contract(contractID)
	require.NoError(t, err)
	right, err := second.ExportRGB11Contract(contractID)
	require.NoError(t, err)
	leftRaw, err := base64.StdEncoding.DecodeString(left.ContractConsignmentBase64)
	require.NoError(t, err)
	rightRaw, err := base64.StdEncoding.DecodeString(right.ContractConsignmentBase64)
	require.NoError(t, err)
	require.Equal(t, left.SHA256, right.SHA256)
	require.Equal(t, leftRaw, rightRaw, "both devices export the same official contract file bytes")
	require.Equal(t, canonical, leftRaw, "export remains byte-for-byte equal to the original standard file")
}

func confirmRGB11RegtestL1Tx(t *testing.T, manager *wallet.Manager, txid, minerAddress string,
	mineL1 func(address string, blocks int)) {
	t.Helper()
	height, err := manager.GetIndexerRPCClient().GetTxHeight(txid)
	if err == nil && height > 0 {
		assertRGB11RegtestCoreConfirmation(t, txid)
		return
	}
	var mempool []string
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t, "getrawmempool"), &mempool))
	require.Contains(t, mempool, txid,
		"only mine after the exact public SDK transaction is present in the real Core mempool")
	mineL1(minerAddress, 1)
	require.Eventually(t, func() bool {
		confirmedAt, confirmErr := manager.GetIndexerRPCClient().GetTxHeight(txid)
		return confirmErr == nil && confirmedAt > 0
	}, rpcReadyTimeout, 200*time.Millisecond, "real L1 indexer did not confirm SDK transaction %s", txid)
	assertRGB11RegtestCoreConfirmation(t, txid)
}

func assertRGB11RegtestCoreConfirmation(t *testing.T, txid string) {
	t.Helper()
	var transaction struct {
		BlockHash     string `json:"blockhash"`
		Confirmations int    `json:"confirmations"`
	}
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t,
		"getrawtransaction", txid, "true"), &transaction))
	require.NotEmpty(t, transaction.BlockHash)
	require.Positive(t, transaction.Confirmations)
	var block struct {
		Tx []string `json:"tx"`
	}
	require.NoError(t, json.Unmarshal(realRegtestBitcoinCLI(t,
		"getblock", transaction.BlockHash, "1"), &block))
	require.Contains(t, block.Tx, txid)
}
