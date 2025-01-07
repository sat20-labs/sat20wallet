//go:build wasm

package lightnode

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/ecdsa"
	"github.com/sat20-labs/satsnet_btcd/btcec/schnorr"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
	schaincfg "github.com/sat20-labs/satsnet_btcd/chaincfg"
	"github.com/sat20-labs/sat20wallet/wallet/utils"
	"github.com/tyler-smith/go-bip39"
	// spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
	// swire "github.com/sat20-labs/satsnet_btcd/wire"
)

var (
	// PsbtKeyTypeInputSignatureTweakSingle is a custom/proprietary PSBT key
	// for an input that specifies what single tweak should be applied to
	// the key before signing the utils. The value 51 is leet speak for
	// "si", short for "single".
	PsbtKeyTypeInputSignatureTweakSingle = []byte{0x51}

	// PsbtKeyTypeInputSignatureTweakDouble is a custom/proprietary PSBT key
	// for an input that specifies what double tweak should be applied to
	// the key before signing the utils. The value d0 is leet speak for
	// "do", short for "double".
	PsbtKeyTypeInputSignatureTweakDouble = []byte{0xd0}

	// ErrInputMissingUTXOInfo is returned if a PSBT input is supplied that
	// does not specify the witness UTXO info.
	ErrInputMissingUTXOInfo = errors.New(
		"input doesn't specify any UTXO info",
	)

	// ErrScriptSpendFeeEstimationUnsupported is returned if a PSBT input is
	// of a script spend type.
	ErrScriptSpendFeeEstimationUnsupported = errors.New(
		"cannot estimate fee for script spend inputs",
	)

	// ErrUnsupportedScript is returned if a supplied pk script is not
	// known or supported.
	ErrUnsupportedScript = errors.New("unsupported or unknown pk script")
)

const (
	// KeyFamilyMultiSig are keys to be used within multi-sig scripts.
	KeyFamilyMultiSig uint32 = 0

	// KeyFamilyRevocationBase are keys that are used within channels to
	// create revocation basepoints that the remote party will use to
	// create revocation keys for us.
	KeyFamilyRevocationBase uint32 = 1

	// KeyFamilyRevocationRoot is a family of keys which will be used to
	// derive the root of a revocation tree for a particular channel.
	KeyFamilyRevocationRoot uint32 = 2

	// KeyFamilyHtlcBase are keys used within channels that will be
	// combined with per-state randomness to produce public keys that will
	// be used in HTLC scripts.
	//KeyFamilyHtlcBase uint32 = 3

	// KeyFamilyPaymentBase are keys used within channels that will be
	// combined with per-state randomness to produce public keys that will
	// be used in scripts that pay directly to us without any delay.
	//KeyFamilyPaymentBase uint32 = 4

	// KeyFamilyDelayBase are keys used within channels that will be
	// combined with per-state randomness to produce public keys that will
	// be used in scripts that pay to us, but require a CSV delay before we
	// can sweep the funds.
	//KeyFamilyDelayBase uint32 = KeyFamilyPaymentBase

	// KeyFamilyNodeKey is a family of keys that will be used to derive
	// keys that will be advertised on the network to represent our current
	// "identity" within the network. Peers will need our latest node key
	// in order to establish a transport session with us on the Lightning
	// p2p level (BOLT-0008).
	KeyFamilyNodeKey uint32 = 6

	// KeyFamilyBaseEncryption is the family of keys that will be used to
	// derive keys that we use to encrypt and decrypt any general blob data
	// like static channel backups and the TLS private key. Often used when
	// encrypting files on disk.
	KeyFamilyBaseEncryption uint32 = 7

	// KeyFamilyTowerSession is the family of keys that will be used to
	// derive session keys when negotiating sessions with watchtowers. The
	// session keys are limited to the lifetime of the session and are used
	// to increase privacy in the watchtower protocol.
	// KeyFamilyTowerSession uint32 = 8

	// KeyFamilyTowerID is the family of keys used to derive the public key
	// of a watchtower. This made distinct from the node key to offer a form
	// of rudimentary whitelisting, i.e. via knowledge of the pubkey,
	// preventing others from having full access to the tower just as a
	// result of knowing the node key.
	// KeyFamilyTowerID uint32 = 9
)

