package fakeindexer

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type fakeRGB11RegistryAuthority struct{}

func (fakeRGB11RegistryAuthority) CanWriteSystem(key string, pubKey []byte) error {
	if !strings.HasPrefix(key, rgb11wallet.RGB11RegistryPath+"/") ||
		hex.EncodeToString(pubKey) != indexer.GetCoreNodePubKey() {
		return dkvsindexer.ErrPermissionDenied
	}
	return nil
}

// The in-process L2 accepts only the same signed, public, immutable technical
// record that the Core registration route accepts. It is not a generic DKVS
// CAS escape hatch for contract keys.
func (p *Client) fakeRGB11Register(body []byte) ([]byte, error) {
	var request struct {
		Record *swire.DKVSRecord `json:"record"`
	}
	if err := json.Unmarshal(body, &request); err != nil || request.Record == nil {
		return fakeDKVSErrorResponse(dkvsindexer.ErrInvalidRecord)
	}
	id := strings.TrimPrefix(request.Record.Key, rgb11wallet.RGB11RegistryPath+"/")
	if _, err := rgb11wallet.VerifyRGB11RegistrationForClient(request.Record, id, fakeRGB11RegistryAuthority{}); err != nil {
		return fakeDKVSErrorResponse(err)
	}
	p.Network.Mutex.Lock()
	if p.Network.DkvsRecords == nil {
		p.Network.DkvsRecords = make(map[string]*swire.DKVSRecord)
	}
	stored := p.Network.DkvsRecords[request.Record.Key]
	if stored != nil {
		if _, err := rgb11wallet.VerifyRGB11RegistrationForClient(stored, id, fakeRGB11RegistryAuthority{}); err != nil {
			p.Network.Mutex.Unlock()
			return fakeDKVSErrorResponse(err)
		}
		if !bytes.Equal(stored.Value, request.Record.Value) {
			p.Network.Mutex.Unlock()
			return fakeDKVSErrorResponse(dkvsindexer.ErrWriteConflict)
		}
	} else {
		p.Network.DkvsRecords[request.Record.Key] = cloneFakeDKVSRecord(request.Record)
		if p.Network.DkvsGenerations == nil {
			p.Network.DkvsGenerations = make(map[string]uint64)
		}
		if p.Network.DkvsChangedAt == nil {
			p.Network.DkvsChangedAt = make(map[string]uint64)
		}
		p.Network.DkvsGenerations[rgb11wallet.RGB11RegistryPath]++
		p.Network.DkvsChangedAt[request.Record.Key] = p.Network.DkvsGenerations[rgb11wallet.RGB11RegistryPath]
	}
	readback := cloneFakeDKVSRecord(p.Network.DkvsRecords[request.Record.Key])
	p.Network.Mutex.Unlock()
	if _, err := rgb11wallet.VerifyRGB11RegistrationForClient(readback, id, fakeRGB11RegistryAuthority{}); err != nil ||
		!bytes.Equal(readback.Value, request.Record.Value) {
		return fakeDKVSErrorResponse(dkvsindexer.ErrInvalidRecord)
	}
	return json.Marshal(map[string]any{"code": 0, "msg": "ok", "contract_id": id})
}
