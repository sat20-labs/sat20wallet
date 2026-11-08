package wallet

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// The client reads and verifies signed endpoint responses. The shared memory
// endpoint applies real signatures, exact CAS preconditions and sequences;
// the Core E2E separately verifies admission and no-op traffic on real nodes.
func TestCoreBindingNoop(t *testing.T) {
	previous := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previous })
	for _, name := range []string{"same", "missing", "capability", "core_change", "bad_signature", "read_failure", "wrong_endpoint"} {
		t.Run(name, func(t *testing.T) {
			root := dkvsTestWalletFromPriv(t, exampleDKVSPriv(41))
			coreID := hex.EncodeToString(exampleDKVSPriv(42).PubKey().SerializeCompressed())
			remote := newRGB11MemoryDKVSHTTP()
			remote.endpointID, remote.bestHeight = coreID, 7
			accountID, err := dkvsAccountID(root)
			require.NoError(t, err)
			key, err := dkvs.AccountMappingKey(GetChainParam().Name, root.GetAddress())
			require.NoError(t, err)
			desired := dkvs.AccountServiceDescriptor{AccountID: accountID, CoreNodeID: coreID, Capabilities: accountServiceCapabilityRGB11Direct}
			value, err := dkvs.EncodeAccountServiceDescriptor(desired)
			require.NoError(t, err)
			beforeHash := ""
			if name != "missing" {
				existing := desired
				existing.Capabilities |= 1 << 9
				existing.Extensions = []dkvs.AccountServiceExtension{{Type: 7, Value: []byte("preserved")}}
				if name == "capability" {
					existing.Capabilities &^= accountServiceCapabilityRGB11Direct
				}
				if name == "core_change" {
					existing.CoreNodeID = hex.EncodeToString(exampleDKVSPriv(43).PubKey().SerializeCompressed())
				}
				encoded, err := dkvs.EncodeAccountServiceDescriptor(existing)
				require.NoError(t, err)
				record, err := NewDKVSAccountSignedRecord(root, key, encoded, dkvs.RecordOptions{Seq: 4, IssueHeight: 7})
				require.NoError(t, err)
				if name == "bad_signature" {
					record.Signature[0] ^= 1
				}
				remote.records[key] = record
				beforeHash = dkvs.RecordHash(record).String()
			}
			gets, posts := 0, 0
			keyMatched := true
			var captured DKVSBatchCASRequest
			injected := errors.New("binding record read unavailable")
			transport := &reviewAcceptanceHTTP{rgb11MemoryDKVSHTTP: remote}
			transport.get = func(ctx context.Context, path string, query map[string]string) ([]byte, error) {
				if path == "/v3/dkvs/record" {
					gets++
					keyMatched = keyMatched && query["key"] == key
					if name == "read_failure" {
						return nil, injected
					}
				}
				return remote.SendDKVSGetContext(ctx, path, query)
			}
			transport.post = func(ctx context.Context, path string, body []byte) ([]byte, error) {
				if path == "/v3/dkvs/records/batch-cas" {
					posts++
					if err := json.Unmarshal(body, &captured); err != nil {
						return nil, err
					}
				}
				return remote.SendDKVSPostContext(ctx, path, body)
			}
			client := NewSatsNetDKVSClient("http", "binding.test", "testnet", transport)
			target := coreID
			if name == "wrong_endpoint" {
				target = hex.EncodeToString(exampleDKVSPriv(44).PubKey().SerializeCompressed())
			}
			result, err := client.acceptCoreBinding(root, key, target, value)
			if name == "read_failure" || name == "bad_signature" || name == "wrong_endpoint" {
				require.Error(t, err)
				require.Nil(t, result)
				if name == "read_failure" {
					require.ErrorIs(t, err, injected)
				}
				if name == "wrong_endpoint" {
					require.ErrorIs(t, err, dkvs.ErrEndpointMismatch)
					require.Zero(t, gets)
				} else {
					require.Equal(t, 1, gets)
				}
				require.Zero(t, posts)
				require.Equal(t, beforeHash, dkvs.RecordHash(remote.records[key]).String())
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, keyMatched)
			require.Zero(t, result.TTL, "binding has no renewable lease")
			_, _, descriptor, err := dkvs.ValidateAccountMappingBindingRecord(result)
			require.NoError(t, err)
			require.Equal(t, accountID, descriptor.AccountID)
			require.Equal(t, coreID, descriptor.CoreNodeID)
			require.NotZero(t, descriptor.Capabilities&accountServiceCapabilityRGB11Direct)
			if name == "same" {
				require.Equal(t, 1, gets, "same value still requires an actual verified endpoint read")
				require.Zero(t, posts, "identical binding must not issue a CAS")
				require.Equal(t, beforeHash, dkvs.RecordHash(result).String())
			} else {
				require.Equal(t, 2, gets, "changed binding must be independently read back")
				require.Equal(t, 1, posts)
				require.Equal(t, coreID, captured.EndpointID)
				require.NotEmpty(t, captured.RequestID)
				require.Len(t, captured.Mutations, 1)
				mutation := captured.Mutations[0]
				require.Equal(t, key, mutation.Record.Key)
				require.Equal(t, dkvs.RecordHash(result), dkvs.RecordHash(mutation.Record))
				if name == "missing" {
					require.True(t, mutation.ExpectAbsent)
					require.Empty(t, mutation.ExpectedETag)
					require.Equal(t, uint64(1), result.Seq)
				} else {
					require.False(t, mutation.ExpectAbsent)
					require.Equal(t, beforeHash, mutation.ExpectedETag)
					require.Equal(t, uint64(5), result.Seq)
				}
			}
			if name != "missing" {
				require.NotZero(t, descriptor.Capabilities&(1<<9), "unrelated capabilities were lost")
				require.Equal(t, []dkvs.AccountServiceExtension{{Type: 7, Value: []byte("preserved")}}, descriptor.Extensions)
			}
			priorGets, priorPosts := gets, posts
			again, err := client.acceptCoreBinding(root, key, coreID, value)
			require.NoError(t, err)
			require.Equal(t, priorGets+1, gets, "repeat must read the endpoint, not a cached binding")
			require.Equal(t, priorPosts, posts, "repeat must not publish an unchanged record")
			require.Equal(t, dkvs.RecordHash(result), dkvs.RecordHash(again))
		})
	}
}