const DEFAULT_PURPOSE = 86

var Log = utils.Log

type ExternalWallet struct {
	masterkey             *hdkeychain.ExtendedKey
	netParamsL1           *chaincfg.Params // L1
	netParamsL2           *schaincfg.Params // L2
	paymentPrivKey        *secp256k1.PrivateKey
	revocationBasePrivKey *secp256k1.PrivateKey
	purposes              map[uint32]*hdkeychain.ExtendedKey // key: purpose
	accounts              map[uint64]*hdkeychain.ExtendedKey // key: purpose<<32+account
	addresses             map[string]btcutil.Address         // key: address type
}



func ConnectExternalWallet() (*ExternalWallet, error) {
	if wallet != nil {
		return wallet, nil
	}

	// 连接外部钱包，这里模拟外部钱包的实现
	var param1 *chaincfg.Params
	var param2 *schaincfg.Params
	switch chain {
	case "mainnet":
		param1 = &chaincfg.MainNetParams
		param2 = &schaincfg.SatsMainNetParams

	case "testnet", "testnet4":
		param1 = &chaincfg.TestNet4Params
		param2 = &schaincfg.SatsTestNetParams
	}

	var err error
	wallet, err = NewExternalWallet(param1, param2)
	if err != nil {
		return nil, err
	}
	return wallet, nil
}

// 以下代码，需要由插件钱包实现，并且提供接口给stp调用。接口定义参考 lncore.Wallet , 具体实现参考 InternalWallet
var wallet *ExternalWallet
var chain = "testnet"

func NewExternalWallet(param *chaincfg.Params, param2 *schaincfg.Params) (*ExternalWallet, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return nil, err
	}

	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, err
	}

	return NewExternalWalletWithMnemonic(mnemonic, "", param), nil
}

func NewExternalWalletWithMnemonic(mnemonic string, password string, param *chaincfg.Params) *ExternalWallet {

	if !bip39.IsMnemonicValid(mnemonic) {
		Log.Errorf("Mnomonic is invalid")
		return nil
	}

	seed := bip39.NewSeed(mnemonic, password)
	masterkey := SeedToMasterKey(seed, param)
	if masterkey == nil {
		return nil
	}
	return &ExternalWallet{
		masterkey:   masterkey,
		netParamsL1: param,
		purposes:    make(map[uint32]*hdkeychain.ExtendedKey),
		accounts:    make(map[uint64]*hdkeychain.ExtendedKey),
		addresses:   make(map[string]btcutil.Address),
	}
}

func (p *ExternalWallet) getPurposeKey(purpose uint32) (*hdkeychain.ExtendedKey, error) {
	acckey, ok := p.purposes[purpose]
	if !ok {
		var err error
		acckey, err = GeneratePurposeKey(p.masterkey, purpose)
		if err != nil {
			return nil, err
		}
		p.purposes[purpose] = acckey
	}
	return acckey, nil
}

func (p *ExternalWallet) getAddress(addressType string) (btcutil.Address, error) {
	addressType = strings.ToUpper(addressType)
	address, ok := p.addresses[addressType]
	if ok {
		return address, nil
	}

	purpose := getPurposeFromAddrType(addressType)
	purposeKey, err := p.getPurposeKey(purpose)
	if err != nil {
		return nil, err
	}

	_, pubkey, err := generateKeyFromPurposeKey(purposeKey, 0, 0)
	if err != nil {
		return nil, err
	}

	address, err = getAddressFromPubKey(pubkey, addressType, p.netParamsL1)
	if err != nil {
		return nil, err
	}
	p.addresses[addressType] = address
	return address, nil
}

func (p *ExternalWallet) GetP2TRAddress() string {
	addr, err := p.getAddress("P2TR")
	if err != nil {
		return ""
	}
	return addr.EncodeAddress()
}

// 可以直接暴露这个PrivateKey，不影响钱包私钥的安全
func (p *ExternalWallet) GetCommitRootKey(peer []byte) (*secp256k1.PrivateKey, *secp256k1.PublicKey) {
	privkey := p.GetCommitSecret(peer, 0)
	if privkey == nil {
		return nil, nil
	}
	return privkey, privkey.PubKey()
}

