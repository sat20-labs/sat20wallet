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

type rgb11LaunchPoolAnchor struct {
	Version      uint32 `json:"version"`
	ContractURL  string `json:"contract_url"`
	ContractHash string `json:"contract_hash"`
	TxID         string `json:"tx_id"`
	Transaction  []byte `json:"transaction"`
}

var rgb11LaunchPoolAnchorMu sync.Mutex

func (p *LaunchPoolContractRunTime) rgb11LaunchAnchorIdentity() (string, error) {
	raw, err := p.LaunchPoolContract.Encode()
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}

func (p *LaunchPoolContractRunTime) loadRGB11LaunchAnchor() (*swire.MsgTx, error) {
	raw, err := p.db.Read(rgb11LaunchPoolKey("anchor", p.URL()))
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	identity, err := p.rgb11LaunchAnchorIdentity()
	if err != nil {
		return nil, err
	}
	var record rgb11LaunchPoolAnchor
	if json.Unmarshal(raw, &record) != nil || record.Version != 1 || record.ContractURL != p.URL() ||
		record.ContractHash != identity || !canonicalSettlementTxID(record.TxID) {
		return nil, fmt.Errorf("invalid fixed RGB11 launchpool anchor")
	}
	var tx swire.MsgTx
	reader := bytes.NewReader(record.Transaction)
	if tx.Deserialize(reader) != nil || reader.Len() != 0 || tx.TxID() != record.TxID {
		return nil, fmt.Errorf("RGB11 launchpool anchor bytes differ from durable identity")
	}
	return &tx, nil
}

// Persist before the first peer notification, because the peer may broadcast
// successfully even when its reply is lost. A retry must use these exact bytes.
func (p *LaunchPoolContractRunTime) saveRGB11LaunchAnchor(tx *swire.MsgTx) error {
	if tx == nil {
		return fmt.Errorf("nil RGB11 launchpool anchor")
	}
	identity, err := p.rgb11LaunchAnchorIdentity()
	if err != nil {
		return err
	}
	var encoded bytes.Buffer
	if err := tx.Serialize(&encoded); err != nil {
		return err
	}
	rgb11LaunchPoolAnchorMu.Lock()
	defer rgb11LaunchPoolAnchorMu.Unlock()
	old, err := p.loadRGB11LaunchAnchor()
	if err != nil {
		return err
	}
	if old != nil {
		var previous bytes.Buffer
		if err := old.Serialize(&previous); err != nil {
			return err
		}
		if !bytes.Equal(previous.Bytes(), encoded.Bytes()) {
			return fmt.Errorf("RGB11 launchpool installation anchor is immutable")
		}
		return nil
	}
	raw, err := json.Marshal(rgb11LaunchPoolAnchor{Version: 1, ContractURL: p.URL(), ContractHash: identity,
		TxID: tx.TxID(), Transaction: encoded.Bytes()})
	if err != nil {
		return err
	}
	return p.db.Write(rgb11LaunchPoolKey("anchor", p.URL()), raw)
}

func (p *LaunchPoolContractRunTime) sendRGB11LaunchAnchor(ctx context.Context, tx *swire.MsgTx) error {
	// Recovery after an accepted broadcast must not ask an already activated
	// peer to install another genesis. Check canonical inclusion of the exact
	// durable anchor before considering that first attempt complete.
	info, err := p.stp.GetIndexerClient_SatsNet().GetTxInfo(tx.TxID())
	if err == nil && info != nil && info.Confirmations > 0 {
		chains, err := p.stp.GetWalletMgr().newSettlementChains(ctx, p.localWallet)
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
