//go:build js && wasm

package main

import (
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"strings"
)

func parseRGB11AssetName(value string) (indexer.AssetName, error) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 3)
	if len(parts) != 3 || !strings.EqualFold(parts[0], "rgb11") ||
		strings.TrimSpace(parts[1]) == "" || strings.TrimSpace(parts[2]) == "" {
		return indexer.AssetName{}, wallet.ErrRGB11Inconsistent
	}
	return indexer.AssetName{
		Protocol: strings.ToLower(parts[0]),
		Type:     parts[1],
		Ticker:   parts[2],
	}, nil
}