// 返回secret，用于生成commitsecrect和commitpoint
// 可以直接暴露这个PrivateKey，不影响钱包私钥的安全
func (p *ExternalWallet) GetCommitSecret(peer []byte, index int) *secp256k1.PrivateKey {
	purpose := getPurposeFromAddrType("LND")
	account := getAccountFromFamilyKey(KeyFamilyRevocationRoot)
	key := uint64(purpose)<<32 + uint64(account)
	acckey, ok := p.accounts[key]
	if !ok {
		var err error
		acckey, err = GenerateAccountKey(p.masterkey, purpose, account)
		if err != nil {
			return nil
		}
		p.accounts[key] = acckey
	}

	privkey, _, err := generateKeyFromAccountKey(acckey, uint32(index))
	if err != nil {
		Log.Errorf("generateKeyFromAccountKey failed. %v", err)
		return nil
	}

	h := sha256.New()
	h.Write(privkey.Serialize())
	h.Write(peer)
	revRootPrivBytes := h.Sum(nil)

	privk, _ := btcec.PrivKeyFromBytes(revRootPrivBytes)
	return privk
}

// 可以直接暴露这个PrivateKey，不影响钱包私钥的安全
func (p *ExternalWallet) DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey {
	revBasePrivKey, _ := p.getRevocationBaseKey()
	return utils.DeriveRevocationPrivKey(revBasePrivKey, commitsecret)
}

func (p *ExternalWallet) GetRevocationBaseKey() *secp256k1.PublicKey {
	_, pubk := p.getRevocationBaseKey()
	return pubk
}

func (p *ExternalWallet) getRevocationBaseKey() (*secp256k1.PrivateKey, *secp256k1.PublicKey) {

	if p.revocationBasePrivKey != nil {
		return p.revocationBasePrivKey, p.revocationBasePrivKey.PubKey()
	}

	purpose := getPurposeFromAddrType("LND")
	purposekey, err := p.getPurposeKey(purpose)
	if err != nil {
		Log.Errorf("getPurposeKey failed. %v", err)
		return nil, nil
	}
	privk, pubk, err := generateKeyFromPurposeKey(purposekey,
		getAccountFromFamilyKey(KeyFamilyRevocationBase), 0,
	)

	if err != nil {
		Log.Errorf("generateKeyFromPurposeKey failed. %v", err)
		return nil, nil
	}
	p.revocationBasePrivKey = privk

	return privk, pubk
}

func (p *ExternalWallet) GetNodePubKey() *secp256k1.PublicKey {
	purpose := getPurposeFromAddrType("LND")
	account := getAccountFromFamilyKey(KeyFamilyNodeKey)
	key := uint64(purpose)<<32 + uint64(account)
	acckey, ok := p.accounts[key]
	if !ok {
		var err error
		acckey, err = GenerateAccountKey(p.masterkey, purpose, account)
		if err != nil {
			return nil
		}
		p.accounts[key] = acckey
	}

	_, pubkey, err := generateKeyFromAccountKey(acckey, 0)
	if err != nil {
		Log.Errorf("generateKeyFromAccountKey failed. %v", err)
		return nil
	}

	return pubkey
}

func (p *ExternalWallet) GetPaymentPubKey() *secp256k1.PublicKey {
	key := p.getPaymentPrivKey()
	if key == nil {
		Log.Errorf("GetPaymentPrivKey failed.")
		return nil
	}
	return key.PubKey()
}

func (p *ExternalWallet) getPaymentPrivKey() *secp256k1.PrivateKey {
	if p.paymentPrivKey != nil {
		return p.paymentPrivKey
	}
	key, _, err := p.getKey("P2TR")
	if err != nil {
		Log.Errorf("GetPubKey P2TR failed. %v", err)
		return nil
	}
	p.paymentPrivKey = key
	return key
}

func (p *ExternalWallet) getKey(addressType string) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	purpose := getPurposeFromAddrType(addressType)
	purposekey, err := p.getPurposeKey(purpose)
	if err != nil {
		return nil, nil, err
	}
	return generateKeyFromPurposeKey(purposekey, 0, 0)
}

