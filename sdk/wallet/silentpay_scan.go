package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"

	"github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
)

// Each SP-enabled account has a distinct signing identity and local records.
// The public Indexer data is fetched once per refresh and matched by the SDK;
// neither the scan private key nor the per-account match set is uploaded.
type SPReceivedOutput struct {
	TxID string `json:"txid"`
	Vout uint32 `json:"vout"`
	Height int `json:"height"`
	BlockHash string `json:"block_hash"`
	Value int64 `json:"value"`
	K uint32 `json:"k"`
	Tweak [32]byte `json:"-"` // local-only signing data, never in HTTP JSON
	Unspent bool `json:"unspent"`
}

type SPAccountBalance struct {
	WalletID int64 `json:"wallet_id"`
	AccountIndex uint32 `json:"account_index"`
	Anchor SPBaseAnchor `json:"anchor"`
	Outputs []SPReceivedOutput `json:"outputs"`
	Balance int64 `json:"balance"`
	Complete bool `json:"complete"`
}

// The object is a local gob value. It is not synced to the remote account
// provider; only monotonic SP enablement is managed globally.
type spAccountSyncDisk struct {
	Version uint32
	Network string
	Anchor SPBaseAnchor
	Outputs []SPReceivedOutput
}

type spScanAccount struct {
	walletID int64
	accountIndex uint32
	keys SilentPayKeys
	fingerprint string
	result SPAccountBalance
}

func spAccountDataKey(network, fingerprint string, index uint32) []byte {
	return []byte(GetDBKeyPrefix() + "sp/" + network + "/" + fingerprint +
		"/" + strconv.FormatUint(uint64(index), 10))
}

func (p *Manager) getSPScanAccounts() ([]*spScanAccount, error) {
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	ids := make([]int64, 0, len(p.walletInfoMap))
	for id := range p.walletInfoMap { ids = append(ids, id) }
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var result []*spScanAccount
	for _, id := range ids {
		info := p.walletInfoMap[id]
		if info == nil { continue }
		for index := uint32(0); index < uint32(info.Accounts); index++ {
			if !info.SPEnabled[index] { continue }
			if info.Type != WALLET_TYPE_MNEMONIC || info.Wallet == nil {
				return nil, ErrSilentPayUnsupported
			}
			w, ok := info.Wallet.(*InternalWallet)
			if !ok { return nil, ErrSilentPayUnsupported }
			keys, err := w.DeriveSilentPayKeys(index)
			if err != nil { return nil, err }
			result = append(result, &spScanAccount{
				walletID: id, accountIndex: index, fingerprint: walletFingerprint(info.Wallet),
				keys: keys,
				result: SPAccountBalance{WalletID:id, AccountIndex:index},
			})
		}
	}
	return result, nil
}

func spIndexerExpectedNetwork() string {
	return GetChainParam().Name
}

func spParseFoundTransaction(raw string, txid string) (*wire.MsgTx, error) {
	data, err := hex.DecodeString(raw)
	if err != nil || len(data) == 0 || len(data) > 8*1024*1024 {
		return nil, fmt.Errorf("silentpay: malformed raw Bitcoin transaction")
	}
	tx := new(wire.MsgTx)
	reader := bytes.NewReader(data)
	if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 {
		return nil, fmt.Errorf("silentpay: malformed Bitcoin transaction data")
	}
	if err := SilentPayVerifyTxID(txid, tx); err != nil { return nil, err }
	return tx, nil
}

type spBitcoinRawTxLookup interface {
	GetBitcoinRawTx(string) (*indexerwire.BitcoinRawTx, error)
}

func (p *Manager) spScanRPC() (SPIndexerRPC, spBitcoinRawTxLookup, error) {
	if p == nil || p.l1IndexerClient == nil {
		return nil, nil, fmt.Errorf("silentpay: L1 Indexer unavailable")
	}
	client := p.l1IndexerClient.getActiveIndexer()
	if client == nil { return nil, nil, fmt.Errorf("silentpay: no active L1 Indexer") }
	sp, ok := client.(SPIndexerRPC)
	if !ok { return nil, nil, fmt.Errorf("silentpay: L1 Indexer lacks BIP352 endpoints") }
	bitcoinReader, ok := client.(spBitcoinRawTxLookup)
	if !ok { return nil, nil, fmt.Errorf("silentpay: Indexer lacks raw transaction lookup") }
	return sp, bitcoinReader, nil
}

