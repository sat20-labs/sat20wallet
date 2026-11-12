package wallet

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Only exact reads are implemented. An initialization, scan, batch, write,
// delete, flush or close through this getter fails immediately via the nil
// embedded interface. This is a synthetic in-memory DB, not a wallet fixture.
type metadataReadOnlyDB struct {
	indexer.KVDB
	data      *memoryKVDB
	keys      map[string]bool
	reads     []string
	failKey   string
	afterRead func(string)
}

func (d *metadataReadOnlyDB) Read(key []byte) ([]byte, error) {
	if !d.keys[string(key)] {
		panic("unexpected metadata DB read")
	}
	d.reads = append(d.reads, string(key))
	if string(key) == d.failKey {
		return nil, errors.New("private-path secret-fixture credential=must-not-escape")
	}
	raw, err := d.data.Read(key)
	if d.afterRead != nil {
		d.afterRead(string(key))
	}
	return raw, err
}

type metadataNoHTTP struct{ calls int }

func (h *metadataNoHTTP) SendGetRequest(*URL) ([]byte, error) {
	h.calls++
	panic("metadata network GET")
}
func (h *metadataNoHTTP) SendPostRequest(*URL, []byte) ([]byte, error) {
	h.calls++
	panic("metadata network POST")
}
func (h *metadataNoHTTP) SendGetRequestContext(context.Context, *URL) ([]byte, error) {
	h.calls++
	panic("metadata network GET")
}
func (h *metadataNoHTTP) SendPostRequestContext(context.Context, *URL, []byte) ([]byte, error) {
	h.calls++
	panic("metadata network POST")
}

type metadataFixture struct {
	m                                                    *Manager
	db                                                   *metadataReadOnlyDB
	http                                                 *metadataNoHTTP
	reservation                                          *RGB11TransferReservation
	outbox                                               *accountMessageOutboxRecord
	transferID, reservationKey, projectionKey, outboxKey string
}

