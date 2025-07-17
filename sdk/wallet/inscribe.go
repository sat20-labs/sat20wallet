package wallet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"

	indexer "github.com/sat20-labs/indexer/common"
)

type InscriptionData struct {
	ContentType      string `json:"contentType"`
	Body             []byte `json:"body"`
	RuneName         []byte `json:"runeName"`
	RevealTxNullData []byte `json:"nullData"` // op_return data
}

type PrevOutput struct {
	TxId     string `json:"txId"`
	VOut     uint32 `json:"vOut"`
	Amount   int64  `json:"amount"`
	PkScript []byte `json:"pkscript"`
}

type PrevOutputs []*PrevOutput

type UtxoViewpoint map[wire.OutPoint][]byte

func (s PrevOutputs) UtxoViewpoint(net *chaincfg.Params) (UtxoViewpoint, error) {
	view := make(UtxoViewpoint, len(s))
	for _, v := range s {
		h, err := chainhash.NewHashFromStr(v.TxId)
		if err != nil {
			return nil, err
		}

		view[wire.OutPoint{Index: v.VOut, Hash: *h}] = v.PkScript
	}
	return view, nil
}

type InscriptionRequest struct {
	CommitTxPrevOutputList PrevOutputs     `json:"commitTxPrevOutputList"`
	CommitFeeRate          int64           `json:"commitFeeRate"`
	RevealFeeRate          int64           `json:"revealFeeRate"`
	InscriptionData        InscriptionData `json:"inscriptionData"`
	RevealOutValue         int64           `json:"revealOutValue"`
	MinChangeValue         int64           `json:"minChangeValue"`

	DestAddress   string `json:"destAddress"`
	ChangeAddress string `json:"changeAddress"`

	Signer    Signer
	PublicKey *btcec.PublicKey
}

type inscriptionTxCtxData struct {
	InscriptionScript       []byte
	CommitTxAddress         string
	CommitTxAddressPkScript []byte
	ControlBlockWitness     []byte
	RevealTxPrevOutput      *wire.TxOut
	RevealTxNullData        []byte
}

type Signer func(tx *wire.MsgTx, prevOutFetcher txscript.PrevOutputFetcher) error

type InscriptionBuilder struct {
	Network                   *chaincfg.Params
	CommitTxPrevOutputFetcher *txscript.MultiPrevOutFetcher
	Signer                    Signer
	PublicKey                 *btcec.PublicKey
	RevealPrivateKey          *btcec.PrivateKey

	CommitAddr string
	RevealAddr string

	InscriptionTxCtxData      *inscriptionTxCtxData
	RevealTxPrevOutputFetcher *txscript.MultiPrevOutFetcher
	CommitTxPrevOutputList    []*PrevOutput
	RevealTx                  *wire.MsgTx
	CommitTx                  *wire.MsgTx
	MustCommitTxFee           int64
	MustRevealTxFee           int64
}

// 跟ResvStatus保持一致
const (
	RS_FAILED        int = -1
	RS_CLOSED        int = 0

	RS_INIT      int = 0x99
	RS_CONFIRMED int = 0x10000

	RS_INSCRIBING_COMMIT_BROADCASTED int = 0x3000
	RS_INSCRIBING_REVEAL_BROADCASTED int = 0x3001
	RS_INSCRIBING_CONFIRMED          int = RS_CONFIRMED
)
type InscribeResv struct {
	Id               int64
	Status           int

	CommitTx         *wire.MsgTx `json:"commitTx"`
	RevealTx         *wire.MsgTx `json:"revealTx"`
	CommitTxFee      int64       `json:"commitTxFee"`
	RevealTxFee      int64       `json:"revealTxFee"`
	CommitAddr       string      `json:"commitAddr"`
	RevealPrivateKey []byte      `json:"revealPrivateKey"`
}


const (
	DefaultTxVersion      = 2
	DefaultSequenceNum    = 0xfffffffd
	DefaultRevealOutValue = int64(330)
	DefaultMinChangeValue = int64(330)

	MaxStandardTxWeight = 4000000 / 10
	WitnessScaleFactor  = 4

	OrdPrefix = "ord"
)

