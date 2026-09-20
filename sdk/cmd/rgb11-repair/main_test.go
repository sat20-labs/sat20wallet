//go:build rgb11repair

package main

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)
type routeResponse struct {
	status int
	body   string
}

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func repairHTTPClient(routes map[string]routeResponse) *http.Client {
	return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		response, ok := routes[request.URL.Path]
		if !ok {
			response.status = http.StatusInternalServerError
		}
		return &http.Response{
			StatusCode: response.status,
			Body:       io.NopCloser(strings.NewReader(response.body)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}
}

func TestVerifyWitnessAbsentAcceptsUnconfirmedStatusWithoutTransaction(t *testing.T) {
	const txid = "abababababababababababababababababababababababababababababababab"
	client := repairHTTPClient(map[string]routeResponse{
		"/testnet4/api/tx/" + txid + "/status": {status: http.StatusOK, body: `{"confirmed":false}`},
		"/testnet4/api/tx/" + txid:             {status: http.StatusNotFound},
		"/testnet4/api/tx/" + txid + "/hex":    {status: http.StatusNotFound},
		"/testnet4/api/mempool/txids":          {status: http.StatusOK, body: `["cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"]`},
	})

	if err := (&evidence{client: client}).verifyWitnessAbsent(txid); err != nil {
		t.Fatalf("status confirmed=false with no transaction or mempool entry must prove absence: %v", err)
	}
}

func TestVerifyWitnessAbsentRequiresEveryAbsenceSignal(t *testing.T) {
	const txid = "abababababababababababababababababababababababababababababababab"
	paths := struct{ status, tx, hex, mempool string }{
		status:  "/testnet4/api/tx/" + txid + "/status",
		tx:      "/testnet4/api/tx/" + txid,
		hex:     "/testnet4/api/tx/" + txid + "/hex",
		mempool: "/testnet4/api/mempool/txids",
	}
	base := func() map[string]routeResponse {
		return map[string]routeResponse{
			paths.status:  {status: http.StatusOK, body: `{"confirmed":false}`},
			paths.tx:      {status: http.StatusNotFound},
			paths.hex:     {status: http.StatusNotFound},
			paths.mempool: {status: http.StatusOK, body: `[]`},
		}
	}
	tests := []struct {
		name   string
		change func(map[string]routeResponse)
	}{
		{name: "confirmed status", change: func(routes map[string]routeResponse) {
			routes[paths.status] = routeResponse{status: http.StatusOK, body: `{"confirmed":true}`}
		}},
		{name: "transaction detail exists", change: func(routes map[string]routeResponse) {
			routes[paths.tx] = routeResponse{status: http.StatusOK, body: `{}`}
		}},
		{name: "transaction hex exists", change: func(routes map[string]routeResponse) {
			routes[paths.hex] = routeResponse{status: http.StatusOK, body: `00`}
		}},
		{name: "mempool contains witness", change: func(routes map[string]routeResponse) {
			routes[paths.mempool] = routeResponse{status: http.StatusOK, body: `["` + txid + `"]`}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			routes := base()
			test.change(routes)
			if err := (&evidence{client: repairHTTPClient(routes)}).verifyWitnessAbsent(txid); err == nil {
				t.Fatal("witness absence accepted without every required signal")
			}
		})
	}
}
