package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/sat20-labs/satoshinet/wire"
)

// A private test handoff from real issuance and paid DKVS. The browser reads
// and decrypts the backup through public APIs. The cold local-channel case also
// uses encoded persisted device records; no production test endpoint is added.
func coreRGBBrowserE2E(t *testing.T, source *Manager, cfg coreE2EConfig, chain *coreE2EChain, material *coreRecoveryMaterial) {
	t.Helper()
	reference := coreRestoreE2E(t, cfg, chain, material)
	type scope struct {
		Fingerprint string            `json:"fingerprint"`
		Index       uint32            `json:"index"`
		State       any               `json:"state"`
		Locks       map[string]string `json:"locks"`
	}
	var scopes []scope
	nonempty := 0
	for _, entry := range reference.GetWalletCatalog() {
		coreRequire(t, "select recovered native RGB reference", reference.SwitchWallet(entry.ID, coreE2EPassword))
		for _, sub := range entry.Accounts {
			reference.SwitchAccount(sub.Index)
			state, err := reference.GetRGB11State()
			coreRequire(t, "read recovered native RGB reference", err)
			if len(state.Proofs) > 0 {
				nonempty++
			}
			scopes = append(scopes, scope{entry.Fingerprint, sub.Index, state, coreRGBSemantic(t, reference).Locked})
		}
	}
	coreAssert(t, nonempty >= 3, "browser fixture must contain actual issued RGB data in three scopes")
	public := AccountPublicLocator{Version: account.Version, Network: "testnet", StorageLocation: material.auth.Location,
		StorageMode: material.auth.Mode, AutopayContract: cfg.Contract, Locator: material.locator}
	coreRequire(t, "sign browser public locator", source.SignAccountPublicLocator(&public))
	locator, err := EncodeAccountPublicLocator(public, "testnet")
	coreRequire(t, "encode real browser recovery locator", err)
	share, err := account.EncodeRecoveryShare(material.userShare)
	coreRequire(t, "encode real browser user share", err)
	configuration := &sdkcommon.Config{Env: "test", Chain: "testnet", Mode: LIGHT_NODE,
		IndexerL1: &sdkcommon.Indexer{Scheme: cfg.Core.Scheme, Host: cfg.Core.Host, Proxy: cfg.Core.Proxy},
		IndexerL2: &sdkcommon.Indexer{Scheme: cfg.Core.Scheme, Host: cfg.Core.Host, Proxy: cfg.Core.Proxy},
		Peers:     []string{cfg.CorePeer, cfg.BootstrapPeer}}
	// Export the same controlled BTC facts used by the native issuance tests.
	// Only the HTTP transport boundary is supplied; RGB decoding and validation
	// stay inside the real browser SDK.
	chain.mu.Lock()
	utxos := map[string]indexerwire.BitcoinUTXOStatus{}
	outspends := map[string]indexerwire.BitcoinOutspend{}
	raw := map[string]indexerwire.BitcoinRawTx{}
	statuses := map[string]indexerwire.BitcoinTxStatus{}
	for point, output := range chain.outputs {
		status := chain.status[strings.Split(point, ":")[0]]
		utxos[point] = indexerwire.BitcoinUTXOStatus{Outpoint: point, Exists: true, Unspent: chain.spent[point] == "",
			Value: output.OutValue.Value, PkScript: hex.EncodeToString(output.OutValue.PkScript), Confirmations: status.Confirmations, BlockHash: status.BlockHash}
		outspends[point] = indexerwire.BitcoinOutspend{Outpoint: point, Exists: true, Spent: chain.spent[point] != "", SpendingTx: chain.spent[point]}
	}
	for id, bytes := range chain.raw {
		raw[id] = indexerwire.BitcoinRawTx{TxID: id, RawTx: hex.EncodeToString(bytes)}
	}
	for id, status := range chain.status {
		statuses[id] = indexerwire.BitcoinTxStatus{TxID: id, Exists: true, InMempool: status.InMempool,
			Confirmed: status.Confirmed, BlockHeight: status.BlockHeight, BlockHash: status.BlockHash, Confirmations: status.Confirmations}
	}
	chain.mu.Unlock()
	tip, err := chain.GetTip()
	coreRequire(t, "read controlled browser Bitcoin tip", err)
	evidence := map[string]any{"utxos": utxos, "outspends": outspends, "raw": raw, "status": statuses,
		"tip": indexerwire.BitcoinTip{Height: tip.Height, BlockHash: tip.BlockHash}}
	// Local channel work is not part of account/RGB recovery. Supply the same
	// persisted funding records a PWA keeps across a reload, using the existing
	// native restart fixture and production encoders. No transaction is sent.
	pendingDB := newMemoryKVDB()
	channelID, err := source.GetChannelAddress()
	coreRequire(t, "derive browser pending channel address", err)
	const pendingID int64 = 9301
	stored := savePendingFundingFixture(t, pendingDB, source.wallet.(*InternalWallet), channelID, pendingID)
	stored.RemoteChanCfg.PaymentKey = source.serverNode.Pubkey
	stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
	stored.ChannelHash = stored.CalcHash()
	coreRequire(t, "persist browser pending channel fixture", SaveChannelInDB(pendingDB, stored))
	pendingRecords := make(map[string]string)
	// WASM always runs as LIGHT_NODE, unlike the native CoreModules manager.
	// Keep the existing env/chain key namespace when handing off local records.
	browserPrefix := configuration.Env + "-" + configuration.Chain + "-"
	nativePrefix := GetDBKeyPrefix()
	for _, key := range []string{GetChannelKey(channelID), GetResvKey(RESV_TYPE_OPEN, pendingID)} {
		encoded, readErr := pendingDB.Read([]byte(key))
		coreRequire(t, "encode browser pending channel handoff", readErr)
		pendingRecords[browserPrefix+strings.TrimPrefix(key, nativePrefix)] = base64.StdEncoding.EncodeToString(encoded)
	}
	// A channel belongs to a device's process-local wallet ID, not the ID of a
	// new wallet restored on another device. Keep the complete local DB handoff
	// consistent, then let actual WASM perform both healthy and interrupted cold
	// startup. Only the existing light-node namespace differs from native mode.
	deviceRecords := make(map[string]string)
	coreRequire(t, "capture persisted local channel device", source.db.BatchRead(nil, false, func(key, value []byte) error {
		name := string(key)
		if !strings.HasPrefix(name, DB_KEY_WALLET) && name != DB_KEY_STATUS && !strings.HasPrefix(name, "rgb11-") {
			name = browserPrefix + strings.TrimPrefix(name, nativePrefix)
		}
		deviceRecords[name] = base64.StdEncoding.EncodeToString(value)
		return nil
	}))
	for key, value := range pendingRecords {
		deviceRecords[key] = value
	}
	encodedStatus, err := base64.StdEncoding.DecodeString(deviceRecords[DB_KEY_STATUS])
	coreRequire(t, "decode handed-off SDK selection", err)
	var selection Status
	coreRequire(t, "read handed-off SDK selection", decodeStatusFromBytes(encodedStatus, &selection))
	encodedWallet, err := base64.StdEncoding.DecodeString(deviceRecords[getWalletDBKey(selection.CurrentWallet)])
	coreRequire(t, "decode handed-off selected wallet", err)
	var selectedWallet WalletInDB
	coreRequire(t, "read handed-off selected wallet", DecodeFromBytes(encodedWallet, &selectedWallet))
	coreAssert(t, selectedWallet.Id == selection.CurrentWallet && int(selection.CurrentAccount) < selectedWallet.Accounts,
		"native device handoff has an unavailable SDK selection")
	fixture, err := json.Marshal(map[string]any{"config": configuration, "locator": locator, "user_share": share,
		"answers": coreAnswers(), "password": coreE2EPassword, "root_mnemonic": coreE2ESenderMnemonic,
		"maintenance_mnemonic": cfg.MaintenanceMnemonic,
		"bitcoin_evidence":     evidence, "scopes": scopes, "wallet_count": len(reference.GetWalletCatalog()),
		"pending_channel": map[string]any{"id": channelID, "reservation_id": pendingID, "records": pendingRecords,
			"device_records": deviceRecords, "wallet_id": strconv.FormatInt(source.wallet.GetId(), 10),
			"import_marker_key": browserPrefix + strings.TrimPrefix(string(accountManagedDataImportKey()), nativePrefix)}})
	coreRequire(t, "encode private native/browser handoff", err)
	path := filepath.Join(t.TempDir(), "rgb-browser-fixture.json")
	coreRequire(t, "write private native/browser handoff", os.WriteFile(path, fixture, 0600))
	_, file, _, ok := runtime.Caller(0)
	coreAssert(t, ok, "resolve existing PWA runner")
	pwa := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "pwa"))
	// Recovery and real delayed AUTOPAY funding share this process; individual operation timeouts
	// remain in the browser runner.
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/verify/account-management-e2e.mjs", "--rgb-fixture", path)
	command.Dir = pwa
	before := chain.broadcastCount()
	output, err := command.CombinedOutput()
	t.Logf("RGB browser verdicts:\n%s", output)
	coreAssert(t, chain.broadcastCount() == before, "browser recovery broadcast a Bitcoin transaction")
	coreRequire(t, "real PWA multiwallet RGB recovery and failure retry", err)
	verifyBrowserAutopayFundingTransactions(t, source, output)
}

