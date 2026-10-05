package e2e

import "context"

// Context-aware transport delegates to the existing active/prefix sync
// endpoint. There is no separate per-PUT write-context request.
func (s *releaseReviewStore) SendDKVSPostContext(ctx context.Context, path string, body []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil { return nil, err }
	return s.SendDKVSPost(path, body)
}
