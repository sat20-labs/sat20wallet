package e2e

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	rgbwallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Exercise the actual wallet-facing Core router and the independently signed
// indexer readback. No management endpoint, client authority or handler stub.
func TestRGBPublicRouteE2E(t *testing.T) {
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	runRGBPublicRouteE2E(t, network)
}

func runRGBPublicRouteE2E(t *testing.T, network *realSatoshiNet) {
	t.Helper()
	// The public listener starts before the Core accepts protocol requests.
	healthClient := &http.Client{Timeout: time.Second}
	require.Eventually(t, func() bool {
		response, err := healthClient.Get("http://" + network.Core.stpAddr + "/health")
		if err != nil {
			return false
		}
		defer response.Body.Close()
		return response.StatusCode == http.StatusOK
	}, 15*time.Second, 100*time.Millisecond, "CoreNode STP public service was not ready at %s; node log=%s", network.Core.stpAddr, network.Core.LogFile())
	endpoint := "http://" + network.Core.stpAddr + "/testnet/v3/rgb11/contracts/register"
	t.Run("invalid_contract", func(t *testing.T) {
		body, err := json.Marshal(map[string]string{"contract_consignment_base64": base64.StdEncoding.EncodeToString([]byte("not a contract"))})
		require.NoError(t, err)
		request, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
		require.NoError(t, err)
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
		var result struct {
			Code int `json:"code"`
		}
		require.NoError(t, json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result))
		require.NotZero(t, result.Code)
	})
	t.Run("success", func(t *testing.T) {
		// This is a native-generated public technical contract. Spendability
		// and carrier ownership remain covered by the original NIA workflow.
		seed := sha256.Sum256([]byte(t.Name()))
		seal, err := seals.NewGraphBlindSeal(seed[:], 0, 7)
		require.NoError(t, err)
		issued, err := issuance.Issue(issuance.Spec{
			Kind: schemas.NIA, Network: issuance.BitcoinTestnet4,
			Ticker: "RGP", Name: "Public route control", Precision: 0,
			Allocations: []issuance.Allocation{{Seal: seal, Amount: 7}},
		})
		require.NoError(t, err)
		private, err := consignment.EncodeFile(issued.Container)
		require.NoError(t, err)
		public, err := rgbwallet.PublicRGB11Contract(private)
		require.NoError(t, err)
		_, expected, err := rgbwallet.NewRGB11ContractValue(public)
		require.NoError(t, err)
		registryKey, err := rgbwallet.RGB11RegistryKey(expected.ContractID)
		require.NoError(t, err)
		reader := dkvsClientForNode(t, network.Core)
		_, err = reader.GetRecordDirect(registryKey)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound, "the case-specific registration key must start absent")
		core := wallet.NewNodeClient("http", network.Core.stpAddr, "/testnet", wallet.NewHTTPClient())
		registered, err := core.RegisterPublicRGB11(public)
		require.NoError(t, err)
		require.Equal(t, *expected, *registered)
		raw, err := network.Core.IndexerURL("testnet")
		require.NoError(t, err)
		indexer, err := url.Parse(raw)
		require.NoError(t, err)
		readbackClient := wallet.NewSatsNetDKVSClient(indexer.Scheme, indexer.Host, strings.Trim(indexer.Path, "/"), wallet.NewHTTPClient())
		readback, err := readbackClient.GetRGB11Registration(expected.ContractID)
		require.NoError(t, err)
		require.Equal(t, *expected, *readback)
		record, err := reader.GetRecordDirect(registryKey)
		require.NoError(t, err)
		require.Equal(t, uint64(1), record.Seq)
		t.Logf("dkvs-shared-rgb-route-state: key=%s registration=absent->present seq=0->%d hash=%s",
			registryKey, record.Seq, dkvs.RecordHash(record).String())
	})
}
