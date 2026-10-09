//go:build rgb11regtest

package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/invoicing"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type regtestEsplora struct {
	base   string
	client *http.Client
}

type regtestTxStatus struct {
	Confirmed   bool   `json:"confirmed"`
	BlockHeight int64  `json:"block_height"`
	BlockHash   string `json:"block_hash"`
}

type regtestTx struct {
	TxID   string          `json:"txid"`
	Status regtestTxStatus `json:"status"`
	Vout   []struct {
		Value    int64  `json:"value"`
		PkScript string `json:"scriptpubkey"`
	} `json:"vout"`
}

type regtestAddressUTXO struct {
	TxID   string          `json:"txid"`
	Vout   uint32          `json:"vout"`
	Value  int64           `json:"value"`
	Status regtestTxStatus `json:"status"`
}

type regtestOutspend struct {
	Spent bool   `json:"spent"`
	TxID  string `json:"txid"`
	Vin   uint32 `json:"vin"`
}

func newRegtestEsplora(base string) *regtestEsplora {
	return &regtestEsplora{
		base:   strings.TrimRight(base, "/"),
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (e *regtestEsplora) request(method, path, body string, target any) error {
	req, err := http.NewRequest(method, e.base+path, strings.NewReader(body))
	if err != nil {
		return err
	}
	if body != "" {
		req.Header.Set("Content-Type", "text/plain")
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("esplora %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if target == nil {
		return nil
	}
	if text, ok := target.(*string); ok {
		*text = strings.TrimSpace(string(data))
		return nil
	}
	return json.Unmarshal(data, target)
}

func (e *regtestEsplora) tx(txid string) (*regtestTx, error) {
	var item regtestTx
	if err := e.request(http.MethodGet, "/tx/"+txid, "", &item); err != nil {
		return nil, err
	}
	return &item, nil
}

func (e *regtestEsplora) tipHeight() (int64, error) {
	var text string
	if err := e.request(http.MethodGet, "/blocks/tip/height", "", &text); err != nil {
		return 0, err
	}
	return strconv.ParseInt(text, 10, 64)
}

func (e *regtestEsplora) GetUTXO(outpoint string) (*rgb11wallet.BitcoinUTXO, error) {
	point, err := wire.NewOutPointFromString(outpoint)
	if err != nil {
		return nil, err
	}
	tx, err := e.tx(point.Hash.String())
	if err != nil {
		return nil, err
	}
	if int(point.Index) >= len(tx.Vout) {
		return nil, fmt.Errorf("outpoint %s has no output", outpoint)
	}
	outspend, err := e.GetOutspend(outpoint)
	if err != nil {
		return nil, err
	}
	if outspend.Spent {
		return nil, fmt.Errorf("outpoint %s is spent by %s", outpoint, outspend.SpendingTx)
	}
	script, err := hex.DecodeString(tx.Vout[point.Index].PkScript)
	if err != nil {
		return nil, err
	}
	confirmations := int64(0)
	if tx.Status.Confirmed {
		tip, err := e.tipHeight()
		if err != nil {
			return nil, err
		}
		confirmations = tip - tx.Status.BlockHeight + 1
	}
	return &rgb11wallet.BitcoinUTXO{
		OutPoint: outpoint, Value: tx.Vout[point.Index].Value,
		PkScript: script, Confirmations: confirmations,
	}, nil
}

func (e *regtestEsplora) GetRawTx(txid string) ([]byte, error) {
	var data string
	if err := e.request(http.MethodGet, "/tx/"+txid+"/hex", "", &data); err != nil {
		return nil, err
	}
	return hex.DecodeString(data)
}

func (e *regtestEsplora) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	// Esplora's /status returns confirmed=false even for an unknown txid.
	// Transaction info must exist before it can be classified as mempool-visible.
	tx, err := e.tx(txid)
	if err != nil {
		return nil, err
	}
	status := tx.Status
	confirmations := int64(0)
	if status.Confirmed {
		tip, err := e.tipHeight()
		if err != nil {
			return nil, err
		}
		confirmations = tip - status.BlockHeight + 1
	}
	return &rgb11wallet.BitcoinTxStatus{
		TxID: txid, InMempool: !status.Confirmed, Confirmed: status.Confirmed,
		BlockHeight: status.BlockHeight, BlockHash: status.BlockHash, Confirmations: confirmations,
	}, nil
}

func TestRegtestEsploraTransactionPresence(t *testing.T) {
	known, unknown := strings.Repeat("1", 64), strings.Repeat("0", 64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/status") {
			fmt.Fprint(w, `{"confirmed":false}`)
			return
		}
		if r.URL.Path == "/tx/"+known {
			fmt.Fprintf(w, `{"txid":%q,"status":{"confirmed":false},"vout":[]}`, known)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	esplora := newRegtestEsplora(server.URL)
	if status, err := esplora.GetTxStatus(unknown); err == nil || status != nil {
		t.Fatalf("nonexistent transaction treated as visible: status=%+v err=%v", status, err)
	}
	if status, err := esplora.GetTxStatus(known); err != nil || status == nil || !status.InMempool || status.Confirmed {
		t.Fatalf("known unconfirmed transaction not recognized: status=%+v err=%v", status, err)
	}
}

func (e *regtestEsplora) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	point, err := wire.NewOutPointFromString(outpoint)
	if err != nil {
		return nil, err
	}
	var item regtestOutspend
	path := fmt.Sprintf("/tx/%s/outspend/%d", point.Hash, point.Index)
	if err := e.request(http.MethodGet, path, "", &item); err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinOutspend{Spent: item.Spent, SpendingTx: item.TxID, Vin: item.Vin}, nil
}

func (e *regtestEsplora) GetTip() (*rgb11wallet.BitcoinTip, error) {
	height, err := e.tipHeight()
	if err != nil {
		return nil, err
	}
	var hash string
	if err := e.request(http.MethodGet, "/block-height/"+strconv.FormatInt(height, 10), "", &hash); err != nil {
		return nil, err
	}
	return &rgb11wallet.BitcoinTip{Height: height, BlockHash: hash}, nil
}

func (e *regtestEsplora) Broadcast(rawTx []byte) (string, error) {
	tx := wire.NewMsgTx(wire.TxVersion)
	if err := tx.Deserialize(bytes.NewReader(rawTx)); err != nil {
		return "", err
	}
	txid := tx.TxHash().String()
	var response string
	if err := e.request(http.MethodPost, "/tx", hex.EncodeToString(rawTx), &response); err != nil {
		if _, statusErr := e.GetTxStatus(txid); statusErr == nil {
			return txid, nil
		}
		return "", err
	}
	if response != txid {
		return "", fmt.Errorf("broadcast txid=%s, want %s", response, txid)
	}
	return txid, nil
}

func (e *regtestEsplora) addressUTXOs(address string) ([]regtestAddressUTXO, error) {
	var items []regtestAddressUTXO
	err := e.request(http.MethodGet, "/address/"+address+"/utxo", "", &items)
	return items, err
}

func populateRegtestIndexer(e *regtestEsplora, rpc *rgb11FlowIndexer, addresses ...string) error {
	rpc.outputs = make(map[string]*TxOutput)
	rpc.plain = nil
	for _, address := range addresses {
		items, err := e.addressUTXOs(address)
		if err != nil {
			return err
		}
		for _, item := range items {
			outpoint := fmt.Sprintf("%s:%d", item.TxID, item.Vout)
			if rpc.outputs[outpoint] != nil {
				continue
			}
			utxo, err := e.GetUTXO(outpoint)
			if err != nil {
				return err
			}
			output := indexer.NewTxOutput(utxo.Value)
			output.OutPointStr = outpoint
			output.OutValue.PkScript = append([]byte(nil), utxo.PkScript...)
			rpc.outputs[outpoint] = output
			// Direct regtest funding uses coinbase outputs. Keep immature outputs
			// available as Bitcoin evidence, but never offer them as fee inputs.
			if utxo.Confirmations < 101 {
				continue
			}
			rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
				OutPoint: outpoint, Value: utxo.Value, PkScript: append([]byte(nil), utxo.PkScript...),
			})
		}
	}
	return nil
}