// SyncSilentPayLive obtains one consistent Live snapshot for all enabled
// accounts. We publish balances to local storage ONLY after exhausting every
// page. A failed page, expired cursor or cancelled request cannot create a
// false zero balance or advance any account's durable checkpoint.
//
// This is the fast UTXO recovery stage; Archive/history reconciliation is a
// separate stage and must not be marked complete by this function.
func (p *Manager) SyncSilentPayLive(ctx context.Context) ([]SPAccountBalance, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	accounts, err := p.getSPScanAccounts()
	if err != nil { return nil, err }
	if len(accounts) == 0 { return []SPAccountBalance{}, nil }
	sp, bitcoinClient, err := p.spScanRPC()
	if err != nil { return nil, err }
	status, err := sp.SPStatus(ctx)
	if err != nil { return nil, err }
	if status.SchemaVersion != 1 || status.Network != spIndexerExpectedNetwork() ||
		!status.DataReady || status.BaseAnchor.Height < status.CoverageStartHeight {
		return nil, fmt.Errorf("silentpay: Indexer SP coverage unavailable or wrong network")
	}
	snapshot, err := sp.SPSnapshot(ctx)
	if err != nil { return nil, err }
	if snapshot.SchemaVersion != status.SchemaVersion ||
		snapshot.CoverageStartHeight != status.CoverageStartHeight ||
		snapshot.Anchor.Height > status.BaseAnchor.Height ||
		snapshot.SnapshotID == "" {
		return nil, fmt.Errorf("silentpay: inconsistent Indexer snapshot")
	}
	for _, account := range accounts {
		account.result.Anchor = snapshot.Anchor
		account.result.Outputs = []SPReceivedOutput{}
	}
	// Do not retain every hit's raw Bitcoin transaction for the entire
	// wallet recovery; a page-local cache is enough to deduplicate lookups.
	next := ""
	seenCursors := make(map[string]bool)
	for pages := 0; ; pages++ {
		if pages >= 100000 {
			return nil, fmt.Errorf("silentpay: exceeded scan page limit")
		}
		if err := ctx.Err(); err != nil { return nil, err }
		page, err := sp.SPScan(ctx, SPScanRequest{
			Mode: "live", SnapshotID: snapshot.SnapshotID, FromHeight: status.CoverageStartHeight,
			ToHeight: snapshot.Anchor.Height, Limit: 100, Cursor: next,
		})
		if err != nil { return nil, err }
		if page.SnapshotID != snapshot.SnapshotID || page.Anchor != snapshot.Anchor {
			return nil, fmt.Errorf("silentpay: scanner view drifted")
		}
		txCache := make(map[string]*wire.MsgTx)
		for _, block := range page.Blocks {
			if block.Height < status.CoverageStartHeight || block.Height > snapshot.Anchor.Height {
				return nil, fmt.Errorf("silentpay: invalid candidate block height")
			}
			for _, item := range block.Transactions {
				if item.TxIndex == 0 || len(item.P2TROutputs) == 0 ||
					len(item.UnspentBitmap) != (len(item.P2TROutputs)+7)/8 {
					return nil, fmt.Errorf("silentpay: malformed Live candidate")
				}
				outputs := make([]SilentPayCandidate, len(item.P2TROutputs))
				for i, output := range item.P2TROutputs {
					if len(output.OutputKey) != 32 ||
						(i > 0 && output.Vout <= item.P2TROutputs[i-1].Vout) {
						return nil, fmt.Errorf("silentpay: invalid ordered candidate")
					}
					outputs[i] = SilentPayCandidate{Vout:output.Vout, OutputKey:output.OutputKey}
				}
				type accountMatch struct {
					account *spScanAccount
					match SilentPayMatch
					bit int
				}
				var found []accountMatch
				for _, acct := range accounts {
					matches, err := MatchSilentPayOutputs(acct.keys, item.Tweak, outputs)
					if err != nil { return nil, err }
					for _, match := range matches {
						idx := sort.Search(len(outputs), func(i int) bool { return outputs[i].Vout >= match.Vout })
						if idx == len(outputs) || outputs[idx].Vout != match.Vout {
							return nil, fmt.Errorf("silentpay: derived output missing")
						}
						if item.UnspentBitmap[idx/8]&(1<<uint(idx%8)) != 0 {
							found = append(found, accountMatch{account:acct, match:match, bit:idx})
						}
					}
				}
				if len(found) == 0 { continue }
				positions := []SPIndexerPosition{{
					Height:block.Height, BlockHash:block.BlockHash, TxIndex:item.TxIndex,
				}}
				located, err := sp.SPLookup(ctx, positions)
				if err != nil { return nil, err }
				txid := located[0].TxID
				tx := txCache[txid]
				if tx == nil {
					rawTx, err := bitcoinClient.GetBitcoinRawTx(txid)
					if err != nil || rawTx == nil || rawTx.Error != "" {
						return nil, fmt.Errorf("silentpay: raw transaction lookup failed: %v", err)
					}
					tx, err = spParseFoundTransaction(rawTx.RawTx, txid)
					if err != nil { return nil, err }
					txCache[txid] = tx
				}
				for _, entry := range found {
					if uint64(entry.match.Vout) >= uint64(len(tx.TxOut)) ||
						!bytes.Equal(tx.TxOut[entry.match.Vout].PkScript,
							append([]byte{0x51,0x20}, entry.match.OutputKey...)) {
						return nil, fmt.Errorf("silentpay: Bitcoin output key mismatch")
					}
					value := tx.TxOut[entry.match.Vout].Value
					if value < 0 { return nil, fmt.Errorf("silentpay: negative Bitcoin output amount") }
					entry.account.result.Outputs = append(entry.account.result.Outputs, SPReceivedOutput{
						TxID:txid, Vout:entry.match.Vout, Height:block.Height,
						BlockHash:block.BlockHash, Value:value, K:entry.match.K,
						Tweak:entry.match.Tweak, Unspent:true,
					})
					entry.account.result.Balance += value
				}
			}
		}
		if page.Done {
			if page.NextCursor != "" {
				return nil, fmt.Errorf("silentpay: completed page contains cursor")
			}
			break
		}
		if page.NextCursor == "" || seenCursors[page.NextCursor] ||
			page.NextCursor == next {
			return nil, fmt.Errorf("silentpay: missing or repeated continuation cursor")
		}
		seenCursors[page.NextCursor] = true
		next = page.NextCursor
	}
	if err := ctx.Err(); err != nil { return nil, err }
	batch := p.db.NewWriteBatch()
	if batch == nil { return nil, fmt.Errorf("silentpay: local persistence unavailable") }
	defer batch.Close()
	for _, acct := range accounts {
		stored := spAccountSyncDisk{Version:1, Network:status.Network,
			Anchor:snapshot.Anchor, Outputs:acct.result.Outputs}
		encoded, err := EncodeToBytes(&stored)
		if err != nil { return nil, err }
		if err := batch.Put(spAccountDataKey(status.Network, acct.fingerprint, acct.accountIndex), encoded); err != nil {
			return nil, err
		}
	}
	if err := batch.Flush(); err != nil { return nil, err }
	results := make([]SPAccountBalance, 0, len(accounts))
	for _, acct := range accounts {
		acct.result.Complete = true
		results = append(results, acct.result)
	}
	return results, nil
}

