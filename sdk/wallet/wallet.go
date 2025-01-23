package wallet

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/ecdsa"
	"github.com/sat20-labs/satsnet_btcd/btcec/schnorr"
	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
	"github.com/tyler-smith/go-bip39"
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

// m/purpose'/coinType'/account'/change/index
// 钱包的p2tr地址:  	m/86'   /0'/0'/0     /index1 
// 对应的通道钱包的地址:  m/1017' /0'/0'/index1/commitHeight
// 这样一个钱包下的每一个子账户都可以跟节点建立通道

// 可以支持其他类型，但为了方便，默认只支持p2tr地址类型。
type InternalWallet struct {
	masterkey             *hdkeychain.ExtendedKey
	netParamsL1           *chaincfg.Params // L1
	paymentPrivKeys        map[uint32]*secp256k1.PrivateKey
	revocationBasePrivKeys map[uint32]*secp256k1.PrivateKey
	purposes              map[uint32]*hdkeychain.ExtendedKey // key: purpose
	accounts              map[uint64]*hdkeychain.ExtendedKey // key: purpose<<32+account
	addresses             map[uint32]btcutil.Address         // key: index

	subWallets            map[uint32]*channelWallet
}

func NewInteralWallet(param *chaincfg.Params) (*InternalWallet, string, error) {
	entropy, err := bip39.NewEntropy(128)
	if err != nil {
		return nil, "", err
	}

	mnemonic, err := bip39.NewMnemonic(entropy)
	if err != nil {
		return nil, "", err
	}

	return NewInternalWalletWithMnemonic(mnemonic, "", param), mnemonic, nil
}

func NewInternalWalletWithMnemonic(mnemonic string, password string, param *chaincfg.Params) *InternalWallet {

	if !bip39.IsMnemonicValid(mnemonic) {
		Log.Errorf("Mnomonic is invalid")
		return nil
	}

	seed := bip39.NewSeed(mnemonic, password)
	masterkey := SeedToMasterKey(seed, param)
	if masterkey == nil {
		return nil
	}
	return &InternalWallet{
		masterkey:   masterkey,
		netParamsL1: param,
		paymentPrivKeys: make(map[uint32]*secp256k1.PrivateKey), // key: index
		revocationBasePrivKeys: make(map[uint32]*secp256k1.PrivateKey), // key: change
		purposes:    make(map[uint32]*hdkeychain.ExtendedKey),
		accounts:    make(map[uint64]*hdkeychain.ExtendedKey),
		addresses:   make(map[uint32]btcutil.Address),
		subWallets:  make(map[uint32]*channelWallet),
	}
}

func (p *InternalWallet) CreateChannelWallet(peer []byte, id uint32) common.ChannelWallet {
	subWallet := NewChannelWallet(p, peer, uint32(id))
	p.subWallets[id] = subWallet
	return subWallet
}

func (p *InternalWallet) GetChannelWallet(id uint32) common.ChannelWallet {
	return p.subWallets[id]
}

