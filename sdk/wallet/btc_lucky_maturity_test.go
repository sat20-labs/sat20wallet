package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	dbcodec "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/indexer/share/btclucky"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func setLuckyMaturityService(t *testing.T, p *Manager, maturity uint64) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/btc/lucky/info" {
			t.Errorf("unexpected maturity service path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(btclucky.APIResponse[*btclucky.InfoResponse]{Data: &btclucky.InfoResponse{Service: btclucky.TemplateServiceStatus{CoinbaseMaturity: maturity}}})
	}))
	t.Cleanup(server.Close)
	p.cfg = &common.Config{IndexerL1: &common.Indexer{Scheme: "http", Host: server.Listener.Addr().String()}}
}

type luckyAscendRPC struct {
	*luckyRewardRPC
	peer       []byte
	broadcasts int
}

func (r *luckyAscendRPC) GetIndexerPubKey() ([]byte, error) { return r.peer, nil }
func (r *luckyAscendRPC) IsCoreNode([]byte) (bool, error)   { return false, nil }
func (r *luckyAscendRPC) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	r.broadcasts++
	return tx.TxID(), nil
}

type luckyAscendPeer struct {
	NodeRPCClient
	sign func(*wwire.SignRequest) ([][][]byte, error)
}

func (p *luckyAscendPeer) SendSigReq(req *wwire.SignRequest, _ []byte) ([][][]byte, error) {
	return p.sign(req)
}

func TestBTCLuckyAscendRechecksBeforeSigningAndBroadcast(t *testing.T) {
	params := GetChainParam_SatsNet()
	previous := params.POSV2Height
	params.POSV2Height = 1
	t.Cleanup(func() { params.POSV2Height = previous })
	previousNoSend := NOT_SEND_TX
	NOT_SEND_TX = false
	t.Cleanup(func() { NOT_SEND_TX = previousNoSend })
	for _, mode := range []string{"immature", "mature", "rollback while co-signing", "reorg while co-signing"} {
		t.Run(mode, func(t *testing.T) {
			p, channel, l1, l2, _ := luckyChannelFixture(t, true, true)
			setLuckyMaturityService(t, p, 10)
			p.channelMap = nil
			l2.ascend = nil
			var err error
			p.wallet, _, err = NewInternalWalletWithPrivKey(bytes.Repeat([]byte{41}, 32), GetChainParam())
			require.NoError(t, err)
			peerKey, _ := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{42}, 32))
			client1 := &luckyAscendRPC{luckyRewardRPC: l1, peer: peerKey.PubKey().SerializeCompressed()}
			client2 := &luckyAscendRPC{luckyRewardRPC: l2}
			p.l1IndexerClient.active, p.l2IndexerClient.active = client1, client2
			database := dbcodec.NewKVDB(t.TempDir())
			t.Cleanup(func() { require.NoError(t, database.Close()) })
			p.utxoLockerL2 = NewUtxoLocker(database, client2, L2_NETWORK_SATOSHI)
			p.utxoLockerL2.Init()
			requests := 0
			peer := &luckyAscendPeer{sign: func(req *wwire.SignRequest) ([][][]byte, error) {
				requests++
				var more wwire.RemoteSignMoreData
				require.NoError(t, json.Unmarshal(req.MoreData, &more))
				tx, err := DecodeMsgTx_SatsNet(more.Tx[0].Tx)
				require.NoError(t, err)
				_, err = p.ValidateBTCLuckyAnchor(tx, channel.RedeemScript)
				require.NoError(t, err)
				invoice, err := sindexer.AnchorInvoice(tx, true)
				require.NoError(t, err)
				sig := ecdsa.Sign(peerKey, chainhash.HashB(invoice)).Serialize()
				if mode == "rollback while co-signing" {
					l1.confirmations = 9
				} else if mode == "reorg while co-signing" {
					l1.blockHash = fmt.Sprintf("%064x", 2)
				}
				return [][][]byte{{sig}}, nil
			}}
			p.serverNode = &Node{Pubkey: peerKey.PubKey(), client: peer}
			l1.confirmations = 10
			if mode == "immature" {
				l1.confirmations = 9
			}
			txid, err := p.AscendBTCLuckyReward(l1.output.OutPointStr)
			if mode == "mature" {
				require.NoError(t, err)
				require.NotEmpty(t, txid)
				require.Equal(t, 1, client2.broadcasts)
			} else {
				require.Error(t, err)
				require.Zero(t, client2.broadcasts)
				if mode == "immature" {
					require.ErrorContains(t, err, "immature")
					require.Zero(t, requests)
				} else {
					require.Equal(t, 1, requests)
				}
			}
		})
	}
}