func NewInscriptionTool(network *chaincfg.Params, request *InscriptionRequest) (
	*InscriptionBuilder, error) {
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		return nil, err
	}
	tool := &InscriptionBuilder{
		Network:                   network,
		CommitTxPrevOutputFetcher: txscript.NewMultiPrevOutFetcher(nil),
		RevealTxPrevOutputFetcher: txscript.NewMultiPrevOutFetcher(nil),
		CommitTxPrevOutputList:    request.CommitTxPrevOutputList,

		Signer:           request.Signer,
		PublicKey:        request.PublicKey,
		RevealAddr:       request.DestAddress,
		RevealPrivateKey: privKey,
	}
	return tool, tool.initTool(network, request)
}

func (builder *InscriptionBuilder) initTool(network *chaincfg.Params,
	request *InscriptionRequest) error {
	revealOutValue := DefaultRevealOutValue
	if request.RevealOutValue > 0 {
		revealOutValue = request.RevealOutValue
	}
	minChangeValue := DefaultMinChangeValue
	if request.MinChangeValue > 0 {
		minChangeValue = request.MinChangeValue
	}

	inscriptionTxCtxData, err := newInscriptionTxCtxData(network, request,
		builder.RevealPrivateKey.PubKey())
	if err != nil {
		return err
	}
	builder.InscriptionTxCtxData = inscriptionTxCtxData

	totalRevealPrevOutputValue, err := builder.buildEmptyRevealTx(revealOutValue,
		request.RevealFeeRate)
	if err != nil {
		return err
	}
	err = builder.buildCommitTx(request.CommitTxPrevOutputList,
		request.ChangeAddress, totalRevealPrevOutputValue, request.CommitFeeRate, minChangeValue)
	if err != nil {
		return err
	}
	err = builder.signCommitTx()
	if err != nil {
		return errors.New("sign commit tx error")
	}
	err = builder.completeRevealTx()
	if err != nil {
		return err
	}
	return nil
}

func newInscriptionTxCtxData(network *chaincfg.Params, inscriptionRequest *InscriptionRequest,
	pubkey *secp256k1.PublicKey) (*inscriptionTxCtxData, error) {

	inscriptionBuilder := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(pubkey)).
		AddOp(txscript.OP_CHECKSIG).
		AddOp(txscript.OP_FALSE).
		AddOp(txscript.OP_IF).
		AddData([]byte(OrdPrefix)).
		AddOp(txscript.OP_DATA_1)

	if inscriptionRequest.InscriptionData.ContentType != "" {
		inscriptionBuilder.AddOp(indexer.FIELD_CONTENT_TYPE). // FIELD_CONTENT_TYPE
									AddData([]byte(inscriptionRequest.InscriptionData.ContentType))
	}

	if len(inscriptionRequest.InscriptionData.RuneName) > 0 {
		inscriptionBuilder.AddOp(indexer.FIELD_RUNE_NAME).
			AddData([]byte(inscriptionRequest.InscriptionData.RuneName))
	}

	// body
	inscriptionBuilder.AddOp(txscript.OP_0)
	maxChunkSize := 520
	// use taproot to skip txscript.MaxScriptSize 10000
	bodySize := len(inscriptionRequest.InscriptionData.Body)
	for i := 0; i < bodySize; i += maxChunkSize {
		end := i + maxChunkSize
		if end > bodySize {
			end = bodySize
		}

		inscriptionBuilder.AddFullData(inscriptionRequest.InscriptionData.Body[i:end])
	}
	inscriptionScript, err := inscriptionBuilder.Script()
	if err != nil {
		return nil, err
	}
	inscriptionScript = append(inscriptionScript, txscript.OP_ENDIF)

	proof := &txscript.TapscriptProof{
		TapLeaf:  txscript.NewBaseTapLeaf(schnorr.SerializePubKey(pubkey)),
		RootNode: txscript.NewBaseTapLeaf(inscriptionScript),
	}

	controlBlock := proof.ToControlBlock(pubkey)
	controlBlockWitness, err := controlBlock.ToBytes()
	if err != nil {
		return nil, err
	}

	tapHash := proof.RootNode.TapHash()
	tpOutputKey := txscript.ComputeTaprootOutputKey(pubkey, tapHash[:])
	commitTxAddress, err := btcutil.NewAddressTaproot(schnorr.SerializePubKey(tpOutputKey), network)
	if err != nil {
		return nil, err
	}
	commitTxAddressPkScript, err := txscript.PayToAddrScript(commitTxAddress)
	if err != nil {
		return nil, err
	}

	return &inscriptionTxCtxData{
		InscriptionScript:       inscriptionScript,
		CommitTxAddress:         commitTxAddress.EncodeAddress(),
		CommitTxAddressPkScript: commitTxAddressPkScript,
		ControlBlockWitness:     controlBlockWitness,
		RevealTxNullData:        inscriptionRequest.InscriptionData.RevealTxNullData,
	}, nil
}