// Decode the actual signed transactions collected at the existing browser
// broadcast boundary. Operation-log/request parameters alone are insufficient
// evidence that the approved quotation reached the transaction.
func verifyBrowserAutopayFundingTransactions(t *testing.T, source *Manager, output []byte) {
	t.Helper()
	type funding struct {
		RawTx       string `json:"raw_tx"`
		TxID        string `json:"transaction_id"`
		Rate        string `json:"rate"`
		Amount      string `json:"amount"`
		Asset       string `json:"asset"`
		Contract    string `json:"contract"`
		QuoteHeight uint64 `json:"quote_height"`
	}
	var proofs []funding
	for _, line := range strings.Split(string(output), "\n") {
		var entry struct {
			Transactions []funding `json:"autopay_funding_confirmation_transactions"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Transactions != nil {
			coreAssert(t, proofs == nil, "duplicate browser funding proof")
			proofs = entry.Transactions
		}
	}
	coreAssert(t, len(proofs) == 3, "browser must verify below/equal/above-required funding quotations")
	for _, proof := range proofs {
		raw, err := hex.DecodeString(proof.RawTx)
		coreRequire(t, "decode browser signed funding transaction", err)
		var tx wire.MsgTx
		coreRequire(t, "deserialize actual SatoshiNet funding transaction", tx.Deserialize(bytes.NewReader(raw)))
		coreAssert(t, tx.TxHash().String() == proof.TxID, "funding response TXID does not match signed transaction")
		authoritative, err := source.l2IndexerClient.GetRawTx(proof.TxID)
		coreRequire(t, "read actual funding transaction from isolated node", err)
		authoritativeBytes, err := hex.DecodeString(authoritative)
		coreRequire(t, "decode authoritative funding transaction", err)
		coreAssert(t, bytes.Equal(raw, authoritativeBytes), "node transaction differs from browser broadcast")
		invokeCount, fundingCount := 0, 0
		for _, out := range tx.TxOut {
			if invoke, err := contractcommon.ReadInvokeNullDataScript(out.PkScript); err == nil {
				invokeCount++
				coreAssert(t, invoke.Action == contractcommon.TemplateInvokeAPIConfig, "funding transaction is not Config")
				var param contractcommon.TemplateAutopayConfigInvokeParam
				coreRequire(t, "decode actual Config fee rate", param.Decode(invoke.Param))
				coreAssert(t, param.AmountPerBlock == proof.Rate, "transaction fee rate differs from confirmation")
			}
			address, isContract, err := contractcommon.ParseContractPkScript(out.PkScript, contractcommon.TestnetContractPrefix)
			coreRequire(t, "parse funding output contract", err)
			if !isContract {
				continue
			}
			fundingCount++
			coreAssert(t, address.MustEncode() == proof.Contract, "transaction funds another contract")
			matched := false
			for _, asset := range out.Assets {
				if asset.Name.String() != proof.Asset {
					continue
				}
				matched = true
				amount, err := decimalRat(asset.Amount.String())
				coreRequire(t, "decode actual funding amount", err)
				// When the fee asset is SGAS, the output also carries the
				// existing separate Result gas budget; it is not delegate principal.
				if proof.Asset == contractcommon.GetGasAssetName() {
					info, err := source.l2IndexerClient.GetTxInfo(proof.TxID)
					coreRequire(t, "read funding inclusion height", err)
					coreAssert(t, info != nil && info.BlockHeight > 0, "funding transaction is not included")
					fee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas, proof.QuoteHeight)
					coreRequire(t, "calculate Result gas budget", err)
					includedFee, err := contractcommon.GasFeeAtHeight(contractcommon.ResultBaseGas, uint64(info.BlockHeight))
					coreRequire(t, "check fixture gas epoch", err)
					coreAssert(t, fee == includedFee, "fixture crossed a gas-price epoch")
					gas, err := decimalRat(strconv.FormatInt(fee, 10))
					coreRequire(t, "decode Result gas amount", err)
					amount.Sub(amount, gas)
				}
				expected, err := decimalRat(proof.Amount)
				coreRequire(t, "decode approved principal", err)
				coreAssert(t, amount.Cmp(expected) == 0, "transaction principal differs from confirmation")
			}
			coreAssert(t, matched, "approved fee asset is missing from funding output")
		}
		coreAssert(t, invokeCount == 1 && fundingCount == 1, "funding transaction must contain one Config and funding output")
	}
}
