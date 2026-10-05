package e2e

import (
	"bytes"
	"sync"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type generationPeerPair struct {
	*activePeerPair
	mu sync.Mutex
	committed map[chainhash.Hash]*wire.MsgDKVSNotify
}

func newGenerationPeerPair(t *testing.T) *generationPeerPair {
	t.Helper()
	p := &generationPeerPair{
		activePeerPair: newActivePeerPair(t),
		committed: make(map[chainhash.Hash]*wire.MsgDKVSNotify),
	}
	p.source.backend.SetNotify(func(event *dkvs.NotifyEvent) {
		record, err := dkvs.RecordFromNotifyEvent(event)
		if err != nil {
			t.Errorf("decode committed event: %v", err)
			return
		}
		p.mu.Lock()
		p.committed[dkvs.RecordHash(record)] = &wire.MsgDKVSNotify{
			EventType: event.EventType,
			Data: bytes.Clone(event.Data),
		}
		p.mu.Unlock()
	})
	return p
}

func (p *generationPeerPair) notification(t *testing.T, record *wire.DKVSRecord) *wire.MsgDKVSNotify {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	message := p.committed[dkvs.RecordHash(record)]
	require.NotNil(t, message, "must use the actual committed notification")
	plain, err := wire.SerializeDKVSRecord(record)
	require.NoError(t, err)
	require.Equal(t, plain, message.Data, "P2P notify must contain only the signed KV record; generation is wallet/service-node metadata")
	actual, err := dkvs.RecordFromNotifyEvent(&dkvs.NotifyEvent{EventType: message.EventType, Data: message.Data})
	require.NoError(t, err)
	require.Equal(t, dkvs.RecordHash(record), dkvs.RecordHash(actual))
	return &wire.MsgDKVSNotify{EventType: message.EventType, Data: bytes.Clone(message.Data)}
}
