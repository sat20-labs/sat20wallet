package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type nativeLaunchPoolAnchor struct {
	Version      uint32 `json:"version"`
	ContractURL  string `json:"contract_url"`
	ContractHash string `json:"contract_hash"`
	TxID         string `json:"tx_id"`
	Transaction  []byte `json:"transaction"`
}

var nativeLaunchPoolAnchorMu sync.Mutex

func (p *LaunchPoolContractRunTime) nativeLaunchAnchorIdentity() (string, error) {
	raw, err := p.LaunchPoolContract.Encode()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (p *LaunchPoolContractRunTime) loadNativeLaunchAnchor() (*swire.MsgTx, error) {
	raw, err := p.db.Read(nativeLaunchPoolKey("anchor", p.URL()))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	identity, err := p.nativeLaunchAnchorIdentity()
	if err != nil {
		return nil, err
	}
	var record nativeLaunchPoolAnchor
	if json.Unmarshal(raw, &record) != nil || record.Version != 1 || record.ContractURL != p.URL() ||
		record.ContractHash != identity || !canonicalNativeTxID(record.TxID) {
		return nil, fmt.Errorf("invalid fixed native launchpool anchor")
	}
	var tx swire.MsgTx
	reader := bytes.NewReader(record.Transaction)
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxID() != record.TxID {
		return nil, fmt.Errorf("native launchpool anchor bytes differ from durable identity")
	}
	return &tx, nil
}

// Persist before the first peer notification, because the peer may broadcast
// successfully even when its reply is lost. A retry must use these exact bytes.
func (p *LaunchPoolContractRunTime) saveNativeLaunchAnchor(tx *swire.MsgTx) error {
	if tx == nil {
		return fmt.Errorf("nil native launchpool anchor")
	}
	identity, err := p.nativeLaunchAnchorIdentity()
	if err != nil {
		return err
	}
	var encoded bytes.Buffer
	if err := tx.Serialize(&encoded); err != nil {
		return err
	}
	nativeLaunchPoolAnchorMu.Lock()
	defer nativeLaunchPoolAnchorMu.Unlock()
	old, err := p.loadNativeLaunchAnchor()
	if err != nil {
		return err
	}
	if old != nil {
		var previous bytes.Buffer
		if err := old.Serialize(&previous); err != nil {
			return err
		}
		if !bytes.Equal(previous.Bytes(), encoded.Bytes()) {
			return fmt.Errorf("native launchpool installation anchor is immutable")
		}
		return nil
	}
	raw, err := json.Marshal(nativeLaunchPoolAnchor{Version: 1, ContractURL: p.URL(), ContractHash: identity,
		TxID: tx.TxID(), Transaction: encoded.Bytes()})
	if err != nil {
		return err
	}
	return p.db.Write(nativeLaunchPoolKey("anchor", p.URL()), raw)
}

func (p *LaunchPoolContractRunTime) sendNativeLaunchAnchor(ctx context.Context, tx *swire.MsgTx) error {
	// Recovery after an accepted broadcast must not ask an already activated
	// peer to install another genesis. Check canonical inclusion of the exact
	// durable anchor before considering that first attempt complete.
	info, err := p.stp.GetIndexerClient_SatsNet().GetTxInfo(tx.TxID())
	if err == nil && info != nil && info.Confirmations > 0 {
		chains, err := p.stp.GetWalletMgr().newNativeSettlementChains(ctx, p.localWallet)
		if err != nil {
			return err
		}
		observed, err := chains.satoshiTx(tx.TxID())
		if err != nil {
			return err
		}
		var expected, actual bytes.Buffer
		if err := tx.Serialize(&expected); err != nil {
			return err
		}
		if err := observed.Serialize(&actual); err != nil {
			return err
		}
		if !bytes.Equal(expected.Bytes(), actual.Bytes()) {
			return fmt.Errorf("observed launchpool anchor differs from durable signed bytes")
		}
		return chains.stable()
	}
	return p.notifyAndSendDepositAnchorTxs([]*swire.MsgTx{tx})
}
