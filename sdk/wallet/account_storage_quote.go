package wallet

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/url"
	"strings"
	"syscall"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// A preview is not a grant. An absent root remains a valid identity for a
// read that did not previously require one; a root appearing later is not.
type storageQuoteKey struct {
	accountStorageBinding
	profile, address, namespace string
	walletID                    int64
	generation                  uint64
}

type storageQuoteRead struct {
	owner    *Manager
	store    *dkvsStore
	reader   *SatsNetDKVSClient
	ctx      context.Context
	stop     <-chan struct{}
	key      storageQuoteKey
	label    string
	endpoint string // source of this request's verified policy, not a shared cache
}

// Caller holds the Manager read gate. Preserve the original quote's display
// ID (zero before activation), while pinning the actual root independently.
func (p *Manager) quoteKeyLocked() (key storageQuoteKey, err error) {
	key.network = p.GetChain()
	key.namespace = p.dkvsReplicaNamespace()
	key.generation = p.accountGeneration
	key.location, err = p.AccountIndexerLocation()
	if err != nil {
		return key, err
	}
	info := p.firstWalletLocked()
	if p.accountProfile != nil {
		key.profile = p.accountProfile.AccountID
		info, err = p.accountManagementRootWalletLocked()
		if err != nil && !errors.Is(err, ErrAccountManagementWalletUnavailable) {
			return key, err
		}
		if info != nil {
			key.walletID = info.Id
		}
	}
	if info != nil && info.Wallet != nil {
		root := cloneWalletAtAccountZero(info.Wallet)
		key.accountID, err = dkvsAccountID(root)
		if err != nil {
			return key, err
		}
		if p.accountProfile != nil || info.Type == WALLET_TYPE_MNEMONIC {
			key.address = PublicKeyToP2TRAddress_SatsNet(root.GetPubKey())
		}
	}
	return key, nil
}

