package wallet

import (
	"encoding/json"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
)

// RGB11 receipts have no ordinal offsets. Serialize only the RGB11 asset and
// exact Bitcoin carrier instead of leaking the indexer's map-key wire format
// into the protocol transport.
type rgb11ChannelOutputJSON struct {
	UtxoID   uint64           `json:"utxo_id"`
	Outpoint string           `json:"outpoint"`
	Value    int64            `json:"value"`
	PkScript []byte           `json:"pk_script"`
	Assets   indexer.TxAssets `json:"assets"`
}

type rgb11ChannelSourceJSON struct {
	Version        uint32                   `json:"version"`
	ChannelID      string                   `json:"channel_id"`
	Output         *rgb11ChannelOutputJSON `json:"output"`
	PublicProof    []byte                   `json:"public_proof"`
	DescendTxIDs   []string                 `json:"descend_txids,omitempty"`
	PrivateWitness []byte                   `json:"private_witness,omitempty"`
}

func (s RGB11ChannelSource) MarshalJSON() ([]byte, error) {
	dto := rgb11ChannelSourceJSON{Version: s.Version, ChannelID: s.ChannelID, PublicProof: s.PublicProof, DescendTxIDs: s.DescendTxIDs, PrivateWitness: s.PrivateWitness}
	if s.Output != nil {
		if len(s.Output.Offsets) != 0 {
			return nil, fmt.Errorf("RGB11 source cannot contain ordinal offsets")
		}
		dto.Output = &rgb11ChannelOutputJSON{UtxoID: s.Output.UtxoId, Outpoint: s.Output.OutPointStr, Value: s.Output.Value(), PkScript: s.Output.OutValue.PkScript, Assets: s.Output.Assets}
	}
	return json.Marshal(dto)
}

func (s *RGB11ChannelSource) UnmarshalJSON(raw []byte) error {
	var dto rgb11ChannelSourceJSON
	if err := json.Unmarshal(raw, &dto); err != nil {
		return err
	}
	*s = RGB11ChannelSource{Version: dto.Version, ChannelID: dto.ChannelID, PublicProof: dto.PublicProof, DescendTxIDs: dto.DescendTxIDs, PrivateWitness: dto.PrivateWitness}
	if dto.Output != nil {
		s.Output = indexer.NewTxOutput(dto.Output.Value)
		s.Output.UtxoId, s.Output.OutPointStr = dto.Output.UtxoID, dto.Output.Outpoint
		s.Output.OutValue.PkScript, s.Output.Assets = dto.Output.PkScript, dto.Output.Assets
	}
	return nil
}
