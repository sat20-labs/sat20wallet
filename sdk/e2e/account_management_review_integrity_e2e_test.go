package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type accountReviewTamperTransport struct {
	inner wallet.HttpClient
	mode  string
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

func accountReviewCorruptRecord(record *wire.DKVSRecord) {
	if record == nil || len(record.Signature) == 0 {
		return
	}
	record.Signature = append([]byte(nil), record.Signature...)
	record.Signature[0] ^= 0x01
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
		accountReviewCorruptRecord(&record)
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
		accountReviewCorruptRecord(state.Record)
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
		accountReviewCorruptRecord(result.Records[0])
		for index := range result.KeyStates {
			if result.KeyStates[index].Key == result.Records[0].Key {
				result.KeyStates[index].ETag = dkvs.RecordHash(result.Records[0]).String()
				result.KeyStates[index].Record = result.Records[0]
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
		accountReviewCorruptRecord(page.Records[0])
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
			device.SetDKVSHttpClient(&accountReviewTamperTransport{inner: wallet.NewHTTPClient(), mode: scenario.mode})
			client, err := device.GetDKVSClient()
			require.NoError(t, err)
			err = scenario.read(client)
			require.Error(t, err, "SDK must reject a server record whose author signature was changed even when hashes/ETags are recomputed")
		})
	}
}
