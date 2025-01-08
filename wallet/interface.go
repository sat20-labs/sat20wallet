package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/wallet/indexer"
	"github.com/sat20-labs/sat20wallet/wallet/sindexer"
	"github.com/sat20-labs/sat20wallet/wallet/utils"
	sbtcutil "github.com/sat20-labs/satsnet_btcd/btcutil"
	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
)

func NewManager(cfg *Config, quit chan struct{}) *Manager {
	Log.Infof("sat20wallet_ver:%s, DB_ver:%s", SOFTWARE_VERSION, DB_VERSION)

	//////////

	mgr := &Manager{
		cfg:                cfg,
		walletInfoMap:      nil,
		bInited:            false,
		bStop:              false,
		quit:               quit,
	}

	_chain = cfg.Chain

	mgr.db = NewKVDB(cfg.DB)
	if mgr.db == nil {
		Log.Errorf("NewKVDB failed")
		return nil
	}

	return mgr
}

func (p *Manager) Init() error {

	if p.wallet == nil {
		return fmt.Errorf("wallet is not created/unlocked/connected")
	}

	err := p.init()
	if err != nil {
		return err
	}

	return nil
}

// 使用内部钱包
func (p *Manager) CreateWallet(password string) (string, error) {
	// if p.wallet != nil {
	// 	return "", fmt.Errorf("wallet has been created, please unlock it first")
	// }

	// if p.IsWalletExist() {
	// 	return "", fmt.Errorf("wallet has been created, please unlock it first")
	// }

	wallet, mnemonic, err := NewInteralWallet(GetChainParam())
	if err != nil {
		return "", err
	}

	_, err = p.saveMnemonic(mnemonic, password)
	if err != nil {
		return "", err
	}

	p.wallet = wallet
	p.password = password

	return mnemonic, nil
}

func (p *Manager) ImportWallet(mnemonic string, password string) error {
	// Log.Infof("ImportWallet %s %s", mnemonic, password)
	// if p.wallet != nil {
	// 	return fmt.Errorf("wallet exists, not allow to import new wallet")
	// }

	// if p.IsWalletExist() {
	// 	return fmt.Errorf("wallet exists, not allow to import new wallet")
	// }

	wallet := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if wallet == nil {
		return fmt.Errorf("NewWalletWithMnemonic failed")
	}

	_, err := p.saveMnemonic(mnemonic, password)
	if err != nil {
		return err
	}

	p.wallet = wallet
	p.password = password

	return nil
}

func (p *Manager) UnlockWallet(password string) error {

	if p.wallet != nil {
		return fmt.Errorf("wallet has been unlocked")
	}

	mnemonic, err := p.loadMnemonic(p.status.CurrentWallet, password)
	if err != nil {
		return err
	}

	wallet := NewInternalWalletWithMnemonic(string(mnemonic), "", GetChainParam())
	if wallet == nil {
		return fmt.Errorf("NewWalletWithMnemonic failed")
	}

	p.wallet = wallet
	p.password = password

	return nil
}

func (p *Manager) GetAllWallets() []int64 {
	result := make([]int64, 0)
	for k, _ := range p.walletInfoMap {
		result = append(result, k)
	}
	return result
}

func (p *Manager) SwitchWallet(id int64) {
	if p.status.CurrentWallet == id {
		return
	}
		
	p.status.CurrentWallet = id
	err := p.UnlockWallet(p.password)
	if err == nil {
		p.saveStatus()
	}
}

func (p *Manager) SwitchChain(chain string) {
	if _chain == chain {
		return
	}
	if chain == "mainnet" || chain == "testnet" {
		_chain = chain
		p.status.CurrentChain = chain
		err := p.UnlockWallet(p.password)
		if err == nil {
			p.saveStatus()
		}
	}
}

func (p *Manager) GetChain() string {
	return _chain
}


func (p *Manager) GetMnemonic(id int64, password string) string {
	mnemonic, err := p.loadMnemonic(id, password)
	if err != nil {
		return ""
	}

	return mnemonic
}

// private key
func (p *Manager) GetCommitRootKey(peer []byte) []byte {
	if p.wallet == nil {
		return nil
	}
	privkey, _ := p.wallet.GetCommitRootKey(peer)
	return privkey.Serialize()
}

