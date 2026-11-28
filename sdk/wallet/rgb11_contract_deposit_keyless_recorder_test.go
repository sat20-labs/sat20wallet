package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

var errDepositRecorderDenied = errors.New("keyless RGB11 recorder denies signing")

// No sending or receiving wallet or key capable of signing is constructed.
// The public account clones used by Resume retain the same denial counter.
type rgb11DepositRecorder struct {
	*MonitorWallet
	pub   *secp256k1.PublicKey
	calls *atomic.Int64
}

func (w *rgb11DepositRecorder) Clone() common.Wallet {
	clone := *w
	return &clone
}
func (w *rgb11DepositRecorder) CloneByPubKey(pub []byte) common.Wallet {
	if bytes.Equal(pub, w.pub.SerializeCompressed()) {
		return w.Clone()
	}
	return nil
}
func (w *rgb11DepositRecorder) GetPubKey() *secp256k1.PublicKey        { return w.pub }
func (w *rgb11DepositRecorder) GetNodePubKey() *secp256k1.PublicKey    { return w.pub }
func (w *rgb11DepositRecorder) GetPaymentPubKey() *secp256k1.PublicKey { return w.pub }
func (w *rgb11DepositRecorder) GetPubKeyByIndex(i uint32) *secp256k1.PublicKey {
	if i == 0 {
		return w.pub
	}
	return nil
}
func (w *rgb11DepositRecorder) SignRGB11Psbt(*psbt.Packet, map[int]RGB11InputSigningKey) error {
	w.calls.Add(1)
	return errDepositRecorderDenied
}

// Never forward a broadcast, even on an unexpected regression. Only the
// receiver's selected funding read can be unavailable; no chain data is spent
// or replaced, and no previously accepted receipt is reused for that case.
type rgb11DepositRecorderEvidence struct {
	*rgb11FlowEvidence
	funding       string
	unavailable   atomic.Bool
	misses, sends atomic.Int64
}

func (e *rgb11DepositRecorderEvidence) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	if point == e.funding && e.unavailable.Load() {
		e.misses.Add(1)
		return nil, errors.New("fixture funding evidence unavailable")
	}
	return e.rgb11FlowEvidence.GetUTXO(point)
}
func (e *rgb11DepositRecorderEvidence) Broadcast([]byte) (string, error) {
	e.sends.Add(1)
	return "", errors.New("offline RGB11 recorder denies broadcast")
}

// This is an in-process transport, not a success stub. Positive responses
// require the real receiver and the exact receipt in its independent DB.
type rgb11DepositReceiptTransport struct {
	NodeRPCClient
	receiver *Manager
	contract ContractRuntime
	acks     int
}

func (p *rgb11DepositReceiptTransport) SendActionResultNfty(_ common.Wallet, _ int64, action string, ret int, payload string) error {
	if action != RGB11DepositPeerPrepareAction || ret != 0 {
		return errors.New("unexpected deposit notification")
	}
	var deposit RGB11ContractDeposit
	if err := json.Unmarshal([]byte(payload), &deposit); err != nil {
		return err
	}
	if err := p.receiver.ReceiveRGB11ContractDeposit(context.Background(), p.contract, &deposit); err != nil {
		return err
	}
	receipt, err := p.receiver.GetDB().Read(rgb11DepositKey("receive", deposit.Source.Output.OutPointStr))
	if err != nil || !bytes.Equal(receipt, []byte(payload)) {
		return errors.New("real receiver did not persist this exact envelope")
	}
	p.acks++
	return nil
}

