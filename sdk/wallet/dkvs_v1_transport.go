package wallet

import dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"

// dkvsV1HTTPTransport is an optional in-process transport used by deterministic
// SDK tests. Production NetClient does not implement it and therefore always
// exercises the public HTTP V1 contract.
type dkvsV1HTTPTransport interface {
	SendDKVSV1Get(path string, query map[string]string) ([]byte, error)
	SendDKVSV1Post(path string, body []byte) ([]byte, error)
}

// dkvsV1ConfigProvider lets an in-process transport expose its node-local
// policy and endpoint identity without synthesizing an HTTP server.
type dkvsV1ConfigProvider interface {
	DKVSClientConfigV1() (*dkvsindexer.ClientConfig, error)
}