func (p *InternalWallet) getPurposeKey(purpose uint32) (*hdkeychain.ExtendedKey, error) {
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

func (p *InternalWallet) getBtcUtilAddress(index uint32) (btcutil.Address, error) {
	addressType := "P2TR"
	address, ok := p.addresses[index]
	if ok {
		return address, nil
	}

	purpose := getPurposeFromAddrType(addressType)
	purposeKey, err := p.getPurposeKey(purpose)
	if err != nil {
		return nil, err
	}

	_, pubkey, err := generateKeyFromPurposeKey(purposeKey, 0, 0, index)
	if err != nil {
		return nil, err
	}

	address, err = getAddressFromPubKey(pubkey, addressType, p.netParamsL1)
	if err != nil {
		return nil, err
	}
	p.addresses[index] = address
	return address, nil
}

func (p *InternalWallet) GetPubKey(index uint32) *secp256k1.PublicKey {
	_, pubKey, err := p.getKey("P2TR", 0, index)
	if err != nil {
		Log.Errorf("GetPubKey failed. %v", err)
		return nil
	}
	return pubKey
}

func (p *InternalWallet) GetAddress(index uint32) string {
	addr, err := p.getBtcUtilAddress(index)
	if err != nil {
		return ""
	}
	return addr.EncodeAddress()
}

// 可以直接暴露这个PrivateKey，不影响钱包私钥的安全
func (p *InternalWallet) GetCommitRootKey(peer []byte) (*secp256k1.PrivateKey, *secp256k1.PublicKey) {
	privkey := p.GetCommitSecret(peer, 0)
	if privkey == nil {
		return nil, nil
	}
	return privkey, privkey.PubKey()
}

// 返回secret，用于生成commitsecrect和commitpoint
// 可以直接暴露这个PrivateKey，不影响钱包私钥的安全
func (p *InternalWallet) GetCommitSecret(peer []byte, index uint32) *secp256k1.PrivateKey {
	return p.getCommitSecret(peer, 0, index)
}

func (p *InternalWallet) getCommitSecret(peer []byte, change, index uint32) *secp256k1.PrivateKey {
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

	privkey, _, err := generateKeyFromAccountKey(acckey, uint32(change), uint32(index))
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
func (p *InternalWallet) DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey {
	revBasePrivKey, _ := p.getRevocationBaseKey(0)
	return utils.DeriveRevocationPrivKey(revBasePrivKey, commitsecret)
}

func (p *InternalWallet) GetRevocationBaseKey() *secp256k1.PublicKey {
	_, pubk := p.getRevocationBaseKey(0)
	return pubk
}

func (p *InternalWallet) getRevocationBaseKey(change uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey) {

	key, ok := p.revocationBasePrivKeys[change]
	if ok {
		return key, key.PubKey()
	}

	purpose := getPurposeFromAddrType("LND")
	purposekey, err := p.getPurposeKey(purpose)
	if err != nil {
		Log.Errorf("getPurposeKey failed. %v", err)
		return nil, nil
	}
	privk, pubk, err := generateKeyFromPurposeKey(purposekey,
		getAccountFromFamilyKey(KeyFamilyRevocationBase), change, 0,
	)

	if err != nil {
		Log.Errorf("generateKeyFromPurposeKey failed. %v", err)
		return nil, nil
	}
	p.revocationBasePrivKeys[change] = privk

	return privk, pubk
}

func (p *InternalWallet) GetNodePubKey() *secp256k1.PublicKey {
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

	_, pubkey, err := generateKeyFromAccountKey(acckey, 0, 0)
	if err != nil {
		Log.Errorf("generateKeyFromAccountKey failed. %v", err)
		return nil
	}

	return pubkey
}

func (p *InternalWallet) GetPaymentPubKey() *secp256k1.PublicKey {
	key := p.getPaymentPrivKey()
	if key == nil {
		Log.Errorf("GetPaymentPrivKey failed.")
		return nil
	}
	return key.PubKey()
}

func (p *InternalWallet) getPaymentPrivKey() *secp256k1.PrivateKey {
	return p.getPaymentPrivKeyWithIndex(0)
}

func (p *InternalWallet) getPaymentPrivKeyWithIndex(index uint32) *secp256k1.PrivateKey {
	key, ok := p.paymentPrivKeys[index]
	if ok {
		return key
	}
	key, _, err := p.getKey("P2TR", 0, index)
	if err != nil {
		Log.Errorf("GetPubKey P2TR failed. %v", err)
		return nil
	}
	p.paymentPrivKeys[index] = key
	return key
}

func (p *InternalWallet) getKey(addressType string, change, index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	purpose := getPurposeFromAddrType(addressType)
	purposekey, err := p.getPurposeKey(purpose)
	if err != nil {
		return nil, nil, err
	}
	return generateKeyFromPurposeKey(purposekey, 0, change, index)
}

func (p *InternalWallet) SignTxInput(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	sigHashes *txscript.TxSigHashes,
	index int, witnessScript []byte) ([]byte, error) {
	privKey := p.getPaymentPrivKey()

	var result []byte
	preOut := prevFetcher.FetchPrevOutput(tx.TxIn[index].PreviousOutPoint)
	scriptType := GetPkScriptType(preOut.PkScript)
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

func (p *InternalWallet) SignTxInput_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	sigHashes *stxscript.TxSigHashes,
	index int, witnessScript []byte) ([]byte, error) {
	privKey := p.getPaymentPrivKey()

	var result []byte
	preOut := prevFetcher.FetchPrevOutput(tx.TxIn[index].PreviousOutPoint)
	scriptType := GetPkScriptType(preOut.PkScript)
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

func (p *InternalWallet) SignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) error {

	privKey := p.getPaymentPrivKey()
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		scriptType := GetPkScriptType(preOut.PkScript)
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

func (p *InternalWallet) SignTx_SatsNet(tx *swire.MsgTx,
	prevFetcher stxscript.PrevOutputFetcher) error {

	privKey := p.getPaymentPrivKey()
	sigHashes := stxscript.NewTxSigHashes(tx, prevFetcher)
	for i, in := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(in.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", in.PreviousOutPoint)
			return fmt.Errorf("can't find outpoint %s", in.PreviousOutPoint)
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
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

func (p *InternalWallet) SignTxWithPeer(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error) {

	myPubKey := p.GetPaymentPubKey().SerializeCompressed()
	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}
	pos := GetCurrSignPosition2(myPubKey, peerPubKey)

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
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
				result = append(result, []byte{})
			}
			continue
		}

		scriptType := GetPkScriptType(preOut.PkScript)
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

func (p *InternalWallet) SignTxWithPeer_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error) {

	myPubKey := p.GetPaymentPubKey().SerializeCompressed()
	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}
	pos := GetCurrSignPosition2(myPubKey, peerPubKey)

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
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
				result = append(result, []byte{})
			}
			continue
		}

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
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

func (p *InternalWallet) PartialSignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, pos int) ([][]byte, error) {

	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
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

		scriptType := GetPkScriptType(preOut.PkScript)
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

func (p *InternalWallet) PartialSignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, pos int) ([][]byte, error) {

	mulpkScript, err := utils.WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, err
	}

	privKey := p.getPaymentPrivKey()
	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
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

		scriptType := GetPkScriptType_SatsNet(preOut.PkScript)
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

func (p *InternalWallet) SignMessage(msg []byte) (*ecdsa.Signature, error) {
	//privKey, err := p.deriveKeyByLocator(KeyFamilyBaseEncryption, 0, 0)
	// if err != nil {
	// 	return nil, err
	// }
	privKey := p.getPaymentPrivKey()
	return p.signMessage(privKey, msg)
}