func (builder *InscriptionBuilder) buildEmptyRevealTx(revealOutValue, revealFeeRate int64) (int64, error) {

	tx := wire.NewMsgTx(DefaultTxVersion)

	in := wire.NewTxIn(&wire.OutPoint{Index: 0}, nil, nil)
	in.Sequence = DefaultSequenceNum
	tx.AddTxIn(in)
	scriptPubKey, err := AddrToPkScript(builder.RevealAddr, builder.Network)
	if err != nil {
		return 0, err
	}
	out := wire.NewTxOut(revealOutValue, scriptPubKey)
	tx.AddTxOut(out)

	if builder.InscriptionTxCtxData.RevealTxNullData != nil {
		tx.AddTxOut(wire.NewTxOut(0, builder.InscriptionTxCtxData.RevealTxNullData))
	}

	prevOutputValue := revealOutValue + int64(tx.SerializeSize())*revealFeeRate
	emptySignature := make([]byte, 64)
	emptyControlBlockWitness := make([]byte, 33)
	fee := (int64(wire.TxWitness{emptySignature, builder.InscriptionTxCtxData.InscriptionScript,
		emptyControlBlockWitness}.SerializeSize()+2+3) / 4) * revealFeeRate
	prevOutputValue += fee
	builder.InscriptionTxCtxData.RevealTxPrevOutput = &wire.TxOut{
		PkScript: builder.InscriptionTxCtxData.CommitTxAddressPkScript,
		Value:    prevOutputValue,
	}

	builder.RevealTx = tx
	builder.MustRevealTxFee = int64(tx.SerializeSize())*revealFeeRate + fee
	builder.CommitAddr = builder.InscriptionTxCtxData.CommitTxAddress

	return prevOutputValue, nil
}

func (builder *InscriptionBuilder) buildCommitTx(commitTxPrevOutputList PrevOutputs,
	changeAddress string, totalRevealPrevOutputValue, commitFeeRate int64, minChangeValue int64) error {
	totalSenderAmount := btcutil.Amount(0)
	tx := wire.NewMsgTx(DefaultTxVersion)
	changePkScript, err := AddrToPkScript(changeAddress, builder.Network)
	if err != nil {
		return err
	}
	for _, prevOutput := range commitTxPrevOutputList {
		txHash, err := chainhash.NewHashFromStr(prevOutput.TxId)
		if err != nil {
			return err
		}
		outPoint := wire.NewOutPoint(txHash, prevOutput.VOut)
		txOut := wire.NewTxOut(prevOutput.Amount, prevOutput.PkScript)
		builder.CommitTxPrevOutputFetcher.AddPrevOut(*outPoint, txOut)

		in := wire.NewTxIn(outPoint, nil, nil)
		in.Sequence = DefaultSequenceNum
		tx.AddTxIn(in)

		totalSenderAmount += btcutil.Amount(prevOutput.Amount)
	}

	tx.AddTxOut(builder.InscriptionTxCtxData.RevealTxPrevOutput)
	tx.AddTxOut(wire.NewTxOut(0, changePkScript))

	txForEstimate := wire.NewMsgTx(DefaultTxVersion)
	txForEstimate.TxIn = tx.TxIn
	txForEstimate.TxOut = tx.TxOut
	if err = builder.Signer(txForEstimate, builder.CommitTxPrevOutputFetcher); err != nil {
		return err
	}

	view, _ := commitTxPrevOutputList.UtxoViewpoint(builder.Network)
	fee := btcutil.Amount(
		GetTxVirtualSizeByView(btcutil.NewTx(txForEstimate), view)) * btcutil.Amount(commitFeeRate)
	changeAmount := totalSenderAmount - btcutil.Amount(totalRevealPrevOutputValue) - fee
	if int64(changeAmount) >= minChangeValue {
		tx.TxOut[len(tx.TxOut)-1].Value = int64(changeAmount)
	} else {
		tx.TxOut = tx.TxOut[:len(tx.TxOut)-1]
		if changeAmount < 0 {
			txForEstimate.TxOut = txForEstimate.TxOut[:len(txForEstimate.TxOut)-1]
			feeWithoutChange := btcutil.Amount(GetTxVirtualSizeByView(
				btcutil.NewTx(txForEstimate), view)) * btcutil.Amount(commitFeeRate)
			if totalSenderAmount-btcutil.Amount(totalRevealPrevOutputValue)-feeWithoutChange < 0 {
				builder.MustCommitTxFee = int64(fee)
				return errors.New("insufficient balance")
			}
		}
	}
	builder.CommitTx = tx
	return nil
}

