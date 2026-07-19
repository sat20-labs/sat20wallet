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
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
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
	var status regtestTxStatus
	if err := e.request(http.MethodGet, "/tx/"+txid+"/status", "", &status); err != nil {
		return nil, err
	}
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

func populateRegtestIndexer(e *regtestEsplora, rpc *rgb11FlowIndexer, address string) error {
	items, err := e.addressUTXOs(address)
	if err != nil {
		return err
	}
	rpc.outputs = make(map[string]*TxOutput, len(items))
	rpc.plain = make([]*indexerwire.TxOutputInfo, 0, len(items))
	for _, item := range items {
		outpoint := fmt.Sprintf("%s:%d", item.TxID, item.Vout)
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
		t.Skipf("%s is required for the live regtest interop test", name)
	}
	return value
}

func TestRGB11RegtestOfficialBidirectional(t *testing.T) {
	esploraURL := requiredRegtestEnv(t, "RGB11_REGTEST_ESPLORA")
	officialBin := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_BIN")
	officialAlice := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_ALICE")
	officialBob := requiredRegtestEnv(t, "RGB11_REGTEST_OFFICIAL_BOB")
	composeDir := requiredRegtestEnv(t, "RGB11_REGTEST_COMPOSE_DIR")
	assetID := requiredRegtestEnv(t, "RGB11_REGTEST_ASSET_ID")
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

	goWallet, _, err := NewInteralWallet(&chaincfg.RegressionNetParams)
	if err != nil || goWallet == nil {
		t.Fatalf("create Go regtest wallet: %v", err)
	}
	esplora := newRegtestEsplora(esploraURL)
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	manager := newRGB11FlowManager(t, goWallet, rpc, esplora, 1103)

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

	const officialToGo = uint64(50)
	goReceive, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", ContractID: assetID, AmountRaw: strconv.FormatUint(officialToGo, 10),
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	parsedGoInvoice, err := invoicing.Parse(goReceive.Invoice)
	if err != nil {
		t.Fatal(err)
	}
	officialSend := officialJSON(t, officialBin, "send", officialAlice, esploraURL, assetID,
		parsedGoInvoice.Beneficiary.String(), strconv.FormatUint(officialToGo, 10), "true")
	officialTxID, _ := officialSend["txid"].(string)
	binaryConsignment, _ := officialSend["consignment"].(string)
	if officialTxID == "" || binaryConsignment == "" {
		t.Fatalf("unexpected official send output: %+v", officialSend)
	}
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
	mineRegtest(t, composeDir, goWallet.GetAddress(), 1)
	waitRegtest(t, "official-to-Go confirmation", func() bool {
		status, err := esplora.GetTxStatus(officialTxID)
		return err == nil && status.Confirmed
	})
	officialJSON(t, officialBin, "refresh", officialAlice, esploraURL, assetID)
	if err := populateRegtestIndexer(esplora, rpc, goWallet.GetAddress()); err != nil {
		t.Fatal(err)
	}
	goBalance, err := manager.GetRGB11AssetBalance(&receipt.Allocations[0].AssetName)
	if err != nil || goBalance.Value.Uint64() < officialToGo {
		t.Fatalf("Go receive balance=%v err=%v", goBalance, err)
	}

	const goToOfficial = uint64(20)
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
		strconv.FormatUint(goToOfficial, 10))
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
		Invoice: officialInvoice, FeeRate: 2, MinConfirmations: 1,
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
	for _, path := range []string{binaryConsignment, goArmor, goBinary} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	summary := map[string]any{
		"network":                 "regtest",
		"official_rgb_lib_commit": "538f2abaa67d7ce96be32d94092e8f1b9e3ea38e",
		"asset_id":                assetID,
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