func (p *InternalWallet) signMessage(privKey *secp256k1.PrivateKey, msg []byte) (*ecdsa.Signature, error) {
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

// 支持上面所有几种不同的签名方式
func (p *InternalWallet) SignPsbt(packet *psbt.Packet) error {
	privKey := p.getPaymentPrivKey()
	return p.signPsbt(privKey, packet)
}

func (p *InternalWallet) signPsbt(privKey *secp256k1.PrivateKey, packet *psbt.Packet) error {
	err := psbt.InputsReadyToSign(packet)
	if err != nil {
		return err
	}

	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	tx := packet.UnsignedTx
	prevOutputFetcher := PsbtPrevOutputFetcher(packet)
	sigHashes := txscript.NewTxSigHashes(tx, prevOutputFetcher)
	for i := range tx.TxIn {
		in := &packet.Inputs[i]
		if in.WitnessUtxo == nil {
			continue
		}

		// Skip this input if it's got final witness data attached.
		if len(in.FinalScriptWitness) > 0 || len(in.FinalScriptSig) > 0 || len(in.TaprootKeySpendSig) > 0 {
			continue
		}

		if bytes.Equal(in.WitnessUtxo.PkScript, p2trPkScript) {
			// 单签，目前只支持p2tr
			witness, err := txscript.TaprootWitnessSignature(tx, sigHashes, i,
				in.WitnessUtxo.Value, in.WitnessUtxo.PkScript,
				txscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return err
			}
			in.TaprootKeySpendSig = witness[0]
			continue
		}

		// 看看是否是指定的脚本
		var script []byte
		if in.WitnessScript != nil {
			script = in.WitnessScript
		} else if in.RedeemScript != nil {
			script = in.RedeemScript
		} else {
			continue
		}

		mulpkScript, err := utils.WitnessScriptHash(script)
		if err != nil {
			return err
		}
		if bytes.Equal(in.WitnessUtxo.PkScript, mulpkScript) {
			// 如何区分是多签脚本和单签脚本？

			sig, err := txscript.RawTxInWitnessSignature(tx, sigHashes, i, in.WitnessUtxo.Value,
				script, txscript.SigHashAll, privKey)
			if err != nil {
				return err
			}
			in.PartialSigs = append(in.PartialSigs, &psbt.PartialSig{
				PubKey:    pubkey.SerializeCompressed(),
				Signature: sig,
			})
		}
	}
	return nil
}

func (p *InternalWallet) SignPsbts(packet []*psbt.Packet) error {
	privKey := p.getPaymentPrivKey()
	return p.signPsbts(privKey, packet)
}

func (p *InternalWallet) signPsbts(privKey *secp256k1.PrivateKey, packets []*psbt.Packet) error {
	for i, packet := range packets {
		err := p.signPsbt(privKey, packet)
		if err != nil {
			Log.Errorf("signPsbt %d failed, %v", i, err)
			return err
		}
	}
	return nil
}

func (p *InternalWallet) SignPsbt_SatsNet(packet *spsbt.Packet) error {
	privKey := p.getPaymentPrivKey()
	return p.signPsbt_SatsNet(privKey, packet)
}

func (p *InternalWallet) signPsbt_SatsNet(privKey *secp256k1.PrivateKey, packet *spsbt.Packet) error {
	err := spsbt.InputsReadyToSign(packet)
	if err != nil {
		return err
	}

	pubkey := privKey.PubKey()
	p2trPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		Log.Errorf("CreatePkScriptForP2TR failed. %v", err)
		return err
	}

	tx := packet.UnsignedTx
	prevOutputFetcher := PsbtPrevOutputFetcher_SatsNet(packet)
	sigHashes := stxscript.NewTxSigHashes(tx, prevOutputFetcher)
	for i := range tx.TxIn {
		in := &packet.Inputs[i]
		if in.WitnessUtxo == nil {
			continue
		}

		// Skip this input if it's got final witness data attached.
		if len(in.FinalScriptWitness) > 0 || len(in.FinalScriptSig) > 0 || len(in.TaprootKeySpendSig) > 0 {
			continue
		}

		if bytes.Equal(in.WitnessUtxo.PkScript, p2trPkScript) {
			// 单签，目前只支持p2tr
			witness, err := stxscript.TaprootWitnessSignature(tx, sigHashes, i,
				in.WitnessUtxo.Value, in.WitnessUtxo.Assets, in.WitnessUtxo.PkScript,
				stxscript.SigHashDefault, privKey)
			if err != nil {
				Log.Errorf("TaprootWitnessSignature failed. %v", err)
				return err
			}
			in.TaprootKeySpendSig = witness[0]
			continue
		}

		// 看看是否是指定的脚本
		var script []byte
		if in.WitnessScript != nil {
			script = in.WitnessScript
		} else if in.RedeemScript != nil {
			script = in.RedeemScript
		} else {
			continue
		}

		mulpkScript, err := utils.WitnessScriptHash(script)
		if err != nil {
			return err
		}
		if bytes.Equal(in.WitnessUtxo.PkScript, mulpkScript) {
			// 如何区分是多签脚本和单签脚本？

			sig, err := stxscript.RawTxInWitnessSignature(tx, sigHashes, i, in.WitnessUtxo.Value, in.WitnessUtxo.Assets,
				script, stxscript.SigHashAll, privKey)
			if err != nil {
				return err
			}
			in.PartialSigs = append(in.PartialSigs, &spsbt.PartialSig{
				PubKey:    pubkey.SerializeCompressed(),
				Signature: sig,
			})
		}
	}
	return nil
}


func (p *InternalWallet) SignPsbts_SatsNet(packet []*spsbt.Packet) error {
	privKey := p.getPaymentPrivKey()
	return p.signPsbts_SatsNet(privKey, packet)
}

