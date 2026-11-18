package wallet

import (
	"errors"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

func (p *Manager) rgb11PublicRegistryVerifier() dkvsindexer.SystemVerifier {
	if p != nil && p.rgb11RegistryVerifier != nil {
		return p.rgb11RegistryVerifier
	}
	return rgb11NetworkRegistryVerifier{}
}

// Account management owns the verified registry read. RGB receives only the
// authenticated public contract and checks its own asset identity/content.
func (p *Manager) readAccountRGBContract(id string) (*rgb11wallet.RGB11Registration, *rgb11wallet.RGB11ContractValue, error) {
	client, err := p.ensureDKVSManager().primaryClient()
	if err != nil {
		return nil, nil, err
	}
	registration, value, err := client.getRGB11ContractWithVerifier(id, p.rgb11PublicRegistryVerifier())
	if errors.Is(err, dkvsindexer.ErrRecordNotFound) {
		return nil, nil, ErrRGB11PublicRegistrationRequired
	}
	return registration, value, err
}

// Core authority and content checks remain in the caller. Account management
// owns transport and authenticated readback; no private backup gate is involved.
func (p *Manager) RegisterRGB11SignedContract(record *swire.DKVSRecord) (*rgb11wallet.RGB11Registration, error) {
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, err
	}
	return client.registerRGB11SignedContract(record)
}

func (p *Manager) GetRGB11Registration(id string) (*rgb11wallet.RGB11Registration, error) {
	client, err := p.accountDKVSClient()
	if err != nil {
		return nil, err
	}
	return client.getRGB11Registration(id)
}
