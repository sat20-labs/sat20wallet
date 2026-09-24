//go:build rgb11discard

// This one-time maintenance command verifies and tombstones one exact invalid
// historical Direct mailbox message. It never starts wallet monitors.
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
	targetPath := flag.String("target", "", "exact C/172d discard target JSON")
	planPath := flag.String("plan", "", "NEW dry-run plan output")
	approvedPath := flag.String("approved", "", "reviewed plan JSON required by -apply")
	apply := flag.Bool("apply", false, "tombstone the exact mailbox record")
	passwordEnv := flag.String("password-env", "SAT20_WALLET_PASSWORD", "wallet password environment variable")
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
	var target wallet.RGB11DiscardTarget
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
	if _, err := manager.UnlockRGB11Discard(password); err != nil {
		return fmt.Errorf("unlock wallet: %w", err)
	}
	plan, err := manager.PlanRGB11Discard(target)
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
		fmt.Println("verified exact Direct message; dry-run plan written; no record changed")
		return nil
	}
	if *approvedPath == "" {
		return errors.New("-apply requires -approved reviewed plan")
	}
	var approved wallet.RGB11DiscardPlan
	if err := readJSON(*approvedPath, &approved); err != nil {
		return err
	}
	if approved.Target != target {
		return errors.New("approved plan target differs from -target")
	}
	if err := manager.ApplyRGB11Discard(approved); err != nil {
		return err
	}
	fmt.Println("exact Direct message tombstoned and locally marked processed")
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "discard rejected:", err)
		os.Exit(1)
	}
}
