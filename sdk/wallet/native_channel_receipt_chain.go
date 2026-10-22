package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"math"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	stxscript "github.com/sat20-labs/satoshinet/txscript"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// This request-local cache records the canonical facts used by a public
// settlement receipt. Neither an indexer row nor a transaction ID supplied by
// the peer substitutes for inclusion in the actual canonical block.
type nativeSettlementChains struct {
	manager           *Manager
	ctx               context.Context
	bitcoin           rgb11wallet.BitcoinEvidenceProvider
	bitcoinHeadersRPC interface {
		GetBitcoinBlockHeader(int) (*indexerwire.BitcoinBlockHeader, error)
	}
	tip            *rgb11wallet.BitcoinTip
	l1             map[string]*wire.MsgTx
	l2             map[string]*swire.MsgTx
	l2Heights      map[string]int
	bitcoinHeaders map[int64]string
	satoshiHeaders map[int]string
}

// Capture the selected RPC client for the entire proof. Header-aware clients
// expose the same canonical fact directly; the general client already has the
// canonical block-hash and synchronized-height methods, so no new RPC route is
// required for native assets.
type nativeSettlementBitcoinHeaders struct{ client IndexerRPCClient }

func (h nativeSettlementBitcoinHeaders) GetBitcoinBlockHeader(height int) (*indexerwire.BitcoinBlockHeader, error) {
	if h.client == nil || height < 0 {
		return nil, fmt.Errorf("canonical Bitcoin header evidence unavailable")
	}
	if client, ok := h.client.(interface {
		GetBitcoinBlockHeader(int) (*indexerwire.BitcoinBlockHeader, error)
	}); ok {
		return client.GetBitcoinBlockHeader(height)
	}
	tip := h.client.GetSyncHeight()
	if tip < height {
		return nil, fmt.Errorf("canonical Bitcoin header is above the synchronized tip")
	}
	hash, err := h.client.GetBlockHash(height)
	if err != nil {
		return nil, err
	}
	if !canonicalNativeTxID(hash) {
		return nil, fmt.Errorf("invalid canonical Bitcoin block hash")
	}
	return &indexerwire.BitcoinBlockHeader{Height: int64(height), Hash: hash, Confirmations: tip - height + 1}, nil
}

func (p *Manager) newNativeSettlementChains(ctx context.Context, owner common.Wallet) (*nativeSettlementChains, error) {
	scope, err := p.stpRGB11Scope(owner)
	if err != nil {
		return nil, err
	}
	client := p.l1IndexerClient.getActiveIndexer()
	headers := nativeSettlementBitcoinHeaders{client: client}
	evidence := scope.evidence
	tip, err := evidence.GetTip()
	if err != nil || tip == nil || tip.Height <= 0 || tip.BlockHash == "" {
		return nil, fmt.Errorf("native settlement Bitcoin tip unavailable: %w", err)
	}
	return &nativeSettlementChains{manager: p, ctx: ctx, bitcoin: evidence, bitcoinHeadersRPC: headers, tip: tip,
		l1: make(map[string]*wire.MsgTx), l2: make(map[string]*swire.MsgTx), l2Heights: make(map[string]int),
		bitcoinHeaders: make(map[int64]string), satoshiHeaders: make(map[int]string)}, nil
}

func canonicalNativeTxID(id string) bool {
	raw, err := hex.DecodeString(id)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == id
}

func (c *nativeSettlementChains) bitcoinTx(id string, confirmed bool) (*wire.MsgTx, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if !canonicalNativeTxID(id) {
		return nil, fmt.Errorf("invalid native settlement Bitcoin transaction ID")
	}
	tx := c.l1[id]
	if tx == nil {
		raw, err := c.bitcoin.GetRawTx(id)
		if err != nil {
			return nil, err
		}
		tx = wire.NewMsgTx(2)
		reader := bytes.NewReader(raw)
		if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || tx.TxID() != id {
			return nil, fmt.Errorf("native settlement Bitcoin raw transaction mismatch")
		}
		c.l1[id] = tx
	}
	if confirmed {
		status, err := c.bitcoin.GetTxStatus(id)
		if err != nil {
			return nil, err
		}
		if status == nil || status.TxID != id || !status.Confirmed || status.InMempool ||
			status.BlockHeight <= 0 || status.BlockHeight > c.tip.Height || status.Confirmations <= 0 {
			return nil, fmt.Errorf("native settlement Bitcoin transaction is not confirmed")
		}
		header, err := c.bitcoinHeadersRPC.GetBitcoinBlockHeader(int(status.BlockHeight))
		if err != nil {
			return nil, err
		}
		if header == nil || header.Height != status.BlockHeight || header.Hash != status.BlockHash || header.Confirmations <= 0 {
			return nil, fmt.Errorf("native settlement Bitcoin transaction has no active header")
		}
		if previous := c.bitcoinHeaders[status.BlockHeight]; previous != "" && previous != header.Hash {
			return nil, fmt.Errorf("native settlement Bitcoin chain changed during validation")
		}
		c.bitcoinHeaders[status.BlockHeight] = header.Hash
	}
	return tx, nil
}

func (c *nativeSettlementChains) bitcoinOutput(point string) (*wire.TxOut, error) {
	op, err := wire.NewOutPointFromString(point)
	if err != nil || op == nil || op.String() != point {
		return nil, fmt.Errorf("invalid native settlement outpoint")
	}
	tx, err := c.bitcoinTx(op.Hash.String(), false)
	if err != nil {
		return nil, err
	}
	if uint64(op.Index) >= uint64(len(tx.TxOut)) || tx.TxOut[op.Index] == nil {
		return nil, fmt.Errorf("native settlement outpoint is absent from its Bitcoin transaction")
	}
	return tx.TxOut[op.Index], nil
}