func (p *ExternalWallet) SignTxInput(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	sigHashes *txscript.TxSigHashes,
	index int, witnessScript []byte) ([]byte, error) {
	privKey := p.getPaymentPrivKey()

	var result []byte
	preOut := prevFetcher.FetchPrevOutput(tx.TxIn[index].PreviousOutPoint)
	scriptType := GetPkScriptType(preOut.PkScript, p.netParamsL1)
	switch scriptType {
	case txscript.WitnessV1TaprootTy: // p2tr
		witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, index,
			preOut.Value, preOut.PkScript,
			txscript.SigHashDefault, privKey)
		if err != nil {
			Log.Errorf("TaprootWitnessSignature failed. %v", err)
			return nil, err
		}
		result = witness[0]

	case txscript.WitnessV0ScriptHashTy: //"P2WSH":
		sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, index,
			preOut.Value,
			witnessScript, txscript.SigHashAll, privKey)
		if err != nil {
			Log.Errorf("RawTxInWitnessSignature failed, %v", err)
			return nil, err
		}
		result = sig
	}
	return result, nil
}

func (p *ExternalWallet) SignTxInput_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	sigHashes *stxscript.TxSigHashes,
	index int, witnessScript []byte) ([]byte, error) {
	privKey := p.getPaymentPrivKey()

	var result []byte
	preOut := prevFetcher.FetchPrevOutput(tx.TxIn[index].PreviousOutPoint)
	scriptType := GetPkScriptType(preOut.PkScript, p.netParamsL1)
	switch scriptType {
	case txscript.WitnessV1TaprootTy: // p2tr
		witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, index,
			preOut.Value, preOut.Assets, preOut.PkScript,
			stxscript.SigHashDefault, privKey)
		if err != nil {
			Log.Errorf("TaprootWitnessSignature failed. %v", err)
			return nil, err
		}
		result = witness[0]

	case txscript.WitnessV0ScriptHashTy: //"P2WSH":
		sig, err := stxscript.RawTxInWitnessSignature(tx, sigHashes, index,
			preOut.Value, preOut.Assets,
			witnessScript, stxscript.SigHashAll, privKey)
		if err != nil {
			Log.Errorf("RawTxInWitnessSignature failed, %v", err)
			return nil, err
		}
		result = sig
	}
	return result, nil
}

func (p *ExternalWallet) SignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) error {

	privKey := p.getPaymentPrivKey()
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		scriptType := GetPkScriptType(preOut.PkScript, p.netParamsL1)
		switch scriptType {
		case txscript.WitnessV1TaprootTy: // p2tr
			witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.PkScript,
				txscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return err
			}
			tx.TxIn[i].Witness = witness

		default:
			Log.Errorf("not support type %d", scriptType)
			return fmt.Errorf("not support type %d", scriptType)
		}
	}

	return nil
}

func (p *ExternalWallet) SignTx_SatsNet(tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) error {

	privKey := p.getPaymentPrivKey()
	sigHashes := stxscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript, p.netParamsL2)
		switch scriptType {
		case stxscript.WitnessV1TaprootTy: // p2tr
			witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.Assets, preOut.PkScript,
				stxscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return err
			}
			tx.TxIn[i].Witness = witness

		default:
			Log.Errorf("not support type %d", scriptType)
			return fmt.Errorf("not support type %d", scriptType)
		}
	}

	return nil
}

func (p *ExternalWallet) SignTxWithPeer(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error) {

	myPubKey := p.GetPaymentPubKey().SerializeCompressed()
	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}
	pos := GetCurrSignPosition2(myPubKey, peerPubKey)

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey, p.netParamsL1)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return nil, err
	}

	result := make([][]byte, 0)
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			if peerSig != nil {
				tx.TxIn[i].Witness = wire.TxWitness{peerSig[i]}
			}
			continue
		}

		scriptType := GetPkScriptType(preOut.PkScript, p.netParamsL1)
		switch scriptType {
		case txscript.WitnessV1TaprootTy: // p2tr
			witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.PkScript,
				txscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return nil, err
			}
			tx.TxIn[i].Witness = witness
			result = append(result, witness[0])

		case txscript.WitnessV0ScriptHashTy: //"P2WSH":
			sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, i,
				preOut.Value,
				witnessScript, txscript.SigHashAll, privKey)
			if err != nil {
				Log.Errorf("failed to create signature for input %s: %v", in.PreviousOutPoint.String(), err)
				return nil, err
			}

			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = wire.TxWitness{nil, nil, nil, witnessScript}
			}
			tx.TxIn[i].Witness[pos+1] = sig
			if peerSig != nil {
				if pos == 0 {
					tx.TxIn[i].Witness[pos+2] = peerSig[i]
				} else {
					tx.TxIn[i].Witness[pos] = peerSig[i]
				}
			}
			result = append(result, sig)
		}
	}

	return result, nil
}