func (p *InternalWallet) signPsbts_SatsNet(privKey *secp256k1.PrivateKey, packets []*spsbt.Packet) error {
	for i, packet := range packets {
		err := p.signPsbt_SatsNet(privKey, packet)
		if err != nil {
			Log.Errorf("signPsbt %d failed, %v", i, err)
			return err
		}
	}
	return nil
}

// func (p *Wallet) SignPsbt(packet *psbt.Packet) ([]uint32, error) {
// 	// In signedInputs we return the indices of psbt inputs that were signed
// 	// by our wallet. This way the caller can check if any inputs were signed.
// 	var signedInputs []uint32

// 	// Let's check that this is actually something we can and want to sign.
// 	// We need at least one input and one output. In addition each
// 	// input needs nonWitness Utxo or witness Utxo data specified.
// 	err := psbt.InputsReadyToSign(packet)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Go through each input that doesn't have final witness data attached
// 	// to it already and try to sign it. If there is nothing more to sign or
// 	// there are inputs that we don't know how to sign, we won't return any
// 	// error. So it's possible we're not the final signer.
// 	tx := packet.UnsignedTx
// 	prevOutputFetcher := PsbtPrevOutputFetcher(packet)
// 	sigHashes := txscript.NewTxSigHashes(tx, prevOutputFetcher)
// 	for idx := range tx.TxIn {
// 		in := &packet.Inputs[idx]

// 		// We can only sign if we have UTXO information available. Since
// 		// we don't finalize, we just skip over any input that we know
// 		// we can't do anything with. Since we only support signing
// 		// witness inputs, we only look at the witness UTXO being set.
// 		if in.WitnessUtxo == nil {
// 			continue
// 		}

// 		// Skip this input if it's got final witness data attached.
// 		if len(in.FinalScriptWitness) > 0 {
// 			continue
// 		}

// 		// Skip this input if there is no BIP32 derivation info
// 		// available.
// 		if len(in.Bip32Derivation) == 0 {
// 			continue
// 		}

// 		// TODO(guggero): For multisig, we'll need to find out what key
// 		// to use and there should be multiple derivation paths in the
// 		// BIP32 derivation field.

// 		// Let's try and derive the key now. This method will decide if
// 		// it's a BIP49/84 key for normal on-chain funds or a key of the
// 		// custom purpose 1017 key scope.
// 		derivationInfo := in.Bip32Derivation[0]
// 		privKey, err := p.deriveKeyByBIP32Path(derivationInfo.Bip32Path)
// 		if err != nil {
// 			Log.Warnf("SignPsbt: Skipping input %d, error "+
// 				"deriving signing key: %v", idx, err)
// 			continue
// 		}

// 		// We need to make sure we actually derived the key that was
// 		// expected to be derived.
// 		pubKeysEqual := bytes.Equal(
// 			derivationInfo.PubKey,
// 			privKey.PubKey().SerializeCompressed(),
// 		)
// 		if !pubKeysEqual {
// 			Log.Warnf("SignPsbt: Skipping input %d, derived "+
// 				"public key %x does not match bip32 "+
// 				"derivation info public key %x", idx,
// 				privKey.PubKey().SerializeCompressed(),
// 				derivationInfo.PubKey)
// 			continue
// 		}

// 		// Do we need to tweak anything? Single or double tweaks are
// 		// sent as custom/proprietary fields in the PSBT input section.
// 		privKey = maybeTweakPrivKeyPsbt(in.Unknowns, privKey)

// 		// What kind of signature is expected from us and do we have all
// 		// information we need?
// 		signMethod, err := validateSigningMethod(in)
// 		if err != nil {
// 			return nil, err
// 		}

// 		switch signMethod {
// 		// For p2wkh, np2wkh and p2wsh.
// 		case utils.WitnessV0SignMethod:
// 			err = signSegWitV0(in, tx, sigHashes, idx, privKey)

// 		// For p2tr BIP0086 key spend only.
// 		case utils.TaprootKeySpendBIP0086SignMethod:
// 			rootHash := make([]byte, 0)
// 			err = signSegWitV1KeySpend(
// 				in, tx, sigHashes, idx, privKey, rootHash,
// 			)

// 		// For p2tr with script commitment key spend path.
// 		case utils.TaprootKeySpendSignMethod:
// 			rootHash := in.TaprootMerkleRoot
// 			err = signSegWitV1KeySpend(
// 				in, tx, sigHashes, idx, privKey, rootHash,
// 			)

// 		// For p2tr script spend path.
// 		case utils.TaprootScriptSpendSignMethod:
// 			leafScript := in.TaprootLeafScript[0]
// 			leaf := txscript.TapLeaf{
// 				LeafVersion: leafScript.LeafVersion,
// 				Script:      leafScript.Script,
// 			}
// 			err = signSegWitV1ScriptSpend(
// 				in, tx, sigHashes, idx, privKey, leaf,
// 			)

// 		default:
// 			err = fmt.Errorf("unsupported signing method for "+
// 				"PSBT signing: %v", signMethod)
// 		}
// 		if err != nil {
// 			return nil, err
// 		}
// 		signedInputs = append(signedInputs, uint32(idx))
// 	}
// 	return signedInputs, nil
// }

// func (p *Wallet) SignOutputRaw(tx *wire.MsgTx, signDesc *utils.SignDescriptor) (utils.Signature, error) {
// 	witnessScript := signDesc.WitnessScript

