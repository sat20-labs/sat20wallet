package wallet

import (
	"errors"
	"fmt"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Reuse the real local KVDB and existing contract interfaces. Only the outbound
// signer is replaced: these tests exercise runtime accounting without a node.
type launchPoolCloseAccountingManager struct {
	ContractManager
	database indexer.KVDB
	sends    int
	sendErr  error
}

func (m *launchPoolCloseAccountingManager) GetDB() indexer.KVDB                        { return m.database }
func (m *launchPoolCloseAccountingManager) GetMode() string                            { return "test" }
func (m *launchPoolCloseAccountingManager) SaveReservation(ContractDeployResvIF) error { return nil }

type launchPoolCloseAccountingReservation struct {
	ContractDeployResvIF
	initiator bool
	status    ResvStatus
}

func (r *launchPoolCloseAccountingReservation) LocalIsInitiator() bool { return r.initiator }
func (r *launchPoolCloseAccountingReservation) SetStatus(s ResvStatus) { r.status = s }

func (m *launchPoolCloseAccountingManager) CoBatchSend_SatsNet(_ common.Wallet, _ []string, _ string, _ []*Decimal,
	_, _ string, _ int64, _, _, _ []byte) (string, error) {
	m.sends++
	return "close-result", m.sendErr
}
func newLaunchPoolCloseAccountingRuntime(t *testing.T, initiator bool, explicit bool, remaining int64) (*LaunchPoolContractRunTime, *launchPoolCloseAccountingManager) {
	t.Helper()
	database := NewKVDB(t.TempDir())
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = database.Close() })
	m := &launchPoolCloseAccountingManager{database: database}
	p := NewLaunchPoolContractRuntime(m)
	p.ChannelAddr = "launchpool-accounting"
	p.AssetName = swire.AssetName{Protocol: "ordx", Type: "f", Ticker: "lprf1016"}
	p.Deployer = "test-deployer"
	p.ReturnToDeployer = true
	p.CloseRequested = explicit
	p.Status = CONTRACT_STATUS_CLOSING
	p.AssetAmtInPool = indexer.NewDefaultDecimal(200000)
	p.TotalInputAssets = p.AssetAmtInPool.Clone()
	p.SatsValueInPool = remaining
	p.resv = &launchPoolCloseAccountingReservation{initiator: initiator}
	if explicit {
		p.CloseInvokeTxID = "close-invoke"
		p.history["close-invoke:0"] = &MintHistoryItem{OrderType: ORDERTYPE_CLOSE, InUtxo: "close-invoke:0", InValue: INVOKE_FEE, ServiceFee: INVOKE_FEE, Address: p.Deployer}
	}
	return p, m
}

func TestLaunchPoolCloseResultPreservesParticipantBalance(t *testing.T) {
	for _, initiator := range []bool{true, false} {
		for _, explicit := range []bool{true, false} {
			for _, remaining := range []int64{0, 100} {
				t.Run(fmt.Sprintf("initiator=%t/explicit=%t/remaining=%d", initiator, explicit, remaining), func(t *testing.T) {
					p, m := newLaunchPoolCloseAccountingRuntime(t, initiator, explicit, remaining)
					amount := p.AssetAmtInPool.Clone()
					apply := func() {
						if initiator {
							if err := p.returnAssetsToDeployer(); err != nil {
								t.Fatal(err)
							}
						} else {
							p.SetPeerActionResult(wwire.STP_ACTION_SIGN, &DealInfo{Reason: INVOKE_RESULT_CLOSE, TxId: "close-result", TotalAmt: amount.Clone(), Height: 130})
						}
					}
					apply()
					if p.AssetAmtInPool.Sign() != 0 || p.TotalOutputAssets.Cmp(amount) != 0 || p.CloseAssetTxID != "close-result" {
						t.Fatalf("asset return not recorded: %+v", p.LaunchPoolRunningData)
					}
					// Participation refunds have already paid their own network fee. The
					// separate close operation must not debit participation money again.
					if p.SatsValueInPool != remaining {
						t.Errorf("participant balance=%d, want %d after separately funded asset return", p.SatsValueInPool, remaining)
					}
					got := p.SatsValueInPool
					apply()
					if p.SatsValueInPool != got || p.TotalOutputAssets.Cmp(amount) != 0 {
						t.Fatal("repeated result changed accounting")
					}
					if initiator && m.sends != 1 {
						t.Fatalf("duplicate asset broadcast: %d", m.sends)
					}
				})
			}
		}
	}
}

func TestLaunchPoolRefundStillPaysItsOwnNetworkFee(t *testing.T) {
	for _, initiator := range []bool{true, false} {
		t.Run(fmt.Sprintf("initiator=%t", initiator), func(t *testing.T) {
			p, _ := newLaunchPoolCloseAccountingRuntime(t, initiator, true, 50000)
			item := &MintHistoryItem{OrderType: ORDERTYPE_MINT, InUtxo: "mint:0", Address: p.Deployer, InValue: 50000, OutValue: 50000}
			p.history[item.InUtxo] = item
			p.invalidMintMap[p.Deployer] = &MinterStatus{TotalAmt: indexer.NewDefaultDecimal(49990), History: []*MintHistoryItem{item}}
			if initiator {
				if err := p.refund(); err != nil {
					t.Fatal(err)
				}
			} else {
				p.SetPeerActionResult(wwire.STP_ACTION_SIGN, &DealInfo{Reason: INVOKE_RESULT_REFUND, TxId: "refund-result", Height: 130, SendInfo: map[string]*SendAssetInfo{p.Deployer: {Value: 49990}}})
			}
			if p.SatsValueInPool != 0 || p.TotalOutputSats != 49990 || item.Done != ITEM_STATUS_REFUNDED {
				t.Fatalf("refund accounting balance=%d refunded=%d done=%d", p.SatsValueInPool, p.TotalOutputSats, item.Done)
			}
		})
	}
}

func TestLaunchPoolCloseBroadcastFailureKeepsAccounting(t *testing.T) {
	p, m := newLaunchPoolCloseAccountingRuntime(t, true, true, 0)
	m.sendErr = errors.New("test signer failure")
	if err := p.returnAssetsToDeployer(); err == nil {
		t.Fatal("expected signer error")
	}
	if p.SatsValueInPool != 0 || p.CloseAssetTxID != "" || p.AssetAmtInPool.Int64() != 200000 || p.TotalOutputAssets.Sign() != 0 {
		t.Fatal("failed broadcast changed accounting")
	}
}

func TestLaunchPoolLegacyNegativeBalanceRoundTripIsNotRepaired(t *testing.T) {
	p, m := newLaunchPoolCloseAccountingRuntime(t, true, true, -10)
	encoded, err := p.GobEncode()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewLaunchPoolContractRuntime(m)
	if err := restored.GobDecode(encoded); err != nil {
		t.Fatal(err)
	}
	if restored.SatsValueInPool != -10 {
		t.Fatalf("legacy balance silently changed to %d", restored.SatsValueInPool)
	}
}
