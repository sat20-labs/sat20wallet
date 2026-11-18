package wallet

import (
	"context"
	"errors"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// AccountFreeLocalPolicy is the wallet-facing alias of the service-node policy.
type AccountFreeLocalPolicy = dkvscore.FreeLocalPolicy

func (s *dkvsStore) nodeConfigKey() []byte {
	c := s.client
	return []byte("dkvs/node-config/" + s.manager.owner.dkvsReplicaNamespace() + "/" +
		dkvsEndpointKey(c.Scheme, c.Host, c.Proxy))
}

// The wallet runtime refreshes the connected node's policy once per start.
func (p *Manager) refreshNodeConfig(ctx context.Context) error {
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	config, err := store.client.getDKVSClientConfigContext(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.EndpointID) == "" {
		return dkvsindexer.ErrStaleEndpoint
	}
	if err := store.manager.validateVerificationEndpoint(store.client, config.EndpointID); err != nil {
		return err
	}
	raw, err := EncodeToBytes(config)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.db.Write(store.nodeConfigKey(), raw)
}

func (s *dkvsStore) storageConfig(ctx context.Context, reader *SatsNetDKVSClient) (*dkvsindexer.ClientConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.manager != nil && s.manager.owner != nil && s.manager.owner.db != nil {
		raw, err := s.manager.owner.db.Read(s.nodeConfigKey())
		if err == nil {
			var config dkvsindexer.ClientConfig
			if err := DecodeFromBytes(raw, &config); err != nil {
				return nil, err
			}
			if strings.TrimSpace(config.EndpointID) == "" {
				return nil, dkvsindexer.ErrStaleEndpoint
			}
			return &config, nil
		}
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, err
		}
	}
	// Native callers can request storage before starting the background runtime.
	return reader.getDKVSClientConfigContext(ctx)
}

// GetConfig reads the FREE_LOCAL cache policy of the connected endpoint.
func (p *SatsNetDKVSClient) GetConfig() (*AccountFreeLocalPolicy, error) {
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	policy := config.FreeLocal
	return &AccountFreeLocalPolicy{
		Enabled:             policy.Enabled,
		MaxTTL:              policy.MaxTTL,
		MaxRecordsPerSigner: policy.MaxRecordsPerSigner,
		MaxBytesPerSigner:   policy.MaxBytesPerSigner,
		MaxTotalRecords:     policy.MaxTotalRecords,
		MaxTotalBytes:       policy.MaxTotalBytes,
	}, nil
}

func (p *SatsNetDKVSClient) configureFreeLocalRetention(options *dkvsindexer.RecordOptions) (*AccountFreeLocalPolicy, error) {
	policy, err := p.GetConfig()
	if err != nil {
		return nil, err
	}
	if err := dkvscore.ApplyFreeLocalRetention(policy, options); err != nil {
		return nil, err
	}
	return policy, nil
}
