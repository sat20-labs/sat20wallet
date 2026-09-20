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

func (e *evidence) verifyWitnessAbsent(txid string) error {
	statusResponse, err := e.client.Get("https://mempool.space/testnet4/api/tx/" + txid + "/status")
	if err != nil {
		return err
	}
	statusBody, readErr := io.ReadAll(io.LimitReader(statusResponse.Body, 4<<10))
	statusResponse.Body.Close()
	if readErr != nil {
		return readErr
	}
	switch statusResponse.StatusCode {
	case http.StatusOK:
		var status struct {
			Confirmed *bool `json:"confirmed"`
		}
		if err := json.Unmarshal(statusBody, &status); err != nil || status.Confirmed == nil {
			if err == nil {
				err = errors.New("missing confirmed field")
			}
			return fmt.Errorf("invalid witness status: %w", err)
		}
		if *status.Confirmed {
			return fmt.Errorf("witness is confirmed")
		}
	case http.StatusNotFound:
	default:
		return fmt.Errorf("witness absence not proven for status: HTTP %d", statusResponse.StatusCode)
	}
	for _, path := range []string{"tx/" + txid, "tx/" + txid + "/hex"} {
		r, err := e.client.Get("https://mempool.space/testnet4/api/" + path)
		if err != nil {
			return err
		}
		io.Copy(io.Discard, io.LimitReader(r.Body, 4<<10))
		r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			return fmt.Errorf("witness absence not proven for %s: HTTP %d", path, r.StatusCode)
		}
	}
	rawMempool, err := e.get("mempool/txids")
	if err != nil {
		return err
	}
	var txids []string
	if err := json.Unmarshal(rawMempool, &txids); err != nil {
		return fmt.Errorf("invalid mempool txid list: %w", err)
	}
	for _, candidate := range txids {
		if candidate == txid {
			return fmt.Errorf("witness is present in mempool")
		}
	}
	return nil
}

func (e *evidence) verifyOrphanReceive(pending *rgb.PendingTransfer) error {
	if pending == nil {
		return errors.New("missing rejected sender evidence")
	}
	if err := e.verifyWitnessAbsent(pending.State.WitnessTxID); err != nil {
		return err
	}
	for _, outpoint := range pending.State.InputOutPoints {
		spent, err := e.GetOutspend(outpoint)
		if err != nil {
			return err
		}
		if spent == nil || spent.Spent {
			return fmt.Errorf("original input %s is spent or unknown", outpoint)
		}
	}
	return nil
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
func writeNew(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err = f.Write(raw); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
func run() error {
	input := flag.String("input", "", "scope export JSON")
	targetPath := flag.String("target", "", "independently reviewed exact target JSON")
	output := flag.String("patch", "", "optional NEW patch artifact; omitted is dry-run")
	orphanTargetPath := flag.String("orphan-target", "", "exact orphan self-receive target JSON")
	approvedPath := flag.String("approved", "", "approved orphan repair plan JSON")
	applyOutput := flag.String("apply", "", "NEW repaired snapshot artifact; requires -approved")
	flag.Parse()
	if *orphanTargetPath != "" {
		var snapshot rgb.RGB11WalletSnapshot
		var target rgb.OrphanReceiveRepairTarget
		if err := read(*input, &snapshot); err != nil {
			return err
		}
		if err := read(*orphanTargetPath, &target); err != nil {
			return err
		}
		if !strings.HasPrefix(target.WalletID, "rgb11-") {
			return errors.New("invalid RGB11 wallet identity")
		}
		e := &evidence{client: &http.Client{Timeout: 30 * time.Second}}
		if *applyOutput == "" {
			plan, _, err := rgb.PlanOrphanReceiveRepair(&snapshot, target, e.verifyOrphanReceive)
			if err != nil {
				return err
			}
			if *output != "" {
				if err := writeNew(*output, plan); err != nil {
					return err
				}
			}
			fmt.Println("verified orphan self-receive; dry-run only; patch artifact:", *output != "")
			return nil
		}
		if *approvedPath == "" {
			return errors.New("orphan repair apply requires -approved plan")
		}
		var approved rgb.OrphanReceiveRepairPlan
		if err := read(*approvedPath, &approved); err != nil {
			return err
		}
		if approved.Target != target {
			return errors.New("approved orphan repair target does not match -orphan-target")
		}
		candidate, err := rgb.ApplyOrphanReceiveRepair(&snapshot, &approved, e.verifyOrphanReceive)
		if err != nil {
			return err
		}
		if err := writeNew(*applyOutput, candidate); err != nil {
			return err
		}
		fmt.Println("approved orphan repair written to new offline snapshot; no database import performed")
		return nil
	}
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
		if err := writeNew(*output, patch); err != nil {
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
