package e2e

import (
    "bufio"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "os"
    "strconv"
    "strings"
    "testing"
    "time"

    "github.com/sat20-labs/sat20wallet/sdk/wallet"
    "github.com/sat20-labs/satoshinet/chaincfg/chainhash"
    contract "github.com/sat20-labs/satoshinet/contract"
    "github.com/sat20-labs/satoshinet/wire"
    "github.com/stretchr/testify/require"
)

const reviewAssetA = "ordx:f:sdkreviewa"
const reviewAssetB = "ordx:f:sdkreviewb"

type sdkContractReviewFixture struct {
    network *realSatoshiNet
    owner, other, reader *wallet.Manager
    blocked bool
}

// Only initial fixture funding uses anchor/split builders. Every contract
// deployment/call/close/query goes through public SDK Manager methods.
func newSDKContractReviewFixture(t *testing.T) *sdkContractReviewFixture {
    t.Helper()
    f := newTemplateFixture(t, map[string]int64{reviewAssetA: 20000, reviewAssetB: 20000})
    ownerKey := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
    otherKey := newDKVSKeyPathActor(t, keyFromMnemonic(t, messageTopicMemberMnemonic, 0))
    var amounts, values []int64
    var actors []*dkvsKeyPathActor
    for i := 0; i < 10; i++ {
        amounts = append(amounts, 10000000); values = append(values, 1000000)
        if i < 6 { actors = append(actors, ownerKey) } else { actors = append(actors, otherKey) }
    }
    splitToDKVSKeyPathActors(t, f, f.gasAnchor, wallet.GetGasAssetName(), amounts, values, actors)
    for _, asset := range []string{reviewAssetA, reviewAssetB} {
        splitToDKVSKeyPathActors(t, f, f.assetAnchors[asset], asset,
            []int64{10000, 10000}, []int64{100000, 100000}, []*dkvsKeyPathActor{ownerKey, otherKey})
    }
    owner, _ := newWalletManagerForNode(t, f.Network.Core, dkvsClientMnemonic)
    other, _ := newWalletManagerForNode(t, f.Network.Core, messageTopicMemberMnemonic)
    reader, _ := newWalletManagerForNode(t, f.Network.Bootstrap, "")
    require.Equal(t, ownerKey.Address, owner.GetWallet().GetAddress())
    require.Equal(t, otherKey.Address, other.GetWallet().GetAddress())
    return &sdkContractReviewFixture{network: f.Network, owner: owner, other: other, reader: reader}
}

func (f *sdkContractReviewFixture) nodeDiagnostics(t *testing.T) {
    t.Helper()
    for _, node := range f.network.Nodes {
        file, err := os.Open(node.logFile)
        if err != nil { continue }
        var lines []string
        scan := bufio.NewScanner(file); scan.Buffer(make([]byte, 65536), 2<<20)
        seen := make(map[string]bool)
        for scan.Scan() {
            line := scan.Text(); lower := strings.ToLower(line)
            if !(strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.Contains(lower, "insufficient")) { continue }
            if !(strings.Contains(lower, "result") || strings.Contains(lower, "evm") || strings.Contains(lower, "template") || strings.Contains(lower, "gas")) { continue }
            if strings.Contains(lower, "mnemonic") || strings.Contains(lower, "private") || len(line) > 1200 || seen[line] { continue }
            seen[line] = true
            lines = append(lines, line)
            if len(lines) > 6 { lines = lines[1:] }
        }
        _ = file.Close()
        for _, line := range lines { t.Logf("contract-review: node[%s] %s", node.role, line) }
    }
}

func (f *sdkContractReviewFixture) mined(t *testing.T, result *wallet.ContractTxResult) (*wire.MsgTx, *wire.MsgBlock) {
    t.Helper()
    if f.blocked { t.Skip("contract-review: blocked by an earlier unmineable transaction in this fixture") }
    require.NotNil(t, result)
    hash, err := chainhash.NewHashFromStr(result.TxID); require.NoError(t, err)
    deadline := time.Now().Add(20 * time.Second)
    var lastGenerateError error
    for time.Now().Before(deadline) {
        info, err := f.network.Bootstrap.Client.GetRawTransactionVerbose(hash)
        if err == nil && info.BlockHash != "" && info.Confirmations > 0 {
            blockHash, err := chainhash.NewHashFromStr(info.BlockHash); require.NoError(t, err)
            block, err := f.network.Bootstrap.Client.GetBlock(blockHash); require.NoError(t, err)
            raw, err := f.network.Bootstrap.Client.GetRawTransaction(hash); require.NoError(t, err)
            require.Eventually(t, func() bool {
                for _, node := range f.network.Nodes { if _, err := node.Client.GetBlock(blockHash); err != nil { return false } }
                return true
            }, 20*time.Second, 100*time.Millisecond)
            return raw.MsgTx(), block
        }
        if _, err := f.network.Bootstrap.Client.Generate(1); err != nil { lastGenerateError = err }
        time.Sleep(150 * time.Millisecond)
    }
    f.blocked = true
    f.nodeDiagnostics(t)
    t.Fatalf("contract-review: SDK broadcast unconfirmed; last mining error=%v", lastGenerateError)
    return nil, nil
}

