package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexercommon "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

type spSweepIndexer struct {
	IndexerRPCClient
	raw *wire.MsgTx
	txid string
	output *TxOutput
	anchor SPBaseAnchor
	spent bool
}

func (s *spSweepIndexer) SPStatus(context.Context) (SPIndexerStatus,error) {
	return SPIndexerStatus{
		Network:"testnet4",SchemaVersion:1,CoverageStartHeight:0,
		BaseAnchor:s.anchor,DataReady:true,
	},nil
}
func (s *spSweepIndexer) SPSnapshot(context.Context) (SPIndexerSnapshot,error) {
	return SPIndexerSnapshot{SnapshotID:"sweep-view",Anchor:s.anchor,SchemaVersion:1,CoverageStartHeight:0},nil
}
func (s *spSweepIndexer) SPScan(context.Context,SPScanRequest) (SPIndexerPage,error) {
	return SPIndexerPage{},fmt.Errorf("unexpected public scan during snapshot spend")
}
func (s *spSweepIndexer) SPLookup(context.Context,[]SPIndexerPosition) ([]SPIndexerPositionResult,error) {
	return nil,fmt.Errorf("unexpected txid lookup during snapshot spend")
}
func (s *spSweepIndexer) SPOutpoints(_ context.Context,_ string,req []SPIndexerPosition) (SPBaseAnchor,[]SPIndexerPositionResult,error) {
	if len(req)!=1{return SPBaseAnchor{},nil,fmt.Errorf("expected one outpoint")}
	state:="unspent"
	if s.spent {state="spent"}
	return s.anchor,[]SPIndexerPositionResult{{
		SPIndexerPosition:req[0],TxID:s.txid,State:state,
	}},nil
}
func (s *spSweepIndexer) GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx,error) {
	if txid!=s.txid {return nil,fmt.Errorf("unexpected Bitcoin transaction")}
	var raw bytes.Buffer
	if err:=s.raw.Serialize(&raw);err!=nil{return nil,err}
	return &indexerwire.BitcoinRawTx{TxID:txid,RawTx:hex.EncodeToString(raw.Bytes())},nil
}
func (s *spSweepIndexer) GetTxOutput(outpoint string) (*TxOutput,error) {
	if outpoint!=s.output.OutPointStr{return nil,fmt.Errorf("outpoint unknown")}
	copyOutput:=*s.output
	return &copyOutput,nil
}

