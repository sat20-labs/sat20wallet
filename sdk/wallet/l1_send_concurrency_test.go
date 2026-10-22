package wallet

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/stretchr/testify/require"
)

// Keep spent outputs in the listing to exercise the wallet's durable input
// locks as well as concurrent selection. Acceptance verifies real signatures.
type concurrentL1SendIndexer struct {
	IndexerRPCClient
	mutex     sync.Mutex
	outputs   map[wire.OutPoint]*wire.TxOut
	listed    []*TxOutput
	spent     map[wire.OutPoint]bool
	script    []byte
	queried   chan struct{}
	first     sync.Once
	broadcast chan struct{}
	release   chan struct{}
	firstTx   *wire.MsgTx
}

func (c *concurrentL1SendIndexer) GetUtxoListWithTicker(_ string, _ *indexer.AssetName) []*indexer.AssetsInUtxo {
	c.queried <- struct{}{}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	result := make([]*indexer.AssetsInUtxo, 0, len(c.listed))
	for _, output := range c.listed {
		result = append(result, output.Clone().ToAssetsInUtxo())
	}
	return result
}

func (c *concurrentL1SendIndexer) verify(txs []*wire.MsgTx, consume bool) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	outputs := make(map[wire.OutPoint]*wire.TxOut, len(c.outputs))
	for point, output := range c.outputs {
		outputs[point] = output
	}
	spent := make(map[wire.OutPoint]bool, len(c.spent))
	for point := range c.spent {
		spent[point] = true
	}
	for _, tx := range txs {
		for _, input := range tx.TxIn {
			if outputs[input.PreviousOutPoint] == nil || spent[input.PreviousOutPoint] {
				return fmt.Errorf("missing or already spent input %s", input.PreviousOutPoint)
			}
			spent[input.PreviousOutPoint] = true
		}
		if err := VerifySignedTx(tx, txscript.NewMultiPrevOutFetcher(outputs)); err != nil {
			return err
		}
		for i, output := range tx.TxOut {
			outputs[wire.OutPoint{Hash: tx.TxHash(), Index: uint32(i)}] = output
		}
	}
	if consume {
		c.outputs, c.spent = outputs, spent
		for _, tx := range txs {
			for i, output := range tx.TxOut {
				// Only large, plain wallet change is selectable; inscription
				// outputs and the small test payment are not fee candidates.
				if output.Value > 10_000 && bytes.Equal(output.PkScript, c.script) {
					c.listed = append(c.listed, indexer.GenerateTxOutput(tx, i))
				}
			}
		}
	}
	return nil
}

func (c *concurrentL1SendIndexer) TestRawTx_Bitcoin(raw []string) error {
	txs := make([]*wire.MsgTx, len(raw))
	for i, encoded := range raw {
		tx, err := DecodeMsgTx(encoded)
		if err != nil {
			return err
		}
		txs[i] = tx
	}
	return c.verify(txs, false)
}

func (c *concurrentL1SendIndexer) BroadCastTxs(txs []*wire.MsgTx) error {
	c.first.Do(func() {
		c.firstTx = txs[0]
		close(c.broadcast)
		<-c.release
	})
	return c.verify(txs, true)
}

func (c *concurrentL1SendIndexer) BroadCastTx(tx *wire.MsgTx) (string, error) {
	if err := c.BroadCastTxs([]*wire.MsgTx{tx}); err != nil {
		return "", err
	}
	return tx.TxID(), nil
}

func TestL1WalletSendSerializesConcurrentInscriptionSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		v3   bool
		run  func(*Manager) (*InscribeResv, error)
	}{
		{"ordx", false, func(p *Manager) (*InscribeResv, error) {
			return p.DeployTicker_ordx("concurrent", 100_000, 10_000, 1, 1)
		}},
		{"brc20", true, func(p *Manager) (*InscribeResv, error) {
			return p.DeployTicker_brc20("race", 100_000, 10_000, 0, 1)
		}},
		{"runes", false, func(p *Manager) (*InscribeResv, error) {
			return p.DeployTicker_runes("", "SAT•CONCURRENT•TEST", 'T', 100_000, 1)
		}},
		{"runes_prepare", true, func(p *Manager) (*InscribeResv, error) {
			return p.PrepareDeployTicker_runes("", "SAT•CONCURRENT•TEST", 'T', 100_000, 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			manager := safetyTestManager(t, &Channel{ChannelInDB: *NewChannelInDB()})
			manager.db = newMemoryKVDB()
			manager.inscibeMap = make(map[int64]*InscribeResv)
			manager.resvMap = make(map[int64]Reservation)
			address := manager.wallet.GetAddress()
			script, err := GetPkScriptFromAddress(address)
			require.NoError(t, err)
			funding := indexer.NewTxOutput(100_000)
			funding.OutPointStr = fmt.Sprintf("%064x:0", 1)
			funding.OutValue.PkScript = script
			client := &concurrentL1SendIndexer{
				outputs: map[wire.OutPoint]*wire.TxOut{*funding.OutPoint(): &funding.OutValue},
				listed:  []*TxOutput{funding}, spent: make(map[wire.OutPoint]bool), script: script,
				queried: make(chan struct{}, 16), broadcast: make(chan struct{}), release: make(chan struct{}),
			}
			var release sync.Once
			unblock := func() { release.Do(func() { close(client.release) }) }
			t.Cleanup(unblock)
			manager.l1IndexerClient = &IndexerRPCClientMgr{active: client}
			manager.utxoLockerL1 = NewUtxoLocker(manager.db, client, L1_NETWORK_BITCOIN)
			sent := make(chan error, 1)
			go func() {
				if test.v3 {
					_, _, err := manager.BatchSendAssetsV3([]*SendAssetInfo{{Address: address,
						AssetName: &ASSET_PLAIN_SAT, AssetAmt: indexer.NewDefaultDecimal(1000)}},
						ASSET_PLAIN_SAT.String(), 1, nil, "concurrent send regression", false)
					sent <- err
				} else {
					_, _, err := manager.BatchSendAssetsWithWallet(manager.wallet, address,
						ASSET_PLAIN_SAT.String(), "1000", 1, 1, nil)
					sent <- err
				}
			}()
			select {
			case <-client.broadcast:
			case err := <-sent:
				t.Fatalf("send did not reach broadcast: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("send did not reach broadcast")
			}
			<-client.queried
			type result struct {
				reservation *InscribeResv
				err         error
			}
			deployed := make(chan result, 1)
			go func() {
				reservation, err := test.run(manager)
				deployed <- result{reservation, err}
			}()
			prematureSelection := false
			select {
			case <-client.queried:
				prematureSelection = true
			case <-time.After(100 * time.Millisecond):
			}
			unblock()
			require.NoError(t, <-sent)
			var deployment result
			select {
			case deployment = <-deployed:
			case <-time.After(5 * time.Second):
				t.Fatal("deployment did not complete after send")
			}
			require.False(t, prematureSelection, "deployment selected inputs before the in-flight send was reserved")
			require.NoError(t, deployment.err)
			require.NotNil(t, deployment.reservation)
			for _, input := range deployment.reservation.CommitTx.TxIn {
				for _, sentInput := range client.firstTx.TxIn {
					require.NotEqual(t, sentInput.PreviousOutPoint, input.PreviousOutPoint,
						"the stale listing must not allow a second spend of the same input")
				}
				require.True(t, manager.utxoLockerL1.IsLocked(input.PreviousOutPoint.String()),
					"prepared and broadcast inscriptions must both reserve their inputs")
			}
		})
	}
}
