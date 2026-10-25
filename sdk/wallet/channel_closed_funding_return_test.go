package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
)

var errClosedFundingReturnWrite = errors.New("ordinary test: returned FundingCreated persistence unavailable")
var errClosedFundingReturnRPC = errors.New("ordinary test: FundingCreated returned error")

// Keep the real backend's atomic transaction, including when a failed batch is
// closed without Flush. The fault is armed by an already-returning RPC only.
type closedFundingReturnDB struct {
	indexer.KVDB
	failWrite, failBatch atomic.Bool
	beforeFlush          func()
}

func (d *closedFundingReturnDB) Write(key, value []byte) error {
	if d.failWrite.Load() {
		return errClosedFundingReturnWrite
	}
	return d.KVDB.Write(key, value)
}
func (d *closedFundingReturnDB) NewAtomicWriteBatch() indexer.WriteBatch {
	return &closedFundingReturnBatch{WriteBatch: d.KVDB.(indexer.AtomicWriteBatchDB).NewAtomicWriteBatch(), db: d}
}

type closedFundingReturnBatch struct {
	indexer.WriteBatch
	db *closedFundingReturnDB
}

func (b *closedFundingReturnBatch) Flush() error {
	if b.db.beforeFlush != nil {
		b.db.beforeFlush()
	}
	if b.db.failBatch.Load() {
		return errClosedFundingReturnWrite
	}
	return b.WriteBatch.Flush()
}

type closedFundingReturnIndexer struct{ IndexerRPCClient }

func (*closedFundingReturnIndexer) GetBestHeight() int64 { return 1000 }

type closedFundingReturnClient struct {
	NodeRPCClient
	manager                                 *Manager
	old                                     *Channel
	database                                *closedFundingReturnDB
	openCalls, createdCalls, broadcastCalls atomic.Int32
	created                                 func(*FundingReservation, int) error
}

func (c *closedFundingReturnClient) SendOpenChannelReq(f *FundingReservation) error {
	c.openCalls.Add(1)
	server := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	fee := &ChannelFeeConfig{CommitmentFee: 1000, CommitmentFeeRate: 1}
	f.Id = 7002
	candidate := *f.Channel
	candidate.RemoteChanCfg.PaymentKey = server.GetPaymentPubKey()
	candidate.RemoteChanCfg.RevocationBasePoint = server.GetRevocationBaseKey()
	bind, err := c.manager.AnchorOutputsActive()
	if err != nil {
		return err
	}
	anchor := CreateOpeningAnchorTx(&candidate, f.FundingUtxos[0], candidate.Capacity, 0, nil, c.manager.GetDAOPkScript(&candidate), bind)
	if anchor == nil {
		return errors.New("fixture unsigned anchor")
	}
	invoice, err := c.manager.AnchorInvoice(anchor)
	if err != nil {
		return err
	}
	invoiceSig, err := server.SignMessage(invoice)
	if err != nil {
		return err
	}
	f.Accept = &wwire.AcceptChannel{Id: f.Id, CommitHeight: c.old.CommitHeight + 1, CsvDelay: 10, OpenFee: fee.ToOpenChannelFee(),
		FundingKey: server.GetPaymentPubKey().SerializeCompressed(), RevocationBasePoint: server.GetRevocationBaseKey().SerializeCompressed(),
		CommitmentPoint: server.GetCommitSecret(f.Req.NodeId, uint32(c.old.CommitHeight+1)).PubKey().SerializeCompressed(), InvoiceSig: invoiceSig}
	return nil
}
func (c *closedFundingReturnClient) SendFundingCreatedReq(f *FundingReservation) error {
	return c.created(f, int(c.createdCalls.Add(1)))
}
func (c *closedFundingReturnClient) SendFundingBroadcastedReq(*FundingReservation) error {
	c.broadcastCalls.Add(1)
	return nil
}
func closedFundingReturnSigned(c *closedFundingReturnClient, f *FundingReservation) error {
	server := NewInternalWalletWithMnemonic("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about", "", GetChainParam())
	point := f.LocalWallet().GetCommitSecret(f.Channel.PeerNodeId, uint32(f.Channel.CommitHeight))
	bootstrap := c.manager.GetBootstrapNodePaymentPubKey()
	keys := DeriveCommitmentKeys(point.PubKey(), 0, bootstrap, nil, f.Channel)
	tx, _, _, err := c.manager.CreateCommitTx(0, f.Channel, keys, bootstrap, f.Req.FeeRate)
	if err != nil {
		return err
	}
	sigs, err := PartialSignTxWithWallet(server, tx, f.Channel.GetCommitmentPrefetchor(), f.Channel.RedeemScript, false, f.LocalWallet().GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return err
	}
	deanchor, fetcher, err := CreateClosingDeAnchorTx(f.Channel, tx.TxID(), c.manager.GetDAOPkScript(f.Channel))
	if err != nil {
		return err
	}
	deSigs, err := PartialSignTxWithWallet_SatsNet(server, deanchor, fetcher, f.Channel.RedeemScript, f.LocalWallet().GetPaymentPubKey().SerializeCompressed())
	if err != nil {
		return err
	}
	f.FundingSigned = &wwire.FundingSigned{Id: f.Id, CommitSig: sigs, DeAnchorSig: deSigs}
	return nil
}
func newClosedFundingReturnFixture(t *testing.T) (*Manager, *Channel, *closedFundingReturnClient, *closedFundingReturnDB, string) {
	t.Helper()
	manager, old, _, _ := closedOpenDispatchFixture(t)
	directory := t.TempDir()
	backend := NewKVDB(directory)
	if backend == nil {
		t.Fatal("real funding-return database")
	}
	database := &closedFundingReturnDB{KVDB: backend}
	t.Cleanup(func() { _ = database.Close() })
	manager.db = database
	if err := manager.SaveChannelToDB(old); err != nil {
		t.Fatal(err)
	}
	manager.bootstrapNode = []*Node{manager.serverNode}
	manager.l2IndexerClient = &IndexerRPCClientMgr{active: &closedFundingReturnIndexer{}}
	client := &closedFundingReturnClient{manager: manager, old: old, database: database}
	manager.serverNode.SetRPCClient(client)
	return manager, old, client, database, directory
}
func callClosedFundingReturn(m *Manager, old *Channel) error {
	_, err := m.FunderInitReOpenProcess(old.ChanPoint.Value(), old.ChanPoint, "reopen", false, false, nil, "")
	return err
}
func durableClosedFundingReturn(t *testing.T, d *closedFundingReturnDB, owner *LocalActionPerformData) *closedChannelIntent {
	t.Helper()
	raw, err := d.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
	if err != nil {
		t.Fatal(err)
	}
	var stored LocalActionPerformData
	if err := DecodeFromBytes(raw, &stored); err != nil {
		t.Fatal(err)
	}
	intent, err := loadClosedChannelIntent(&stored)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}
func closedFundingReturnJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func assertClosedFundingReturnIdentity(t *testing.T, original, next *closedChannelIntent) {
	t.Helper()
	if original.ChannelID != next.ChannelID || original.OldParentID != next.OldParentID ||
		!bytes.Equal(closedFundingReturnJSON(t, original.Request), closedFundingReturnJSON(t, next.Request)) ||
		!bytes.Equal(original.RequestSignature, next.RequestSignature) ||
		!bytes.Equal(closedFundingReturnJSON(t, original.FundingCreated), closedFundingReturnJSON(t, next.FundingCreated)) ||
		!bytes.Equal(closedFundingReturnJSON(t, original.Accept), closedFundingReturnJSON(t, next.Accept)) ||
		!bytes.Equal(original.FundingInputsSnapshot, next.FundingInputsSnapshot) {
		t.Fatal("returned retry replaced original Open, child, signed FundingCreated or funding inputs")
	}
}

func TestClosedFundingCreatedReturnedSuccessWriteFailureResumesWithoutRPC(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	var response []byte
	client.created = func(f *FundingReservation, call int) error {
		if call != 1 {
			return errors.New("success receipt replayed FundingCreated")
		}
		if err := closedFundingReturnSigned(client, f); err != nil {
			return err
		}
		response = closedFundingReturnJSON(t, f.FundingSigned)
		database.failBatch.Store(true)
		return nil
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatalf("success did not reach returned-result atomic batch fault: %v", err)
	}
	owner, before := onlyClosedOpenOwner(t, m)
	if before.Step != "created_inflight" || before.FundingSigned != nil || len(owner.ActionResvs) != 1 {
		t.Fatal("failed write published result or lost exact child")
	}
	if disk := durableClosedFundingReturn(t, database, owner); disk.source != before.source {
		t.Fatal("failed batch changed durable owner")
	}
	childBefore, err := database.Read([]byte(GetResvKey(RESV_TYPE_OPEN, before.Accept.Id)))
	if err != nil {
		t.Fatal(err)
	}
	database.failBatch.Store(false)
	if err := callClosedFundingReturn(m, old); err != nil {
		t.Fatalf("same-process returned FundingSigned stayed blocked: %v", err)
	}
	nextOwner, after := onlyClosedOpenOwner(t, m)
	if nextOwner != owner || after.Step != "funding_monitor" || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 || client.broadcastCalls.Load() != 1 {
		t.Fatalf("known success did not resume locally: step=%s Open=%d Created=%d", after.Step, client.openCalls.Load(), client.createdCalls.Load())
	}
	assertClosedFundingReturnIdentity(t, before, after)
	if !bytes.Equal(response, closedFundingReturnJSON(t, after.FundingSigned)) {
		t.Fatal("retry replaced exact returned FundingSigned bytes")
	}
	var beforeFunding, afterFunding FundingDataInDB
	if err := DecodeFromBytes(childBefore, &beforeFunding); err != nil {
		t.Fatal(err)
	}
	raw, err := database.Read([]byte(GetResvKey(RESV_TYPE_OPEN, before.Accept.Id)))
	if err != nil {
		t.Fatal(err)
	}
	if err := DecodeFromBytes(raw, &afterFunding); err != nil {
		t.Fatal(err)
	}
	if beforeFunding.Id != afterFunding.Id || beforeFunding.FundingTx != nil || afterFunding.FundingTx != nil || afterFunding.NeedSendFundingTx {
		t.Fatal("retry created a new payment or funding transaction")
	}
}

