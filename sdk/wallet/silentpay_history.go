package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	indexercommon "github.com/sat20-labs/indexer/common"
)

// SPHistoryPayment is immutable historical BTC receipt evidence. Unlike the
// Live view, Archive cannot say whether a past output is currently unspent.
type SPHistoryPayment struct {
	TxID      string `json:"txid"`
	TxIndex   uint32 `json:"tx_index"`
	Vout      uint32 `json:"vout"`
	Height    int    `json:"height"`
	BlockHash string `json:"block_hash"`
	Value     int64  `json:"value"`
	K         uint32 `json:"k"`
}

type SPAccountHistory struct {
	WalletID     int64              `json:"wallet_id"`
	AccountIndex uint32             `json:"account_index"`
	Anchor       SPBaseAnchor       `json:"anchor"`
	Payments     []SPHistoryPayment `json:"payments"`
	Complete     bool               `json:"complete"`
}

type spHistoryDisk struct {
	Version  uint32
	Network  string
	Anchor   SPBaseAnchor
	Payments []SPHistoryPayment
	Complete bool
}

func spHistoryKey(network, fingerprint string, index uint32) []byte {
	return []byte(GetDBKeyPrefix() + "sp/history/" + network + "/" + fingerprint +
		"/" + strconv.FormatUint(uint64(index), 10))
}

func (p *Manager) spReadHistory(network string, account *spScanAccount) (spHistoryDisk, error) {
	value := spHistoryDisk{Version: 1, Network: network, Anchor: SPBaseAnchor{Height: -1}}
	encoded, err := p.db.Read(spHistoryKey(network, account.fingerprint, account.accountIndex))
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return value, nil
	}
	if err != nil {
		return spHistoryDisk{}, err
	}
	if err := DecodeFromBytes(encoded, &value); err != nil || value.Version != 1 || value.Network != network ||
		value.Anchor.Height < -1 {
		return spHistoryDisk{}, fmt.Errorf("silentpay: invalid historical checkpoint")
	}
	return value, nil
}