func runRegtestCommand(t *testing.T, dir, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return bytes.TrimSpace(output)
}

func mineRegtest(t *testing.T, composeDir, address string, blocks int) {
	t.Helper()
	if binary := os.Getenv("RGB11_REGTEST_BITCOIN_CLI"); binary != "" {
		dataDir := requiredRegtestEnv(t, "RGB11_REGTEST_BITCOIN_DATADIR")
		runRegtestCommand(t, "", binary, "-datadir="+dataDir, "-regtest", "-rpcport="+requiredRegtestEnv(t, "RGB11_REGTEST_BITCOIN_RPC_PORT"),
			"generatetoaddress", strconv.Itoa(blocks), address)
		return
	}
	runRegtestCommand(t, composeDir, "docker", "compose", "exec", "-T", "bitcoin-core",
		"bitcoin-cli", "-regtest", "generatetoaddress", strconv.Itoa(blocks), address)
}

func waitRegtest(t *testing.T, description string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("timeout waiting for %s", description)
}

func officialJSON(t *testing.T, binary string, args ...string) map[string]any {
	t.Helper()
	output := runRegtestCommand(t, "", binary, args...)
	var value map[string]any
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatalf("decode official output %q: %v", output, err)
	}
	return value
}

func optionalOfficialJSON(binary string, args ...string) (map[string]any, error) {
	output, err := exec.Command(binary, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, output)
	}
	var value map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output), &value); err != nil {
		return nil, err
	}
	return value, nil
}

func requiredRegtestEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Fatalf("%s is required for the requested live regtest interop test", name)
	}
	return value
}

func requireWalletLiveNetwork(t *testing.T) {
	t.Helper()
	if os.Getenv("SAT20WALLET_RUN_LIVE_NETWORK_TESTS") != "1" {
		t.Skip("SAT20WALLET_RUN_LIVE_NETWORK_TESTS=1 is required for live wallet tests")
	}
}

func TestRGB11RegtestOfficialBidirectional(t *testing.T) {
	requireWalletLiveNetwork(t)
	esploraURL := requiredRegtestEnv(t, "RGB11_REGTEST_ESPLORA")
	officialBin := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_BIN")
	officialAlice := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_ALICE")
	officialBob := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_BOB")
	composeDir := os.Getenv("RGB11_REGTEST_COMPOSE_DIR")
	if os.Getenv("RGB11_REGTEST_BITCOIN_CLI") == "" && composeDir == "" {
		t.Fatal("a local Bitcoin CLI or disposable compose directory is required")
	}
	assetID := requiredRegtestEnv(t, "RGB11_REGTEST_ASSET_ID")
	schema := os.Getenv("RGB11_REGTEST_SCHEMA")
	if schema == "" {
		schema = "NIA"
	}
	if schema != "NIA" && schema != "IFA" && schema != "UDA" {
		t.Fatalf("unsupported live schema %s", schema)
	}
	artifactDir := os.Getenv("RGB11_REGTEST_EVIDENCE_DIR")
	if artifactDir == "" {
		artifactDir = t.TempDir()
	}
	if err := os.MkdirAll(artifactDir, 0o700); err != nil {
		t.Fatal(err)
	}

	previousChain := _chain
	_chain = "regtest"
	t.Cleanup(func() { _chain = previousChain })

	goWallet, mnemonic, err := NewInteralWallet(&chaincfg.RegressionNetParams)
	if err != nil || goWallet == nil {
		t.Fatalf("create Go regtest wallet: %v", err)
	}
	esplora := newRegtestEsplora(esploraURL)
	var genesisHash string
	if err := esplora.request(http.MethodGet, "/block-height/0", "", &genesisHash); err != nil {
		t.Fatal(err)
	}
	if genesisHash != chaincfg.RegressionNetParams.GenesisHash.String() {
		t.Fatalf("live environment is not Bitcoin regtest: genesis=%s", genesisHash)
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	dbPath := filepath.Join(artifactDir, "go-wallet-db")
	manager := newRGB11FlowManagerAt(t, goWallet, rpc, esplora, 1103, dbPath)

	// Give the Go wallet confirmed ordinary fee UTXOs before any RGB allocation
	// is projected. The isolated chain is disposable and the coinbase outputs
	// are never reused outside this test.
	mineRegtest(t, composeDir, goWallet.GetAddress(), 101)
	waitRegtest(t, "Go wallet funding", func() bool {
		items, err := esplora.addressUTXOs(goWallet.GetAddress())
		if err != nil {
			return false
		}
		tip, err := esplora.tipHeight()
		if err != nil {
			return false
		}
		for _, item := range items {
			if item.Status.Confirmed && tip-item.Status.BlockHeight+1 >= 101 {
				return true
			}
		}
		return false
	})
	if err := populateRegtestIndexer(esplora, rpc, goWallet.GetAddress()); err != nil {
		t.Fatal(err)
	}

	officialToGo, goToOfficial := uint64(50), uint64(20)
	assignment, receiveAssignment := "fungible", "fungible"
	if schema == "UDA" {
		officialToGo, goToOfficial, assignment, receiveAssignment = 1, 1, "uda", "any"
	}
	aliceBefore := officialJSON(t, officialBin, "balance", officialAlice, assetID)
	aliceInitial, ok := aliceBefore["settled"].(float64)
	if !ok || aliceInitial < float64(officialToGo) {
		t.Fatalf("official Alice initial balance=%v", aliceBefore)
	}
	goReceive, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", ContractID: assetID, AmountRaw: strconv.FormatUint(officialToGo, 10),
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, receiveAddresses, _, err := txscript.ExtractPkScriptAddrs(goReceive.WitnessScript, &chaincfg.RegressionNetParams)
	if err != nil || len(receiveAddresses) != 1 {
		t.Fatalf("resolve actual witness receive address: %v", err)
	}
	receiveAddress := receiveAddresses[0].EncodeAddress()
	parsedGoInvoice, err := invoicing.Parse(goReceive.Invoice)
	if err != nil {
		t.Fatal(err)
	}
	officialSend := officialJSON(t, officialBin, "send", officialAlice, esploraURL, assetID,
		parsedGoInvoice.Beneficiary.String(), strconv.FormatUint(officialToGo, 10), "true", assignment)
	officialTxID, _ := officialSend["txid"].(string)
	binaryConsignment, _ := officialSend["consignment"].(string)
	if officialTxID == "" || binaryConsignment == "" {
		t.Fatalf("unexpected official send output: %+v", officialSend)
	}
	waitRegtest(t, "official transfer Bitcoin witness", func() bool {
		status, err := esplora.GetTxStatus(officialTxID)
		return err == nil && (status.InMempool || status.Confirmed)
	})
	officialStrictFile, err := os.ReadFile(binaryConsignment)
	if err != nil {
		t.Fatal(err)
	}
	officialConsignmentHash := sha256.Sum256(officialStrictFile)
	receipt, err := manager.AcceptRGB11Consignment(context.Background(), goReceive.RequestID, officialStrictFile)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractID != assetID {
		t.Fatalf("official transfer contract=%s, want %s", receipt.ContractID, assetID)
	}
	if len(receipt.Allocations) == 0 {
		t.Fatal("official receive produced no allocation")
	}
	var receivedAllocation *rgb11wallet.ValidatedAllocation
	for i := range receipt.Allocations {
		allocation := &receipt.Allocations[i]
		if allocation.AssignmentType != 4000 || !strings.HasPrefix(allocation.OutPoint, officialTxID+":") {
			continue
		}
		utxo, err := esplora.GetUTXO(allocation.OutPoint)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(utxo.PkScript, goReceive.WitnessScript) {
			receivedAllocation = allocation
			break
		}
	}
	if receivedAllocation == nil || receivedAllocation.Amount.Value.Uint64() != officialToGo {
		t.Fatalf("actual recipient asset allocation missing or wrong: %+v", receipt.Allocations)
	}
	if schema == "UDA" && (receivedAllocation.StateClass != "structured" || len(receivedAllocation.StateData) != 12) {
		t.Fatalf("UDA token assignment not preserved: %+v", receivedAllocation)
	}
	mineRegtest(t, composeDir, goWallet.GetAddress(), 1)
	waitRegtest(t, "official-to-Go confirmation", func() bool {
		status, err := esplora.GetTxStatus(officialTxID)
		return err == nil && status.Confirmed
	})
	officialJSON(t, officialBin, "refresh", officialAlice, esploraURL, assetID)
	if err := populateRegtestIndexer(esplora, rpc, goWallet.GetAddress(), receiveAddress); err != nil {
		t.Fatal(err)
	}
	confirmedReceive, err := manager.rgbManager.projectionStore.LoadTransferState(receipt.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("receive status before confirmed-chain refresh: %s", confirmedReceive.Status)
	if _, err := manager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	confirmedReceive, err = manager.rgbManager.projectionStore.LoadTransferState(receipt.TransferID)
	if err != nil || confirmedReceive == nil || confirmedReceive.Status != "settled" {
		t.Fatalf("confirmed receive not settled: receive=%+v err=%v", confirmedReceive, err)
	}
	goBalance, err := manager.GetRGB11AssetBalance(&receivedAllocation.AssetName)
	if err != nil || goBalance == nil || goBalance.Value.Uint64() != officialToGo {
		t.Fatalf("Go receive balance=%v err=%v", goBalance, err)
	}

	bobSettledBefore := float64(0)
	if bobBalanceBefore, balanceErr := optionalOfficialJSON(officialBin, "balance", officialBob, assetID); balanceErr == nil {
		var ok bool
		bobSettledBefore, ok = bobBalanceBefore["settled"].(float64)
		if !ok {
			t.Fatalf("official Bob pre-transfer settled balance=%v", bobBalanceBefore["settled"])
		}
	} else if !strings.Contains(balanceErr.Error(), "AssetNotFound") {
		t.Fatal(balanceErr)
	}
	officialReceive := officialJSON(t, officialBin, "receive-witness", officialBob, "-",
		strconv.FormatUint(goToOfficial, 10), receiveAssignment)
	officialInvoice, _ := officialReceive["invoice"].(string)
	if officialInvoice == "" {
		t.Fatalf("unexpected official receive output: %+v", officialReceive)
	}
	parsedOfficialInvoice, err := invoicing.Parse(officialInvoice)
	if err != nil {
		t.Fatal(err)
	}
	contractID, err := consensus.ParseContractID(assetID)
	if err != nil {
		t.Fatal(err)
	}
	parsedOfficialInvoice.Contract = &contractID
	officialInvoice = parsedOfficialInvoice.String()
	prepared, err := manager.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{
		Invoice: officialInvoice, ContractID: assetID, AmountRaw: strconv.FormatUint(goToOfficial, 10), FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	goArmor := filepath.Join(artifactDir, "go-to-official.rgba")
	goBinary := filepath.Join(artifactDir, "go-to-official.rgb")
	if err := os.WriteFile(goArmor, []byte(prepared.RecipientConsignment), 0o600); err != nil {
		t.Fatal(err)
	}
	goConsignmentHash := sha256.Sum256([]byte(prepared.RecipientConsignment))
	officialJSON(t, officialBin, "dearmor", goArmor, goBinary)
	officialAccept := officialJSON(t, officialBin, "accept", officialBob, esploraURL, goBinary)
	if len(officialAccept) == 0 {
		t.Fatal("official Bob returned an empty acceptance result")
	}
	broadcastTxID, err := manager.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatal(err)
	}
	if broadcastTxID != prepared.TxID {
		t.Fatalf("Go broadcast txid=%s, prepared=%s", broadcastTxID, prepared.TxID)
	}
	mineRegtest(t, composeDir, goWallet.GetAddress(), 1)
	waitRegtest(t, "Go-to-official confirmation", func() bool {
		status, err := esplora.GetTxStatus(prepared.TxID)
		return err == nil && status.Confirmed
	})
	officialJSON(t, officialBin, "refresh", officialBob, esploraURL, assetID)
	bobBalance := officialJSON(t, officialBin, "balance", officialBob, assetID)
	settled, ok := bobBalance["settled"].(float64)
	if !ok || uint64(settled) != uint64(bobSettledBefore)+goToOfficial {
		t.Fatalf("official Bob settled balance=%v, before=%v", bobBalance["settled"], bobSettledBefore)
	}
	if _, err := manager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	pending, err := manager.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != "settled" || len(pending.RecipientConsignment) != 0 {
		t.Fatalf("settled Go transfer was not compacted: status=%s recipient_bytes=%d",
			pending.State.Status, len(pending.RecipientConsignment))
	}
	// Every CLI invocation reloads the official on-disk wallet. Check the actual
	// receive assignment and final state, rather than inferring them from balance.
	transfersRaw := runRegtestCommand(t, "", officialBin, "transfers", officialBob, assetID)
	var transfers []struct {
		Status      string            `json:"status"`
		TxID        string            `json:"txid"`
		Assignments []json.RawMessage `json:"assignments"`
	}
	if err := json.Unmarshal(transfersRaw, &transfers); err != nil {
		t.Fatal(err)
	}
	settledReceive := false
	for _, transfer := range transfers {
		if transfer.TxID != prepared.TxID || transfer.Status != "Settled" {
			continue
		}
		if len(transfer.Assignments) != 1 {
			t.Fatalf("official assignments=%s", transfersRaw)
		}
		want := fmt.Sprintf(`{"Fungible":%d}`, goToOfficial)
		if schema == "UDA" {
			want = `"NonFungible"`
		}
		if string(transfer.Assignments[0]) != want {
			t.Fatalf("official assignment=%s want=%s", transfer.Assignments[0], want)
		}
		settledReceive = true
	}
	if !settledReceive {
		t.Fatalf("official receive not Settled: %s", transfersRaw)
	}
	beforeRestart, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.scopeStates.stopReconciliations()
	if err := manager.db.Close(); err != nil {
		t.Fatal(err)
	}
	manager.db = nil
	goWallet = NewInternalWalletWithMnemonic(mnemonic, "", &chaincfg.RegressionNetParams)
	manager = newRGB11FlowManagerAt(t, goWallet, rpc, esplora, 1103, dbPath)
	if _, err := manager.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	remaining, err := manager.GetRGB11AssetBalance(&receivedAllocation.AssetName)
	remainingAmount, remainingText := uint64(0), "0"
	if remaining != nil {
		remainingAmount, remainingText = remaining.Value.Uint64(), remaining.Value.String()
	}
	// The SDK returns nil when no allocation remains, including a UDA sent in full.
	if err != nil || remainingAmount != officialToGo-goToOfficial {
		t.Fatalf("reopened Go balance=%v err=%v", remaining, err)
	}
	afterRestart, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if len(beforeRestart.Proofs) != len(afterRestart.Proofs) {
		t.Fatalf("reopened proof count=%d want=%d", len(afterRestart.Proofs), len(beforeRestart.Proofs))
	}
	beforeProofs := make(map[string]*rgb11wallet.AllocationProof, len(beforeRestart.Proofs))
	for _, proof := range beforeRestart.Proofs {
		beforeProofs[proof.OutPoint] = proof
	}
	for _, proof := range afterRestart.Proofs {
		before := beforeProofs[proof.OutPoint]
		if before == nil || before.AssetName != proof.AssetName {
			t.Fatalf("reopened proof identity mismatch: %+v", proof)
		}
		if before.OperationID != proof.OperationID || before.Status != proof.Status || before.StateClass != proof.StateClass ||
			!bytes.Equal(before.StateData, proof.StateData) || !bytes.Equal(before.SealDisclosure, proof.SealDisclosure) ||
			before.SealCommitment != proof.SealCommitment || before.ConsignmentHash != proof.ConsignmentHash {
			t.Fatalf("reopened proof changed: %s", proof.OutPoint)
		}
	}
	bobReopened := officialJSON(t, officialBin, "balance", officialBob, assetID)
	if bobReopened["settled"] != settled {
		t.Fatalf("reopened official balance=%v want=%v", bobReopened["settled"], settled)
	}
	aliceReopened := officialJSON(t, officialBin, "balance", officialAlice, assetID)
	if aliceReopened["settled"] != aliceInitial-float64(officialToGo) {
		t.Fatalf("reopened Alice balance=%v want=%v", aliceReopened["settled"], aliceInitial-float64(officialToGo))
	}
	for _, path := range []string{binaryConsignment, goArmor, goBinary} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	summary := map[string]any{
		"network":                     "regtest",
		"official_rgb_lib_commit":     "538f2abaa67d7ce96be32d94092e8f1b9e3ea38e",
		"asset_id":                    assetID,
		"schema":                      schema,
		"official_receive_settled":    settledReceive,
		"wallet_restart_verified":     true,
		"go_balance_after_restart":    remainingText,
		"alice_balance_after_restart": aliceReopened["settled"],
		"official_to_go": map[string]any{
			"txid": officialTxID, "amount": officialToGo,
			"consignment_sha256": hex.EncodeToString(officialConsignmentHash[:]),
			"go_balance":         goBalance.Value.String(),
		},
		"go_to_official": map[string]any{
			"txid": broadcastTxID, "amount": goToOfficial,
			"consignment_sha256": hex.EncodeToString(goConsignmentHash[:]),
			"bob_balance_before": uint64(bobSettledBefore), "bob_balance_after": uint64(settled),
		},
		"consignments_deleted_after_persist": true,
		"go_recipient_consignment_compacted": true,
	}
	summaryRaw, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "bidirectional-summary.json"), append(summaryRaw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Logf("official->Go txid=%s amount=%d", officialTxID, officialToGo)
	t.Logf("Go->official txid=%s amount=%d", prepared.TxID, goToOfficial)
}