func (c *nativeSettlementChains) verifyBitcoinSpend(tx *wire.MsgTx) error {
	if tx == nil || len(tx.TxIn) == 0 {
		return fmt.Errorf("native settlement has no Bitcoin inputs")
	}
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	seen := make(map[wire.OutPoint]bool)
	for _, input := range tx.TxIn {
		if input == nil || seen[input.PreviousOutPoint] {
			return fmt.Errorf("duplicate native settlement Bitcoin input")
		}
		seen[input.PreviousOutPoint] = true
		previous, err := c.bitcoinOutput(input.PreviousOutPoint.String())
		if err != nil {
			return err
		}
		fetcher.AddPrevOut(input.PreviousOutPoint, previous)
	}
	return VerifySignedTx(tx, fetcher)
}

func (c *nativeSettlementChains) satoshiTx(id string) (*swire.MsgTx, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if !canonicalNativeTxID(id) {
		return nil, fmt.Errorf("invalid native settlement SatoshiNet transaction ID")
	}
	if tx := c.l2[id]; tx != nil {
		return tx, nil
	}
	client := c.manager.l2IndexerClient
	if client == nil {
		return nil, fmt.Errorf("native settlement SatoshiNet evidence unavailable")
	}
	info, err := client.GetTxInfo(id)
	if err != nil {
		return nil, err
	}
	if info == nil || info.TxID != id || info.Confirmations <= 0 || info.BlockHeight <= 0 ||
		info.BlockHeight > math.MaxInt32 || client.GetSyncHeight() < int(info.BlockHeight) {
		return nil, fmt.Errorf("native settlement SatoshiNet transaction is not confirmed")
	}
	height := int(info.BlockHeight)
	hash, err := client.GetBlockHash(height)
	if err != nil || !canonicalNativeTxID(hash) {
		return nil, fmt.Errorf("native settlement canonical L2 block unavailable: %w", err)
	}
	raw, err := client.GetBlock(hash)
	if err != nil {
		return nil, err
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var block swire.MsgBlock
	reader := bytes.NewReader(decoded)
	if err := block.Deserialize(reader); err != nil || reader.Len() != 0 || block.BlockHash().String() != hash || len(block.Transactions) == 0 {
		return nil, fmt.Errorf("native settlement canonical L2 block is malformed")
	}
	var result *swire.MsgTx
	tree := newRollingMerkleTreeStore(uint64(len(block.Transactions)))
	for _, tx := range block.Transactions {
		if tx == nil {
			return nil, fmt.Errorf("native settlement canonical L2 block contains a nil transaction")
		}
		tree.add(tx.TxHash())
		if tx.TxID() == id {
			if result != nil {
				return nil, fmt.Errorf("native settlement transaction is repeated in its L2 block")
			}
			result = tx
		}
	}
	if result == nil || tree.calcRoot() != block.Header.MerkleRoot {
		return nil, fmt.Errorf("native settlement transaction is not committed by its canonical L2 block")
	}
	if previous := c.satoshiHeaders[height]; previous != "" && previous != hash {
		return nil, fmt.Errorf("native settlement canonical L2 chain changed during validation")
	}
	c.satoshiHeaders[height], c.l2[id] = hash, result
	c.l2Heights[id] = height
	return result, nil
}

func (c *nativeSettlementChains) satoshiPrevious(tx *swire.MsgTx) (*stxscript.MultiPrevOutFetcher, error) {
	fetcher := stxscript.NewMultiPrevOutFetcher(nil)
	seen := make(map[swire.OutPoint]bool)
	for _, input := range tx.TxIn {
		if input == nil || seen[input.PreviousOutPoint] {
			return nil, fmt.Errorf("duplicate native settlement L2 input")
		}
		seen[input.PreviousOutPoint] = true
		raw, err := c.manager.l2IndexerClient.GetRawTx(input.PreviousOutPoint.Hash.String())
		if err != nil {
			return nil, err
		}
		previous, err := DecodeMsgTx_SatsNet(raw)
		if err != nil || previous.TxHash() != input.PreviousOutPoint.Hash || uint64(input.PreviousOutPoint.Index) >= uint64(len(previous.TxOut)) {
			return nil, fmt.Errorf("native settlement L2 prevout does not match its signed input")
		}
		fetcher.AddPrevOut(input.PreviousOutPoint, previous.TxOut[input.PreviousOutPoint.Index])
	}
	return fetcher, nil
}

func (c *nativeSettlementChains) stable() error {
	for height, hash := range c.satoshiHeaders {
		actual, err := c.manager.l2IndexerClient.GetBlockHash(height)
		if err != nil || actual != hash || c.manager.l2IndexerClient.GetSyncHeight() < height {
			return fmt.Errorf("native settlement canonical L2 chain changed during validation: %w", err)
		}
	}
	for height, hash := range c.bitcoinHeaders {
		header, err := c.bitcoinHeadersRPC.GetBitcoinBlockHeader(int(height))
		if err != nil || header == nil || header.Hash != hash || header.Confirmations <= 0 {
			return fmt.Errorf("native settlement Bitcoin header changed during validation: %w", err)
		}
	}
	tip, err := c.bitcoin.GetTip()
	if err != nil || tip == nil || tip.Height != c.tip.Height || tip.BlockHash != c.tip.BlockHash {
		return fmt.Errorf("native settlement Bitcoin tip changed during validation: %w", err)
	}
	return c.ctx.Err()
}
