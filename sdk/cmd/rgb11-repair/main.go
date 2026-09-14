//go:build rgb11repair

// Offline-file verifier. Only GET requests to the fixed Testnet4 evidence API;
// never opens a wallet database, loads secrets, signs, or broadcasts.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	rgb "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type evidence struct {
	client *http.Client
	tip    int64
}

func (e *evidence) get(path string) ([]byte, error) {
	r, err := e.client.Get("https://mempool.space/testnet4/api/" + path)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("evidence HTTP %d", r.StatusCode)
	}
	return io.ReadAll(io.LimitReader(r.Body, 16<<20))
}
func (e *evidence) GetTxStatus(id string) (*rgb.BitcoinTxStatus, error) {
	b, err := e.get("tx/" + id + "/status")
	if err != nil {
		return nil, err
	}
	var s struct {
		Confirmed bool
		Height    int64  `json:"block_height"`
		Hash      string `json:"block_hash"`
	}
	if err = json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	n := int64(0)
	if s.Confirmed {
		n = e.tip - s.Height + 1
	}
	return &rgb.BitcoinTxStatus{TxID: id, Confirmed: s.Confirmed, InMempool: !s.Confirmed, BlockHeight: s.Height, BlockHash: s.Hash, Confirmations: n}, nil
}
func (e *evidence) GetOutspend(op string) (*rgb.BitcoinOutspend, error) {
	p := strings.Split(op, ":")
	if len(p) != 2 {
		return nil, errors.New("outpoint")
	}
	b, err := e.get("tx/" + p[0] + "/outspend/" + p[1])
	if err != nil {
		return nil, err
	}
	var s struct {
		Spent bool
		TxID  string `json:"txid"`
		Vin   uint32
	}
	err = json.Unmarshal(b, &s)
	return &rgb.BitcoinOutspend{Spent: s.Spent, SpendingTx: s.TxID, Vin: s.Vin}, err
}
func (e *evidence) GetRawTx(id string) ([]byte, error) {
	b, err := e.get("tx/" + id + "/hex")
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimSpace(string(b)))
}
func (e *evidence) GetUTXO(op string) (*rgb.BitcoinUTXO, error) {
	spent, err := e.GetOutspend(op)
	if err != nil {
		return nil, err
	}
	if spent.Spent {
		return nil, errors.New("spent output")
	}
	p := strings.Split(op, ":")
	b, err := e.get("tx/" + p[0])
	if err != nil {
		return nil, err
	}
	var tx struct {
		Vout []struct {
			Value  int64
			Script string `json:"scriptpubkey"`
		}
	}
	if err = json.Unmarshal(b, &tx); err != nil {
		return nil, err
	}
	v, err := strconv.Atoi(p[1])
	if err != nil || v < 0 || v >= len(tx.Vout) {
		return nil, errors.New("vout")
	}
	script, err := hex.DecodeString(tx.Vout[v].Script)
	if err != nil {
		return nil, err
	}
	status, err := e.GetTxStatus(p[0])
	if err != nil {
		return nil, err
	}
	return &rgb.BitcoinUTXO{OutPoint: op, Value: tx.Vout[v].Value, PkScript: script, Confirmations: status.Confirmations}, nil
}
func (e *evidence) GetTip() (*rgb.BitcoinTip, error) {
	b, err := e.get("blocks/tip/hash")
	return &rgb.BitcoinTip{Height: e.tip, BlockHash: strings.TrimSpace(string(b))}, err
}
func (e *evidence) Broadcast([]byte) (string, error) {
	return "", errors.New("repair cannot broadcast")
}
func read(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func run() error {
	input := flag.String("input", "", "scope export JSON")
	targetPath := flag.String("target", "", "independently reviewed exact target JSON")
	output := flag.String("patch", "", "optional NEW patch artifact; omitted is dry-run")
	flag.Parse()
	var value rgb.RepairExport
	var target rgb.SingleKeyRepairTarget
	if err := read(*input, &value); err != nil {
		return err
	}
	if err := read(*targetPath, &target); err != nil {
		return err
	}
	// The maintenance identity format is fixed to this testnet-only tool.
	if !strings.HasPrefix(target.Identity, "prd|testnet|") {
		return errors.New("only production Testnet4 repair is supported")
	}
	e := &evidence{client: &http.Client{Timeout: 30 * time.Second}}
	b, err := e.get("blocks/tip/height")
	if err != nil {
		return err
	}
	e.tip, err = strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return err
	}
	patch, err := rgb.PrepareSingleKeyRepair(context.Background(), value, target, e)
	if err != nil {
		return err
	}
	if *output != "" {
		raw, err := json.Marshal(patch)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		if _, err = f.Write(raw); err != nil {
			f.Close()
			os.Remove(*output)
			return err
		}
		if err = f.Sync(); err != nil {
			f.Close()
			os.Remove(*output)
			return err
		}
		if err = f.Close(); err != nil {
			return err
		}
	}
	fmt.Println("verified: one pending-status key; no database writes; patch artifact:", *output != "")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "repair rejected:", err)
		os.Exit(1)
	}
}
