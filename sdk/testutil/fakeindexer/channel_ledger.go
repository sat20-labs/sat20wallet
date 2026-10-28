package fakeindexer

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Project the current confirmed block snapshot; do not keep a second ledger.
func (p *Client) GetChannelLedger(channel string) ([]*sindexer.ChannelLedgerEntry, error) {
	p.Network.Mutex.RLock()
	defer p.Network.Mutex.RUnlock()
	result := make([]*sindexer.ChannelLedgerEntry, 0)
	if p.Network.IsBitcoinNet() {
		return result, nil
	}
	heights := make([]int, 0, len(p.Network.Blocks))
	for height := range p.Network.Blocks {
		if height >= 0 && height < p.Network.Height {
			heights = append(heights, height)
		}
	}
	sort.Ints(heights)
	for _, height := range heights {
		for _, id := range p.Network.Blocks[height] {
			tx, err := wallet.DecodeMsgTx_SatsNet(p.Network.TxBroadcasted[id])
			if err != nil || tx.TxID() != id {
				return nil, fmt.Errorf("invalid confirmed channel transaction %s", id)
			}
			if len(tx.TxIn) == 1 && tx.TxIn[0].PreviousOutPoint == (swire.OutPoint{Index: swire.AnchorTxOutIndex}) {
				anchor, err := wallet.ParseStandardAnchorScript(tx.TxIn[0].SignatureScript)
				if err != nil {
					return nil, err
				}
				script, err := utils.WitnessScriptHash(anchor.WitnessScript)
				if err != nil {
					return nil, err
				}
				address, err := getSatsNetAddressFromPkScript(script)
				if err != nil {
					return nil, err
				}
				if address == channel {
					result = append(result, &sindexer.ChannelLedgerEntry{ChannelId: address, Direction: "ascending", Operation: "ascending", L2TxId: id, L2Height: height,
						L1TxId: strings.Split(anchor.Utxo, ":")[0], L1Outpoints: []string{anchor.Utxo}, Value: anchor.Value, Assets: anchor.Assets.Clone()})
				}
				continue
			}
			for vout, out := range tx.TxOut {
				kind, data, err := sindexer.ReadDataFromNullDataScript(out.PkScript)
				if err != nil || kind != sindexer.CONTENT_TYPE_DESCENDING {
					continue
				}
				if len(tx.TxIn) == 0 {
					return nil, fmt.Errorf("descending transaction has no input")
				}
				index, ok := p.Network.UtxoIndex[tx.TxIn[0].PreviousOutPoint.String()]
				if !ok {
					return nil, fmt.Errorf("descending transaction source unavailable")
				}
				script, err := hex.DecodeString(p.Network.State.PkScripts[p.Network.UtxoOwner[index]])
				if err != nil {
					return nil, err
				}
				address, err := getSatsNetAddressFromPkScript(script)
				if err != nil {
					return nil, err
				}
				if address != channel {
					continue
				}
				payload, err := sindexer.ParseDescendPayload(data)
				if err != nil {
					return nil, err
				}
				entry := &sindexer.ChannelLedgerEntry{ChannelId: address, Direction: "descending", Operation: sindexer.DescendOperationName(payload.Operation), L2TxId: id, L2Height: height,
					NullDataUtxo: fmt.Sprintf("%s:%d", id, vout), L1TxId: payload.L1TxId, Value: out.Value, Assets: out.Assets.Clone(), Legacy: payload.Version < 2}
				for _, returned := range payload.ReturnedOutputVouts {
					entry.ReturnedChannelOutputs = append(entry.ReturnedChannelOutputs, fmt.Sprintf("%s:%d", payload.L1TxId, returned))
				}
				result = append(result, entry)
			}
		}
	}
	return result, nil
}