func (builder *InscriptionBuilder) completeRevealTx() error {

	builder.RevealTxPrevOutputFetcher.AddPrevOut(wire.OutPoint{
		Hash:  builder.CommitTx.TxHash(),
		Index: uint32(0),
	}, builder.InscriptionTxCtxData.RevealTxPrevOutput)
	builder.RevealTx.TxIn[0].PreviousOutPoint.Hash = builder.CommitTx.TxHash()

	revealTx := builder.RevealTx
	witnessArray, err := txscript.CalcTapscriptSignaturehash(
		txscript.NewTxSigHashes(revealTx, builder.RevealTxPrevOutputFetcher),
		txscript.SigHashDefault, revealTx, 0, builder.RevealTxPrevOutputFetcher,
		txscript.NewBaseTapLeaf(builder.InscriptionTxCtxData.InscriptionScript))
	if err != nil {
		return err
	}
	signature, err := schnorr.Sign(builder.RevealPrivateKey, witnessArray)
	if err != nil {
		return err
	}
	witness := wire.TxWitness{signature.Serialize(),
		builder.InscriptionTxCtxData.InscriptionScript, builder.InscriptionTxCtxData.ControlBlockWitness}
	builder.RevealTx.TxIn[0].Witness = witness

	// check tx max tx wight
	revealWeight := GetTransactionWeight(btcutil.NewTx(builder.RevealTx))
	if revealWeight > MaxStandardTxWeight {
		return fmt.Errorf("reveal(index %d) transaction weight greater than (MAX_STANDARD_TX_WEIGHT): %d", MaxStandardTxWeight, revealWeight)
	}

	return nil
}

func (builder *InscriptionBuilder) signCommitTx() error {
	return builder.Signer(builder.CommitTx, builder.CommitTxPrevOutputFetcher)
}

func Sign(tx *wire.MsgTx, privateKeys []*btcec.PrivateKey, prevOutFetcher *txscript.MultiPrevOutFetcher) error {
	for i, in := range tx.TxIn {
		prevOut := prevOutFetcher.FetchPrevOutput(in.PreviousOutPoint)
		txSigHashes := txscript.NewTxSigHashes(tx, prevOutFetcher)
		privKey := privateKeys[i]
		if txscript.IsPayToTaproot(prevOut.PkScript) {
			witness, err := txscript.TaprootWitnessSignature(tx, txSigHashes, i, prevOut.Value, prevOut.PkScript, txscript.SigHashDefault, privKey)
			if err != nil {
				return err
			}
			in.Witness = witness
		} else if txscript.IsPayToPubKeyHash(prevOut.PkScript) {
			sigScript, err := txscript.SignatureScript(tx, i, prevOut.PkScript, txscript.SigHashAll, privKey, true)
			if err != nil {
				return err
			}
			in.SignatureScript = sigScript
		} else {
			pubKeyBytes := privKey.PubKey().SerializeCompressed()
			script, err := PayToPubKeyHashScript(btcutil.Hash160(pubKeyBytes))
			if err != nil {
				return err
			}
			amount := prevOut.Value
			witness, err := txscript.WitnessSignature(tx, txSigHashes, i, amount, script, txscript.SigHashAll, privKey, true)
			if err != nil {
				return err
			}
			in.Witness = witness

			if txscript.IsPayToScriptHash(prevOut.PkScript) {
				redeemScript, err := PayToWitnessPubKeyHashScript(btcutil.Hash160(pubKeyBytes))
				if err != nil {
					return err
				}
				in.SignatureScript = append([]byte{byte(len(redeemScript))}, redeemScript...)
			}
		}
	}

	return nil
}