// SyncSilentPayArchive backfills confirmed SP receipts with a single public
// Indexer scan across all enabled subaccounts, but independent durable
// checkpoints per account. A partially scanned block is NEVER checkpointed:
// the last block on a nonfinal page might continue on the next page.
func (p *Manager) SyncSilentPayArchive(ctx context.Context) ([]SPAccountHistory, error) {
	if p == nil || p.db == nil || ctx == nil {
		return nil, ErrSilentPayUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	accounts, err := p.getSPScanAccounts()
	if err != nil {
		return nil, err
	}
	defer func() {
		for _, a := range accounts {
			if a.keys.Scan != nil {
				a.keys.Scan.Zero()
			}
			if a.keys.Spend != nil {
				a.keys.Spend.Zero()
			}
		}
	}()
	if len(accounts) == 0 {
		return []SPAccountHistory{}, nil
	}
	sp, bitcoinReader, err := p.spScanRPC()
	if err != nil {
		return nil, err
	}
	status, err := sp.SPStatus(ctx)
	if err != nil {
		return nil, err
	}
	if status.SchemaVersion != 1 || status.Network != spIndexerExpectedNetwork() ||
		!status.DataReady || status.BaseAnchor.Height < status.CoverageStartHeight {
		return nil, fmt.Errorf("silentpay: Archive Indexer not ready")
	}

	histories := make([]spHistoryDisk, len(accounts))
	minFrom := status.BaseAnchor.Height + 1
	for i, a := range accounts {
		record, e := p.spReadHistory(status.Network, a)
		if e != nil {
			return nil, e
		}
		if record.Anchor.Height >= status.CoverageStartHeight {
			// We must not reuse a checkpoint from a different Bitcoin fork.
			currentHash, e := p.l1IndexerClient.GetBlockHash(record.Anchor.Height)
			if e != nil {
				return nil, e
			}
			if record.Anchor.Height > status.BaseAnchor.Height ||
				currentHash != record.Anchor.BlockHash {
				record = spHistoryDisk{Version: 1, Network: status.Network, Anchor: SPBaseAnchor{Height: status.CoverageStartHeight - 1}}
			}
		} else {
			record.Anchor.Height = status.CoverageStartHeight - 1
		}
		if record.Anchor.Height < status.BaseAnchor.Height {
			record.Complete = false
		}
		histories[i] = record
		if from := record.Anchor.Height + 1; from < minFrom {
			minFrom = from
		}
	}
	toResults := func() []SPAccountHistory {
		result := make([]SPAccountHistory, len(accounts))
		for i, a := range accounts {
			result[i] = SPAccountHistory{WalletID: a.walletID, AccountIndex: a.accountIndex,
				Anchor: histories[i].Anchor, Payments: append([]SPHistoryPayment(nil), histories[i].Payments...),
				Complete: histories[i].Anchor.Height == status.BaseAnchor.Height &&
					histories[i].Anchor.BlockHash == status.BaseAnchor.BlockHash}
		}
		return result
	}
	if minFrom > status.BaseAnchor.Height {
		return toResults(), nil
	}

	// An atomic batch for all enabled subaccounts gives a monotonic durable
	// restart point. No block with a partially returned transaction page may
	// contribute to this commit.
	persist := func() error {
		batch := p.db.NewWriteBatch()
		if batch == nil {
			return fmt.Errorf("silentpay: no history write batch")
		}
		defer batch.Close()
		for i, a := range accounts {
			encoded, e := EncodeToBytes(&histories[i])
			if e != nil {
				return e
			}
			if e := batch.Put(spHistoryKey(status.Network, a.fingerprint, a.accountIndex), encoded); e != nil {
				return e
			}
		}
		return batch.Flush()
	}
	var currentHeight = -1
	var currentHash string
	var previousTxIndex uint32
	var lastPersistHeight = minFrom - 1
	completeBlock := func() error {
		if currentHeight < minFrom {
			return nil
		}
		for i := range histories {
			if histories[i].Anchor.Height < currentHeight {
				histories[i].Anchor = SPBaseAnchor{Height: currentHeight, BlockHash: currentHash}
			}
		}
		if currentHeight-lastPersistHeight >= 1000 {
			if e := persist(); e != nil {
				return e
			}
			lastPersistHeight = currentHeight
		}
		return nil
	}
	seen := make(map[string]bool)
	cursor := ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, e := sp.SPScan(ctx, SPScanRequest{
			Mode: "archive", FromHeight: minFrom, ToHeight: status.BaseAnchor.Height,
			AnchorHash: status.BaseAnchor.BlockHash, Limit: 100, Cursor: cursor,
		})
		if e != nil {
			return nil, e
		}
		if page.Anchor != status.BaseAnchor || page.SnapshotID != "" {
			return nil, fmt.Errorf("silentpay: Archive anchor changed during scan")
		}
		for _, block := range page.Blocks {
			if block.Height < minFrom || block.Height > status.BaseAnchor.Height ||
				(currentHeight < 0 && block.Height != minFrom) ||
				(currentHeight >= 0 && block.Height != currentHeight &&
					block.Height != currentHeight+1) {
				return nil, fmt.Errorf("silentpay: archive block order is invalid")
			}
			if currentHeight != block.Height {
				if e := completeBlock(); e != nil {
					return nil, e
				}
				currentHeight, currentHash = block.Height, block.BlockHash
				previousTxIndex = 0
			} else if currentHash != block.BlockHash {
				return nil, fmt.Errorf("silentpay: archived block hash changed")
			}
			for _, tx := range block.Transactions {
				if tx.TxIndex == 0 || tx.TxIndex <= previousTxIndex ||
					len(tx.P2TROutputs) == 0 || len(tx.Tweak) != 33 {
					return nil, fmt.Errorf("silentpay: noncanonical candidate order")
				}
				previousTxIndex = tx.TxIndex
				outputs := make([]SilentPayCandidate, len(tx.P2TROutputs))
				for j, out := range tx.P2TROutputs {
					if len(out.OutputKey) != 32 || (j > 0 && out.Vout <= tx.P2TROutputs[j-1].Vout) {
						return nil, fmt.Errorf("silentpay: invalid archived output order")
					}
					outputs[j] = SilentPayCandidate{Vout: out.Vout, OutputKey: out.OutputKey}
				}
				var matched [][]SilentPayMatch
				hasMatch := false
				for i, a := range accounts {
					if histories[i].Anchor.Height >= block.Height {
						matched = append(matched, nil)
						continue
					}
					m, e := MatchSilentPayOutputs(a.keys, tx.Tweak, outputs)
					if e != nil {
						return nil, e
					}
					matched = append(matched, m)
					if len(m) != 0 {
						hasMatch = true
					}
				}
				if !hasMatch {
					continue
				}
				position := SPIndexerPosition{Height: block.Height, BlockHash: block.BlockHash, TxIndex: tx.TxIndex}
				located, e := sp.SPLookup(ctx, []SPIndexerPosition{position})
				if e != nil || len(located) != 1 {
					return nil, fmt.Errorf("silentpay: failed archived tx position: %v", e)
				}
				txid := located[0].TxID
				raw, err := bitcoinReader.GetBitcoinRawTx(txid)
				if err != nil || raw == nil || raw.Error != "" {
					return nil, fmt.Errorf("silentpay: archived raw Bitcoin transaction unavailable: %v", err)
				}
				rawTx, err := spParseFoundTransaction(raw.RawTx, txid)
				if err != nil {
					return nil, err
				}
				for i, ms := range matched {
					for _, m := range ms {
						if uint64(m.Vout) >= uint64(len(rawTx.TxOut)) ||
							!bytes.Equal(rawTx.TxOut[m.Vout].PkScript,
								append([]byte{0x51, 0x20}, m.OutputKey...)) {
							return nil, fmt.Errorf("silentpay: archived payment output mismatch")
						}
						value := rawTx.TxOut[m.Vout].Value
						if value < 0 {
							return nil, fmt.Errorf("silentpay: negative Bitcoin output amount")
						}
						histories[i].Payments = append(histories[i].Payments, SPHistoryPayment{
							TxID: txid, TxIndex: tx.TxIndex, Vout: m.Vout, Height: block.Height,
							BlockHash: block.BlockHash, Value: value, K: m.K,
						})
					}
				}
			}
		}
		if page.Done {
			if page.NextCursor != "" {
				return nil, fmt.Errorf("silentpay: finished Archive page has a cursor")
			}
			if e := completeBlock(); e != nil {
				return nil, e
			}
			if currentHeight != status.BaseAnchor.Height {
				// Empty trailing blocks must also be present in Archive.
				return nil, fmt.Errorf("silentpay: incomplete archived height coverage")
			}
			for i := range histories {
				histories[i].Complete = true
			}
			if e := persist(); e != nil {
				return nil, e
			}
			return toResults(), nil
		}
		if page.NextCursor == "" || page.NextCursor == cursor || seen[page.NextCursor] {
			return nil, fmt.Errorf("silentpay: missing/repeated Archive cursor")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
}

func (p *Manager) LastSyncedSilentPayHistory(walletID int64, index uint32) (SPAccountHistory, error) {
	if p == nil || p.db == nil {
		return SPAccountHistory{}, ErrSilentPayUnsupported
	}
	p.mutex.RLock()
	info := p.walletInfoMap[walletID]
	if info == nil || int(index) >= info.Accounts || !info.SPEnabled[index] || info.Wallet == nil {
		p.mutex.RUnlock()
		return SPAccountHistory{}, ErrSilentPayUnsupported
	}
	fingerprint := walletFingerprint(info.Wallet)
	p.mutex.RUnlock()
	account := &spScanAccount{walletID: walletID, accountIndex: index, fingerprint: fingerprint}
	record, err := p.spReadHistory(spIndexerExpectedNetwork(), account)
	if err != nil {
		return SPAccountHistory{}, err
	}
	if record.Anchor.Height < 0 {
		return SPAccountHistory{}, fmt.Errorf("silentpay: no completed historical scan")
	}
	return SPAccountHistory{WalletID: walletID, AccountIndex: index, Anchor: record.Anchor,
		Payments: record.Payments, Complete: record.Complete}, nil
}