// private key
func (p *Manager) GetCommitSecret(peer []byte, index int) []byte {
	if p.wallet == nil {
		return nil
	}
	privkey := p.wallet.GetCommitSecret(peer, index)
	return privkey.Serialize()
}

// private key
func (p *Manager) DeriveRevocationPrivKey(commitsecret []byte) []byte {
	if p.wallet == nil {
		return nil
	}
	privSecret, _ := btcec.PrivKeyFromBytes(commitsecret)
	privkey := p.wallet.DeriveRevocationPrivKey(privSecret)
	return privkey.Serialize()
}

// pub key
func (p *Manager) GetRevocationBaseKey() []byte {
	if p.wallet == nil {
		return nil
	}
	pubKey := p.wallet.GetRevocationBaseKey()
	return pubKey.SerializeCompressed()
}

// pub key
func (p *Manager) GetNodePubKey() []byte {
	if p.wallet == nil {
		return nil
	}
	pubKey := p.wallet.GetNodePubKey()
	return pubKey.SerializeCompressed()
}

func (p *Manager) GetPublicKey() string {
	if p.wallet == nil {
		return ""
	}

	pubkey := p.wallet.GetPaymentPubKey()
	if pubkey == nil {
		return ""
	}

	return hex.EncodeToString(pubkey.SerializeCompressed())
}

func (p *Manager) SignMessage(msg []byte) ([]byte, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	sig, err := p.wallet.SignMessage(msg)
	if err != nil {
		return nil, err
	}
	return sig.Serialize(), nil
}

func (p *Manager) SignPsbt(psbtHex string) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, _ := hex.DecodeString(psbtHex)
    packet, err := psbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
    if err != nil {
        Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
    }

	err = p.wallet.SignPsbt(packet)
	if err != nil {
        Log.Errorf("SignPsbt failed, %v", err)
		return "", err
    }

	var buf bytes.Buffer
	err = packet.Serialize(&buf)
	if err != nil {
		Log.Errorf("Serialize failed, %v", err)
		return "", err
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

func (p *Manager) SignPsbt_SatsNet(psbtHex string) (string, error) {
	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	hexBytes, _ := hex.DecodeString(psbtHex)
    packet, err := spsbt.NewFromRawBytes(bytes.NewReader(hexBytes), false)
    if err != nil {
        Log.Errorf("NewFromRawBytes failed, %v", err)
		return "", err
    }

	err = p.wallet.SignPsbt_SatsNet(packet)
	if err != nil {
        Log.Errorf("SignPsbt_SatsNet failed, %v", err)
		return "", err
    }

	var buf bytes.Buffer
	err = packet.Serialize(&buf)
	if err != nil {
		Log.Errorf("Serialize failed, %v", err)
		return "", err
	}

	return hex.EncodeToString(buf.Bytes()), nil
}

// 仅用于测试使用，以后不提供这样的接口
func (p *Manager) SendUtxos_SatsNet(destAddr string, utxos, fees []string) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	start := time.Now()
	Log.Infof("SendUtxos_SatsNet")
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	pkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)

	var input TxOutput_SatsNet
	value := int64(0)
	for _, utxo := range utxos {
		outpoint, err := UtxoToWireOutpoint_SatsNet(utxo)
		if err != nil {
			Log.Errorf("invalid utxo %s", utxo)
			return "", err
		}

		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return "", fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		txOut_SatsNet := OutputToSatsNet(txOut)

		value += txOut.OutValue.Value
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut_SatsNet.OutValue)

		output := sindexer.TxOutput{
			OutPointStr: utxo,
			OutValue:    txOut_SatsNet.OutValue,
		}
		input.Merge(&output)
	}

	var feeInput TxOutput_SatsNet
	feeValue := int64(0)
	for _, utxo := range utxos {
		outpoint, err := UtxoToWireOutpoint_SatsNet(utxo)
		if err != nil {
			Log.Errorf("invalid utxo %s", utxo)
			return "", err
		}

		txOut, err := p.l2IndexerClient.GetTxOutput(utxo)
		if err != nil {
			return "", fmt.Errorf("GetTxOutput %s failed, %v", utxo, err)
		}
		txOut_SatsNet := OutputToSatsNet(txOut)

		feeValue += txOut.OutValue.Value
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut_SatsNet.OutValue)

		output := sindexer.TxOutput{
			OutPointStr: utxo,
			OutValue:    txOut_SatsNet.OutValue,
		}
		feeInput.Merge(&output)
	}
	if feeValue < DEFAULT_FEE_SATSNET {
		return "", fmt.Errorf("not enough fee")
	}
	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     DEFAULT_FEE_SATSNET,
		BindingSat: 1,
	}
	err = feeInput.SubAsset(&feeAsset)
	if err != nil {
		return "", err
	}

	txOut := swire.NewTxOut(input.Value(), GenTxAssetsFromAssets(input.OutValue.Assets), pkScript)
	tx.AddTxOut(txOut)

	if !feeInput.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(feeInput.Value(), GenTxAssetsFromAssets(feeInput.OutValue.Assets), changePkScript)
		tx.AddTxOut(txOut2)
	}

	// sign
	err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "SendUtxos_SatsNet")

	txid, err := p.l2IndexerClient.BroadCastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	Log.Infof("SendUtxos_SatsNet finished, %v", time.Since(start))

	return txid, nil
}