func sdkReviewResultStatus(t *testing.T, work *wire.MsgTx, block *wire.MsgBlock) contract.ResultStatus {
    t.Helper(); hash := work.TxHash()
    for _, result := range contractResultTxs(block) {
        for _, input := range result.TxIn {
            if input.PreviousOutPoint.Hash == hash {
                payload := requireResultPayload(t, result)
                require.Greater(t, payload.ResultCount, uint16(0))
                return payload.Status
            }
        }
    }
    t.Fatal("contract-review: mined work transaction has no matching canonical Result")
    return contract.ResultStatusInvalid
}

func (f *sdkContractReviewFixture) invoke(t *testing.T, manager *wallet.Manager, request *wallet.ContractInvokeRequest, expected contract.ResultStatus) (*wire.MsgTx, *wire.MsgBlock) {
    t.Helper()
    if f.blocked { t.Skip("contract-review: blocked by earlier unmineable transaction") }
    result, err := manager.InvokeUnifiedContract(request)
    if err != nil { t.Fatalf("contract-review: public InvokeUnifiedContract: %v", err) }
    work, block := f.mined(t, result)
    require.Equal(t, expected, sdkReviewResultStatus(t, work, block), "contract-review: broadcast success is not execution success")
    return work, block
}

func sdkReviewEVMCall(address string, calldata []byte) *wallet.ContractInvokeRequest {
    return &wallet.ContractInvokeRequest{ContractType: wallet.ContractTypeEVM, ContractAddress: address,
        Action: "call", GasLimit: 1000000, Param: hex.EncodeToString(calldata), ParamEncoding: "hex"}
}

func (f *sdkContractReviewFixture) deployEVM(t *testing.T, code []byte) string {
    t.Helper()
    request := &wallet.ContractDeployRequest{ContractType: wallet.ContractTypeEVM,
        ContractContent: hex.EncodeToString(code), ContentEncoding: "hex", GasLimit: 5000000, FundingValue: 1000}
    quote, err := f.owner.EstimateDeployUnifiedContract(request); require.NoError(t, err)
    require.NotZero(t, request.DeployNonce)
    again, err := f.owner.EstimateDeployUnifiedContract(request); require.NoError(t, err)
    require.Equal(t, quote.ContractAddress, again.ContractAddress)
    result, err := f.owner.DeployUnifiedContract(request); require.NoError(t, err)
    require.Equal(t, quote.ContractAddress, result.ContractAddress)
    work, block := f.mined(t, result)
    require.Equal(t, contract.ResultStatusSuccess, sdkReviewResultStatus(t, work, block))
    return result.ContractAddress
}

func sdkReviewFindJSON(value any, name string) (any, bool) {
    object, ok := value.(map[string]any); if !ok { return nil, false }
    if v, found := object[name]; found { return v, true }
    for _, key := range []string{"state", "evm", "template", "details", "data"} {
        if v, found := sdkReviewFindJSON(object[key], name); found { return v, true }
    }
    return nil, false
}

func sdkReviewQueryState(t *testing.T, manager *wallet.Manager, address string) map[string]any {
    t.Helper()
    text, err := manager.QueryContract(&wallet.ContractQueryRequest{Query: wallet.ContractQueryState, Contract: address}); require.NoError(t, err)
    var result map[string]any
    require.NoError(t, json.Unmarshal([]byte(text), &result), "contract-review: state is not JSON")
    return result
}

func sdkReviewProbeState(t *testing.T, manager *wallet.Manager, address string) map[string]any {
    t.Helper()
    state := sdkReviewQueryState(t, manager, address)
    custom, found := sdkReviewFindJSON(state, "custom")
    require.True(t, found, "contract-review: missing probe state: %.1500s", fmt.Sprint(state))
    result, ok := custom.(map[string]any); require.True(t, ok)
    return result
}

func sdkReviewRequireCounter(t *testing.T, manager *wallet.Manager, address string, n int) {
    t.Helper(); require.Equal(t, float64(n), sdkReviewProbeState(t, manager, address)["counter"], "contract-review: wrong committed counter")
}

func sdkReviewAssetAmount(tx *wire.MsgTx, address, asset string) int64 {
    var total int64
    for _, output := range tx.TxOut {
        recipient, err := wallet.AddrFromPkScript_SatsNet(output.PkScript)
        if err != nil || recipient != address { continue }
        for _, item := range output.Assets {
            if item.Name.String() == asset { n, _ := strconv.ParseInt(item.Amount.String(), 10, 64); total += n }
        }
    }
    return total
}

func sdkReviewReturnedAsset(block *wire.MsgBlock, recipient, asset string) int64 {
    var total int64
    for _, result := range contractResultTxs(block) { total += sdkReviewAssetAmount(result, recipient, asset) }
    return total
}