func (p *ExternalWallet) SignTxWithPeer_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error) {

	myPubKey := p.GetPaymentPubKey().SerializeCompressed()
	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}
	pos := GetCurrSignPosition2(myPubKey, peerPubKey)

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey, p.netParamsL1)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return nil, err
	}

	result := make([][]byte, 0)
	sigHashes := stxscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			if peerSig != nil {
				tx.TxIn[i].Witness = swire.TxWitness{peerSig[i]}
			}
			continue
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript, p.netParamsL2)
		switch scriptType {
		case stxscript.WitnessV1TaprootTy: // p2tr
			witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.Assets, preOut.PkScript,
				stxscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return nil, err
			}
			tx.TxIn[i].Witness = witness
			result = append(result, witness[0])

		case stxscript.WitnessV0ScriptHashTy: //"P2WSH":
			sig, err := stxscript.RawTxInWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.Assets, witnessScript,
				stxscript.SigHashAll, privKey)
			if err != nil {
				Log.Errorf("failed to create signature for input %s: %v", in.PreviousOutPoint.String(), err)
				return nil, err
			}

			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = swire.TxWitness{nil, nil, nil, witnessScript}
			}
			tx.TxIn[i].Witness[pos+1] = sig
			if peerSig != nil {
				if pos == 0 {
					tx.TxIn[i].Witness[pos+2] = peerSig[i]
				} else {
					tx.TxIn[i].Witness[pos] = peerSig[i]
				}
			}
			result = append(result, sig)
		}
	}

	return result, nil
}

func (p *ExternalWallet) PartialSignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, pos int) ([][]byte, error) {

	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey, p.netParamsL1)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return nil, err
	}

	result := make([][]byte, 0)
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			result = append(result, []byte{})
			continue
		}

		scriptType := GetPkScriptType(preOut.PkScript, p.netParamsL1)
		switch scriptType {
		case txscript.WitnessV1TaprootTy: // p2tr
			witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.PkScript,
				txscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return nil, err
			}
			tx.TxIn[i].Witness = witness
			result = append(result, witness[0])

		case txscript.WitnessV0ScriptHashTy: //"P2WSH":
			sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, i,
				preOut.Value,
				witnessScript, txscript.SigHashAll, privKey)
			if err != nil {
				Log.Errorf("failed to create signature for input %s: %v", in.PreviousOutPoint.String(), err)
				return nil, err
			}
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = wire.TxWitness{nil, nil, nil, witnessScript}
			}
			tx.TxIn[i].Witness[pos+1] = sig

			result = append(result, sig)
		}
	}

	return result, nil
}

func (p *ExternalWallet) PartialSignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, pos int) ([][]byte, error) {

	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey, p.netParamsL1)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return nil, err
	}

	result := make([][]byte, 0)
	sigHashes := stxscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		if !bytes.Equal(preOut.PkScript, p2trPkScript) &&
			!bytes.Equal(preOut.PkScript, mulpkScript) {
			result = append(result, []byte{})
			continue
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript, p.netParamsL2)
		switch scriptType {
		case stxscript.WitnessV1TaprootTy: // p2tr
			witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.Assets, preOut.PkScript,
				stxscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return nil, err
			}
			tx.TxIn[i].Witness = witness
			result = append(result, witness[0])

		case stxscript.WitnessV0ScriptHashTy: //"P2WSH":
			sig, err := stxscript.RawTxInWitnessSignature(tx, sigHashes, i,
				preOut.Value, preOut.Assets, witnessScript,
				stxscript.SigHashAll, privKey)
			if err != nil {
				Log.Errorf("failed to create signature for input %s: %v", in.PreviousOutPoint.String(), err)
				return nil, err
			}
			if tx.TxIn[i].Witness == nil {
				tx.TxIn[i].Witness = swire.TxWitness{nil, nil, nil, witnessScript}
			}
			tx.TxIn[i].Witness[pos+1] = sig
			result = append(result, sig)
		}
	}

	return result, nil
}

