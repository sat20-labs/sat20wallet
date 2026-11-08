package e2e

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// Observe the existing real-node workflow. Bodies, signatures, secrets and raw
// account/key identifiers never enter the log; every HTTP call is forwarded once.
type accountBackupTrace struct {
	t         *testing.T
	inner     wallet.HttpClient
	db        indexercommon.KVDB
	namespace string
	device    string
}

func accountTraceTag(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", digest[:6])
}

func (p *accountBackupTrace) SendGetRequest(u *wallet.URL) ([]byte, error) {
	return p.inner.SendGetRequest(u)
}

func (p *accountBackupTrace) SendGetRequestContext(ctx context.Context, u *wallet.URL) ([]byte, error) {
	if inner, ok := p.inner.(wallet.ContextHttpClient); ok {
		return inner.SendGetRequestContext(ctx, u)
	}
	return p.inner.SendGetRequest(u)
}

func (p *accountBackupTrace) SendPostRequest(u *wallet.URL, body []byte) ([]byte, error) {
	return p.SendPostRequestContext(context.Background(), u, body)
}

func (p *accountBackupTrace) SendPostRequestContext(ctx context.Context, u *wallet.URL, body []byte) ([]byte, error) {
	start := time.Now().UnixNano()
	batch := strings.HasSuffix(u.Path, "/records/batch-cas")
	active := strings.HasSuffix(u.Path, "/active/sync")
	var request struct {
		RequestID     string `json:"request_id"`
		Authorization *struct {
			Context dkvs.WalletWriteContext `json:"context"`
		} `json:"authorization"`
		Mutations []struct {
			Record struct {
				Key string `json:"key"`
				Seq uint64 `json:"seq"`
			} `json:"record"`
		} `json:"mutations"`
		Scope dkvs.ActiveScope `json:"scope"`
		After uint64           `json:"after"`
		Full  bool             `json:"full"`
	}
	if batch || active {
		if err := json.Unmarshal(body, &request); err != nil {
			p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=decode-request-failed", start, p.device)
		}
	}
	requestTag := accountTraceTag(request.RequestID)
	if batch {
		for _, mutation := range request.Mutations {
			p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=send request_tag=%s key_tag=%s seq=%d",
				start, p.device, requestTag, accountTraceTag(mutation.Record.Key), mutation.Record.Seq)
		}
		if request.Authorization != nil {
			for _, prefix := range request.Authorization.Context.Prefixes {
				p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=authorize request_tag=%s endpoint_tag=%s prefix_tag=%s signed_generation=%d",
					start, p.device, requestTag, accountTraceTag(request.Authorization.Context.EndpointID), accountTraceTag(prefix.Prefix), prefix.Generation)
			}
		}
	}
	var raw []byte
	var err error
	if inner, ok := p.inner.(wallet.ContextHttpClient); ok {
		raw, err = inner.SendPostRequestContext(ctx, u, body)
	} else {
		raw, err = p.inner.SendPostRequest(u, body)
	}
	if !batch && !active {
		return raw, err
	}
	responseBytes := raw
	var httpError *wallet.HTTPResponseError
	if errors.As(err, &httpError) {
		responseBytes = httpError.Body
	}
	var response struct {
		Code      int    `json:"code"`
		ErrorCode string `json:"error_code"`
		Data      struct {
			PrefixStates []dkvs.PrefixGeneration `json:"prefix_states"`
			Meta         dkvs.ActiveMeta         `json:"meta"`
		} `json:"data"`
	}
	decoded := json.Unmarshal(responseBytes, &response) == nil
	end := time.Now().UnixNano()
	if batch {
		p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=response request_tag=%s start_ns=%d decoded=%t code=%d error_code=%s error_type=%T",
			end, p.device, requestTag, start, decoded, response.Code, response.ErrorCode, err)
		for _, prefix := range response.Data.PrefixStates {
			p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=ack request_tag=%s prefix_tag=%s ack_generation=%d",
				end, p.device, requestTag, accountTraceTag(prefix.Prefix), prefix.Generation)
		}
	} else {
		p.t.Logf("account wire diagnostic: at_ns=%d device=%s phase=receive start_ns=%d prefix_tag=%s after=%d full=%t decoded=%t generation=%d error_code=%s error_type=%T",
			end, p.device, start, accountTraceTag(request.Scope.Prefix), request.After, request.Full, decoded, response.Data.Meta.Generation, response.ErrorCode, err)
	}
	return raw, err
}

func (p *accountBackupTrace) checkpoint(phase string, cause error) {
	code := ""
	if cause != nil {
		code = string(dkvs.ErrorCodeOf(cause))
	}
	p.t.Logf("account wait diagnostic: at_ns=%d device=%s phase=%s error_code=%s error_type=%T",
		time.Now().UnixNano(), p.device, phase, code, cause)
	store := dkvscore.NewReplicaStore(p.db)
	entries, err := store.LoadOutbox(p.namespace)
	p.t.Logf("account wait diagnostic: at_ns=%d device=%s phase=%s outbox_count=%d outbox_read_failed=%t",
		time.Now().UnixNano(), p.device, phase, len(entries), err != nil)
	for _, entry := range entries {
		p.t.Logf("account wait diagnostic: at_ns=%d device=%s phase=%s request_tag=%s key_tag=%s state=%s attempts=%d error_code=%s local_count=%d",
			time.Now().UnixNano(), p.device, phase, accountTraceTag(entry.RequestID), accountTraceTag(entry.OriginKey), entry.State, entry.Attempts, entry.LastErrorCode, entry.OriginGeneration)
	}
	state, err := store.LoadSubscriptionState(p.namespace)
	if err != nil || state == nil {
		p.t.Logf("account wait diagnostic: at_ns=%d device=%s phase=%s cursor_read_failed=%t",
			time.Now().UnixNano(), p.device, phase, err != nil)
		return
	}
	for _, prefix := range state.Prefixes {
		generation, known := state.Generations[prefix]
		p.t.Logf("account wait diagnostic: at_ns=%d device=%s phase=%s prefix_tag=%s cursor_generation=%d known=%t",
			time.Now().UnixNano(), p.device, phase, accountTraceTag(prefix), generation, known)
	}
}