func (p *Manager) beginStorageQuote(ctx context.Context) (*storageQuoteRead, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	stop, ok := p.beginAccountBackgroundTask()
	if !ok {
		return nil, nil, ErrAccountStorageRuntimeStopped
	}
	ctx, cancel := context.WithCancel(ctx)
	// Stop joins only its cancellation relay, never a non-context legacy RPC.
	go func() {
		defer p.finishAccountBackgroundTask()
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	q := &storageQuoteRead{owner: p, ctx: ctx, stop: stop}
	p.mutex.RLock()
	var err error
	q.store, err = p.accountDKVSStore()
	if err == nil {
		q.key, err = p.quoteKeyLocked()
	}
	if err == nil {
		q.label = quoteWalletLabel(q.key.walletID, p.walletCatalogLocked())
		// Only the immutable transport is shared. Local authority reads use
		// the captured manager namespace, never storeFor's mutable copy.
		q.reader = &SatsNetDKVSClient{RESTClient: q.store.client.RESTClient, replicaNamespace: q.key.namespace}
		q.store.client.endpointMu.RLock()
		q.endpoint = q.store.client.endpointID
		q.store.client.endpointMu.RUnlock()
	}
	p.mutex.RUnlock()
	if err == nil {
		err = q.current()
	}
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return q, cancel, nil
}

func (q *storageQuoteRead) paymentSource(v *PaymentQuote) {
	v.Network, v.SourceWallet, v.SourceAddress = q.key.network, q.label, q.key.address
}

func (q *storageQuoteRead) current() error {
	select {
	case <-q.stop:
		return ErrAccountStorageRuntimeStopped
	default:
	}
	if err := q.ctx.Err(); err != nil {
		return err
	}
	p := q.owner
	// Read the confirmed local source outside the identity locks. Compare
	// it even when the request did not itself obtain a config: the actual
	// primary may have learned an ID during this quote.
	var authority *DKVSSubscriptionState
	if p.db != nil && q.key.namespace != "" {
		state, err := q.store.manager.subscriptionStateForClient(q.reader)
		if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
			return err
		}
		authority = state
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	key, err := p.quoteKeyLocked()
	if err != nil {
		return err
	}
	p.accountStorageMu.Lock()
	defer p.accountStorageMu.Unlock()
	if p.accountStorageStopped {
		return ErrAccountStorageRuntimeStopped
	}
	if key != q.key || p.dkvs != q.store.manager {
		return errAccountSnapshotChanged
	}
	q.store.manager.mu.Lock()
	defer q.store.manager.mu.Unlock()
	primary := q.store.manager.clients[dkvsEndpointKey(key.location.Scheme, key.location.Host, key.location.Proxy)]
	if primary != q.store.client {
		return errAccountSnapshotChanged
	}
	primary.endpointMu.RLock()
	defer primary.endpointMu.RUnlock()
	known := primary.endpointID
	if known == "" {
		known = q.endpoint
	} else if q.endpoint != "" && known != q.endpoint {
		return dkvsindexer.ErrEndpointMismatch
	}
	if authority != nil && known != "" {
		if err := validateDKVSSubscriptionEndpoint(authority, known); err != nil {
			return err
		}
	}
	select {
	case <-q.stop:
		return ErrAccountStorageRuntimeStopped
	default:
		return q.ctx.Err()
	}
}

// Only a transport failure can make an otherwise verified policy TTL-only.
// In particular net/url can wrap a typed refusal, and joined errors must
// have no independent protocol or storage failure.
func quoteTransportDown(err error, inURL bool) bool {
	if err == nil || errors.Is(err, context.Canceled) || err == context.DeadlineExceeded ||
		errors.Is(err, fs.ErrPermission) {
		return false
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return inURL
	}
	switch e := err.(type) {
	case *dkvsLocalOutboxReadError, *fs.PathError:
		return false
	case interface{ Unwrap() []error }:
		parts := e.Unwrap()
		for _, part := range parts {
			if !quoteTransportDown(part, inURL) {
				return false
			}
		}
		return len(parts) != 0
	case *HTTPResponseError:
		if e == nil || !(e.StatusCode == 408 || e.StatusCode == 425 ||
			e.StatusCode == 429 || e.StatusCode >= 500 && e.StatusCode < 600) {
			return false
		}
		cause := e.Unwrap()
		return cause == nil || quoteTransportDown(cause, false)
	case *url.Error:
		if e == nil || e.Err == nil {
			return false
		}
		return quoteTransportDown(e.Err, inURL || e.Op == "read" || strings.EqualFold(e.Op, "get"))
	case syscall.Errno:
		// Errno also implements net.Error for ordinary filesystem failures.
		switch e {
		case syscall.ECONNRESET, syscall.ECONNABORTED, syscall.ECONNREFUSED, syscall.EPIPE,
			syscall.ETIMEDOUT, syscall.EHOSTUNREACH, syscall.ENETUNREACH, syscall.ENETDOWN:
			return inURL
		}
		return false
	}
	if cause := errors.Unwrap(err); cause != nil {
		return quoteTransportDown(cause, inURL)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// net/http's own Client.Timeout matches DeadlineExceeded too. The
		// caller context was checked separately; a bare deadline is not an
		// attributed transport timeout.
		timeout, ok := err.(net.Error)
		return inURL && ok && timeout.Timeout()
	}
	return isDKVSNetworkFailure(err) || inURL && strings.HasPrefix(err.Error(), "net/http: fetch() failed:")
}

func (q *storageQuoteRead) readPolicy() (*AccountFreeLocalPolicy, uint64, bool, error) {
	// This request-local read-only client may learn an endpoint ID; neither
	// that ID nor its observed height can change anyone's verification authority.
	client := q.reader
	config, err := q.store.storageConfig(q.ctx, client)
	if err != nil {
		if check := q.current(); check != nil {
			return nil, 0, false, check
		}
		if quoteTransportDown(err, false) {
			return nil, 0, false, nil // temporary policy unknown; paid browsing remains available
		}
		return nil, 0, false, err
	}
	endpoint := strings.TrimSpace(config.EndpointID)
	if endpoint == "" {
		return nil, 0, false, dkvsindexer.ErrStaleEndpoint
	}
	if q.endpoint != "" && q.endpoint != endpoint {
		return nil, 0, false, dkvsindexer.ErrEndpointMismatch
	}
	q.endpoint = endpoint
	if err := q.current(); err != nil {
		return nil, 0, false, err
	}
	policy := AccountFreeLocalPolicy(config.FreeLocal)
	height, err := client.getBestHeightContext(q.ctx)
	if check := q.current(); check != nil {
		return nil, 0, false, check
	}
	if err != nil {
		if quoteTransportDown(err, false) {
			return &policy, 0, false, nil
		}
		return nil, 0, false, err
	}
	return &policy, height, true, nil
}