func TestClosedFundingCreatedReturnedErrorWriteFailureKeepsExactRetry(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	var exact []byte
	client.created = func(f *FundingReservation, call int) error {
		raw := closedFundingReturnJSON(t, f.FundingCreated)
		if call == 1 {
			exact = raw
			database.failWrite.Store(true)
		} else if !bytes.Equal(exact, raw) {
			return errors.New("error retry changed exact FundingCreated")
		}
		return errClosedFundingReturnRPC
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) || !errors.Is(err, errClosedFundingReturnRPC) {
		t.Fatalf("returned error/write failure missing: %v", err)
	}
	owner, before := onlyClosedOpenOwner(t, m)
	database.failWrite.Store(false)
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnRPC) {
		t.Fatalf("known returned error remained inflight: %v", err)
	}
	next, after := onlyClosedOpenOwner(t, m)
	if next != owner || after.Step != "created_retry" || client.createdCalls.Load() != 2 || client.openCalls.Load() != 1 {
		t.Fatalf("error retry did not reuse exact child: step=%s calls=%d", after.Step, client.createdCalls.Load())
	}
	assertClosedFundingReturnIdentity(t, before, after)
}

func TestClosedFundingCreatedLiveConcurrentCallStaysPending(t *testing.T) {
	m, old, client, database, _ := newClosedFundingReturnFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	client.created = func(f *FundingReservation, _ int) error {
		close(started)
		<-release
		database.failWrite.Store(true)
		return errClosedFundingReturnRPC
	}
	done := make(chan error, 1)
	go func() { done <- callClosedFundingReturn(m, old) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("FundingCreated did not start")
	}
	if err := callClosedFundingReturn(m, old); err == nil || client.createdCalls.Load() != 1 {
		t.Fatal("concurrent call released live FundingCreated dispatcher")
	}
	close(release)
	if err := <-done; !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatal(err)
	}
}

func TestClosedFundingCreatedUnknownDiskRestartRemainsPending(t *testing.T) {
	m, old, client, database, directory := newClosedFundingReturnFixture(t)
	client.created = func(f *FundingReservation, _ int) error {
		if err := closedFundingReturnSigned(client, f); err != nil {
			return err
		}
		database.failBatch.Store(true)
		return nil
	}
	if err := callClosedFundingReturn(m, old); !errors.Is(err, errClosedFundingReturnWrite) {
		t.Fatal(err)
	}
	owner, before := onlyClosedOpenOwner(t, m)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database.KVDB = NewKVDB(directory)
	if database.KVDB == nil {
		t.Fatal("reopen real database")
	}
	database.failBatch.Store(false)
	// A new Manager and decoded owner have no trustworthy returned receipt.
	peer := &channelHeartbeatTestClient{}
	restarted := newChannelHeartbeatTestManager(t, peer)
	restarted.db, restarted.status, restarted.walletInfoMap = database, m.status, m.walletInfoMap
	restarted.wallet = m.wallet.Clone()
	restarted.serverNode = NewNode(client, "test", SERVER_NODE, m.serverNode.Pubkey, m.serverNode.NodeId)
	restarted.ResetResvMaps()
	raw, err := database.Read([]byte(GetResvKey(RESV_TYPE_LOCALACTION, owner.Id)))
	if err != nil {
		t.Fatal(err)
	}
	var decoded LocalActionPerformData
	if err := DecodeFromBytes(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	restarted.AddResv(&decoded)
	if err := restarted.rehydrateClosedChannelIntents(); err != nil {
		t.Fatal(err)
	}
	if err := callClosedFundingReturn(restarted, old); err == nil || !strings.Contains(err.Error(), "created_inflight") {
		t.Fatalf("cold unknown dispatch did not stay explicitly pending: %v", err)
	}
	_, after := onlyClosedOpenOwner(t, restarted)
	if after.source != before.source || client.createdCalls.Load() != 1 || client.openCalls.Load() != 1 {
		t.Fatal("cold restart replayed unknown FundingCreated or replaced source")
	}
}
