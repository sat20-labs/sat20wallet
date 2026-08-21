package wallet

import (
	"context"
	"sync"
	"testing"
	"time"

	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
)

type rgb11L1MonitorIndexer struct {
	IndexerRPCClient
	height       int
	hashes       map[int]string
	blockStarted chan struct{}
	blockRelease chan struct{}
	startOnce    sync.Once
}

func (p *rgb11L1MonitorIndexer) GetSyncHeight() int {
	return p.height
}

func (p *rgb11L1MonitorIndexer) GetBlockHash(height int) (string, error) {
	if p.blockStarted != nil {
		p.startOnce.Do(func() { close(p.blockStarted) })
	}
	if p.blockRelease != nil {
		<-p.blockRelease
	}
	return p.hashes[height], nil
}

type rgb11L1MonitorEvidence struct {
	statuses map[string]*rgb11wallet.BitcoinTxStatus
}

func (p *rgb11L1MonitorEvidence) GetUTXO(string) (*rgb11wallet.BitcoinUTXO, error) {
	return nil, nil
}

func (p *rgb11L1MonitorEvidence) GetRawTx(string) ([]byte, error) {
	return nil, nil
}

func (p *rgb11L1MonitorEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if status := p.statuses[txid]; status != nil {
		copy := *status
		return &copy, nil
	}
	return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
}

func (p *rgb11L1MonitorEvidence) GetOutspend(string) (*rgb11wallet.BitcoinOutspend, error) {
	return &rgb11wallet.BitcoinOutspend{}, nil
}

func (p *rgb11L1MonitorEvidence) GetTip() (*rgb11wallet.BitcoinTip, error) {
	return &rgb11wallet.BitcoinTip{}, nil
}

func (p *rgb11L1MonitorEvidence) Broadcast([]byte) (string, error) {
	return "", nil
}

func rgb11MonitorHashes(prefix string, end int) map[int]string {
	result := make(map[int]string, end+1)
	for height := 0; height <= end; height++ {
		result[height] = prefix + string(rune('a'+height))
	}
	return result
}

func TestDetectRGB11L1ReorgUsesSixBlocksAndAnchor(t *testing.T) {
	hashes := rgb11MonitorHashes("main-", 10)
	hashAt := func(height int64) (string, error) { return hashes[int(height)], nil }
	checkpoint, err := buildRGB11L1MonitorCheckpoint(
		RGB11L1ChainPoint{Height: 10, Hash: hashes[10]}, hashAt)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Blocks) != 7 || checkpoint.Blocks[0].Height != 4 {
		t.Fatalf("checkpoint window=%+v", checkpoint.Blocks)
	}

	hashes[10] = "fork-10"
	event, err := detectRGB11L1Reorg(checkpoint,
		RGB11L1ChainPoint{Height: 10, Hash: hashes[10]}, hashAt)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || event.Deep || event.CommonAncestor == nil || event.CommonAncestor.Height != 9 {
		t.Fatalf("unexpected shallow reorg event: %+v", event)
	}

	for height := 4; height <= 10; height++ {
		hashes[height] = "deep-" + string(rune('a'+height))
	}
	event, err = detectRGB11L1Reorg(checkpoint,
		RGB11L1ChainPoint{Height: 10, Hash: hashes[10]}, hashAt)
	if err != nil {
		t.Fatal(err)
	}
	if event == nil || !event.Deep || event.CommonAncestor != nil {
		t.Fatalf("unexpected deep reorg event: %+v", event)
	}
}

func TestRGB11L1MonitorReconcilesSettledReceiveOnReorg(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, privateKey, 701)
	evidence := &rgb11L1MonitorEvidence{statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	manager.rgbManager.evidence = evidence

	chain := &rgb11L1MonitorIndexer{height: 10, hashes: rgb11MonitorHashes("main-", 11)}
	l1 := NewIndexerRPCClientMgr()
	l1.Set(chain)
	manager.l1IndexerClient = l1

	state := &rgb11wallet.TransferState{
		TransferID: "monitor-reorg", Direction: "receive", Status: "settled",
		WitnessTxID: "monitor-witness", OutputOutPoints: []string{"monitor-witness:0"},
		MinConfirmations: 1,
	}
	if err := manager.rgbManager.projectionStore.SaveTransferState(state); err != nil {
		t.Fatal(err)
	}
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{
		TxID: state.WitnessTxID, Confirmed: true, Confirmations: 2,
	}

	if err := manager.handleRGB11L1MonitorTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	chain.height = 11
	if err := manager.handleRGB11L1MonitorTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err := manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if err != nil || stored.Status != "settled" {
		t.Fatalf("normal extension changed transfer: state=%+v err=%v", stored, err)
	}

	chain.hashes[11] = "fork-11"
	evidence.statuses[state.WitnessTxID] = &rgb11wallet.BitcoinTxStatus{TxID: state.WitnessTxID}
	if err := manager.handleRGB11L1MonitorTick(context.Background()); err != nil {
		t.Fatal(err)
	}
	stored, err = manager.rgbManager.projectionStore.LoadTransferState(state.TransferID)
	if err != nil || stored.Status != "pending" {
		t.Fatalf("reorg was not reconciled: state=%+v err=%v", stored, err)
	}
	checkpoint, err := manager.rgbManager.loadRGB11L1MonitorCheckpoint()
	if err != nil || checkpoint.Tip.Hash != "fork-11" || len(checkpoint.Blocks) != 7 {
		t.Fatalf("reorg checkpoint=%+v err=%v", checkpoint, err)
	}
}

func TestRGB11L1MonitorDoesNotBlockAccountSwitchDuringRemoteQuery(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, privateKey, 702)
	chain := &rgb11L1MonitorIndexer{
		height: 10, hashes: rgb11MonitorHashes("main-", 10),
		blockStarted: make(chan struct{}), blockRelease: make(chan struct{}),
	}
	l1 := NewIndexerRPCClientMgr()
	l1.Set(chain)
	manager.l1IndexerClient = l1

	tickDone := make(chan error, 1)
	go func() { tickDone <- manager.handleRGB11L1MonitorTick(context.Background()) }()
	select {
	case <-chain.blockStarted:
	case <-time.After(time.Second):
		t.Fatal("monitor did not start remote block hash query")
	}

	switchDone := make(chan struct{})
	go func() {
		manager.SwitchAccount(1)
		close(switchDone)
	}()
	select {
	case <-switchDone:
	case <-time.After(time.Second):
		t.Fatal("account switch waited for monitor remote query")
	}
	if got := manager.GetCurrentAccountId(); got != 1 {
		t.Fatalf("current account=%d want=1", got)
	}

	close(chain.blockRelease)
	select {
	case err := <-tickDone:
		if err != nil {
			t.Fatalf("monitor tick failed after query release: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("monitor tick did not finish after query release")
	}

	manager.SwitchAccount(0)
	if got := manager.GetCurrentAccountId(); got != 0 {
		t.Fatalf("current account=%d want=0", got)
	}
}
