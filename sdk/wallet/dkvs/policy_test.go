package dkvs

import (
	"errors"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestApplyFreeLocalRetentionUsesServiceNodePolicy(t *testing.T) {
	options := dkvsindexer.RecordOptions{TTL: 12}
	if err := ApplyFreeLocalRetention(&FreeLocalPolicy{Enabled: true, MaxTTL: 777}, &options); err != nil {
		t.Fatal(err)
	}
	if options.TTL != 777 {
		t.Fatalf("TTL=%d", options.TTL)
	}
	if err := ApplyFreeLocalRetention(&FreeLocalPolicy{}, &options); !errors.Is(err, dkvsindexer.ErrFreeLocalDisabled) {
		t.Fatalf("disabled policy err=%v", err)
	}
}