func GetTxHex(tx *wire.MsgTx) (string, error) {
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf.Bytes()), nil
}

func (builder *InscriptionBuilder) GetCommitTxHex() (string, error) {
	return GetTxHex(builder.CommitTx)
}

func (builder *InscriptionBuilder) GetRevealTxHex() (string, error) {
	txHex, err := GetTxHex(builder.RevealTx)
	if err != nil {
		return "", err
	}

	return txHex, nil
}

func (builder *InscriptionBuilder) CalculateFee() (int64, int64) {
	commitTxFee := int64(0)
	for _, in := range builder.CommitTx.TxIn {
		commitTxFee += builder.CommitTxPrevOutputFetcher.FetchPrevOutput(in.PreviousOutPoint).Value
	}
	for _, out := range builder.CommitTx.TxOut {
		commitTxFee -= out.Value
	}

	revealTxFee := int64(0)
	for i, in := range builder.RevealTx.TxIn {
		revealTxFee += builder.RevealTxPrevOutputFetcher.FetchPrevOutput(in.PreviousOutPoint).Value
		revealTxFee -= builder.RevealTx.TxOut[i].Value
	}

	return commitTxFee, revealTxFee
}

func Inscribe(network *chaincfg.Params, request *InscriptionRequest, resvId int64) (*InscribeResv, error) {
	tool, err := NewInscriptionTool(network, request)
	if err != nil && err.Error() == "insufficient balance" {
		return &InscribeResv{
			CommitTx:    nil,
			RevealTx:    nil,
			CommitTxFee: tool.MustCommitTxFee,
			RevealTxFee: tool.MustRevealTxFee,
			CommitAddr:  tool.CommitAddr,
		}, nil
	}

	err = VerifySignedTx(tool.CommitTx, tool.CommitTxPrevOutputFetcher)
	if err != nil {
		return nil, err
	}

	err = VerifySignedTx(tool.RevealTx, tool.RevealTxPrevOutputFetcher)
	if err != nil {
		return nil, err
	}

	commitTxFee, revealTxFees := tool.CalculateFee()
	return &InscribeResv{
		Id: resvId,
		Status: RS_INIT,

		CommitTx:         tool.CommitTx,
		RevealTx:         tool.RevealTx,
		CommitTxFee:      commitTxFee,
		RevealTxFee:      revealTxFees,
		CommitAddr:       tool.CommitAddr,
		RevealPrivateKey: tool.RevealPrivateKey.Serialize(),
	}, nil
}

// GetTransactionWeight computes the value of the weight metric for a given
// transaction. Currently the weight metric is simply the sum of the
// transactions's serialized size without any witness data scaled
// proportionally by the WitnessScaleFactor, and the transaction's serialized
// size including any witness data.
func GetTransactionWeight(tx *btcutil.Tx) int64 {
	msgTx := tx.MsgTx()
	return GetTransactionWeight2(msgTx)
}

func GetTransactionWeight2(tx *wire.MsgTx) int64 {
	baseSize := tx.SerializeSizeStripped()
	totalSize := tx.SerializeSize()

	// (baseSize * 3) + totalSize
	return int64((baseSize * (WitnessScaleFactor - 1)) + totalSize)
}

func GetTxVirtualSize2(tx *wire.MsgTx) int64 {
	// vSize := (weight(tx) + 3) / 4
	//       := (((baseSize * 3) + totalSize) + 3) / 4
	// We add 3 here as a way to compute the ceiling of the prior arithmetic
	// to 4. The division by 4 creates a discount for wit witness data.
	return (GetTransactionWeight2(tx) + (WitnessScaleFactor - 1)) / WitnessScaleFactor
}

// GetTxVirtualSize computes the virtual size of a given transaction. A
// transaction's virtual size is based off its weight, creating a discount for
// any witness data it contains, proportional to the current
// blockchain.WitnessScaleFactor value.
func GetTxVirtualSize(tx *btcutil.Tx) int64 {
	return GetTxVirtualSizeByView(tx, nil)
}

