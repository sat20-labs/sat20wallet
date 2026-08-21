package wallet

import (
	"bytes"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	dkvsPathReplicaStateMagic        = "DKPS"
	dkvsPathReplicaStateCodecVersion = uint8(1)
	dkvsPathReplicaMaxText           = 4096
	dkvsPathReplicaMaxDeleteFloors   = 65536
)

func encodeDKVSPathReplicaState(state *dkvsPathReplicaState) ([]byte, error) {
	if state == nil || state.Path == "" || state.SessionState == "" ||
		len(state.DeleteFloors) > dkvsPathReplicaMaxDeleteFloors ||
		len(state.LocalDeleteFloors) > dkvsPathReplicaMaxDeleteFloors {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	var buf bytes.Buffer
	e := strict.NewEncoder(&buf)
	if e.Raw([]byte(dkvsPathReplicaStateMagic)) != nil || e.U8(dkvsPathReplicaStateCodecVersion) != nil ||
		e.U32(state.Version) != nil || e.String(state.Path, 1, dkvsPathReplicaMaxText) != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	hasMeta := state.PathMeta != nil
	if e.Bool(hasMeta) != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if hasMeta {
		m := state.PathMeta
		if e.U32(m.Version) != nil || e.String(m.Path, 1, dkvsPathReplicaMaxText) != nil || e.U64(m.Generation) != nil ||
			e.Raw(m.StateRoot[:]) != nil || e.U64(m.ActiveRecords) != nil || e.U64(m.ActiveTotalSize) != nil ||
			e.U64(m.MinExpiryHeight) != nil || e.U64(m.ViewHeight) != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	if e.Length(uint64(len(state.DeleteFloors)), dkvsPathReplicaMaxDeleteFloors) != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	for _, f := range state.DeleteFloors {
		if e.String(f.Key, 1, dkvsPathReplicaMaxText) != nil || e.U64(f.FloorSeq) != nil || e.U64(f.PathGeneration) != nil ||
			e.Bytes(f.PubKey, 0, 128) != nil || e.Raw(f.EffectiveHash[:]) != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	if e.U64(state.ServerTimeMS) != nil || e.String(state.EndpointID, 0, dkvsPathReplicaMaxText) != nil ||
		e.Bool(state.HasLocalOnly) != nil || e.String(state.SessionState, 1, 64) != nil ||
		e.String(state.LastErrorCode, 0, 256) != nil || e.U64(state.UpdatedAtMS) != nil ||
		e.Length(uint64(len(state.LocalDeleteFloors)), dkvsPathReplicaMaxDeleteFloors) != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	for _, f := range state.LocalDeleteFloors {
		if e.String(f.Key, 1, dkvsPathReplicaMaxText) != nil || e.U64(f.FloorSeq) != nil || e.U64(f.PathGeneration) != nil ||
			e.Bytes(f.PubKey, 0, 128) != nil || e.Raw(f.EffectiveHash[:]) != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	return buf.Bytes(), nil
}

func decodeDKVSPathReplicaState(encoded []byte, state *dkvsPathReplicaState) error {
	if state == nil || len(encoded) == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	r := bytes.NewReader(encoded)
	d := strict.NewDecoder(r)
	magic, err := d.Raw(uint64(len(dkvsPathReplicaStateMagic)))
	if err != nil || string(magic) != dkvsPathReplicaStateMagic {
		return dkvsindexer.ErrInvalidRecord
	}
	version, err := d.U8()
	if err != nil || version != dkvsPathReplicaStateCodecVersion {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.Version, err = d.U32(); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.Path, err = d.String(1, dkvsPathReplicaMaxText); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	hasMeta, err := d.Bool()
	if err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if hasMeta {
		m := &dkvsindexer.PathMeta{}
		if m.Version, err = d.U32(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if m.Path, err = d.String(1, dkvsPathReplicaMaxText); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if m.Generation, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		raw, e := d.Raw(chainhash.HashSize)
		if e != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		copy(m.StateRoot[:], raw)
		if m.ActiveRecords, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if m.ActiveTotalSize, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if m.MinExpiryHeight, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if m.ViewHeight, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		m.UpdatedHeight = m.ViewHeight
		state.PathMeta = m
	}
	count, err := d.Length(dkvsPathReplicaMaxDeleteFloors)
	if err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	state.DeleteFloors = make([]dkvsindexer.DeleteFloor, 0, count)
	for i := uint64(0); i < count; i++ {
		var f dkvsindexer.DeleteFloor
		if f.Key, err = d.String(1, dkvsPathReplicaMaxText); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.FloorSeq, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.PathGeneration, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.PubKey, err = d.Bytes(0, 128); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		raw, e := d.Raw(chainhash.HashSize)
		if e != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		copy(f.EffectiveHash[:], raw)
		state.DeleteFloors = append(state.DeleteFloors, f)
	}
	if state.ServerTimeMS, err = d.U64(); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.EndpointID, err = d.String(0, dkvsPathReplicaMaxText); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.HasLocalOnly, err = d.Bool(); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.SessionState, err = d.String(1, 64); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.LastErrorCode, err = d.String(0, 256); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.UpdatedAtMS, err = d.U64(); err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	// The trailing local-floor section was added without changing the codec
	// version. An older binary path state ends immediately after UpdatedAtMS.
	// Keep accepting that representation so existing SDK replicas migrate in
	// place when they are next written.
	if r.Len() == 0 {
		return nil
	}
	count, err = d.Length(dkvsPathReplicaMaxDeleteFloors)
	if err != nil {
		return dkvsindexer.ErrInvalidRecord
	}
	state.LocalDeleteFloors = make([]dkvsindexer.DeleteFloor, 0, count)
	for i := uint64(0); i < count; i++ {
		var f dkvsindexer.DeleteFloor
		if f.Key, err = d.String(1, dkvsPathReplicaMaxText); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.FloorSeq, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.PathGeneration, err = d.U64(); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		if f.PubKey, err = d.Bytes(0, 128); err != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		raw, e := d.Raw(chainhash.HashSize)
		if e != nil {
			return dkvsindexer.ErrInvalidRecord
		}
		copy(f.EffectiveHash[:], raw)
		state.LocalDeleteFloors = append(state.LocalDeleteFloors, f)
	}
	if r.Len() != 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	return nil
}
