package wallet

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Reuse the ordinary persisted-channel fixture, but create and sign the actual
// payment transactions. The second payment spends the first one's change.
func pendingPaymentRestartFixture(t *testing.T, phases ...ResvStatus) (*Manager, *ChannelInDB, []*PaymentReservation) {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	manager, stored := localReadyTestFixture(t, len(phases))
	channel := &Channel{ChannelInDB: *stored, manager: manager, localWallet: manager.wallet}
	input := &TxOutput_SatsNet{OutPointStr: fmt.Sprintf("%064x:0", 701),
		OutValue: swire.TxOut{Value: 20_000, PkScript: channel.GetLocalPkScript()}}
	var payments []*PaymentReservation
	for i, phase := range phases {
		channel.CommitHeight = i
		payment := &PaymentReservation{PaymentDataInDB: PaymentDataInDB{
			ReservationBase: NewReservationBase(int64(701+i), true, phase, manager.wallet),
			ChannelId:       stored.ChannelId, AssetName: &PLAIN_ASSET,
			Amt: indexer.NewDefaultDecimal(1_000), NeedSendLockTx: true,
			Utxos: []*TxOutput_SatsNet{input},
		}}
		tx, fetcher, err := CreateLockTx(channel, payment.AssetName, payment.Amt, payment.Utxos, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		payment.PaymentTx, err = SignTxWithWallet_SatsNet(manager.wallet, tx, fetcher)
		if err != nil {
			t.Fatal(err)
		}
		if err := SaveReservation(manager.db, payment); err != nil {
			t.Fatal(err)
		}
		payments = append(payments, payment)
		input = &TxOutput_SatsNet{OutPointStr: payment.PaymentTx.TxID() + ":1", OutValue: *payment.PaymentTx.TxOut[1]}
	}
	last := payments[len(payments)-1]
	stored.UpdateTime = last.Id
	stored.LastPaymentTxId = last.PaymentTx.TxID()
	if err := manager.SaveChannelInDB(stored); err != nil {
		t.Fatal(err)
	}
	return manager, stored, payments
}

func restartPaymentFixture(t *testing.T, manager *Manager) {
	t.Helper()
	manager.wallet = nil
	manager.clearAccountManagementSession()
	manager.initResvMap()
	manager.localReadyChannelsInitialized = false // A new process has no completed runtime initialization.
	if err := manager.initDB(); err != nil {
		t.Fatal(err)
	}
}

func TestUnlockWalletRestoresPendingPayments(t *testing.T) {
	for _, phases := range [][]ResvStatus{{RS_PAYMENT_STARTED}, {RS_PAYMENT_BROADCASTED}, {RS_PAYMENT_BROADCASTED, RS_PAYMENT_BROADCASTED}} {
		t.Run(fmt.Sprint(phases), func(t *testing.T) {
			manager, stored, payments := pendingPaymentRestartFixture(t, phases...)
			before := make(map[string][]byte)
			for _, key := range append([]string{GetChannelKey(stored.ChannelId)}, paymentFixtureKeys(payments)...) {
				var err error
				before[key], err = manager.db.Read([]byte(key))
				if err != nil {
					t.Fatal(err)
				}
			}
			restartPaymentFixture(t, manager)
			if manager.GetCurrentChannel() != nil {
				t.Fatal("locked initialization installed a channel")
			}
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			runtime := manager.GetCurrentChannel()
			if runtime == nil || runtime.ChannelId != stored.ChannelId || runtime.CommitHeight != len(phases) || runtime.ResvId != 0 {
				t.Fatal("cold unlock did not restore the current payment channel")
			}
			for _, payment := range payments {
				restored, ok := manager.GetPaymentReservation(payment.Id)
				if !ok || restored.Channel != runtime || restored.LocalWallet() == nil ||
					restored.LocalWallet().GetWalletId() != payment.WalletId || restored.Status != payment.Status {
					t.Fatal("payment runtime or original phase was not restored")
				}
			}
			for key, original := range before {
				after, err := manager.db.Read([]byte(key))
				if err != nil || !bytes.Equal(original, after) {
					t.Fatalf("cold restoration rewrote persisted state %s: %v", key, err)
				}
			}
			// UI reauthentication must retain the already installed runtime.
			runtime.ResvId = 999
			if _, err := manager.UnlockWallet("password"); err != nil || manager.GetCurrentChannel() != runtime || runtime.ResvId != 999 {
				t.Fatal("warm unlock replaced or reset the active runtime")
			}
			runtime.ResvId = 0
			runtime.PeerRPC = nil // No transport is needed to test durable local completion.
			for _, payment := range payments {
				restored, _ := manager.GetPaymentReservation(payment.Id)
				if err := manager.HandlePaymentFinished(restored); err != nil {
					t.Fatal(err)
				}
			}
			if manager.GetChannel(stored.ChannelId) != runtime || len(manager.GetPaymentReservations()) != 0 {
				t.Fatal("finishing payment reservations lost the restored channel")
			}
			restartPaymentFixture(t, manager)
			if _, err := manager.UnlockWallet("password"); err != nil || manager.GetCurrentChannel() == nil {
				t.Fatal("completed payments did not survive another cold unlock")
			}
		})
	}
}

func paymentFixtureKeys(payments []*PaymentReservation) []string {
	keys := make([]string, 0, len(payments))
	for _, payment := range payments {
		keys = append(keys, GetResvKey(RESV_TYPE_PAYMENT, payment.Id))
	}
	return keys
}

func TestPaymentRuntimeRecoveryRejectsInvalidRecords(t *testing.T) {
	cases := []struct {
		name   string
		change func(*Manager, *ChannelInDB, *PaymentReservation)
	}{
		{"unsigned negotiation", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.Status = RS_INIT }},
		{"expand only", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.NeedSendLockTx = false }},
		{"missing transaction", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.PaymentTx = nil }},
		{"missing prevout", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.Utxos = nil }},
		{"duplicate prevout", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.Fees = p.Utxos }},
		{"missing witness", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.PaymentTx.TxIn[0].Witness = nil }},
		{"invalid witness", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.PaymentTx.TxIn[0].Witness[0][0] ^= 1 }},
		{"wrong prevout value", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.Utxos[0].OutValue.Value++ }},
		{"wrong input owner", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.Utxos[0].OutValue.PkScript = []byte{0x51} }},
		{"unavailable wallet", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.WalletId.Id++ }},
		{"unavailable account", func(_ *Manager, _ *ChannelInDB, p *PaymentReservation) { p.WalletId.SubAccountId = 5 }},
		{"wrong local key", func(m *Manager, c *ChannelInDB, _ *PaymentReservation) {
			other := m.wallet.Clone()
			other.SetSubAccount(1)
			c.LocalChanCfg.PaymentKey = other.GetPaymentPubKey()
		}},
		{"channel not committed", func(_ *Manager, c *ChannelInDB, p *PaymentReservation) { c.UpdateTime = p.Id - 1 }},
		{"wrong latest tx", func(_ *Manager, c *ChannelInDB, _ *PaymentReservation) { c.LastPaymentTxId = strings.Repeat("0", 64) }},
		{"native recovery state", func(_ *Manager, c *ChannelInDB, _ *PaymentReservation) { c.Status = CS_NATIVE_RECOVERY }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manager, stored, payments := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED, RS_PAYMENT_BROADCASTED)
			// Corrupt the earlier record as well as testing current-channel fields.
			// Installing the later valid payment must not implicitly bind this one.
			payment := payments[0]
			tc.change(manager, stored, payment)
			// As in the splicing recovery tests, keep the synthetic record
			// internally hashed so rejection is tested at the recovery boundary.
			stored.StaticMerkleRoot = stored.CalcStaticMerkleRoot()
			if err := manager.SaveChannelInDB(stored); err != nil {
				t.Fatal(err)
			}
			if err := SaveReservation(manager.db, payment); err != nil {
				t.Fatal(err)
			}
			restartPaymentFixture(t, manager)
			before, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_PAYMENT, payment.Id)))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			if manager.GetChannel(stored.ChannelId) != nil {
				t.Fatal("invalid payment group installed a channel")
			}
			for _, p := range manager.GetPaymentReservations() {
				if p.Channel != nil {
					t.Fatal("invalid payment was implicitly rebound by another payment")
				}
			}
			after, err := manager.db.Read([]byte(GetResvKey(RESV_TYPE_PAYMENT, payment.Id)))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid payment evidence was changed or removed")
			}
		})
	}
}

