package wallet

import (
 "bytes"
 "fmt"
 "sort"
 "strings"
 "sync"
 "testing"

 "github.com/btcsuite/btcd/txscript"
 "github.com/btcsuite/btcd/wire"
 indexer "github.com/sat20-labs/indexer/common"
 indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
 rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Bitcoin consensus/network is the one controlled boundary. Native RGB
// validation and transaction construction/signatures are not replaced.
type coreE2EChain struct {
 mu sync.Mutex
 sequence int
 outputs map[string]*TxOutput
 raw map[string][]byte
 status map[string]rgb11wallet.BitcoinTxStatus
 spent map[string]string
 broadcasts int
 names map[string]string
}
func newCoreE2EChain() *coreE2EChain {
 return &coreE2EChain{outputs:map[string]*TxOutput{},raw:map[string][]byte{},status:map[string]rgb11wallet.BitcoinTxStatus{},spent:map[string]string{},names:map[string]string{}}
}
func (c *coreE2EChain) setNameOwner(name,address string) {c.mu.Lock();defer c.mu.Unlock();c.names[name]=address}
func (c *coreE2EChain) fund(t *testing.T,address string,count int) {
 t.Helper()
 script,err:=AddrToPkScript(address,GetChainParam()); coreRequire(t,"fund controlled Bitcoin output",err)
 c.mu.Lock(); defer c.mu.Unlock()
 for i:=0;i<count;i++ {
  c.sequence++
  tx:=wire.NewMsgTx(2)
  tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index:0xffffffff},[]byte(fmt.Sprintf("core-e2e-%d",c.sequence)),nil))
  tx.AddTxOut(wire.NewTxOut(100000,script))
  var raw bytes.Buffer; coreRequire(t,"serialize controlled funding",tx.Serialize(&raw))
  id:=tx.TxID(); point:=id+":0"
  output:=indexer.NewTxOutput(100000); output.OutPointStr=point; output.OutValue.PkScript=append([]byte(nil),script...)
  c.outputs[point]=output; c.raw[id]=raw.Bytes()
  c.status[id]=rgb11wallet.BitcoinTxStatus{TxID:id,Confirmed:true,Confirmations:6}
 }
}
func (c *coreE2EChain) GetUTXO(point string)(*rgb11wallet.BitcoinUTXO,error) {
 c.mu.Lock(); defer c.mu.Unlock()
 output:=c.outputs[point]; if output==nil {return nil,fmt.Errorf("controlled UTXO not found")}
 id:=strings.Split(point,":")[0]
 return &rgb11wallet.BitcoinUTXO{OutPoint:point,Value:output.OutValue.Value,PkScript:append([]byte(nil),output.OutValue.PkScript...),Confirmations:c.status[id].Confirmations},nil
}
func (c *coreE2EChain) GetRawTx(id string)([]byte,error) {
 c.mu.Lock(); defer c.mu.Unlock()
 value:=c.raw[id]; if len(value)==0 {return nil,fmt.Errorf("controlled transaction not found")}
 return append([]byte(nil),value...),nil
}
func (c *coreE2EChain) GetTxStatus(id string)(*rgb11wallet.BitcoinTxStatus,error) {
 c.mu.Lock(); defer c.mu.Unlock()
 value,ok:=c.status[id]; if !ok {value=rgb11wallet.BitcoinTxStatus{TxID:id}}
 return &value,nil
}
func (c *coreE2EChain) GetOutspend(point string)(*rgb11wallet.BitcoinOutspend,error) {
 c.mu.Lock(); defer c.mu.Unlock()
 return &rgb11wallet.BitcoinOutspend{Spent:c.spent[point]!="",SpendingTx:c.spent[point]},nil
}
func (*coreE2EChain) GetTip()(*rgb11wallet.BitcoinTip,error) {
 return &rgb11wallet.BitcoinTip{Height:100,BlockHash:strings.Repeat("1",64)},nil
}
func (c *coreE2EChain) Broadcast(raw []byte)(string,error) {
 tx:=wire.NewMsgTx(2)
 if err:=tx.Deserialize(bytes.NewReader(raw));err!=nil{return "",err}
 c.mu.Lock(); defer c.mu.Unlock()
 c.broadcasts++
 id:=tx.TxID()
 if len(c.raw[id])!=0 {return id,nil}
 prev:=make(map[wire.OutPoint]*wire.TxOut)
 var input,output int64
 for _,in:=range tx.TxIn {
  point:=in.PreviousOutPoint.String(); value:=c.outputs[point]
  if value==nil || c.spent[point]!="" {return "",fmt.Errorf("controlled input missing or spent")}
  v:=value.OutValue; prev[in.PreviousOutPoint]=&v; input+=v.Value
 }
 for _,out:=range tx.TxOut {if out.Value<0{return "",fmt.Errorf("negative output")};output+=out.Value}
 if output>input{return "",fmt.Errorf("Bitcoin value was created")}
 if err:=VerifySignedTx(tx,txscript.NewMultiPrevOutFetcher(prev));err!=nil{return "",fmt.Errorf("verify actual Bitcoin signatures: %w",err)}
 c.raw[id]=append([]byte(nil),raw...)
 c.status[id]=rgb11wallet.BitcoinTxStatus{TxID:id,InMempool:true}
 for _,in:=range tx.TxIn {c.spent[in.PreviousOutPoint.String()]=id}
 for i,out:=range tx.TxOut {
  point:=fmt.Sprintf("%s:%d",id,i); value:=indexer.NewTxOutput(out.Value)
  value.OutPointStr=point; value.OutValue.PkScript=append([]byte(nil),out.PkScript...);c.outputs[point]=value
 }
 return id,nil
}
func (c *coreE2EChain) confirm(id string) {
 c.mu.Lock();defer c.mu.Unlock()
 c.status[id]=rgb11wallet.BitcoinTxStatus{TxID:id,Confirmed:true,Confirmations:6}
}
func (c *coreE2EChain) broadcastCount() int {c.mu.Lock();defer c.mu.Unlock();return c.broadcasts}

