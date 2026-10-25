package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Dynamic interface admission intentionally compiles on the unchanged SDK.
// A missing API is a normal assertion failure, not a compile-error oracle.
type singleUTXOTopupAPI interface {
	QuoteClosedChannelSingleUTXOTopup(int64) (string, error)
	ConfirmClosedChannelSingleUTXOTopup(string) (string, error)
}

func singleUTXOTopupAPIForTest(t *testing.T, manager *Manager) singleUTXOTopupAPI {
	t.Helper()
	api, ok := interface{}(manager).(singleUTXOTopupAPI)
	if !ok {
		t.Fatal("closed-channel single-UTXO quote/confirm API is missing; no safe numeric approval path exists")
	}
	return api
}

type singleUTXOTopupGateClient struct {
	NodeRPCClient
	response *wwire.PingResp
	err      error
	calls    int
}

func (c *singleUTXOTopupGateClient) SendPingReq(req *wwire.PingReq) (*wwire.PingResp, error) {
	c.calls++
	raw, err := json.Marshal(req.PingRequest)
	if err != nil {
		return nil, err
	}
	if err := VerifySignOfMessage(raw, req.Sig, req.NodeId); err != nil {
		return nil, err
	}
	return c.response, c.err
}
func TestSTPSingleUTXOTopupRequiresSafeAPI(t *testing.T) {
	singleUTXOTopupAPIForTest(t, &Manager{})
}
func TestSTPSingleUTXOTopupMissingLocalPackageDoesNotSuggestOthers(t *testing.T) {
	base := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, base)
	manager.resetResvMapsLocked()
	probe := &singleUTXOTopupGateClient{err: errors.New("ordinary offline fixture")}
	manager.serverNode.client = probe
	api := singleUTXOTopupAPIForTest(t, manager)
	quote, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err == nil || quote != "" || strings.Contains(strings.ToLower(err.Error()), "others") {
		t.Fatalf("offline quote=%q error=%v; want no quote and no unverified Others suggestion", quote, err)
	}
	if probe.calls != 0 {
		t.Fatalf("missing local package must fail before liveness, calls=%d", probe.calls)
	}
	if len(manager.GetLocalActionReservations()) != 0 || len(manager.GetFundingReservations()) != 0 {
		t.Fatal("read-only offline quote created a reservation")
	}
}
func TestSTPSingleUTXOTopupRejectsDifferentAccountResponse(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.gate.response = &wwire.PingResp{BaseResp: wwire.BaseResp{Code: 0}, PingResponse: &wwire.PingResponse{NextAction: "rebuild", ActionParam: "different-account-channel"}}
	api := singleUTXOTopupAPIForTest(t, f.manager)
	quote, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err == nil || quote != "" {
		t.Fatalf("wrong-account quote=%q error=%v", quote, err)
	}
	if f.gate.calls != 1 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("wrong-account response advanced recovery")
	}
}
func TestSTPSingleUTXOTopupCannotConfirmMissingQuote(t *testing.T) {
	api := singleUTXOTopupAPIForTest(t, &Manager{})
	for _, raw := range []string{"", "{}", "null", "not-json", `{"channelId":"caller-selected-channel","amount":1}`} {
		if result, err := api.ConfirmClosedChannelSingleUTXOTopup(raw); err == nil || result != "" {
			t.Fatalf("unbound confirmation %q result=%q error=%v", raw, result, err)
		}
	}
}

func (c *singleUTXOTopupGateClient) GetChannelOpenFeeReq() (*wwire.ChannelOpenFeeInfo, error) {
	return NewFeeConfig().ToOpenChannelFeeInfo(), nil
}

type singleUTXOTopupQuoteIndexer struct {
	IndexerRPCClient
	address string
	output  *indexerwire.TxOutputInfo
	parent  *wire.MsgTx
}