func TestPaymentRuntimeRecoveryPreservesOtherOperation(t *testing.T) {
	manager, stored, _ := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED)
	closing := &ClosingReservation{ClosingDataInDB: ClosingDataInDB{
		ReservationBase: NewReservationBase(802, true, RS_INIT, manager.wallet), ChannelId: stored.ChannelId,
	}}
	if err := SaveReservation(manager.db, closing); err != nil {
		t.Fatal(err)
	}
	restartPaymentFixture(t, manager)
	if _, err := manager.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	if manager.GetChannel(stored.ChannelId) != nil {
		t.Fatal("payment recovery took ownership from another unfinished operation")
	}
	if _, ok := manager.GetClosingReservation(closing.Id); !ok {
		t.Fatal("payment recovery removed the other operation")
	}
}

func TestPaymentRuntimeRecoveryKeepsNativeObligations(t *testing.T) {
	for _, state := range []string{"unrevoked", "revoked", "corrupt", "prepared"} {
		t.Run(state, func(t *testing.T) {
			manager, stored, _ := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED)
			context := &Channel{ChannelInDB: *stored}
			key := stpNativeCommitmentKey(context, "local")
			record := &stpNativeCommitmentRecord{Version: 1, Role: "local", ChannelID: stored.ChannelId,
				Height: uint64(stored.CommitHeight) + 1, RevokedPrevious: state == "revoked"}
			raw, err := EncodeToBytes(record)
			if err != nil {
				t.Fatal(err)
			}
			if state == "corrupt" {
				raw = []byte("damaged native obligation")
			} else if state == "prepared" {
				key = stpNativePreparedRecoveryKey(context)
			}
			if err := manager.db.Write(key, raw); err != nil {
				t.Fatal(err)
			}
			restartPaymentFixture(t, manager)
			if _, err := manager.UnlockWallet("password"); err != nil {
				t.Fatal(err)
			}
			if (manager.GetCurrentChannel() != nil) != (state == "unrevoked") {
				t.Fatal("payment recovery bypassed a native obligation or blocked an ordinary unrevoked journal")
			}
			after, err := manager.db.Read(key)
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatal("ordinary payment recovery rewrote native evidence")
			}
		})
	}
}