func newMetadataFixture(t *testing.T) *metadataFixture {
	t.Helper()
	oldChain, oldEnv, oldMode := _chain, _env, _mode
	_chain, _env, _mode = "regtest", "metadata-test", LIGHT_NODE
	t.Cleanup(func() { _chain, _env, _mode = oldChain, oldEnv, oldMode })
	priv, err := btcec.NewPrivateKey()
	require.NoError(t, err)
	w, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	require.NoError(t, err)
	sender, err := dkvsAccountID(w)
	require.NoError(t, err)
	transferID := testRGB11ConsignmentID(t, "outbox-metadata")
	appID, err := rgb11AddressMessageID(transferID)
	require.NoError(t, err)
	asset := indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("12", 32)}
	state := &rgb11wallet.TransferState{TransferID: transferID, Direction: "send", Status: "prepared",
		AddressMode: true, TransportMode: RGB11AddressTransport, AddressMessageID: appID,
		SenderAccountID: sender, ReceiverAccountID: sender, Asset: indexer.AssetInfo{Name: asset, Amount: *indexer.NewDecimal(1, 0)},
		Invoice: "synthetic-private-invoice", AckStatus: "awaiting-persistence"}
	data := newMemoryKVDB()
	scope := rgb11StorageScope(19, 0)
	projection := rgb11wallet.NewProjectionStore(data, nil)
	require.NoError(t, projection.SetScope(scope))
	require.NoError(t, projection.SaveTransferState(state))
	identity := "send:" + transferID
	id := rgb11TransferResvID(scope, identity)
	reservation := &RGB11TransferReservation{ReservationBase: ReservationBase{Id: id, IsInitiator: true, WalletId: common.WalletId{Id: 19}}, Scope: scope, Identity: identity, State: state}
	raw, err := EncodeToBytes(reservation)
	require.NoError(t, err)
	rkey := GetResvKey(RESV_TYPE_RGB11, id)
	require.NoError(t, data.Write([]byte(rkey), raw))
	var idBytes [16]byte
	binary.BigEndian.PutUint64(idBytes[:8], 1700000000123456)
	idBytes[15] = 7
	direct := swire.DirectMessage{MessageID: hex.EncodeToString(idBytes[:]), SenderAccount: sender, RecipientAccount: sender, SenderMsgID: math.MaxUint64, Ciphertext: []byte("synthetic-private-ciphertext")}
	digest, err := swire.DirectMessageSigningHash(&direct)
	require.NoError(t, err)
	sig, err := schnorr.Sign(priv, digest[:])
	require.NoError(t, err)
	direct.SenderSignature = sig.Serialize()
	outbox := &accountMessageOutboxRecord{Version: accountMessageOutboxVersion, ApplicationID: appID, SourceCoreNode: hex.EncodeToString(priv.PubKey().SerializeCompressed()), Recipient: sender, Kind: AccountMessageKindRGB11Consignment, Direct: direct}
	ikey := string(accountMessageOutboxKeyForSender(sender, appID))
	raw, err = EncodeToBytes(outbox)
	require.NoError(t, err)
	require.NoError(t, data.Write([]byte(ikey), raw))
	pkey := "rgb11-" + scope + "-transfer-" + transferID
	db := &metadataReadOnlyDB{data: data, keys: map[string]bool{rkey: true, pkey: true, ikey: true}}
	http := &metadataNoHTTP{}
	m := &Manager{cfg: &common.Config{Chain: _chain, Env: _env, Mode: _mode}, db: db, http: http, wallet: w, bInited: true,
		status: &Status{CurrentWallet: 19}, accountGeneration: 5, accountSecret: make([]byte, 32),
		accountProfile: &accountManagementProfile{AccountID: sender, RootFingerprint: walletFingerprint(w)},
		walletInfoMap:  map[int64]*WalletInfo{19: {WalletInDB: WalletInDB{Id: 19, Accounts: 1}, Wallet: w}}}
	m.rgbManager = &rgb11Manager{Manager: m}
	return &metadataFixture{m: m, db: db, http: http, reservation: reservation, outbox: outbox, transferID: transferID, reservationKey: rkey, projectionKey: pkey, outboxKey: ikey}
}

func (f *metadataFixture) store(t *testing.T, key string, value any) {
	t.Helper()
	raw, err := EncodeToBytes(value)
	require.NoError(t, err)
	require.NoError(t, f.db.data.Write([]byte(key), raw))
}

func TestRGB11TransferOutboxMetadataReadOnly(t *testing.T) {
	f := newMetadataFixture(t)
	before := map[string][]byte{}
	for key, raw := range f.db.data.data {
		before[key] = append([]byte(nil), raw...)
	}
	got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
	require.NoError(t, err)
	require.True(t, got.OutboxExists)
	require.Equal(t, "present", got.ReadStatus)
	require.Equal(t, f.outbox.ApplicationID, got.ApplicationID)
	require.Equal(t, f.outbox.Direct.MessageID, got.TransportMessageID)
	require.Len(t, got.ApplicationID, 64)
	require.Len(t, got.TransportMessageID, 32)
	require.Equal(t, "18446744073709551615", got.SenderMsgID)
	require.Equal(t, "1700000000123456", got.GeneratedAtUnixMicro)
	require.Equal(t, "2023-11-14T22:13:20.123456Z", got.GeneratedAtUTC)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(encoded, &fields))
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	require.ElementsMatch(t, []string{"read_status", "outbox_exists", "application_id", "transport_message_id", "sender_msg_id", "sender", "recipient", "source_core_node", "generated_at_unix_micro", "generated_at_utc"}, keys)
	require.NotContains(t, string(encoded), "synthetic-private")
	require.Equal(t, before, f.db.data.data)
	require.Equal(t, []string{f.reservationKey, f.projectionKey, f.outboxKey}, f.db.reads)
	require.Zero(t, f.http.calls)
	require.Nil(t, f.m.dkvs)
	require.Nil(t, f.m.accountBackgroundStop)
	require.Nil(t, f.m.actionMonitorStop)
}