func TestSilentPayRealSignedOutputSweepAndRebroadcastSafetyE2E(t *testing.T) {
	oldChain:=_chain
	_chain="testnet"
	defer func(){_chain=oldChain}()
	mgr,receiver:=spE2EAccountManager(t)
	if _,err:=mgr.EnableSilentPay(receiver.GetId(),0);err!=nil {t.Fatal(err)}
	receiveAddress,err:=receiver.GetSilentPayAddress(0)
	if err!=nil {t.Fatal(err)}
	receive,err:=DecodeSilentPayAddress(receiveAddress,&chaincfg.TestNet4Params)
	if err!=nil {t.Fatal(err)}
	payerRaw:=make([]byte,32)
	payerRaw[31]=27
	payer,_:=btcec.PrivKeyFromBytes(payerRaw)
	previousOutpoint:=wire.OutPoint{Hash:chainhash.Hash{21,22,23},Index:3}
	script,err:=SilentPayDestinationScript(receive,[]SilentPaySenderInput{{Outpoint:previousOutpoint,Key:payer}},0)
	if err!=nil{t.Fatal(err)}
	originalPayment:=wire.NewMsgTx(2)
	originalPayment.AddTxIn(wire.NewTxIn(&previousOutpoint,nil,nil))
	originalPayment.AddTxOut(wire.NewTxOut(100000,script))
	txid:=originalPayment.TxHash().String()

	normalized:=payer.Key
	if payer.PubKey().SerializeCompressed()[0]==3{normalized.Negate()}
	aggregatePub:=btcec.PrivKeyFromScalar(&normalized).PubKey()
	var outpointBytes [36]byte
	copy(outpointBytes[:32],previousOutpoint.Hash[:])
	binary.LittleEndian.PutUint32(outpointBytes[32:],previousOutpoint.Index)
	ih,err:=silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs",
		outpointBytes[:],aggregatePub.SerializeCompressed()))
	if err!=nil{t.Fatal(err)}
	T,err:=silentPaySharedSecretPoint(&ih,aggregatePub)
	if err!=nil{t.Fatal(err)}
	receiverKeys,err:=receiver.DeriveSilentPayKeys(0)
	if err!=nil{t.Fatal(err)}
	found,err:=MatchSilentPayOutputs(receiverKeys,T.SerializeCompressed(),[]SilentPayCandidate{{
		Vout:0,OutputKey:script[2:],
	}})
	if err!=nil || len(found)!=1 {t.Fatal("cannot recover fixture SP payment",err)}
	blockHash:=(chainhash.Hash{31,32,33}).String()
	anchor:=SPBaseAnchor{Height:15,BlockHash:blockHash}
	value:=SPReceivedOutput{TxID:txid,Vout:0,TxIndex:1,Height:15,
		BlockHash:blockHash,Value:100000,K:0,Tweak:found[0].Tweak,Unspent:true}
	stored:=spAccountSyncDisk{Version:1,Network:"testnet4",Anchor:anchor,Outputs:[]SPReceivedOutput{value}}
	raw,err:=EncodeToBytes(stored)
	if err!=nil{t.Fatal(err)}
	if err:=mgr.db.Write(spAccountDataKey("testnet4",walletFingerprint(receiver),0),raw);err!=nil{t.Fatal(err)}

	utxo:=txid+":0"
	fake:=&spSweepIndexer{
		raw:originalPayment,txid:txid,anchor:anchor,
		output:&TxOutput{OutPointStr:utxo,OutValue:wire.TxOut{Value:100000,PkScript:script}},
	}
	mgr.l1IndexerClient.SetMaster(fake)
	mgr.utxoLockerL1=NewUtxoLocker(mgr.db,fake,L1_NETWORK_BITCOIN)
	mgr.utxoLockerL1.Init()

	other:=NewInternalWalletWithMnemonic(silentPayOtherMnemonic,"",&chaincfg.TestNet4Params)
	spDest,err:=other.GetSilentPayAddress(0)
	if err!=nil{t.Fatal(err)}
	tx,err:=mgr.BuildSilentPaySweep(context.Background(),receiver.GetId(),0,utxo,spDest,2)
	if err!=nil{t.Fatalf("unable to sign SP funds: %v",err)}
	if len(tx.TxIn)!=1 || len(tx.TxOut)!=1 || tx.TxOut[0].Value<=0 ||
		tx.TxOut[0].Value>=100000 || len(tx.TxIn[0].Witness)!=1 {
		t.Fatalf("unexpected spend amount, witness or outputs: %v",tx)
	}

	// Independent BIP352 recipient derivation for the NEW transaction,
	// where the sender's funding input is itself an SP-derived Taproot key.
	spendKey,err:=SilentPayOutputSpendKey(receiverKeys,found[0])
	if err!=nil{t.Fatal(err)}
	scalar:=spendKey.Key
	if spendKey.PubKey().SerializeCompressed()[0]==3{scalar.Negate()}
	scanInputKey:=btcec.PrivKeyFromScalar(&scalar).PubKey()
	var outpointRaw [36]byte
	out,err:=UtxoToWireOutpoint(utxo)
	if err!=nil{t.Fatal(err)}
	copy(outpointRaw[:32],out.Hash[:])
	binary.LittleEndian.PutUint32(outpointRaw[32:],out.Index)
	h,err:=silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs",
		outpointRaw[:],scanInputKey.SerializeCompressed()))
	if err!=nil{t.Fatal(err)}
	newT,err:=silentPaySharedSecretPoint(&h,scanInputKey)
	if err!=nil{t.Fatal(err)}
	nextKeys,err:=other.DeriveSilentPayKeys(0)
	if err!=nil{t.Fatal(err)}
	result,err:=MatchSilentPayOutputs(nextKeys,newT.SerializeCompressed(),[]SilentPayCandidate{{
		Vout:0,OutputKey:tx.TxOut[0].PkScript[2:],
	}})
	if err!=nil || len(result)!=1 || result[0].Vout!=0{
		t.Fatalf("recipient missed sweep from real SP UTXO: %v %+v",err,result)
	}

	// A once-spent or asset-bearing source is never signed.
	fake.spent=true
	if _,err:=mgr.BuildSilentPaySweep(context.Background(),receiver.GetId(),0,utxo,spDest,2);err==nil {
		t.Fatal("spent UTXO passed latest snapshot check")
	}
	fake.spent=false
	fake.output.Assets=indexercommon.TxAssets{{}}
	if _,err:=mgr.BuildSilentPaySweep(context.Background(),receiver.GetId(),0,utxo,spDest,2);err==nil{
		t.Fatal("asset-bearing UTXO was silently spent as BTC")
	}
}