func (p *ExternalWallet) SignMessage(msg []byte) (*ecdsa.Signature, error) {
	privKey, err := p.deriveKeyByLocator(KeyFamilyBaseEncryption, 0)
	if err != nil {
		return nil, err
	}

	// Double hash and sign the data.
	var msgDigest []byte
	doubleHash := false
	if doubleHash {
		msgDigest = chainhash.DoubleHashB(msg)
	} else {
		msgDigest = chainhash.HashB(msg)
	}
	return ecdsa.Sign(privKey, msgDigest), nil
}

func VerifyMessage(pubKey *secp256k1.PublicKey, msg []byte, signature *ecdsa.Signature) bool {
	// Compute the hash of the message.
	var msgDigest []byte
	doubleHash := false
	if doubleHash {
		msgDigest = chainhash.DoubleHashB(msg)
	} else {
		msgDigest = chainhash.HashB(msg)
	}

	// Verify the signature using the public key.
	return signature.Verify(msgDigest, pubKey)
}

func (p *ExternalWallet) deriveKeyByLocator(family, index uint32) (*secp256k1.PrivateKey, error) {
	account := getAccountFromFamilyKey(family)

	acckey, err := p.getPurposeKey(account)
	if err != nil {
		return nil, err
	}
	privkey, _, err := generateKeyFromAccountKey(acckey, index)
	if err != nil {
		return nil, err
	}
	return privkey, nil
}

/* - m/purpose'/coinType'/account'/change/index
purpose
BIP44: 传统多币种HD钱包
BIP49: 隔离见证嵌套在P2SH中
BIP84: 原生隔离见证
BIP86: Taproot

以上purpose由BIP43定义。
BIP39: 助记词和种子的转换，可选密码短语
BIP32: 定义分层确定性钱包，主密钥和子密钥的派生
*/

// 根据种子生成根密钥
func SeedToMasterKey(seed []byte, params *chaincfg.Params) *hdkeychain.ExtendedKey {
	masterKey, err := hdkeychain.NewMaster(seed, params)
	if err != nil {
		Log.Errorf("Failed to generate master key: %v", err)
		return nil
	}
	return masterKey
}

// 生成purpose key
func GeneratePurposeKey(masterKey *hdkeychain.ExtendedKey, purpose uint32) (*hdkeychain.ExtendedKey, error) {
	// 生成目的链 hdkeychain.HardenedKeyStart+
	purposeKey, err := masterKey.Derive(hdkeychain.HardenedKeyStart + purpose)
	if err != nil {
		Log.Errorf("Failed to generate purpose chain: %v", err)
		return nil, err
	}
	return purposeKey, err
}

// 生成account key
func generateAccountKey2(purposeKey *hdkeychain.ExtendedKey, account uint32) (*hdkeychain.ExtendedKey, error) {
	// 生成目的链 hdkeychain.HardenedKeyStart+
	accountKey, err := purposeKey.Derive(hdkeychain.HardenedKeyStart)
	if err != nil {
		Log.Errorf("Failed to generate coin chain: %v", err)
		return nil, err
	}
	accountKey, err = accountKey.Derive(hdkeychain.HardenedKeyStart + account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, err
	}
	return accountKey, err
}

func GenerateAccountKey(masterKey *hdkeychain.ExtendedKey, purpose, account uint32) (*hdkeychain.ExtendedKey, error) {
	// 生成目的链 hdkeychain.HardenedKeyStart+
	accountKey, err := masterKey.Derive(hdkeychain.HardenedKeyStart + purpose)
	if err != nil {
		Log.Errorf("Failed to generate purpose chain: %v", err)
		return nil, err
	}
	accountKey, err = accountKey.Derive(hdkeychain.HardenedKeyStart)
	if err != nil {
		Log.Errorf("Failed to generate coin chain: %v", err)
		return nil, err
	}
	accountKey, err = accountKey.Derive(hdkeychain.HardenedKeyStart + account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, err
	}
	return accountKey, err
}