func TestRGB11TransferOutboxMetadataAbsenceAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*metadataFixture)
		want    error
		missing bool
	}{
		{"missing", func(f *metadataFixture) { delete(f.db.data.data, f.outboxKey) }, nil, true},
		{"empty", func(f *metadataFixture) { f.db.data.data[f.outboxKey] = nil }, ErrRGB11OutboxCorrupt, false},
		{"decode", func(f *metadataFixture) { f.db.data.data[f.outboxKey] = []byte("private-path secret-fixture") }, ErrRGB11OutboxCorrupt, false},
		{"read", func(f *metadataFixture) { f.db.failKey = f.outboxKey }, ErrRGB11OutboxReadFailed, false},
		{"transfer-missing", func(f *metadataFixture) { delete(f.db.data.data, f.reservationKey) }, ErrRGB11OutboxTransferNotFound, false},
		{"reservation-corrupt", func(f *metadataFixture) { f.db.data.data[f.reservationKey] = []byte("bad") }, ErrRGB11OutboxCorrupt, false},
		{"reservation-read", func(f *metadataFixture) { f.db.failKey = f.reservationKey }, ErrRGB11OutboxReadFailed, false},
		{"projection-read", func(f *metadataFixture) { f.db.failKey = f.projectionKey }, ErrRGB11OutboxReadFailed, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMetadataFixture(t)
			tc.change(f)
			got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
			if tc.missing {
				require.NoError(t, err)
				require.False(t, got.OutboxExists)
				require.Equal(t, "not_found", got.ReadStatus)
				require.Empty(t, got.TransportMessageID)
			} else {
				require.ErrorIs(t, err, tc.want)
				require.Nil(t, got)
				require.NotContains(t, err.Error(), "secret-fixture")
			}
		})
	}
}

func TestRGB11TransferOutboxMetadataRejectsMismatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*metadataFixture)
	}{
		{"kind", func(f *metadataFixture) { f.outbox.Kind = AccountMessageKindRGB11ACK }},
		{"application-id", func(f *metadataFixture) { f.outbox.ApplicationID = strings.Repeat("cd", 32) }},
		{"transport-id", func(f *metadataFixture) { f.outbox.Direct.MessageID = f.outbox.ApplicationID }},
		{"sender", func(f *metadataFixture) { f.outbox.Direct.SenderAccount = strings.Repeat("34", 32) }},
		{"recipient", func(f *metadataFixture) { f.outbox.Direct.RecipientAccount = strings.Repeat("34", 32) }},
		{"source-url", func(f *metadataFixture) { f.outbox.SourceCoreNode = "https://private:credential@core.invalid" }},
		{"source-point", func(f *metadataFixture) { f.outbox.SourceCoreNode = strings.Repeat("00", 33) }},
		{"signature", func(f *metadataFixture) { f.outbox.Direct.SenderSignature[0] ^= 1 }},
		{"timestamp", func(f *metadataFixture) { f.outbox.Direct.MessageID = strings.Repeat("ff", 16) }},
		{"scope", func(f *metadataFixture) { f.reservation.Scope = rgb11StorageScope(20, 0) }},
		{"asset", func(f *metadataFixture) { f.reservation.State.Asset.Name.Ticker = strings.Repeat("34", 32) }},
		{"direction", func(f *metadataFixture) { f.reservation.State.Direction = "receive" }},
		{"canonical-id", func(f *metadataFixture) { f.reservation.State.AddressMessageID = strings.Repeat("cd", 32) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMetadataFixture(t)
			tc.change(f)
			f.store(t, f.outboxKey, f.outbox)
			f.store(t, f.reservationKey, f.reservation)
			got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
			require.ErrorIs(t, err, ErrRGB11OutboxCorrupt)
			require.Nil(t, got)
			require.NotContains(t, err.Error(), "credential")
		})
	}
	t.Run("wrong-network", func(t *testing.T) {
		f := newMetadataFixture(t)
		f.m.cfg.Chain = "mainnet"
		_, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.ErrorIs(t, err, ErrRGB11OutboxUnavailable)
		require.Empty(t, f.db.reads)
	})
	t.Run("wrong-account", func(t *testing.T) {
		f := newMetadataFixture(t)
		f.m.status.CurrentAccount = 1
		_, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.ErrorIs(t, err, ErrRGB11OutboxUnavailable)
		require.Empty(t, f.db.reads)
	})
	t.Run("invalid-transfer", func(t *testing.T) {
		f := newMetadataFixture(t)
		_, err := f.m.GetRGB11TransferOutboxMetadata("../secret")
		require.ErrorIs(t, err, ErrRGB11OutboxInvalidID)
		require.Empty(t, f.db.reads)
	})
}