func (c *singleUTXOTopupQuoteIndexer) GetUtxoListWithTicker(address string, _ *swire.AssetName) []*indexerwire.TxOutputInfo {
	if address != c.address {
		return nil
	}
	return []*indexerwire.TxOutputInfo{c.output}
}
func TestSTPSingleUTXOTopupQuotesFullOutputNotOldBalanceDeficit(t *testing.T) {
	base := &channelHeartbeatTestClient{}
	manager := newChannelHeartbeatTestManager(t, base)
	manager.resetResvMapsLocked()
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("create local quote fixture DB")
	}
	defer database.Close()
	manager.db = database
	manager.status = &Status{SyncHeight: 1000}
	channelID, err := manager.GetChannelAddress()
	if err != nil {
		t.Fatal(err)
	}
	parent := wire.NewMsgTx(wire.TxVersion)
	var hash chainhash.Hash
	hash[0] = 98
	parent.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 0), nil, nil))
	// Old residual is only one sat below the minimum. The quote must NOT ask
	// the account wallet for merely that one-sat deficit.
	parent.AddTxOut(wire.NewTxOut(NewFeeConfig().MinCapacity()-1, []byte{0x51}))
	stored := NewChannelInDB()
	stored.ChannelId, stored.Status = channelID, CS_CLOSED
	stored.IsInitiator = true
	stored.LocalWalletId = manager.wallet.GetId()
	stored.LocalChanCfg.WalletId = manager.wallet.GetSubAccount()
	stored.LocalChanCfg.PaymentKey = manager.wallet.GetPaymentPubKey()
	stored.RemoteChanCfg.PaymentKey = manager.serverNode.Pubkey
	stored.RemoteCommitment = NewChannelCommitment()
	stored.RemoteCommitment.CommitTx = parent
	raw, err := EncodeToBytes(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Write([]byte(GetChannelKey(channelID)), raw); err != nil {
		t.Fatal(err)
	}
	source := wire.NewMsgTx(wire.TxVersion)
	hash[0] = 99
	source.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 0), nil, nil))
	script, err := GetPkScriptFromAddress(manager.wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	source.AddTxOut(wire.NewTxOut(200000, script))
	output := indexer.GenerateTxOutput(source, 0)
	probe := &singleUTXOTopupQuoteIndexer{address: manager.wallet.GetAddress(), parent: parent, output: &indexerwire.TxOutputInfo{
		OutPoint: output.OutPointStr, Value: output.Value(), PkScript: script, UtxoId: indexer.ToUtxoId(1, 0, 0),
	}}
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.SetMaster(probe)
	manager.utxoLockerL1 = NewUtxoLocker(database, probe, L1_NETWORK_BITCOIN)
	manager.utxoLockerL1.Init()
	gate := &singleUTXOTopupGateClient{response: &wwire.PingResp{BaseResp: wwire.BaseResp{Code: 0}, PingResponse: &wwire.PingResponse{NextAction: "rebuild", ActionParam: channelID}}}
	manager.serverNode.client = gate
	api := singleUTXOTopupAPIForTest(t, manager)
	quoted, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatal(err)
	}
	var quote struct {
		ChannelID                                 string `json:"channelId"`
		Amount, WalletFee, RequiredInput, FeeRate int64
		UnsignedTx                                string `json:"unsignedTx"`
	}
	if err := json.Unmarshal([]byte(quoted), &quote); err != nil {
		t.Fatal(err)
	}
	if quote.ChannelID != channelID || quote.Amount != NewFeeConfig().MinCapacity() || quote.WalletFee <= 0 || quote.FeeRate != 5 || quote.RequiredInput != quote.Amount+quote.WalletFee {
		t.Fatalf("quote must cover full fresh output plus ordinary transfer fee, not old residual deficit: %s", quoted)
	}
	tx, err := DecodeMsgTx(quote.UnsignedTx)
	if err != nil {
		t.Fatal(err)
	}
	destScript, err := GetPkScriptFromAddress(channelID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.TxIn) != 1 || len(tx.TxOut) != 2 || tx.TxOut[0].Value != quote.Amount || !bytes.Equal(tx.TxOut[0].PkScript, destScript) || len(tx.TxIn[0].Witness) != 0 {
		t.Fatal("quote did not retain a single full channel output in an unsigned ordinary wallet transfer")
	}
	if len(manager.GetLocalActionReservations()) != 0 || len(manager.GetFundingReservations()) != 0 || len(manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("read-only quote reserved funds or created durable recovery work")
	}
}

func (c *singleUTXOTopupQuoteIndexer) IsTxConfirmed(id string) bool {
	return c.parent != nil && id == c.parent.TxID()
}
func (c *singleUTXOTopupQuoteIndexer) GetTxHeight(id string) (int, error) {
	if c.IsTxConfirmed(id) {
		return 100, nil
	}
	return 0, nil
}
func (c *singleUTXOTopupQuoteIndexer) GetRawTx(id string) (string, error) {
	if c.IsTxConfirmed(id) {
		return EncodeMsgTx(c.parent)
	}
	return "", indexer.ErrKeyNotFound
}