func generateKeyFromPurposeKey(purposeKey *hdkeychain.ExtendedKey,
	account, index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	// 生成外部链或内部链
	accountKey, err := generateAccountKey2(purposeKey, account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, nil, err
	}
	changeKey, err := accountKey.Derive(0)
	if err != nil {
		Log.Errorf("Failed to generate change chain: %v", err)
		return nil, nil, err
	}
	// 生成具体地址
	key, err := changeKey.Derive(index)
	if err != nil {
		Log.Errorf("Failed to generate index chain: %v", err)
		return nil, nil, err
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		Log.Errorf("ECPrivKey failed: %v", err)
		return nil, nil, err
	}
	publicKey, err := key.ECPubKey()
	if err != nil {
		Log.Errorf("ECPubKey failed: %v", err)
		return nil, nil, err
	}

	return privateKey, publicKey, nil
}

func generateKeyFromAccountKey(accountKey *hdkeychain.ExtendedKey,
	index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	// 生成外部链或内部链
	changeKey, err := accountKey.Derive(0)
	if err != nil {
		Log.Errorf("Failed to generate change chain: %v", err)
		return nil, nil, err
	}
	// 生成具体地址
	key, err := changeKey.Derive(index)
	if err != nil {
		Log.Errorf("Failed to generate index chain: %v", err)
		return nil, nil, err
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		Log.Errorf("ECPrivKey failed: %v", err)
		return nil, nil, err
	}
	publicKey, err := key.ECPubKey()
	if err != nil {
		Log.Errorf("ECPubKey failed: %v", err)
		return nil, nil, err
	}

	return privateKey, publicKey, nil
}

// 生成密钥
func GenerateKey(masterKey *hdkeychain.ExtendedKey, purpose, account,
	index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	accountKey, err := GenerateAccountKey(masterKey, purpose, account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, nil, err
	}
	// 生成外部链或内部链
	changeKey, err := accountKey.Derive(0)
	if err != nil {
		Log.Errorf("Failed to generate change chain: %v", err)
		return nil, nil, err
	}
	// 生成具体地址
	key, err := changeKey.Derive(index)
	if err != nil {
		Log.Errorf("Failed to generate change chain: %v", err)
		return nil, nil, err
	}

	privateKey, err := key.ECPrivKey()
	if err != nil {
		Log.Errorf("ECPrivKey failed: %v", err)
		return nil, nil, err
	}
	publicKey, err := key.ECPubKey()
	if err != nil {
		Log.Errorf("ECPubKey failed: %v", err)
		return nil, nil, err
	}

	return privateKey, publicKey, nil
}

func GenerateExtendedKey(masterKey *hdkeychain.ExtendedKey, purpose, account, index uint32) (*hdkeychain.ExtendedKey, error) {
	accountKey, err := GenerateAccountKey(masterKey, purpose, account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, err
	}
	// 生成外部链或内部链
	changeKey, err := accountKey.Derive(0)
	if err != nil {
		Log.Errorf("Failed to generate change chain: %v", err)
		return nil, err
	}
	// 生成具体地址
	return changeKey.Derive(index)
}

func getPurposeFromAddrType(addrType string) uint32 {
	purpose := uint32(86)
	switch addrType {
	case "P2PKH":
		purpose = 44
	case "P2SH-P2WPKH":
		purpose = 49
	case "P2SH":
		purpose = 49
	case "P2WPKH":
		purpose = 84
	case "P2TR":
		purpose = 86
	case "LND":
		purpose = 1017
	}
	return purpose
}

func getAccountFromFamilyKey(family uint32) uint32 {
	account := uint32(0)
	switch family {
	case KeyFamilyMultiSig:
		account = 0
	case KeyFamilyRevocationBase:
		account = 1
	case KeyFamilyRevocationRoot:
		account = 2
	case KeyFamilyNodeKey:
		account = 3
	}
	return account
}