// 	// First attempt to fetch the private key which corresponds to the
// 	// specified public key.
// 	privKey, err := p.fetchPrivKey(uint32(signDesc.KeyDesc.KeyLocator.Family), signDesc.KeyDesc.Index)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// If a tweak (single or double) is specified, then we'll need to use
// 	// this tweak to derive the final private key to be used for signing
// 	// this output.
// 	privKey, err = maybeTweakPrivKey(signDesc, privKey)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// In case of a taproot output any signature is always a Schnorr
// 	// signature, based on the new tapscript sighash algorithm.
// 	if txscript.IsPayToTaproot(signDesc.Output.PkScript) {
// 		sigHashes := txscript.NewTxSigHashes(
// 			tx, signDesc.PrevOutputFetcher,
// 		)

// 		// Are we spending a script path or the key path? The API is
// 		// slightly different, so we need to account for that to get the
// 		// raw signature.
// 		var rawSig []byte
// 		switch signDesc.SignMethod {
// 		case utils.TaprootKeySpendBIP0086SignMethod,
// 			utils.TaprootKeySpendSignMethod:

// 			// This function tweaks the private key using the tap
// 			// root key supplied as the tweak.
// 			rawSig, err = txscript.RawTxInTaprootSignature(
// 				tx, sigHashes, signDesc.InputIndex,
// 				signDesc.Output.Value, signDesc.Output.PkScript,
// 				signDesc.TapTweak, signDesc.HashType,
// 				privKey,
// 			)
// 			if err != nil {
// 				return nil, err
// 			}

// 		case utils.TaprootScriptSpendSignMethod:
// 			leaf := txscript.TapLeaf{
// 				LeafVersion: txscript.BaseLeafVersion,
// 				Script:      witnessScript,
// 			}
// 			rawSig, err = txscript.RawTxInTapscriptSignature(
// 				tx, sigHashes, signDesc.InputIndex,
// 				signDesc.Output.Value, signDesc.Output.PkScript,
// 				leaf, signDesc.HashType, privKey,
// 			)
// 			if err != nil {
// 				return nil, err
// 			}

// 		default:
// 			return nil, fmt.Errorf("unknown sign method: %v",
// 				signDesc.SignMethod)
// 		}

// 		// The signature returned above might have a sighash flag
// 		// attached if a non-default type was used. We'll slice this
// 		// off if it exists to ensure we can properly parse the raw
// 		// signature.
// 		sig, err := schnorr.ParseSignature(
// 			rawSig[:schnorr.SignatureSize],
// 		)
// 		if err != nil {
// 			return nil, err
// 		}

// 		return sig, nil
// 	}

// 	amt := signDesc.Output.Value
// 	sig, err := txscript.RawTxInWitnessSignature(
// 		tx, signDesc.SigHashes, signDesc.InputIndex, amt,
// 		witnessScript, signDesc.HashType, privKey,
// 	)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// Chop off the sighash flag at the end of the signature.
// 	return ecdsa.ParseDERSignature(sig[:len(sig)-1])
// }

// func (p *Wallet) SignPsbt_satsnet(packet *spsbt.Packet) error {
// 	return fmt.Errorf("not implemented")
// }

// func (p *Wallet) SignOutputRaw_satsnet(tx *swire.MsgTx, signDesc *utils.SignDescriptor) (utils.Signature, error) {
// 	return nil, fmt.Errorf("not implemented")
// }

// func (p *Wallet) fetchPrivKey(family, index uint32) (*secp256k1.PrivateKey, error) {
// 	return p.deriveKeyByLocator(family, index)
// }

// // deriveKeyByBIP32Path derives a key described by a BIP32 path. We expect the
// // first three elements of the path to be hardened according to BIP44, so they
// // must be a number >= 2^31.
// func (p *Wallet) deriveKeyByBIP32Path(path []uint32) (*btcec.PrivateKey,
// 	error) {

// 	// Make sure we get a full path with exactly 5 elements. A path is
// 	// either custom purpose one with 4 dynamic and one static elements:
// 	//    m/1017'/coinType'/keyFamily'/0/index
// 	// Or a default BIP49/89 one with 5 elements:
// 	//    m/purpose'/coinType'/account'/change/index
// 	const expectedDerivationPathDepth = 5
// 	if len(path) != expectedDerivationPathDepth {
// 		return nil, fmt.Errorf("invalid BIP32 derivation path, "+
// 			"expected path length %d, instead was %d",
// 			expectedDerivationPathDepth, len(path))
// 	}

// 	// Assert that the first three parts of the path are actually hardened
// 	// to avoid under-flowing the uint32 type.
// 	if err := assertHardened(path[0], path[1], path[2]); err != nil {
// 		return nil, fmt.Errorf("invalid BIP32 derivation path, "+
// 			"expected first three elements to be hardened: %w", err)
// 	}

// 	// purpose := path[0] - hdkeychain.HardenedKeyStart
// 	// coinType := path[1] - hdkeychain.HardenedKeyStart
// 	// account := path[2] - hdkeychain.HardenedKeyStart
// 	// change, index := path[3], path[4]

// 	key, err := p.masterkey.Derive(path[0])
// 	if err != nil {
// 		Log.Errorf("Failed to generate purpose chain: %v", err)
// 		return nil, err
// 	}
// 	key, err = key.Derive(path[1])
// 	if err != nil {
// 		Log.Errorf("Failed to generate coin chain: %v", err)
// 		return nil, err
// 	}
// 	key, err = key.Derive(path[2])
// 	if err != nil {
// 		Log.Errorf("Failed to generate account chain: %v", err)
// 		return nil, err
// 	}
// 	key, err = key.Derive(path[3])
// 	if err != nil {
// 		Log.Errorf("Failed to generate change chain: %v", err)
// 		return nil, err
// 	}
// 	key, err = key.Derive(path[4])
// 	if err != nil {
// 		Log.Errorf("Failed to generate index chain: %v", err)
// 		return nil, err
// 	}

