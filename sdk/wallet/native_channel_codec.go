package wallet

import (
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

// Native receipts have no ordinal offsets. Serialize only the native asset and
// exact Bitcoin carrier instead of leaking the indexer's map-key wire format
// into the protocol transport.
type nativeChannelOutputJSON struct {
	UtxoID   uint64           `json:"utxo_id"`
	Outpoint string           `json:"outpoint"`
	Value    int64            `json:"value"`
	PkScript []byte           `json:"pk_script"`
	Assets   indexer.TxAssets `json:"assets"`
}

type nativeChannelSourceJSON struct {
	Version        uint32                   `json:"version"`
	ChannelID      string                   `json:"channel_id"`
	Output         *nativeChannelOutputJSON `json:"output"`
	PublicProof    []byte                   `json:"public_proof"`
	DescendTxIDs   []string                 `json:"descend_txids,omitempty"`
	PrivateWitness []byte                   `json:"private_witness,omitempty"`
}

func (s NativeChannelSource) MarshalJSON() ([]byte, error) {
	dto := nativeChannelSourceJSON{Version: s.Version, ChannelID: s.ChannelID, PublicProof: s.PublicProof, DescendTxIDs: s.DescendTxIDs, PrivateWitness: s.PrivateWitness}
	if s.Output != nil {
		if len(s.Output.Offsets) != 0 {
			return nil, fmt.Errorf("native source cannot contain ordinal offsets")
		}
		dto.Output = &nativeChannelOutputJSON{UtxoID: s.Output.UtxoId, Outpoint: s.Output.OutPointStr, Value: s.Output.Value(), PkScript: s.Output.OutValue.PkScript, Assets: s.Output.Assets}
	}
	return json.Marshal(dto)
}

func (s *NativeChannelSource) UnmarshalJSON(raw []byte) error {
	var dto nativeChannelSourceJSON
	if err := json.Unmarshal(raw, &dto); err != nil {
		return err
	}
	*s = NativeChannelSource{Version: dto.Version, ChannelID: dto.ChannelID, PublicProof: dto.PublicProof, DescendTxIDs: dto.DescendTxIDs, PrivateWitness: dto.PrivateWitness}
	if dto.Output != nil {
		s.Output = indexer.NewTxOutput(dto.Output.Value)
		s.Output.UtxoId, s.Output.OutPointStr = dto.Output.UtxoID, dto.Output.Outpoint
		s.Output.OutValue.PkScript, s.Output.Assets = dto.Output.PkScript, dto.Output.Assets
	}
	return nil
}