// LastSyncedSilentPay is read-only and must not claim a balance if there has
// never been a complete snapshot. The PWA can show an explicit 'not scanned'
// state rather than assuming 0 BTC.
func (p *Manager) LastSyncedSilentPay(walletID int64, index uint32) (SPAccountBalance, error) {
	keys, err := p.silentPayKeysForAccount(walletID, index)
	if err != nil { return SPAccountBalance{}, err }
	_ = keys
	p.mutex.RLock()
	info := p.walletInfoMap[walletID]
	fingerprint := walletFingerprint(info.Wallet)
	p.mutex.RUnlock()
	raw, err := p.db.Read(spAccountDataKey(spIndexerExpectedNetwork(), fingerprint, index))
	if err != nil { return SPAccountBalance{}, fmt.Errorf("silentpay: no complete balance scan: %w", err) }
	var stored spAccountSyncDisk
	if err := DecodeFromBytes(raw, &stored); err != nil || stored.Version != 1 ||
		stored.Network != spIndexerExpectedNetwork() {
		return SPAccountBalance{}, fmt.Errorf("silentpay: invalid stored scan checkpoint")
	}
	answer := SPAccountBalance{WalletID:walletID, AccountIndex:index,
		Anchor:stored.Anchor, Outputs:stored.Outputs, Complete:true}
	for _, output := range answer.Outputs {
		if output.Unspent { answer.Balance += output.Value }
	}
	return answer, nil
}
