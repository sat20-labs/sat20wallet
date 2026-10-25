package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// Bitcoin witnesses alone cannot recover a client-validated asset. Keep the
// original commitment and every native recovery proof in the same durable
// value as the signed punishment chain, before acknowledging revocation.
type nativePunishRecord struct {
	Version      uint32
	ChannelID    string
	CommitTxID   string
	Transactions []*wire.MsgTx
	Proofs       map[string]map[string][]byte // confirmed transaction ID -> protocol proof
}

func (p *WatchTower) nativePunishProofs(channel *Channel, commit *wire.MsgTx, txs []*wire.MsgTx) (map[string]map[string][]byte, error) {
	return p.nativePunishProofsWithRGBView(channel, commit, txs, nil)
}

func (p *WatchTower) nativePunishProofsWithRGBView(channel *Channel, commit *wire.MsgTx, txs []*wire.MsgTx, view *stpRGBEvidenceView) (map[string]map[string][]byte, error) {
	if channel == nil || commit == nil {
		return nil, fmt.Errorf("missing punishment context")
	}
	old := channel.RemoteCommitment
	if old == nil || len(old.AssetProofs) == 0 {
		if old != nil && old.RGBClientRecovery != nil {
			return nil, fmt.Errorf("RGB punishment is missing its old commitment proof")
		}
		for _, protocol := range stpAssetProtocols.ordered {
			if _, native := protocol.(STPAssetChannelProtocol); native && channel.HasProtocolAsset(protocol.Protocol()) {
				return nil, fmt.Errorf("native punishment is missing its old commitment proof")
			}
		}
		return nil, nil
	}
	if old.CommitTx == nil || old.CommitTx.TxID() != commit.TxID() {
		return nil, fmt.Errorf("native punishment requires the revoked channel snapshot")
	}
	proofs := make(map[string]map[string][]byte)
	candidates := append([]*wire.MsgTx{commit}, txs...)
	add := func(bundle map[string][]byte) error {
		for name, raw := range bundle {
			protocol, ok := stpProtocolWithRGBView(stpAssetProtocols.byName[name], view).(STPAssetChannelProtocol)
			if !ok || len(raw) == 0 {
				return fmt.Errorf("unknown native punishment proof %s", name)
			}
			matched := ""
			for _, tx := range candidates {
				if tx == nil {
					return fmt.Errorf("nil punishment transaction")
				}
				if _, err := protocol.VerifyChannelProof(p.manager, nil, tx, raw, STPOpPunish); err != nil {
					continue
				}
				if matched != "" && matched != tx.TxID() {
					return fmt.Errorf("ambiguous native punishment proof")
				}
				matched = tx.TxID()
			}
			if matched == "" {
				return fmt.Errorf("native punishment proof has no matching signed transaction")
			}
			if proofs[matched] == nil {
				proofs[matched] = make(map[string][]byte)
			}
			if previous := proofs[matched][name]; previous != nil && !bytes.Equal(previous, raw) {
				return fmt.Errorf("conflicting native punishment proof")
			}
			proofs[matched][name] = append([]byte(nil), raw...)
		}
		return nil
	}
	if err := add(old.AssetProofs); err != nil {
		return nil, err
	}
	if err := add(old.RecoveryProofs); err != nil {
		return nil, err
	}
	for _, other := range old.Others {
		if other == nil {
			continue
		}
		if err := add(other.AssetProofs); err != nil {
			return nil, err
		}
		if err := add(other.RecoveryProofs); err != nil {
			return nil, err
		}
	}
	if old.RGBClientRecovery != nil {
		child, err := p.manager.verifiedPunishRGBClientRecovery(channel, view)
		if err != nil {
			return nil, err
		}
		matches := 0
		for _, tx := range txs {
			if equalSTPBitcoinTx(tx, child) {
				matches++
			}
		}
		if matches != 1 {
			return nil, fmt.Errorf("native punishment omitted or duplicated its exact signed RGB client recovery")
		}
		if err := add(old.RGBClientRecovery.AssetProofs); err != nil {
			return nil, err
		}
	}
	if err := completeSTPRGBView(view); err != nil {
		return nil, err
	}
	return proofs, nil
}

func loadNativePunishRecord(database indexer.KVDB, channelID, commitID string) (*nativePunishRecord, error) {
	raw, err := database.Read([]byte(GetPunishTxKey(channelID, commitID)))
	if err != nil {
		return nil, err
	}
	var record nativePunishRecord
	if DecodeFromBytes(raw, &record) != nil || record.Version == 0 {
		return nil, nil
	}
	if record.Version != 1 || record.ChannelID != channelID || record.CommitTxID != commitID || len(record.Transactions) == 0 || len(record.Proofs) == 0 {
		return nil, fmt.Errorf("invalid native punishment recovery record")
	}
	allowed := map[string]bool{commitID: true}
	for _, tx := range record.Transactions {
		if tx == nil {
			return nil, fmt.Errorf("nil native punishment transaction")
		}
		allowed[tx.TxID()] = true
	}
	for id, proofs := range record.Proofs {
		if !allowed[id] || len(proofs) == 0 {
			return nil, fmt.Errorf("native punishment proof is detached from its transaction")
		}
	}
	return &record, nil
}

// A mempool acknowledgement is not a native asset receipt. On failure the
// watchtower's retry marker and full proof package remain durable.
func (p *WatchTower) importNativePunishment(channelID, commitID string) error {
	record, err := loadNativePunishRecord(p.manager.db, channelID, commitID)
	if err != nil {
		return err
	}
	if record == nil {
		return nil
	}
	ids := make([]string, 0, len(record.Proofs))
	if len(record.Proofs[commitID]) != 0 {
		ids = append(ids, commitID)
	}
	for _, tx := range record.Transactions {
		if len(record.Proofs[tx.TxID()]) != 0 && tx.TxID() != commitID {
			ids = append(ids, tx.TxID())
		}
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if !p.manager.GetIndexerRPCClient().IsTxConfirmed(id) {
			return fmt.Errorf("native punishment %s is awaiting confirmation", id)
		}
		raw, err := p.manager.GetIndexerRPCClient().GetRawTx(id)
		if err != nil {
			return err
		}
		tx, err := DecodeMsgTx(raw)
		if err != nil || tx.TxID() != id {
			return fmt.Errorf("native punishment canonical transaction is unavailable")
		}
		if err := p.manager.ImportSTPChannelSettlement(tx, record.Proofs[id]); err != nil {
			return err
		}
	}
	return nil
}

func saveNativePunishRecord(database indexer.KVDB, record *nativePunishRecord) error {
	key := []byte(GetPunishTxKey(record.ChannelID, record.CommitTxID))
	if previous, err := loadNativePunishRecord(database, record.ChannelID, record.CommitTxID); err == nil && previous != nil {
		if !equalSTPBitcoinChain(previous.Transactions, record.Transactions) || len(previous.Proofs) != len(record.Proofs) {
			return fmt.Errorf("native punishment package is immutable")
		}
		keys := make([]string, 0, len(record.Proofs))
		for id := range record.Proofs {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			if !equalSTPAssetProofs(previous.Proofs[id], record.Proofs[id]) {
				return fmt.Errorf("native punishment proofs are immutable")
			}
		}
		return nil
	} else if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	raw, err := EncodeToBytes(record)
	if err != nil {
		return err
	}
	return database.Write(key, raw)
}