func TestBTCLuckyDiscoveryIsolatesErrorsAndTracksMaturity(t *testing.T) {
	p, channel, l1, l2, _ := luckyChannelFixture(t, true, true)
	point := l1.output.OutPointStr
	oldPoint := fmt.Sprintf("%064x:0", 7)
	old := *l2.ascend
	old.FundingUtxo = oldPoint
	l2.ascend = nil
	l2.ascends = map[string]*sindexer.AscendData{oldPoint: &old}
	l2.ledger = []*sindexer.ChannelLedgerEntry{{ChannelId: channel.ChannelId, Direction: "ascending", L1Outpoints: []string{oldPoint}}}
	l1.outputs = map[string]*TxOutput{point: l1.output}
	l1.rawByID = map[string]string{strings.Split(point, ":")[0]: l1.raw}
	for _, confirmations := range []uint64{9, 10, 9, 0} {
		l1.confirmations = confirmations
		rewards, err := p.getBTCLuckyRewards(channel.RedeemScript, channel.ChannelId, 10)
		require.NoError(t, err)
		require.Len(t, rewards, 2)
		byPoint := make(map[string]*BTCLuckyReward)
		for _, reward := range rewards {
			byPoint[reward.FundingUtxo] = reward
		}
		require.Equal(t, "error", byPoint[oldPoint].Status)
		encoded, err := json.Marshal(byPoint[oldPoint])
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		require.NotEmpty(t, fields["error"], "the broken historical reward must stay visible")
		want := "immature"
		if confirmations == 10 {
			want = "unascended"
		} else if confirmations == 0 {
			want = "error"
		}
		require.Equal(t, want, byPoint[point].Status)
	}
	l1.confirmations = 10
	l2.ascendErrs = map[string]error{oldPoint: fmt.Errorf("source lookup unavailable")}
	rewards, err := p.getBTCLuckyRewards(channel.RedeemScript, channel.ChannelId, 10)
	require.NoError(t, err)
	require.Len(t, rewards, 2)
	l2.ledgerErr = fmt.Errorf("ledger unavailable")
	_, err = p.getBTCLuckyRewards(channel.RedeemScript, channel.ChannelId, 10)
	require.ErrorContains(t, err, "ledger unavailable")
}

func TestBTCLuckyClaimRechecksAfterCosigning(t *testing.T) {
	previousNoSend := NOT_SEND_TX
	NOT_SEND_TX = false
	t.Cleanup(func() { NOT_SEND_TX = previousNoSend })
	for _, mode := range []string{"unchanged", "immature", "reorg", "backing lost", "source unavailable", "anchor spent", "L1 source spent before signing", "L1 source spent after co-signing"} {
		t.Run(mode, func(t *testing.T) {
			p, channel, l1, l2, _ := luckyChannelFixture(t, true, true)
			setLuckyMaturityService(t, p, 10)
			p.channelMap = nil
			l1.confirmations = 10
			l1.blockHash = l2.ascend.BTCLucky.BlockHash.String()
			var err error
			p.wallet, _, err = NewInternalWalletWithPrivKey(bytes.Repeat([]byte{41}, 32), GetChainParam())
			require.NoError(t, err)
			peerWallet, _, err := NewInternalWalletWithPrivKey(bytes.Repeat([]byte{42}, 32), GetChainParam())
			require.NoError(t, err)
			client1 := &luckyAscendRPC{luckyRewardRPC: l1, peer: peerWallet.GetPubKey().SerializeCompressed()}
			client2 := &luckyAscendRPC{luckyRewardRPC: l2}
			p.l1IndexerClient.active, p.l2IndexerClient.active = client1, client2
			if mode == "L1 source spent before signing" {
				l1.spent = true
			}
			database := dbcodec.NewKVDB(t.TempDir())
			t.Cleanup(func() { require.NoError(t, database.Close()) })
			p.utxoLockerL2 = NewUtxoLocker(database, client2, L2_NETWORK_SATOSHI)
			p.utxoLockerL2.Init()
			requests := 0
			peer := &luckyAscendPeer{sign: func(req *wwire.SignRequest) ([][][]byte, error) {
				requests++
				var more wwire.RemoteSignMoreData
				require.NoError(t, json.Unmarshal(req.MoreData, &more))
				tx, err := DecodeMsgTx_SatsNet(more.Tx[0].Tx)
				require.NoError(t, err)
				prev, err := p.ValidateBTCLuckyClaim(tx, channel.RedeemScript, req.PubKey, peerWallet.GetPubKey().SerializeCompressed())
				require.NoError(t, err)
				sig, err := FinalSignTxWithWallet_SatsNet(peerWallet, tx, prev, channel.RedeemScript, req.PubKey, more.Tx[0].LocalSigs)
				require.NoError(t, err)
				require.NoError(t, VerifySignedTx_SatsNet(tx, prev))
				switch mode {
				case "immature":
					l1.confirmations = 9
				case "reorg":
					l1.blockHash = fmt.Sprintf("%064x", 3)
				case "backing lost":
					l1.outputs = map[string]*TxOutput{}
				case "source unavailable":
					l1.rawErr = fmt.Errorf("source unavailable")
				case "anchor spent":
					l2.outputs = map[string]*TxOutput{}
				case "L1 source spent after co-signing":
					l1.spent = true
				}
				return [][][]byte{sig}, nil
			}}
			p.serverNode = &Node{Pubkey: peerWallet.GetPubKey(), client: peer}
			id, err := p.ClaimBTCLuckyReward(l1.output.OutPointStr)
			expectedRequests := 1
			if mode == "L1 source spent before signing" {
				expectedRequests = 0
			}
			require.Equal(t, expectedRequests, requests)
			if mode == "unchanged" {
				require.NoError(t, err)
				require.NotEmpty(t, id)
				require.Equal(t, 1, client2.broadcasts)
			} else {
				require.Error(t, err)
				require.Empty(t, id)
				require.Zero(t, client2.broadcasts)
			}
		})
	}
}
