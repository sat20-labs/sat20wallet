package wallet

import (
	"bytes"
	indexer "github.com/sat20-labs/indexer/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// Model a confirmed view that has not materialized an already acknowledged
// publication. Only the activation capture reads are held at the older view;
// Update's ordinary current-state read remains unchanged. No writer is added.
type activationBaselineAuditDB struct {
	indexer.KVDB
	classes map[string]string
	mu      sync.Mutex
	events  []string
}

func (d *activationBaselineAuditDB) Read(key []byte) ([]byte, error) {
	value, err := d.KVDB.Read(key)
	if err != nil || !bytes.HasPrefix(key, []byte("dkvs:subscription-record:")) {
		return value, err
	}
	record, decodeErr := dkvs.UnmarshalRecord(value)
	if decodeErr != nil {
		return value, err
	}
	class, ok := d.classes[record.Key]
	if !ok {
		return value, err
	}
	stack := make([]byte, 16<<10)
	trace := string(stack[:runtime.Stack(stack, false)])
	if !strings.Contains(trace, "activateAccountManagement") {
		return value, err
	}
	phase := ""
	if strings.Contains(trace, "getConfirmedVerified") {
		phase = "capture"
	} else if strings.Contains(trace, "currentKeyState") {
		phase = "current"
	}
	if phase == "" {
		return value, err
	}
	suppressed := phase == "capture" && class != "wrapper"
	summary := phase + " class=" + class
	if suppressed {
		summary += " absent controlled-stale-view"
	} else {
		summary += " etag=" + dkvs.RecordHash(record).String()[:16]
	}
	d.mu.Lock()
	d.events = append(d.events, summary)
	d.mu.Unlock()
	if suppressed {
		return nil, indexer.ErrKeyNotFound
	}
	return value, err
}

// This diagnostic proves that the local comparison can reject a stale capture
// without a second writer. It does not declare that a genuine CAS conflict
// should be ignored, nor change any production assertion or operation.
func TestAccountActivationStaleConfirmedCaptureDiagnostic(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	manager, _, _, keys := accountTTLNoopFixture(t)
	audit := &activationBaselineAuditDB{KVDB: manager.db, classes: map[string]string{keys[0]: "state", keys[1]: "data", keys[2]: "wrapper"}}
	pkg, auth, secret := rereviewRecoveryPackage(t, manager)
	defer zeroBytes(secret)
	manager.db = audit
	err := manager.ActivateAccountManagement(secret, "password", *auth, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID)
	audit.mu.Lock()
	events := append([]string{}, audit.events...)
	audit.mu.Unlock()
	for _, event := range events {
		t.Log(event)
	}
	require.ErrorIs(t, err, dkvs.ErrWriteConflict)
	var captured, current bool
	for _, event := range events {
		captured = captured || strings.Contains(event, "capture class=state absent")
		current = current || strings.Contains(event, "current class=state etag=")
	}
	require.True(t, captured, "controlled stale capture was not reached")
	require.True(t, current, "ordinary current-key read was not reached")
}