// 	privateKey, err := key.ECPrivKey()
// 	if err != nil {
// 		Log.Errorf("ECPrivKey failed: %v", err)
// 		return nil, err
// 	}

// 	return privateKey, nil
// }

func (p *InternalWallet) deriveKeyByLocator(family, change, index uint32) (*secp256k1.PrivateKey, error) {
	account := getAccountFromFamilyKey(family)

	acckey, err := p.getPurposeKey(account)
	if err != nil {
		return nil, err
	}
	privkey, _, err := generateKeyFromAccountKey(acckey, change, index)
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
	account, change, index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	// 生成外部链或内部链
	accountKey, err := generateAccountKey2(purposeKey, account)
	if err != nil {
		Log.Errorf("Failed to generate account chain: %v", err)
		return nil, nil, err
	}
	changeKey, err := accountKey.Derive(change)
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
	change, index uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey, error) {
	// 生成外部链或内部链
	changeKey, err := accountKey.Derive(change)
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

// maybeTweakPrivKey examines the single and double tweak parameters on the
// passed sign descriptor and may perform a mapping on the passed private key
// in order to utilize the tweaks, if populated.
func maybeTweakPrivKey(signDesc *utils.SignDescriptor,
	privKey *secp256k1.PrivateKey) (*secp256k1.PrivateKey, error) {

	var retPriv *secp256k1.PrivateKey
	switch {

	case signDesc.SingleTweak != nil:
		retPriv = utils.TweakPrivKey(privKey,
			signDesc.SingleTweak)

	case signDesc.DoubleTweak != nil:
		retPriv = utils.DeriveRevocationPrivKey(privKey,
			signDesc.DoubleTweak)

	default:
		retPriv = privKey
	}

	return retPriv, nil
}

// maybeTweakPrivKeyPsbt examines if there are any tweak parameters given in the
// custom/proprietary PSBT fields and may perform a mapping on the passed
// private key in order to utilize the tweaks, if populated.
func maybeTweakPrivKeyPsbt(unknowns []*psbt.Unknown,
	privKey *secp256k1.PrivateKey) *secp256k1.PrivateKey {

	// There can be other custom/unknown keys in a PSBT that we just ignore.
	// Key tweaking is optional and only one tweak (single _or_ double) can
	// ever be applied (at least for any use cases described in the BOLT
	// spec).
	for _, u := range unknowns {
		if bytes.Equal(u.Key, PsbtKeyTypeInputSignatureTweakSingle) {
			return utils.TweakPrivKey(privKey, u.Value)
		}

		if bytes.Equal(u.Key, PsbtKeyTypeInputSignatureTweakDouble) {
			doubleTweakKey, _ := btcec.PrivKeyFromBytes(
				u.Value,
			)
			return utils.DeriveRevocationPrivKey(
				privKey, doubleTweakKey,
			)
		}
	}

	return privKey
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

// PsbtPrevOutputFetcher returns a txscript.PrevOutFetcher built from the UTXO
// information in a PSBT packet.
func PsbtPrevOutputFetcher_SatsNet(packet *spsbt.Packet) *stxscript.MultiPrevOutFetcher {
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
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

// assertHardened makes sure each given element is >= 2^31.
func assertHardened(elements ...uint32) error {
	for idx, element := range elements {
		if element < hdkeychain.HardenedKeyStart {
			return fmt.Errorf("element at index %d is not hardened",
				idx)
		}
	}

	return nil
}

// validateSigningMethod attempts to detect the signing method that is required
// to sign for the given PSBT input and makes sure all information is available
// to do so.
func validateSigningMethod(in *psbt.PInput) (utils.SignMethod, error) {
	script, err := txscript.ParsePkScript(in.WitnessUtxo.PkScript)
	if err != nil {
		return 0, fmt.Errorf("error detecting signing method, "+
			"couldn't parse pkScript: %v", err)
	}

	switch script.Class() {
	case txscript.WitnessV0PubKeyHashTy, txscript.ScriptHashTy,
		txscript.WitnessV0ScriptHashTy:

		return utils.WitnessV0SignMethod, nil

	case txscript.WitnessV1TaprootTy:
		if len(in.TaprootBip32Derivation) == 0 {
			return 0, fmt.Errorf("cannot sign for taproot input " +
				"without taproot BIP0032 derivation info")
		}

		// Currently, we only support creating one signature per utils.
		//
		// TODO(guggero): Should we support signing multiple paths at
		// the same time? What are the performance and security
		// implications?
		if len(in.TaprootBip32Derivation) > 1 {
			return 0, fmt.Errorf("unsupported multiple taproot " +
				"BIP0032 derivation info found, can only " +
				"sign for one at a time")
		}

		derivation := in.TaprootBip32Derivation[0]
		switch {
		// No leaf hashes means this is the internal key we're signing
		// with, so it's a key spend. And no merkle root means this is
		// a BIP0086 output we're signing for.
		case len(derivation.LeafHashes) == 0 &&
			len(in.TaprootMerkleRoot) == 0:

			return utils.TaprootKeySpendBIP0086SignMethod, nil

		// A non-empty merkle root means we committed to a taproot hash
		// that we need to use in the tap tweak.
		case len(derivation.LeafHashes) == 0:
			// Getting here means the merkle root isn't empty, but
			// is it exactly the length we need?
			if len(in.TaprootMerkleRoot) != sha256.Size {
				return 0, fmt.Errorf("invalid taproot merkle "+
					"root length, got %d expected %d",
					len(in.TaprootMerkleRoot), sha256.Size)
			}

			return utils.TaprootKeySpendSignMethod, nil

		// Currently, we only support signing for one leaf at a time.
		//
		// TODO(guggero): Should we support signing multiple paths at
		// the same time? What are the performance and security
		// implications?
		case len(derivation.LeafHashes) == 1:
			// If we're supposed to be signing for a leaf hash, we
			// also expect the leaf script that hashes to that hash
			// in the appropriate field.
			if len(in.TaprootLeafScript) != 1 {
				return 0, fmt.Errorf("specified leaf hash in " +
					"taproot BIP0032 derivation but " +
					"missing taproot leaf script")
			}

			leafScript := in.TaprootLeafScript[0]
			leaf := txscript.TapLeaf{
				LeafVersion: leafScript.LeafVersion,
				Script:      leafScript.Script,
			}
			leafHash := leaf.TapHash()
			if !bytes.Equal(leafHash[:], derivation.LeafHashes[0]) {
				return 0, fmt.Errorf("specified leaf hash in" +
					"taproot BIP0032 derivation but " +
					"corresponding taproot leaf script " +
					"was not found")
			}

			return utils.TaprootScriptSpendSignMethod, nil

		default:
			return 0, fmt.Errorf("unsupported number of leaf " +
				"hashes in taproot BIP0032 derivation info, " +
				"can only sign for one at a time")
		}

	default:
		return 0, fmt.Errorf("unsupported script class for signing "+
			"PSBT: %v", script.Class())
	}
}

// SignSegWitV0 attempts to generate a signature for a SegWit version 0 input
// and stores it in the PartialSigs (and FinalScriptSig for np2wkh addresses)
// field.
func signSegWitV0(in *psbt.PInput, tx *wire.MsgTx,
	sigHashes *txscript.TxSigHashes, idx int,
	privKey *btcec.PrivateKey) error {

	pubKeyBytes := privKey.PubKey().SerializeCompressed()

	// Extract the correct witness and/or legacy scripts now, depending on
	// the type of input we sign. The txscript package has the peculiar
	// requirement that the PkScript of a P2PKH must be given as the witness
	// script in order for it to arrive at the correct sighash. That's why
	// we call it subScript here instead of witness script.
	subScript := prepareScriptsV0(in)

	// We have everything we need for signing the input now.
	sig, err := txscript.RawTxInWitnessSignature(
		tx, sigHashes, idx, in.WitnessUtxo.Value, subScript,
		in.SighashType, privKey,
	)
	if err != nil {
		return fmt.Errorf("error signing input %d: %w", idx, err)
	}
	in.PartialSigs = append(in.PartialSigs, &psbt.PartialSig{
		PubKey:    pubKeyBytes,
		Signature: sig,
	})

	return nil
}

// signSegWitV1KeySpend attempts to generate a signature for a SegWit version 1
// (p2tr) input and stores it in the TaprootKeySpendSig field.
func signSegWitV1KeySpend(in *psbt.PInput, tx *wire.MsgTx,
	sigHashes *txscript.TxSigHashes, idx int, privKey *btcec.PrivateKey,
	tapscriptRootHash []byte) error {

	rawSig, err := txscript.RawTxInTaprootSignature(
		tx, sigHashes, idx, in.WitnessUtxo.Value,
		in.WitnessUtxo.PkScript, tapscriptRootHash, in.SighashType,
		privKey,
	)
	if err != nil {
		return fmt.Errorf("error signing taproot input %d: %w", idx,
			err)
	}

	in.TaprootKeySpendSig = rawSig

	return nil
}

// signSegWitV1ScriptSpend attempts to generate a signature for a SegWit version
// 1 (p2tr) input and stores it in the TaprootScriptSpendSig field.
func signSegWitV1ScriptSpend(in *psbt.PInput, tx *wire.MsgTx,
	sigHashes *txscript.TxSigHashes, idx int, privKey *btcec.PrivateKey,
	leaf txscript.TapLeaf) error {

	rawSig, err := txscript.RawTxInTapscriptSignature(
		tx, sigHashes, idx, in.WitnessUtxo.Value,
		in.WitnessUtxo.PkScript, leaf, in.SighashType, privKey,
	)
	if err != nil {
		return fmt.Errorf("error signing taproot script input %d: %w",
			idx, err)
	}

	leafHash := leaf.TapHash()
	in.TaprootScriptSpendSig = append(
		in.TaprootScriptSpendSig, &psbt.TaprootScriptSpendSig{
			XOnlyPubKey: in.TaprootBip32Derivation[0].XOnlyPubKey,
			LeafHash:    leafHash[:],
			// We snip off the sighash flag from the end (if it was
			// specified in the first place.)
			Signature: rawSig[:schnorr.SignatureSize],
			SigHash:   in.SighashType,
		},
	)

	return nil
}

// prepareScriptsV0 returns the appropriate witness v0 and/or legacy scripts,
// depending on the type of input that should be signed.
func prepareScriptsV0(in *psbt.PInput) []byte {
	switch {
	// It's a NP2WKH input:
	case len(in.RedeemScript) > 0:
		return in.RedeemScript

	// It's a P2WSH input:
	case len(in.WitnessScript) > 0:
		return in.WitnessScript

	// It's a P2WKH input:
	default:
		return in.WitnessUtxo.PkScript
	}
}

func CreatePsbt(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte) (*psbt.Packet, error) {
	packet, err := psbt.NewFromUnsignedTx(RemoveSignatures(tx))
	if err != nil {
		return nil, err
	}

	var pkScript []byte
	if witnessScript != nil {
		pkScript, err = utils.WitnessScriptHash(witnessScript)
		if err != nil {
			return nil, err
		}
	}

	for i, txIn := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
		}
		input := &packet.Inputs[i]
		input.WitnessUtxo = preOut
		if bytes.Equal(preOut.PkScript, pkScript) {
			input.WitnessScript = witnessScript
		}
	}

	return packet, nil
}

func CreatePsbt_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte) (*spsbt.Packet, error) {
	packet, err := spsbt.NewFromUnsignedTx(RemoveSignatures_SatsNet(tx))
	if err != nil {
		return nil, err
	}

	var pkScript []byte
	if witnessScript != nil {
		pkScript, err = utils.WitnessScriptHash(witnessScript)
		if err != nil {
			return nil, err
		}
	}

	for i, txIn := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
		}
		input := &packet.Inputs[i]
		input.WitnessUtxo = preOut
		if bytes.Equal(preOut.PkScript, pkScript) {
			input.WitnessScript = witnessScript
		}
	}

	return packet, nil
}