func TestPaymentRuntimeRecoveryRejectsMalformedTransaction(t *testing.T) {
	for _, missing := range []string{"input", "output"} {
		t.Run(missing, func(t *testing.T) {
			manager, stored, payments := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED)
			payment := payments[0]
			if missing == "input" {
				payment.PaymentTx.TxIn[0] = nil
			} else {
				payment.PaymentTx.TxOut[0] = nil
			}
			channel := &Channel{ChannelInDB: *stored, manager: manager, localWallet: manager.wallet}
			if _, err := validatePendingPayment(channel, payment); err == nil {
				t.Fatal("malformed transaction was accepted")
			}
		})
	}
}

func TestPaymentRuntimeRecoveryUsesSignedGeneration(t *testing.T) {
	manager, stored, payments := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED, RS_PAYMENT_BROADCASTED)
	// The clock may move backwards between server restarts. Signed channel
	// generations, rather than relative wall-clock IDs, determine payment order.
	older := payments[0]
	if err := DeleteReservation(manager.db, RESV_TYPE_PAYMENT, older.Id); err != nil {
		t.Fatal(err)
	}
	older.Id = payments[1].Id + 100
	if err := SaveReservation(manager.db, older); err != nil {
		t.Fatal(err)
	}
	restartPaymentFixture(t, manager)
	if _, err := manager.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	channel := manager.GetCurrentChannel()
	if channel == nil || channel.CommitHeight != stored.CommitHeight {
		t.Fatal("valid payment history was rejected due to wall-clock ordering")
	}
	for _, p := range manager.GetPaymentReservations() {
		if p.Channel != channel {
			t.Fatal("historical payment was not rebound")
		}
	}
}

type paymentMonitorTestIndexer struct {
	IndexerRPCClient
	confirmed map[string]bool
	queried   []string
	replayed  []*swire.MsgTx
}

func (p *paymentMonitorTestIndexer) IsTxConfirmed(id string) bool {
	p.queried = append(p.queried, id)
	return p.confirmed[id]
}

func (p *paymentMonitorTestIndexer) BroadCastTx_SatsNet(tx *swire.MsgTx) (string, error) {
	p.replayed = append(p.replayed, tx.Copy())
	return "", fmt.Errorf("test transport keeps the original transaction pending")
}

func TestPaymentMonitorUsesReservationTransaction(t *testing.T) {
	manager, stored, payments := pendingPaymentRestartFixture(t, RS_PAYMENT_BROADCASTED, RS_PAYMENT_BROADCASTED)
	restartPaymentFixture(t, manager)
	if _, err := manager.UnlockWallet("password"); err != nil {
		t.Fatal(err)
	}
	channel := manager.GetCurrentChannel()
	if channel == nil {
		t.Fatal("payment runtime was not restored")
	}
	channel.PeerRPC = nil
	// Later payments can change this field; it is not the identity of each
	// pending reservation's transaction.
	channel.LastPaymentTxId = strings.Repeat("f", 64)
	first, second := payments[0].PaymentTx.TxID(), payments[1].PaymentTx.TxID()
	client := &paymentMonitorTestIndexer{confirmed: map[string]bool{first: true}}
	manager.l2IndexerClient = NewIndexerRPCClientMgr()
	manager.SetIndexerHttpClient_SatsNet(client)
	previousNotSend := NOT_SEND_TX
	NOT_SEND_TX = false
	t.Cleanup(func() { NOT_SEND_TX = previousNotSend })
	manager.HandleChannelReservationStatus(false)
	if _, ok := manager.GetPaymentReservation(payments[0].Id); ok {
		t.Fatal("confirmed original payment was not completed")
	}
	if secondPayment, ok := manager.GetPaymentReservation(payments[1].Id); !ok || secondPayment.Status != RS_PAYMENT_BROADCASTED {
		t.Fatal("unconfirmed payment was completed using another transaction")
	}
	if len(client.queried) != 2 || len(client.replayed) != 1 {
		t.Fatalf("unexpected confirmation/replay counts: %d/%d", len(client.queried), len(client.replayed))
	}
	original, err := EncodeMsgTx_SatsNet(payments[1].PaymentTx)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := EncodeMsgTx_SatsNet(client.replayed[0])
	if err != nil || original != replayed {
		t.Fatal("monitor changed the original signed transaction")
	}
	client.confirmed[second] = true
	manager.HandleChannelReservationStatus(false)
	if len(manager.GetPaymentReservations()) != 0 || manager.GetChannel(stored.ChannelId) != channel || channel.CommitHeight != 2 {
		t.Fatal("payment completion lost or advanced the channel")
	}
}