func TestRGB11TransferOutboxMetadataScopeAndConcurrentDelete(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Manager)
	}{
		{"lock", func(m *Manager) { m.clearAccountManagementSession() }},
		{"generation", func(m *Manager) { m.mutex.Lock(); m.bumpAccountGenerationLocked(); m.mutex.Unlock() }},
		{"account", func(m *Manager) { m.mutex.Lock(); m.status.CurrentAccount = 1; m.mutex.Unlock() }},
		{"wallet", func(m *Manager) { m.mutex.Lock(); m.status.CurrentWallet = 20; m.mutex.Unlock() }},
		{"network", func(m *Manager) { m.mutex.Lock(); m.cfg.Chain = "mainnet"; m.mutex.Unlock() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMetadataFixture(t)
			f.db.afterRead = func(key string) {
				if key == f.outboxKey {
					tc.change(f.m)
				}
			}
			got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
			require.ErrorIs(t, err, ErrRGB11OutboxScopeChanged)
			require.Nil(t, got)
		})
	}
	t.Run("release", func(t *testing.T) {
		f := newMetadataFixture(t)
		closeDone := make(chan struct{})
		f.db.afterRead = func(key string) {
			if key != f.outboxKey {
				return
			}
			go func() {
				defer close(closeDone)
				f.m.Close()
			}()
			require.Eventually(t, func() bool {
				f.m.mutex.RLock()
				defer f.m.mutex.RUnlock()
				return len(f.m.accountSecret) == 0
			}, 2*time.Second, 10*time.Millisecond)
		}
		got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.ErrorIs(t, err, ErrRGB11OutboxScopeChanged)
		require.Nil(t, got)
		<-closeDone
		require.False(t, f.m.bInited)
	})
	t.Run("delete-after-atomic-read", func(t *testing.T) {
		f := newMetadataFixture(t)
		f.db.afterRead = func(key string) {
			if key == f.outboxKey {
				delete(f.db.data.data, key)
			}
		}
		got, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.NoError(t, err)
		require.True(t, got.OutboxExists)
		require.Equal(t, f.outbox.Direct.MessageID, got.TransportMessageID)
		got, err = f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.NoError(t, err)
		require.False(t, got.OutboxExists)
	})
	t.Run("lifecycle-and-publication-gates", func(t *testing.T) {
		f := newMetadataFixture(t)
		f.db.afterRead = func(string) {
			if f.m.channelIdentityMu.TryLock() {
				f.m.channelIdentityMu.Unlock()
				t.Error("identity gate not held")
			}
			if f.m.rgbOperationMu.TryLock() {
				f.m.rgbOperationMu.Unlock()
				t.Error("scope gate not held")
			}
			if f.m.rgbReservationMu.TryLock() {
				f.m.rgbReservationMu.Unlock()
				t.Error("publication gate not held")
			}
		}
		_, err := f.m.GetRGB11TransferOutboxMetadata(f.transferID)
		require.NoError(t, err)
	})
}