func CreatePsbtWithPeer(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSigs [][]byte) (*psbt.Packet, error) {
	packet, err := psbt.NewFromUnsignedTx(RemoveSignatures(tx))
	if err != nil {
		return nil, err
	}

	var pkScript []byte
	if witnessScript != nil {
		pkScript, err = utils.WitnessScriptHash(witnessScript)
		if err != nil {
			return nil, err
		}
	}

	pubkey, err := secp256k1.ParsePubKey(peerPubKey)
	if err != nil {
		return nil, err
	}
	peerPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		return nil, err
	}

	for i, txIn := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
		}
		input := &packet.Inputs[i]
		input.WitnessUtxo = preOut
		if bytes.Equal(preOut.PkScript, pkScript) {
			input.WitnessScript = witnessScript
			if len(peerSigs[i]) > 0 {
				input.PartialSigs = append(input.PartialSigs, &psbt.PartialSig{
					PubKey:    peerPubKey,
					Signature: peerSigs[i],
				})
			}
			continue
		}
		if len(peerSigs[i]) > 0 {
			if bytes.Equal(preOut.PkScript, peerPkScript) {
				input.TaprootKeySpendSig = peerSigs[i]
			}
			continue
		}
	}

	return packet, nil
}

func CreatePsbtWithPeer_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
	witnessScript []byte, peerPubKey []byte, peerSigs [][]byte) (*spsbt.Packet, error) {
	// tx 必须是unsigned

	packet, err := spsbt.NewFromUnsignedTx(RemoveSignatures_SatsNet(tx))
	if err != nil {
		return nil, err
	}

	var pkScript []byte
	if witnessScript != nil {
		pkScript, err = utils.WitnessScriptHash(witnessScript)
		if err != nil {
			return nil, err
		}
	}

	pubkey, err := secp256k1.ParsePubKey(peerPubKey)
	if err != nil {
		return nil, err
	}
	peerPkScript, err := GetP2TRpkScript(pubkey)
	if err != nil {
		return nil, err
	}

	for i, txIn := range tx.TxIn {
		preOut := prevFetcher.FetchPrevOutput(txIn.PreviousOutPoint)
		if preOut == nil {
			Log.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
			return nil, fmt.Errorf("can't find outpoint %s", txIn.PreviousOutPoint)
		}
		input := &packet.Inputs[i]
		input.WitnessUtxo = preOut
		if bytes.Equal(preOut.PkScript, pkScript) {
			input.WitnessScript = witnessScript
			if len(peerSigs[i]) > 0 {
				input.PartialSigs = append(input.PartialSigs, &spsbt.PartialSig{
					PubKey:    peerPubKey,
					Signature: peerSigs[i],
				})
			}
			continue
		}
		if len(peerSigs[i]) > 0 {
			if bytes.Equal(preOut.PkScript, peerPkScript) {
				input.TaprootKeySpendSig = peerSigs[i]
			}
			continue
		}
	}

	return packet, nil
}

func RemoveSignatures(signedTx *wire.MsgTx) *wire.MsgTx {
	// 创建一个新的交易对象，保持与原交易相同
	unsingedTx := wire.NewMsgTx(signedTx.Version)
	unsingedTx.LockTime = signedTx.LockTime

	for _, txIn := range signedTx.TxIn {
		newTxIn := *txIn
		newTxIn.SignatureScript = nil
		newTxIn.Witness = nil

		unsingedTx.AddTxIn(&newTxIn)
	}

	for _, txOut := range signedTx.TxOut {
		unsingedTx.AddTxOut(txOut)
	}

	return unsingedTx
}

func RemoveSignatures_SatsNet(signedTx *swire.MsgTx) *swire.MsgTx {
	// 创建一个新的交易对象，保持与原交易相同
	unsingedTx := swire.NewMsgTx(signedTx.Version)
	unsingedTx.LockTime = signedTx.LockTime

	for _, txIn := range signedTx.TxIn {
		newTxIn := *txIn
		newTxIn.SignatureScript = nil
		newTxIn.Witness = nil

		unsingedTx.AddTxIn(&newTxIn)
	}

	for _, txOut := range signedTx.TxOut {
		unsingedTx.AddTxOut(txOut)
	}

	return unsingedTx
}
