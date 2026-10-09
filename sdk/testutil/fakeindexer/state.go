package fakeindexer

import (
	"encoding/hex"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	sindexer "github.com/sat20-labs/satoshinet/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// State is fixture metadata. L1 and L2 may share it within one STP fixture;
// separate browser fixtures each receive their own instance.
type State struct {
	PkScripts    []string
	Tickers      map[string]*indexer.TickerInfo
	CoreNodes    map[string]bool
	MinerInfo    map[string]*sindexer.MinerInfo
	CoreChildren map[string]map[string]*sindexer.MinerAscendInfo
	Names        map[string]*indexerwire.OrdinalsName
}

func (p *State) Clone() *State {
	if p == nil {
		return NewState()
	}
	n := &State{
		PkScripts: append([]string(nil), p.PkScripts...), Tickers: p.CloneTickerInfo(),
		CoreNodes: make(map[string]bool), MinerInfo: make(map[string]*sindexer.MinerInfo),
		CoreChildren: make(map[string]map[string]*sindexer.MinerAscendInfo),
		Names:        make(map[string]*indexerwire.OrdinalsName),
	}
	for k, v := range p.CoreNodes {
		n.CoreNodes[k] = v
	}
	for k, v := range p.MinerInfo {
		c := *v
		n.MinerInfo[k] = &c
	}
	for k, children := range p.CoreChildren {
		n.CoreChildren[k] = make(map[string]*sindexer.MinerAscendInfo)
		for key, v := range children {
			c := *v
			n.CoreChildren[k][key] = &c
		}
	}
	for k, v := range p.Names {
		c := *v
		c.KVItemList = make([]*indexerwire.KVItem, len(v.KVItemList))
		for i, item := range v.KVItemList {
			copy := *item
			c.KVItemList[i] = &copy
		}
		n.Names[k] = &c
	}
	return n
}

// NewNetwork creates an empty fixture, without Transcend's preset UTXOs.
// Height is the height receiving new transactions; confirmation stays under
// the caller's control when this model is used behind the SDK HTTP fixture.
func NewNetwork(name string, height int) *Network {
	state := NewState()
	state.Tickers = make(map[string]*indexer.TickerInfo)
	state.Names = make(map[string]*indexerwire.OrdinalsName)
	return &Network{
		State: state, Name: name, Height: height,
		Blocks: make(map[int][]string), TxBroadcasted: make(map[string]string),
		UtxoUsed: make(map[string]string), UtxoIndex: make(map[string]int),
		AscendMap: make(map[string]string), DescendMap: make(map[string]string),
		AddrAssetMap:    make(map[string]map[swire.AssetName]*Decimal),
		AddrTransferMap: make(map[string]map[swire.AssetName]map[string]bool),
		TransferInfo:    make(map[string]*Brc20Transfer), Invalids: make(map[string]map[swire.AssetName]bool),
		KvCache: make(map[string]*indexerwire.KeyValue), DkvsRecords: make(map[string]*swire.DKVSRecord),
		DkvsDeleted: make(map[string]dkvsindexer.DKVSKeyState), DkvsGenerations: make(map[string]uint64),
	}
}

// SeedOutput imports a confirmed fixture output. A valid BRC20 allocation is
// a transferable inscription; Invalid marks an already consumed/mint carrier.
// This explicit seed is the only way holdings are introduced without a tx.
func (p *Network) SeedOutput(output *indexer.TxOutput) error {
	copy := output.Clone()
	txid, _, err := indexer.ParseUtxo(copy.OutPointStr)
	if err != nil {
		return err
	}
	if copy.UtxoId == indexer.INVALID_ID {
		return fmt.Errorf("seed output must be confirmed")
	}
	for _, asset := range copy.Assets {
		if asset.Name.Protocol == indexer.PROTOCOL_NAME_BRC20 && len(copy.Offsets[asset.Name]) != 1 {
			return fmt.Errorf("seed BRC20 requires one inscription offset")
		}
	}
	p.Mutex.Lock()
	defer p.Mutex.Unlock()
	if _, ok := p.UtxoIndex[copy.OutPointStr]; ok {
		return fmt.Errorf("seed output already exists: %s", copy.OutPointStr)
	}
	p.UtxoIndex[copy.OutPointStr] = len(p.Utxos)
	p.Utxos = append(p.Utxos, copy.OutPointStr)
	p.UtxoValue = append(p.UtxoValue, copy.Value())
	p.UtxoAssets = append(p.UtxoAssets, copy.Assets)
	p.Offsets = append(p.Offsets, copy.Offsets)
	p.UtxoOwner = append(p.UtxoOwner, p.State.insertPkScript(copy.OutValue.PkScript))
	p.Invalids[copy.OutPointStr] = copy.Invalids
	height, _, _ := indexer.FromUtxoId(copy.UtxoId)
	seen := false
	for _, id := range p.Blocks[height] {
		seen = seen || id == txid
	}
	if !seen {
		p.Blocks[height] = append(p.Blocks[height], txid)
	}
	addr := hex.EncodeToString(copy.OutValue.PkScript)
	for _, asset := range copy.Assets {
		if asset.Name.Protocol != indexer.PROTOCOL_NAME_BRC20 {
			continue
		}
		if p.AddrAssetMap[addr] == nil {
			p.AddrAssetMap[addr] = make(map[swire.AssetName]*Decimal)
		}
		p.AddrAssetMap[addr][asset.Name] = p.AddrAssetMap[addr][asset.Name].Add(&asset.Amount)
		if copy.Invalids[asset.Name] {
			continue
		}
		if p.AddrTransferMap[addr] == nil {
			p.AddrTransferMap[addr] = make(map[swire.AssetName]map[string]bool)
		}
		if p.AddrTransferMap[addr][asset.Name] == nil {
			p.AddrTransferMap[addr][asset.Name] = make(map[string]bool)
		}
		p.AddrTransferMap[addr][asset.Name][copy.OutPointStr] = true
		name := asset.Name
		p.TransferInfo[copy.OutPointStr] = &Brc20Transfer{Utxo: copy.OutPointStr, Address: addr, AssetName: &name, Amt: asset.Amount.Clone()}
	}
	return nil
}