type coreE2EL1Indexer struct {IndexerRPCClient; chain *coreE2EChain}
func (p *coreE2EL1Indexer) GetTxOutput(point string)(*TxOutput,error) {
 p.chain.mu.Lock();defer p.chain.mu.Unlock()
 if value:=p.chain.outputs[point];value!=nil{return value.Clone(),nil}
 return nil,fmt.Errorf("controlled indexer output missing")
}
func (p *coreE2EL1Indexer) GetUtxoListWithTicker(address string,_ *indexer.AssetName)[]*indexerwire.TxOutputInfo {
 script,err:=AddrToPkScript(address,GetChainParam());if err!=nil{return nil}
 p.chain.mu.Lock();defer p.chain.mu.Unlock()
 var result []*indexerwire.TxOutputInfo
 for point,value:=range p.chain.outputs {
  if p.chain.spent[point]=="" && bytes.Equal(value.OutValue.PkScript,script) {
   result=append(result,&indexerwire.TxOutputInfo{OutPoint:point,Value:value.OutValue.Value,PkScript:append([]byte(nil),script...)})
  }
 }
 sort.Slice(result,func(i,j int)bool{return result[i].OutPoint<result[j].OutPoint})
 return result
}
func (p *coreE2EL1Indexer) GetNameInfo(name string)(*indexerwire.OrdinalsName,error) {
 p.chain.mu.Lock();defer p.chain.mu.Unlock()
 address:=p.chain.names[name]
 if address=="" {return nil,fmt.Errorf("controlled name not found")}
 return &indexerwire.OrdinalsName{NftItem:indexerwire.NftItem{Name:name,Address:address}},nil
}
func (*coreE2EL1Indexer) GetSyncHeight()int{return 100}