func GetTxVirtualSizeByView(tx *btcutil.Tx, view UtxoViewpoint) int64 {
	weight := getTxVirtualSize(tx)
	if len(view) == 0 {
		return weight
	}
	sigCost := GetSigOps(tx, view)
	if sigCost > weight {
		return sigCost
	}
	return weight
}

func GetSigOps(tx *btcutil.Tx, view UtxoViewpoint) (f int64) {
	defer func() {
		if r := recover(); r != nil {
			f = 0
		}
	}()
	sigops, err := GetSigOpCost(tx, false, view, true, true)
	if err != nil {
		return 0
	}
	return int64(sigops) * 5
}

func getTxVirtualSize(tx *btcutil.Tx) int64 {
	// vSize := (weight(tx) + 3) / 4
	//       := (((baseSize * 3) + totalSize) + 3) / 4
	// We add 3 here as a way to compute the ceiling of the prior arithmetic
	// to 4. The division by 4 creates a discount for wit witness data.
	return (GetTransactionWeight(tx) + (WitnessScaleFactor - 1)) / WitnessScaleFactor
}


// RuleError identifies a rule violation.  It is used to indicate that
// processing of a block or transaction failed due to one of the many validation
// rules.  The caller can use type assertions to determine if a failure was
// specifically due to a rule violation and access the ErrorCode field to
// ascertain the specific reason for the rule violation.
type RuleError struct {
	ErrorCode   utils.ErrorCode // Describes the kind of error
	Description string          // Human readable description of the issue
}

// Error satisfies the error interface and prints human-readable errors.
func (e RuleError) Error() string {
	return e.Description
}

// ruleError creates an RuleError given a set of arguments.
func ruleError(c utils.ErrorCode, desc string) RuleError {
	return RuleError{ErrorCode: c, Description: desc}
}

// CountP2SHSigOps returns the number of signature operations for all input
// transactions which are of the pay-to-script-hash type.  This uses the
// precise, signature operation counting mechanism from the script engine which
// requires access to the input transaction scripts.
func CountP2SHSigOps(tx *btcutil.Tx, isCoinBaseTx bool, utxoView map[wire.OutPoint][]byte) (int, error) {
	// Coinbase transactions have no interesting inputs.
	if isCoinBaseTx {
		return 0, nil
	}

	// Accumulate the number of signature operations in all transaction
	// inputs.
	msgTx := tx.MsgTx()
	totalSigOps := 0
	for txInIndex, txIn := range msgTx.TxIn {
		// Ensure the referenced input transaction is available.
		pkScript := utxoView[txIn.PreviousOutPoint]
		if pkScript == nil {
			str := fmt.Sprintf("output %v referenced from "+
				"transaction %s:%d either does not exist or "+
				"has already been spent", txIn.PreviousOutPoint,
				tx.Hash(), txInIndex)
			return 0, ruleError(utils.ErrMissingTxOut, str)
		}

		if !txscript.IsPayToScriptHash(pkScript) {
			continue
		}

		// Count the precise number of signature operations in the
		// referenced public key script.
		sigScript := txIn.SignatureScript
		numSigOps := txscript.GetPreciseSigOpCount(sigScript, pkScript,
			true)

		// We could potentially overflow the accumulator so check for
		// overflow.
		lastSigOps := totalSigOps
		totalSigOps += numSigOps
		if totalSigOps < lastSigOps {
			str := fmt.Sprintf("the public key script from output "+
				"%v contains too many signature operations - "+
				"overflow", txIn.PreviousOutPoint)
			return 0, ruleError(utils.ErrTooManySigOps, str)
		}
	}

	return totalSigOps, nil
}

