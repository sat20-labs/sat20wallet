package wallet

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

type spArchiveFakeIndexer struct {
	IndexerRPCClient
	tx *wire.MsgTx
	anchor SPBaseAnchor
	blockHash string
	T []byte
	failNext bool
	scanCount int
}

func (s *spArchiveFakeIndexer) SPStatus(context.Context) (SPIndexerStatus,error) {
	return SPIndexerStatus{Network:"testnet4",SchemaVersion:1,CoverageStartHeight:0,
		BaseAnchor:s.anchor,DataReady:true},nil
}
func (s *spArchiveFakeIndexer) SPSnapshot(context.Context) (SPIndexerSnapshot,error) {
	return SPIndexerSnapshot{},fmt.Errorf("Archive cannot use Live snapshot")
}
func (s *spArchiveFakeIndexer) SPScan(_ context.Context,req SPScanRequest) (SPIndexerPage,error) {
	s.scanCount++
	if req.Mode!="archive" || req.FromHeight!=0 || req.ToHeight!=s.anchor.Height ||
		req.AnchorHash!=s.anchor.BlockHash {
		return SPIndexerPage{},fmt.Errorf("incorrect Archive query: %+v",req)
	}
	pk0,pk1:=s.tx.TxOut[0].PkScript[2:],s.tx.TxOut[1].PkScript[2:]
	if req.Cursor=="" {
		return SPIndexerPage{Anchor:s.anchor,NextCursor:"next-page",Done:false,
			Blocks:[]SPIndexerBlock{{Height:10,BlockHash:s.blockHash,
				Transactions:[]SPIndexerTransaction{{
					TxIndex:1,Tweak:s.T,P2TROutputs:[]SPIndexerOutput{
						{Vout:0,OutputKey:pk0},{Vout:1,OutputKey:pk1},
					},
				}},
			}}},nil
	}
	if req.Cursor!="next-page" {return SPIndexerPage{},fmt.Errorf("unexpected Archive cursor")}
	if s.failNext {return SPIndexerPage{},fmt.Errorf("injected Archive transport failure")}
	return SPIndexerPage{Anchor:s.anchor,Done:true,Blocks:[]SPIndexerBlock{
		{Height:10,BlockHash:s.blockHash},
		{Height:11,BlockHash:s.anchor.BlockHash},
	}},nil
}
func (s *spArchiveFakeIndexer) SPLookup(_ context.Context,req []SPIndexerPosition) ([]SPIndexerPositionResult,error) {
	if len(req)!=1 || req[0].Height!=10 || req[0].TxIndex!=1 {
		return nil,fmt.Errorf("unexpected archive locator")
	}
	return []SPIndexerPositionResult{{SPIndexerPosition:req[0],TxID:s.tx.TxHash().String()}},nil
}
func (s *spArchiveFakeIndexer) SPOutpoints(context.Context,string,[]SPIndexerPosition) (SPBaseAnchor,[]SPIndexerPositionResult,error) {
	return SPBaseAnchor{},nil,fmt.Errorf("Archive cannot query Live state")
}
func (s *spArchiveFakeIndexer) GetBitcoinRawTx(txid string) (*indexerwire.BitcoinRawTx,error) {
	if txid!=s.tx.TxHash().String(){return nil,fmt.Errorf("wrong Bitcoin txid")}
	var buf bytes.Buffer
	if err:=s.tx.Serialize(&buf);err!=nil{return nil,err}
	return &indexerwire.BitcoinRawTx{TxID:txid,RawTx:hex.EncodeToString(buf.Bytes())},nil
}
func (s *spArchiveFakeIndexer) GetBlockHash(height int) (string,error) {
	if height==11{return s.anchor.BlockHash,nil}
	if height==10{return s.blockHash,nil}
	return "",fmt.Errorf("height not found")
}

