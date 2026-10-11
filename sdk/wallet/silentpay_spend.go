package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

// BuildSilentPaySweep consumes exactly one previously recovered, confirmed SP
// BTC output and sends all its value minus fees to an ordinary BTC or BIP352
// destination. No change output is made; this is intentionally NOT a generic
// coin selector. Ordinary partial sends and wallet-level balance unification
// require separate asset-safe transaction planning.
func (p *Manager) BuildSilentPaySweep(ctx context.Context, walletID int64, index uint32,
	outpoint string, destination string, feeRate int64) (*wire.MsgTx, error) {

	if err := ctx.Err(); err != nil { return nil, err }
	if p==nil || p.l1IndexerClient==nil || p.utxoLockerL1==nil || p.db==nil {
		return nil, ErrSilentPayUnsupported
	}
	keys,err := p.silentPayKeysForAccount(walletID,index)
	if err!=nil { return nil,err }
	defer keys.Scan.Zero()
	defer keys.Spend.Zero()
	last,err := p.LastSyncedSilentPay(walletID,index)
	if err!=nil || !last.Complete { return nil,fmt.Errorf("silentpay: no complete confirmed snapshot") }

	var selected *SPReceivedOutput
	for i:=range last.Outputs {
		out := &last.Outputs[i]
		if !out.Unspent { continue }
		if out.TxID+":"+strconv.FormatUint(uint64(out.Vout),10)==outpoint {
			selected=out
			break
		}
	}
	if selected==nil || selected.TxIndex==0 || selected.Value<330 {
		return nil,fmt.Errorf("silentpay: selected output missing or not spendable")
	}
	point,err := UtxoToWireOutpoint(outpoint)
	if err!=nil { return nil,err }
	if p.isL1SendInputProtected(outpoint) {
		return nil,fmt.Errorf("silentpay: selected BTC input is locked or protected")
	}

	spReader,rawReader,err:=p.spScanRPC()
	if err!=nil{return nil,err}
	status,err:=spReader.SPStatus(ctx)
	if err!=nil{return nil,err}
	if status.Network!=spIndexerExpectedNetwork() || status.SchemaVersion!=1 ||
		status.BaseAnchor.Height<selected.Height {
		return nil,fmt.Errorf("silentpay: Indexer not ready for spend")
	}
	snapshot,err:=spReader.SPSnapshot(ctx)
	if err!=nil{return nil,err}
	query:=SPIndexerPosition{
		Height:selected.Height,BlockHash:selected.BlockHash,
		TxIndex:selected.TxIndex,Vout:selected.Vout,ExpectedTxID:selected.TxID,
	}
	anchor,positions,err:=spReader.SPOutpoints(ctx,snapshot.SnapshotID,[]SPIndexerPosition{query})
	if err!=nil{return nil,err}
	if anchor!=snapshot.Anchor || len(positions)!=1 ||
		positions[0].State!="unspent" || positions[0].TxID!=selected.TxID {
		return nil,fmt.Errorf("silentpay: selected UTXO is not unspent at the verified snapshot")
	}

	raw,err:=rawReader.GetBitcoinRawTx(selected.TxID)
	if err!=nil || raw==nil || raw.Error!="" {
		return nil,fmt.Errorf("silentpay: confirmed transaction unavailable: %v",err)
	}
	txSource,err:=spParseFoundTransaction(raw.RawTx,selected.TxID)
	if err!=nil{return nil,err}
	if uint64(selected.Vout)>=uint64(len(txSource.TxOut)) {
		return nil,fmt.Errorf("silentpay: confirmed output index invalid")
	}
	source:=txSource.TxOut[selected.Vout]
	if source.Value!=selected.Value {
		return nil,fmt.Errorf("silentpay: recovered amount no longer matches Bitcoin")
	}

	// A receiver private key represents the final Taproot OUTPUT key, NOT the
	// BIP86 internal key. TaprootWitnessSignature would double-tweak it.
	tweak:=SilentPayMatch{Vout:selected.Vout,K:selected.K,Tweak:selected.Tweak}
	var compressedSpendOutput []byte
	{
		// The stored tweak was checked against BIP352 when scanning; derive
		// the x-only key without trusting a caller-supplied script.
		var scalar=keys.Spend.Key
		var delta btcec.ModNScalar
		if delta.SetByteSlice(selected.Tweak[:]) || delta.IsZero() {
			return nil,fmt.Errorf("silentpay: invalid stored output tweak")
		}
		scalar.Add(&delta)
		if scalar.IsZero(){return nil,fmt.Errorf("silentpay: invalid spend scalar")}
		compressedSpendOutput=schnorr.SerializePubKey(btcec.PrivKeyFromScalar(&scalar).PubKey())
	}
	tweak.OutputKey=compressedSpendOutput
	signKey,err:=SilentPayOutputSpendKey(keys,tweak)
	if err!=nil{return nil,err}
	defer signKey.Zero()
	expectedScript:=append([]byte{txscript.OP_1,0x20},compressedSpendOutput...)
	if !bytes.Equal(source.PkScript,expectedScript) {
		return nil,fmt.Errorf("silentpay: scanned output script does not match its spend key")
	}

	// Check the asset-aware indexer as well as the SDK's local RGB reservation
	// protection. An unknown asset state is never interpreted as pure BTC.
	assetValue,err:=p.l1IndexerClient.GetTxOutput(outpoint)
	if err!=nil || assetValue==nil || len(assetValue.Assets)!=0 ||
		assetValue.Value()!=source.Value ||
		!bytes.Equal(assetValue.OutValue.PkScript,expectedScript) {
		return nil,fmt.Errorf("silentpay: input is not verified asset-free BTC")
	}
	if err:=ctx.Err();err!=nil{return nil,err}

	rate:=feeRate
	if rate<=0{rate=p.GetFeeRate()}
	if rate<=0{return nil,fmt.Errorf("silentpay: invalid fee rate")}
	var weight utils.TxWeightEstimator
	weight.AddTaprootKeySpendInput(txscript.SigHashDefault)
	weight.AddP2TROutput()
	fee:=weight.Fee(rate)
	if fee<=0 || source.Value-fee<330 {
		return nil,fmt.Errorf("silentpay: insufficient balance after fee")
	}
	tx:=wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(point,nil,nil))
	var destScript []byte
	if strings.HasPrefix(strings.ToLower(destination),"sp1") ||
		strings.HasPrefix(strings.ToLower(destination),"tsp1") {
		target,err:=DecodeSilentPayAddress(destination,GetChainParam())
		if err!=nil{return nil,err}
		destScript,err=SilentPayDestinationScript(target,[]SilentPaySenderInput{{
			Outpoint:*point,Key:signKey,
		}},0)
		if err!=nil{return nil,err}
	} else {
		addr,err:=btcutil.DecodeAddress(destination,GetChainParam())
		if err!=nil{return nil,err}
		if !addr.IsForNet(GetChainParam()) {
			return nil,fmt.Errorf("silentpay: destination network mismatch")
		}
		destScript,err=txscript.PayToAddrScript(addr)
		if err!=nil{return nil,err}
	}
	tx.AddTxOut(wire.NewTxOut(source.Value-fee,destScript))
	fetcher:=txscript.NewCannedPrevOutputFetcher(expectedScript,source.Value)
	sighashes:=txscript.NewTxSigHashes(tx,fetcher)
	digest,err:=txscript.CalcTaprootSignatureHash(sighashes,txscript.SigHashDefault,tx,0,fetcher)
	if err!=nil{return nil,err}
	sig,err:=schnorr.Sign(signKey,digest)
	if err!=nil{return nil,err}
	tx.TxIn[0].Witness=wire.TxWitness{sig.Serialize()}
	// Refuse to broadcast a transaction that fails Bitcoin consensus
	// verification, including parity/key-path edge cases.
	engine,err:=txscript.NewEngine(expectedScript,tx,0,txscript.StandardVerifyFlags,
		nil,sighashes,source.Value,fetcher)
	if err!=nil{return nil,err}
	if err:=engine.Execute();err!=nil {
		return nil,fmt.Errorf("silentpay: invalid BTC spend signature: %w",err)
	}
	return tx,nil
}

func (p *Manager) SendSilentPaySweep(ctx context.Context, walletID int64, index uint32,
	outpoint,destination string,feeRate int64) (string,error) {
	tx,err:=p.BuildSilentPaySweep(ctx,walletID,index,outpoint,destination,feeRate)
	if err!=nil{return "",err}
	if err:=ctx.Err();err!=nil{return "",err}
	// The ordinary broadcast path manages the same L1 locking as other BTC
	// transactions. A failed broadcast must never report spend success.
	return p.BroadcastTx(tx)
}