// GetSigOpCost returns the unified sig op cost for the passed transaction
// respecting current active soft-forks which modified sig op cost counting.
// The unified sig op cost for a transaction is computed as the sum of: the
// legacy sig op count scaled according to the WitnessScaleFactor, the sig op
// count for all p2sh inputs scaled by the WitnessScaleFactor, and finally the
// unscaled sig op count for any inputs spending witness programs.
func GetSigOpCost(tx *btcutil.Tx, isCoinBaseTx bool, utxoView map[wire.OutPoint][]byte, bip16, segWit bool) (int, error) {
	numSigOps := CountSigOps(tx) * WitnessScaleFactor
	if bip16 {
		numP2SHSigOps, err := CountP2SHSigOps(tx, isCoinBaseTx, utxoView)
		if err != nil {
			return 0, nil
		}
		numSigOps += (numP2SHSigOps * WitnessScaleFactor)
	}

	if segWit && !isCoinBaseTx && utxoView != nil {
		msgTx := tx.MsgTx()
		for txInIndex, txIn := range msgTx.TxIn {
			// Ensure the referenced output is available and hasn't
			// already been spent.
			pkScript := utxoView[txIn.PreviousOutPoint]
			if pkScript == nil {
				str := fmt.Sprintf("output %v referenced from "+
					"transaction %s:%d either does not "+
					"exist or has already been spent",
					txIn.PreviousOutPoint, tx.Hash(),
					txInIndex)
				return 0, ruleError(utils.ErrMissingTxOut, str)
			}
			witness := txIn.Witness
			sigScript := txIn.SignatureScript
			numSigOps += txscript.GetWitnessSigOpCount(sigScript, pkScript, witness)
		}

	}

	return numSigOps, nil
}

// CountSigOps returns the number of signature operations for all transaction
// input and output scripts in the provided transaction.  This uses the
// quicker, but imprecise, signature operation counting mechanism from
// txscript.
func CountSigOps(tx *btcutil.Tx) int {
	msgTx := tx.MsgTx()

	// Accumulate the number of signature operations in all transaction
	// inputs.
	totalSigOps := 0
	for _, txIn := range msgTx.TxIn {
		numSigOps := txscript.GetSigOpCount(txIn.SignatureScript)
		totalSigOps += numSigOps
	}

	// Accumulate the number of signature operations in all transaction
	// outputs.
	for _, txOut := range msgTx.TxOut {
		numSigOps := txscript.GetSigOpCount(txOut.PkScript)
		totalSigOps += numSigOps
	}

	return totalSigOps
}

func CreateInscriptionScript(privateKey *btcec.PrivateKey, contentType string, body []byte) ([]byte, error) {
	inscriptionBuilder := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(privateKey.PubKey())).
		AddOp(txscript.OP_CHECKSIG).
		AddOp(txscript.OP_FALSE).
		AddOp(txscript.OP_IF).
		AddData([]byte("ord")).
		AddOp(txscript.OP_DATA_1).
		AddOp(txscript.OP_DATA_1).
		// text/plain;charset=utf-8
		AddData([]byte(contentType)).
		AddOp(txscript.OP_0)

	maxChunkSize := 520
	bodySize := len(body)
	for i := 0; i < bodySize; i += maxChunkSize {
		end := i + maxChunkSize
		if end > bodySize {
			end = bodySize
		}
		inscriptionBuilder.AddFullData(body[i:end])
	}
	inscriptionScript, err := inscriptionBuilder.Script()
	if err != nil {
		return nil, err
	}
	// to skip txscript.MaxScriptSize 10000
	inscriptionScript = append(inscriptionScript, txscript.OP_ENDIF)
	return inscriptionScript, nil
}

func CreateInscriptionScriptWithPubKey(publicKey []byte, contentType string, body []byte) ([]byte, error) {
	inscriptionBuilder := txscript.NewScriptBuilder().
		AddData(publicKey).
		AddOp(txscript.OP_CHECKSIG).
		AddOp(txscript.OP_FALSE).
		AddOp(txscript.OP_IF).
		AddData([]byte("ord")).
		AddOp(txscript.OP_DATA_1).
		AddOp(txscript.OP_DATA_1).
		// text/plain;charset=utf-8
		AddData([]byte(contentType)).
		AddOp(txscript.OP_0)

	maxChunkSize := 520
	bodySize := len(body)
	for i := 0; i < bodySize; i += maxChunkSize {
		end := i + maxChunkSize
		if end > bodySize {
			end = bodySize
		}
		inscriptionBuilder.AddFullData(body[i:end])
	}
	inscriptionScript, err := inscriptionBuilder.Script()
	if err != nil {
		return nil, err
	}
	// to skip txscript.MaxScriptSize 10000
	inscriptionScript = append(inscriptionScript, txscript.OP_ENDIF)
	return inscriptionScript, nil
}