func TestSilentPayArchiveBackfillIndependentCheckpointsAndSpentKZeroE2E(t *testing.T) {
	previousChain:=_chain
	_chain="testnet"
	defer func(){_chain=previousChain}()
	mgr,receiver:=spE2EAccountManager(t)
	if _,err:=mgr.EnableSilentPay(receiver.GetId(),0);err!=nil{t.Fatal(err)}
	addr,err:=receiver.GetSilentPayAddress(0)
	if err!=nil{t.Fatal(err)}
	dest,err:=DecodeSilentPayAddress(addr,&chaincfg.TestNet4Params)
	if err!=nil{t.Fatal(err)}
	secretRaw:=make([]byte,32)
	secretRaw[31]=17
	payer,_:=btcec.PrivKeyFromBytes(secretRaw)
	point:=wire.OutPoint{Hash:chainhash.Hash{41,42},Index:7}
	script0,err:=SilentPayDestinationScript(dest,[]SilentPaySenderInput{{Outpoint:point,Key:payer}},0)
	if err!=nil{t.Fatal(err)}
	script1,err:=SilentPayDestinationScript(dest,[]SilentPaySenderInput{{Outpoint:point,Key:payer}},1)
	if err!=nil{t.Fatal(err)}
	tx:=wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&point,nil,nil))
	tx.AddTxOut(wire.NewTxOut(50000,script0))
	tx.AddTxOut(wire.NewTxOut(70000,script1))
	var serialized [36]byte
	copy(serialized[:32],point.Hash[:])
	binary.LittleEndian.PutUint32(serialized[32:],point.Index)
	priv:=payer.Key
	if payer.PubKey().SerializeCompressed()[0]==3{priv.Negate()}
	P:=btcec.PrivKeyFromScalar(&priv).PubKey()
	h,err:=silentPayCheckedScalar(silentPayTaggedHash("BIP0352/Inputs",serialized[:],P.SerializeCompressed()))
	if err!=nil{t.Fatal(err)}
	T,err:=silentPaySharedSecretPoint(&h,P)
	if err!=nil{t.Fatal(err)}
	indexer:=&spArchiveFakeIndexer{tx:tx,anchor:SPBaseAnchor{Height:11,BlockHash:(chainhash.Hash{11}).String()},
		blockHash:(chainhash.Hash{10}).String(),T:T.SerializeCompressed(),failNext:true}
	mgr.l1IndexerClient.SetMaster(indexer)

	if _,err:=mgr.SyncSilentPayArchive(context.Background());err==nil{
		t.Fatal("partial Archive pages advanced history")
	}
	if _,err:=mgr.LastSyncedSilentPayHistory(receiver.GetId(),0);err==nil{
		t.Fatal("partial Archive scan was durably published")
	}
	indexer.failNext=false
	result,err:=mgr.SyncSilentPayArchive(context.Background())
	if err!=nil{t.Fatal(err)}
	if len(result)!=1||!result[0].Complete||len(result[0].Payments)!=2 ||
		result[0].Payments[0].Vout!=0||result[0].Payments[1].Vout!=1||
		result[0].Payments[0].Value!=50000||result[0].Payments[1].Value!=70000 {
		t.Fatalf("Archive missed spent k=0 or live k=1: %+v",result)
	}
	cached,err:=mgr.LastSyncedSilentPayHistory(receiver.GetId(),0)
	if err!=nil||!cached.Complete||len(cached.Payments)!=2{t.Fatalf("Archive checkpoint was not persisted: %v",err)}
	before:=indexer.scanCount
	again,err:=mgr.SyncSilentPayArchive(context.Background())
	if err!=nil||len(again)!=1||len(again[0].Payments)!=2||indexer.scanCount!=before{
		t.Fatalf("same-tip historical refresh performed unnecessary replay: %v",err)
	}
	if _,err:=mgr.EnableSilentPay(receiver.GetId(),1);err!=nil{t.Fatal(err)}
	two,err:=mgr.SyncSilentPayArchive(context.Background())
	if err!=nil || len(two)!=2 || len(two[0].Payments)!=2 || len(two[1].Payments)!=0 ||
		!two[1].Complete {
		t.Fatalf("newly enabled subaccount reused another account's cursor: %+v %v",two,err)
	}
}