func TestRGB11DepositKeylessSigner(t *testing.T) {
	oldChain, oldIndexerChain := _chain, indexer.CHAIN
	_chain, indexer.CHAIN = "testnet", "testnet"
	t.Cleanup(func() { _chain, indexer.CHAIN = oldChain, oldIndexerChain })

	// BIP341's fixed NUMS point and a public-only tweak. Neither role can
	// derive a private key; no mnemonic, signing fixture or remote RPC is used.
	raw, err := hex.DecodeString("0250929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")
	if err != nil {
		t.Fatal(err)
	}
	pub, err := secp256k1.ParsePubKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	base := &rgb11FlowEvidence{utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte)}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	newParty := func(key *secp256k1.PublicKey) (*Manager, *rgb11DepositRecorder, *rgb11DepositRecorderEvidence) {
		address, err := PubKeyToAddr(key)
		if err != nil {
			t.Fatal(err)
		}
		w := &rgb11DepositRecorder{NewMonitorWallet(address), key, new(atomic.Int64)}
		e := &rgb11DepositRecorderEvidence{rgb11FlowEvidence: base}
		// Discover the inactive account from its public identity before the
		// deposit. Both parties retain their keyless signing recorders.
		m := newRGB11FlowManager(t, w, rpc, e, w.GetId(), accountGroupInactive)
		m.l2IndexerClient = NewIndexerRPCClientMgr()
		m.l2IndexerClient.Set(rpc)
		m.rgbManager.scopeStates.stopReconciliations()
		return m, w, e
	}
	sender, owner, se := newParty(pub)
	receiver, peer, re := newParty(txscript.ComputeTaprootKeyNoScript(pub))
	script, err := AddrToPkScript(owner.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	point := seedRGB11FlowEvidenceUTXO(t, base, script, 100_000, 73)
	output := indexer.NewTxOutput(100_000)
	output.OutPointStr, output.OutValue.PkScript = point, script
	rpc.outputs[point] = output
	rpc.plain = []*indexerwire.TxOutputInfo{{OutPoint: point, Value: output.Value(), PkScript: script}}
	re.funding = point
	ctx := context.Background()
	asset, err := sender.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "WATCH", Name: "Keyless deposit control", Amounts: []uint64{50000},
	})
	if err != nil {
		t.Fatalf("local RGB issuance: %v", err)
	}
	if len(asset.OutPoints) != 1 || asset.OutPoints[0] != point {
		t.Fatal("wrong issuance carrier")
	}
	// Only the independent registry authority signs the name record. The
	// sender and receiver remain keyless and their signers are still denied.
	nameKey, err := dkvs.RGB11NameKey(asset.AssetName.Ticker)
	if err != nil {
		t.Fatal(err)
	}
	contractID, err := rgb11wallet.RGB11ContractIDHex(asset.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	nameValue, err := dkvs.RGB11NameValue(contractID)
	if err != nil {
		t.Fatal(err)
	}
	authority := NewInternalWalletWithMnemonic(testCoreMnemonic, "", GetChainParam())
	nameRecord, err := NewDKVSSignedRecord(authority, nameKey, nameValue, dkvs.RecordOptions{Seq: dkvs.RGB11NameProofSeq, IssueHeight: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Manager{sender, receiver} {
		registry, ok := m.http.(*rgb11MemoryDKVSHTTP)
		if !ok {
			t.Fatal("missing fixture DKVS endpoint")
		}
		registry.mu.Lock()
		registry.records[nameKey] = nameRecord
		registry.mu.Unlock()
		t.Cleanup(func() {
			registry.mu.Lock()
			defer registry.mu.Unlock()
			// The manually seeded registry record is not a wallet upload.
			if got := registry.records[nameKey]; got == nil || dkvs.RecordHash(got) != dkvs.RecordHash(nameRecord) {
				t.Errorf("keyless wallet changed the registry fixture")
			}
			delete(registry.records, nameKey)
		})
		if registered, lookupErr := m.RGB11ContractID(&asset.AssetName); lookupErr != nil || registered != contractID {
			t.Fatalf("fixture registry lookup: id=%s err=%v", registered, lookupErr)
		}
	}
	_, jointScript, err := GetP2WSHscript(pub.SerializeCompressed(), peer.pub.SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	jointAddr, err := GetP2WSHaddressFromScript(jointScript)
	if err != nil {
		t.Fatal(err)
	}
	contractFor := func(m *Manager, local, other *rgb11DepositRecorder) *TranscendContractRuntime {
		c := NewTranscendContractRuntime(m)
		*c.GetAssetName() = asset.AssetName
		c.ChannelAddr, c.localWallet = jointAddr, local
		c.LocalPubKey, c.RemotePubKey = local.pub.SerializeCompressed(), other.pub.SerializeCompressed()
		return c
	}
	transport := &rgb11DepositReceiptTransport{receiver: receiver, contract: contractFor(receiver, peer, owner)}
	sender.SetServerNode(NewNode(transport, "in-process", SERVER_NODE, peer.pub, peer.pub))
	deposit, err := sender.prepareRGB11ContractDeposit(ctx, contractFor(sender, owner, peer), owner.GetAddress(),
		asset.AssetName.String(), "50000", 2, owner, true)
	if err != nil {
		t.Fatalf("prepare without keys: %v", err)
	}
	before := assertRGB11UnsignedPending(t, sender.rgbManager, deposit.TransferID)
	if owner.calls.Load() != 0 || peer.calls.Load() != 0 || se.sends.Load()+re.sends.Load() != 0 {
		t.Fatal("preparation invoked signer or broadcast")
	}
	key := rgb11DepositKey("receive", deposit.Source.Output.OutPointStr)
	re.unavailable.Store(true)
	if _, err = sender.ResumeRGB11ContractDeposit(ctx, deposit.TransferID); err == nil || errors.Is(err, errDepositRecorderDenied) ||
		re.misses.Load() == 0 || transport.acks != 0 || owner.calls.Load() != 0 || peer.calls.Load() != 0 ||
		se.sends.Load()+re.sends.Load() != 0 {
		t.Fatalf("missing first-receipt funding: err=%v missed=%d ack=%d owner=%d peer=%d backend=%d",
			err, re.misses.Load(), transport.acks, owner.calls.Load(), peer.calls.Load(), se.sends.Load()+re.sends.Load())
	}
	if _, err := receiver.GetDB().Read(key); !errors.Is(err, indexer.ErrKeyNotFound) {
		t.Fatalf("rejected receiver stored a receipt or could not verify absence: %v", err)
	}
	assertRGB11UnsignedPending(t, sender.rgbManager, deposit.TransferID)

	re.unavailable.Store(false)
	_, err = sender.ResumeRGB11ContractDeposit(ctx, deposit.TransferID)
	if !errors.Is(err, errDepositRecorderDenied) || transport.acks != 1 || owner.calls.Load() != 1 || peer.calls.Load() != 0 ||
		se.sends.Load()+re.sends.Load() != 0 {
		t.Fatalf("healthy incoming receipt: err=%v ack=%d owner=%d peer=%d backend=%d",
			err, transport.acks, owner.calls.Load(), peer.calls.Load(), se.sends.Load()+re.sends.Load())
	}
	after := assertRGB11UnsignedPending(t, sender.rgbManager, deposit.TransferID)
	if !bytes.Equal(before.SignedTx, after.SignedTx) || !bytes.Equal(before.SignedPSBT, after.SignedPSBT) {
		t.Fatal("denied signer changed the unsigned material")
	}
	for _, src := range after.State.InputOutPoints {
		u, err := se.GetUTXO(src)
		if err != nil || u == nil || u.OutPoint != src {
			t.Fatalf("fixture lost healthy input %s: %v", src, err)
		}
	}
	row, err := sender.GetDB().Read(rgb11DepositKey("send", deposit.TransferID))
	var journal rgb11ContractDepositJournal
	if err != nil || json.Unmarshal(row, &journal) != nil || !journal.PeerAcknowledged || journal.Broadcast {
		t.Fatalf("sender journal did not distinguish receipt from broadcast: %v", err)
	}
	t.Logf("first-receipt unavailable: reads=%d ack=0 signer=0; healthy: exact_receipts=1 sender_recorder=1 signatures=0 backend=0", re.misses.Load())
}