func (p *Manager) SendAssets_SatsNet(destAddr string,
	assetName string, amt int64) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}
	name := ParseAssetString(assetName)
	if name == nil {
		return "", fmt.Errorf("invalid asseet name %s", assetName)
	}
	if amt <= 0 {
		return "", fmt.Errorf("invalid amount %d", amt)
	}

	address := p.wallet.GetP2TRAddress()
	outputs := p.l2IndexerClient.GetUtxoListWithTicker(address, name)
	if len(outputs) == 0 {
		Log.Errorf("no asset %s", assetName)
		return "", fmt.Errorf("no asset %s", assetName)
	}

	start := time.Now()
	Log.Infof("SendAssets_SatsNet %s %d", assetName, amt)
	tx := swire.NewMsgTx(swire.TxVersion)

	addr, err := sbtcutil.DecodeAddress(destAddr, GetChainParam_SatsNet())
	if err != nil {
		return "", err
	}
	pkScript, err := stxscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	expectedAmt := amt
	if indexer.IsPlainAsset(name) {
		expectedAmt += DEFAULT_FEE_SATSNET
	}

	prevFetcher := stxscript.NewMultiPrevOutFetcher(nil)
	var input TxOutput_SatsNet
	value := int64(0)
	assetAmt := int64(0)
	for _, out := range outputs {
		output := OutputInfoToOutput_SatsNet(out)
		outpoint := output.OutPoint()
		txOut := output.OutValue

		value += out.OutValue.Value
		assetAmt += output.GetAsset(name)
		txIn := swire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, &txOut)
		input.Merge(output)

		if assetAmt >= expectedAmt {
			break
		}
	}
	if assetAmt < expectedAmt {
		return "", fmt.Errorf("not enough asset %s", assetName)
	}

	var feeOutputs []*indexer.TxOutputInfo
	if !indexer.IsPlainAsset(name) {
		if value < input.SizeOfBindingSats()+DEFAULT_FEE_SATSNET {
			feeOutputs = p.l2IndexerClient.GetUtxoListWithTicker(address, &indexer.ASSET_PLAIN_SAT)
			if len(outputs) == 0 {
				Log.Errorf("no plain sats")
				return "", fmt.Errorf("no plain sats")
			}

			feeValue := int64(0)
			for _, out := range feeOutputs {
				output := OutputInfoToOutput_SatsNet(out)
				outpoint := output.OutPoint()
				txOut := output.OutValue

				feeValue += out.OutValue.Value
				txIn := swire.NewTxIn(outpoint, nil, nil)
				tx.AddTxIn(txIn)
				prevFetcher.AddPrevOut(*outpoint, &txOut)
				input.Merge(output)

				if feeValue >= DEFAULT_FEE_SATSNET {
					break
				}
			}

			if feeValue < DEFAULT_FEE_SATSNET {
				return "", fmt.Errorf("not enough fee")
			}
		}
	}

	sendAsset := swire.AssetInfo{
		Name:       *name,
		Amount:     amt,
		BindingSat: indexer.IsBindingSat(name),
	}
	txOut := swire.NewTxOut(amt, GenTxAssetsFromAssetInfo(&sendAsset), pkScript)
	tx.AddTxOut(txOut)

	err = input.SubAsset(&sendAsset)
	if err != nil {
		return "", err
	}
	feeAsset := swire.AssetInfo{
		Name:       ASSET_PLAIN_SAT,
		Amount:     DEFAULT_FEE_SATSNET,
		BindingSat: 1,
	}
	err = input.SubAsset(&feeAsset)
	if err != nil {
		return "", err
	}

	if !input.Zero() {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := swire.NewTxOut(input.Value(), input.OutValue.Assets, changePkScript)
		tx.AddTxOut(txOut2)
	}

	// sign
	err = p.SignTx_SatsNet(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx_SatsNet failed. %v", err)
		return "", err
	}

	PrintJsonTx_SatsNet(tx, "SendAssets_SatsNet")

	txid, err := p.l2IndexerClient.BroadCastTx_SatsNet(tx)
	if err != nil {
		Log.Errorf("BroadCastTx_SatsNet failed. %v", err)
		return "", err
	}

	Log.Infof("SendUtxos_SatsNet finished, %v", time.Since(start))

	return txid, nil
}

