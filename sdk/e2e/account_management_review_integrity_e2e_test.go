package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type accountReviewTamperTransport struct {
	inner     wallet.HttpClient
	mode      string
	targetKey string
	hits      atomic.Int64
}

func (p *accountReviewTamperTransport) SendGetRequest(u *wallet.URL) ([]byte, error) {
	raw, err := p.inner.SendGetRequest(u)
	if err != nil {
		return nil, err
	}
	return p.tamper(u.Path, raw)
}

func (p *accountReviewTamperTransport) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	raw, err := p.inner.SendPostRequest(u, body)
	if err != nil {
		return nil, err
	}
	return p.tamper(u.Path, raw)
}

func (p *accountReviewTamperTransport) corrupt(record *wire.DKVSRecord) error {
	if record == nil || len(record.Signature) == 0 {
		return fmt.Errorf("tamper fixture has no signed record")
	}
	if p.targetKey != "" && record.Key != p.targetKey {
		return fmt.Errorf("tamper fixture missed target key")
	}
	if err := dkvs.VerifySignature(record); err != nil {
		return fmt.Errorf("tamper precondition: %w", err)
	}
	record.Signature = append([]byte(nil), record.Signature...)
	record.Signature[len(record.Signature)-1] ^= 1
	if !errors.Is(dkvs.VerifySignature(record), dkvs.ErrInvalidSignature) {
		return fmt.Errorf("tamper did not produce the intended signature failure")
	}
	p.hits.Add(1)
	return nil
}
func (p *accountReviewTamperTransport) target(records []*wire.DKVSRecord) *wire.DKVSRecord {
	for _, record := range records {
		if record != nil && (p.targetKey == "" || record.Key == p.targetKey) {
			return record
		}
	}
	return nil
}

func (p *accountReviewTamperTransport) tamper(path string, raw []byte) ([]byte, error) {
	var response map[string]json.RawMessage
	if err := json.Unmarshal(raw, &response); err != nil {
		return raw, nil
	}
	switch p.mode {
	case "record":
		if !strings.HasSuffix(path, "/v3/dkvs/record") {
			return raw, nil
		}
		var record wire.DKVSRecord
		if json.Unmarshal(response["data"], &record) != nil {
			return raw, nil
		}
		if err := p.corrupt(&record); err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(&record)
		response["data"] = encoded
		etag, _ := json.Marshal(dkvs.RecordHash(&record).String())
		response["etag"] = etag
	case "key-state":
		if !strings.HasSuffix(path, "/v3/dkvs/key-state") {
			return raw, nil
		}
		var state dkvs.DKVSKeyState
		if json.Unmarshal(response["data"], &state) != nil || state.Record == nil {
			return raw, nil
		}
		if err := p.corrupt(state.Record); err != nil {
			return nil, err
		}
		state.ETag = dkvs.RecordHash(state.Record).String()
		encoded, _ := json.Marshal(&state)
		response["data"] = encoded
	case "prefix-read":
		if !strings.HasSuffix(path, "/v3/dkvs/prefixes/read") {
			return raw, nil
		}
		var result dkvs.PrefixReadResult
		if json.Unmarshal(response["data"], &result) != nil || len(result.Records) == 0 {
			return raw, nil
		}
		target := p.target(result.Records)
		if target == nil {
			return raw, nil
		}
		if err := p.corrupt(target); err != nil {
			return nil, err
		}
		for index := range result.KeyStates {
			if result.KeyStates[index].Key == target.Key {
				result.KeyStates[index].ETag = dkvs.RecordHash(target).String()
				result.KeyStates[index].Record = target
			}
		}
		encoded, _ := json.Marshal(&result)
		response["data"] = encoded
	case "active-sync":
		if !strings.HasSuffix(path, "/v3/dkvs/active/sync") {
			return raw, nil
		}
		var page dkvs.ActivePage
		if json.Unmarshal(response["data"], &page) != nil || len(page.Records) == 0 {
			return raw, nil
		}
		target := p.target(page.Records)
		if target == nil {
			return raw, nil
		}
		if err := p.corrupt(target); err != nil {
			return nil, err
		}
		encoded, _ := json.Marshal(&page)
		response["data"] = encoded
	default:
		return raw, nil
	}
	return json.Marshal(response)
}

func TestSDKAccountReviewServerSignatureVerification(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	f := prepareAccountReview(t, network, true)
	f.activate(t)

	root := f.manager.GetWallet()
	require.NotNil(t, root)
	stateKey, err := dkvs.PersonalKey(root.GetPubKey().SerializeCompressed(), "account/state")
	require.NoError(t, err)
	prefix, err := dkvs.CollectionPathForKey(stateKey)
	require.NoError(t, err)

	for _, scenario := range []struct {
		name string
		mode string
		read func(*wallet.SatsNetDKVSClient) error
	}{
		{"DirectRecord", "record", func(client *wallet.SatsNetDKVSClient) error {
			_, err := client.GetRecordDirect(stateKey)
			return err
		}},
		{"KeyStateEmbeddedRecord", "key-state", func(client *wallet.SatsNetDKVSClient) error {
			_, err := client.GetKeyState(stateKey)
			return err
		}},
		{"ActivePage", "active-sync", func(client *wallet.SatsNetDKVSClient) error {
			_, err := sdkDKVSReviewActivePage(client, prefix)
			return err
		}},
		{"PrefixRead", "prefix-read", func(client *wallet.SatsNetDKVSClient) error {
			_, err := client.ReadPrefixContext(nil, prefix)
			return err
		}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			device, _ := accountReviewDevice(t, network, "")
			client, err := device.GetDKVSClient()
			require.NoError(t, err)
			require.NoError(t, scenario.read(client), "same endpoint must be healthy before injection")
			before := device.GetWalletCatalog()
			transport := &accountReviewTamperTransport{inner: wallet.NewHTTPClient(), mode: scenario.mode, targetKey: stateKey}
			device.SetDKVSHttpClient(transport)
			client, err = device.GetDKVSClient()
			require.NoError(t, err)
			require.ErrorIs(t, scenario.read(client), dkvs.ErrInvalidSignature)
			require.Greater(t, transport.hits.Load(), int64(0), "target signature was not modified")
			require.Equal(t, before, device.GetWalletCatalog())
			require.False(t, device.GetAccountManagementStatus().Active)
			device.SetDKVSHttpClient(wallet.NewHTTPClient())
			client, err = device.GetDKVSClient()
			require.NoError(t, err)
			require.NoError(t, scenario.read(client), "same device must recover after fault removal")
		})
	}
}