func getAddressFromPubKey(pubKey *secp256k1.PublicKey, addrType string,
	params *chaincfg.Params) (btcutil.Address, error) {
	pubKeyHash := btcutil.Hash160(pubKey.SerializeCompressed())
	var address btcutil.Address
	var err error
	switch addrType {
	case "P2PKH":
		address, err = btcutil.NewAddressPubKeyHash(pubKeyHash, params)
	case "P2SH":
		pkScript, err2 := txscript.NewScriptBuilder().AddOp(txscript.OP_HASH160).
			AddData(pubKeyHash).AddOp(txscript.OP_EQUAL).Script()
		if err2 != nil {
			Log.Errorf("Failed to create pay to address script: %v", err2)
			return nil, err2
		}
		address, err = btcutil.NewAddressScriptHash(pkScript, params)
	case "P2SH-P2WPKH": // nested segwit
		witnessProg, err2 := btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, params)
		if err2 != nil {
			Log.Errorf("NewAddressWitnessPubKeyHash failed: %v", err2)
			return nil, err2
		}
		// 将P2WPKH地址嵌套在P2SH地址中，生成嵌套隔离见证地址
		witnessScript, err2 := txscript.NewScriptBuilder().AddOp(txscript.OP_0).
			AddData(witnessProg.WitnessProgram()).Script()
		if err2 != nil {
			Log.Errorf("NewAddressWitnessPubKeyHash failed: %v", err2)
			return nil, err2
		}
		address, err = btcutil.NewAddressScriptHash(witnessScript, params)
	case "P2WPKH":
		address, err = btcutil.NewAddressWitnessPubKeyHash(pubKeyHash, params)
	case "P2TR":
		tapKey := txscript.ComputeTaprootKeyNoScript(pubKey)
		address, err = btcutil.NewAddressTaproot(schnorr.SerializePubKey(tapKey), params)
	default:
		Log.Errorf("Unsupported address type: %v", addrType)
		return nil, err
	}

	if err != nil {
		Log.Errorf("Failed to generate address: %v", err)
		return nil, err
	}

	return address, nil
}


// PsbtPrevOutputFetcher returns a txscript.PrevOutFetcher built from the UTXO
// information in a PSBT packet.
func PsbtPrevOutputFetcher(packet *psbt.Packet) *txscript.MultiPrevOutFetcher {
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	for idx, txIn := range packet.UnsignedTx.TxIn {
		in := packet.Inputs[idx]

		// Skip any input that has no UTXO.
		if in.WitnessUtxo == nil && in.NonWitnessUtxo == nil {
			continue
		}

		if in.NonWitnessUtxo != nil {
			prevIndex := txIn.PreviousOutPoint.Index
			fetcher.AddPrevOut(
				txIn.PreviousOutPoint,
				in.NonWitnessUtxo.TxOut[prevIndex],
			)

			continue
		}

		// Fall back to witness UTXO only for older wallets.
		if in.WitnessUtxo != nil {
			fetcher.AddPrevOut(
				txIn.PreviousOutPoint, in.WitnessUtxo,
			)
		}
	}

	return fetcher
}

func GetPkScriptType(prevOutScript []byte, param *chaincfg.Params) txscript.ScriptClass {
	ty, _, _, err := txscript.ExtractPkScriptAddrs(prevOutScript, param)
	if err != nil {
		return txscript.WitnessUnknownTy
	}
	return ty
}

func GetPkScriptType_SatsNet(prevOutScript []byte, param *schaincfg.Params) stxscript.ScriptClass {
	ty, _, _, err := stxscript.ExtractPkScriptAddrs(prevOutScript, param)
	if err != nil {
		return stxscript.WitnessUnknownTy
	}
	return ty
}

func publicKeyToTaprootAddress(pubKey *btcec.PublicKey, param *chaincfg.Params) (*btcutil.AddressTaproot, error) {
	taprootPubKey := txscript.ComputeTaprootKeyNoScript(pubKey)
	return btcutil.NewAddressTaproot(schnorr.SerializePubKey(taprootPubKey), param)
}

func GetP2TRpkScript(pubKey *btcec.PublicKey, param *chaincfg.Params) ([]byte, error) {
	taprootAddr, err := publicKeyToTaprootAddress(pubKey, param)
	if err != nil {
		return nil, err
	}
	return txscript.PayToAddrScript(taprootAddr)
}

func GetCurrSignPosition2(aPub, bPub []byte) int {
	if bytes.Compare(aPub, bPub) == 1 {
		return 1
	}
	return 0
}