// 仅用于测试使用，以后不提供这样的接口
func (p *Manager) SendUtxos(destAddr string, utxos []string,
	amt int64) (string, error) {

	if p.wallet == nil {
		return "", fmt.Errorf("wallet is not created/unlocked")
	}

	start := time.Now()
	Log.Infof("SendUtxos %d", amt)
	tx := wire.NewMsgTx(wire.TxVersion)

	if amt != 0 && amt < 330 {
		return "", fmt.Errorf("too small amount")
	}

	addr, err := btcutil.DecodeAddress(destAddr, GetChainParam())
	if err != nil {
		return "", err
	}
	pkScript, err := txscript.PayToAddrScript(addr)
	if err != nil {
		return "", err
	}

	var weightEstimate utils.TxWeightEstimator
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	value := int64(0)
	for _, utxo := range utxos {
		outpoint, err := UtxoToWireOutpoint(utxo)
		if err != nil {
			Log.Errorf("invalid utxo %s", utxo)
			return "", err
		}

		txOut := p.getTxOutFromIndexer(utxo)
		if txOut == nil {
			return "", fmt.Errorf("getTxOutFromIndexer %s failed", utxo)
		}

		value += txOut.Value
		txIn := wire.NewTxIn(outpoint, nil, nil)
		tx.AddTxIn(txIn)
		prevFetcher.AddPrevOut(*outpoint, txOut)
		weightEstimate.AddTaprootKeySpendInput(txscript.SigHashDefault)
	}

	feeRate := p.GetFeeRate()
	weightEstimate.AddP2WSHOutput()
	weightEstimate.AddP2TROutput() // change
	vSize := weightEstimate.VSize()
	requiredFee := utils.SatPerVByte(feeRate).FeePerKVByte().FeeForVSize(vSize)

	if amt == 0 {
		amt = value - requiredFee
		if amt < 330 {
			return "", fmt.Errorf("not enough value")
		}
	} else {
		if value-requiredFee < amt {
			return "", fmt.Errorf("not enough fee")
		}
	}

	txOut := wire.NewTxOut(amt, pkScript)
	tx.AddTxOut(txOut)

	change := value - amt - requiredFee
	if change >= 330 {
		changePkScript, err := GetP2TRpkScript(p.wallet.GetPaymentPubKey())
		if err != nil {
			return "", err
		}
		txOut2 := wire.NewTxOut(change, changePkScript)
		tx.AddTxOut(txOut2)
	}

	// sign
	err = p.SignTx(tx, prevFetcher)
	if err != nil {
		Log.Errorf("SignTx failed. %v", err)
		return "", err
	}

	txid, err := p.l1IndexerClient.BroadCastTx(tx)
	if err != nil {
		Log.Errorf("BroadCastTx failed. %v", err)
		return "", err
	}

	Log.Infof("SendUtxos finished, %v", time.Since(start))

	return txid, nil
}
