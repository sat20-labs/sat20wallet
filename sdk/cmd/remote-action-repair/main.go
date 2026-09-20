//go:build remoteactionrepair

// This maintenance command opens a stopped wallet profile, unlocks its original
// signer, and either emits a dry-run plan or advances one approved historical
// remote action through the normal ACK state machine. It never starts monitors.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func readJSON(path string, value any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeNewJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}

func run() error {
	configPath := flag.String("config", "", "wallet SDK config JSON")
	dbPath := flag.String("db", "", "stopped wallet profile database path")
	targetPath := flag.String("target", "", "exact remote-action repair target JSON")
	planPath := flag.String("plan", "", "NEW dry-run plan output")
	approvedPath := flag.String("approved", "", "reviewed plan JSON required by -apply")
	apply := flag.Bool("apply", false, "send the historical ACK through the normal state machine")
	passwordEnv := flag.String("password-env", "SAT20_WALLET_PASSWORD", "environment variable containing the wallet password")
	flag.Parse()
	if *configPath == "" || *dbPath == "" || *targetPath == "" {
		return errors.New("-config, -db and -target are required")
	}
	password := os.Getenv(*passwordEnv)
	if password == "" {
		return fmt.Errorf("wallet password environment variable %s is empty", *passwordEnv)
	}
	defer func() { password = "" }()

	var config common.Config
	if err := readJSON(*configPath, &config); err != nil {
		return err
	}
	if config.Chain != "testnet" || strings.TrimSpace(config.Env) == "" {
		return errors.New("maintenance is restricted to an explicit testnet config")
	}
	var target wallet.RemoteActionRepairTarget
	if err := readJSON(*targetPath, &target); err != nil {
		return err
	}

	database := wallet.NewKVDB(*dbPath)
	if database == nil {
		return errors.New("open wallet database")
	}
	defer database.Close()
	manager := wallet.NewManager(&config, database)
	if manager == nil {
		return errors.New("initialize wallet manager")
	}
	defer manager.Close()
	if _, err := manager.UnlockWalletForRemoteActionRepair(password); err != nil {
		return fmt.Errorf("unlock wallet: %w", err)
	}

	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		return err
	}
	if !*apply {
		if *planPath == "" {
			return errors.New("dry-run requires a NEW -plan output path")
		}
		if err := writeNewJSON(*planPath, plan); err != nil {
			return err
		}
		fmt.Println("verified historical remote action; dry-run plan written; no network request sent")
		return nil
	}
	if *approvedPath == "" {
		return errors.New("-apply requires -approved reviewed plan")
	}
	var approved wallet.RemoteActionRepairPlan
	if err := readJSON(*approvedPath, &approved); err != nil {
		return err
	}
	if approved.Target != target {
		return errors.New("approved plan target differs from -target")
	}
	if err := manager.ApplyRemoteActionRepair(approved); err != nil {
		return err
	}
	fmt.Println("historical remote action ACK completed through the normal state machine")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "repair rejected:", err)
		os.Exit(1)
	}
}
