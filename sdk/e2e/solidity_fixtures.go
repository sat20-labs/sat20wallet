package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LoadSolidityArtifact reads checked-in compiler output. Missing or stale
// fixtures fail explicitly; E2E execution never falls back to running solc.
func LoadSolidityArtifact(sourcePath, contractName string) (SolidityArtifact, error) {
	if contractName == "" {
		return SolidityArtifact{}, errors.New("missing Solidity fixture contract name")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		return SolidityArtifact{}, err
	}
	sourceName := filepath.Base(sourcePath)
	fixturePath := filepath.Join(filepath.Dir(sourcePath), "artifacts",
		strings.TrimSuffix(sourceName, filepath.Ext(sourceName))+"."+contractName+".json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("read precompiled Solidity fixture (generate it offline): %w", err)
	}
	var artifact SolidityArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return SolidityArtifact{}, fmt.Errorf("decode Solidity fixture: %w", err)
	}
	sum := sha256.Sum256(source)
	if artifact.SourceName != sourceName || artifact.ContractName != contractName ||
		artifact.SourceSHA256 != hex.EncodeToString(sum[:]) {
		return SolidityArtifact{}, fmt.Errorf("stale or mismatched Solidity fixture %s; regenerate it offline", fixturePath)
	}
	if artifact.CompilerVersion == "" || artifact.EVMVersion != defaultSolidityEVMVersion ||
		!artifact.OptimizerEnabled || artifact.OptimizerRuns != 200 {
		return SolidityArtifact{}, fmt.Errorf("unexpected compiler profile in Solidity fixture %s", fixturePath)
	}
	if len(artifact.ABI) == 0 {
		return SolidityArtifact{}, fmt.Errorf("missing ABI in Solidity fixture %s", fixturePath)
	}
	artifact.Bytecode, err = decodeHexBytecode(artifact.BytecodeHex)
	if err != nil || len(artifact.Bytecode) == 0 {
		return SolidityArtifact{}, fmt.Errorf("invalid deploy bytecode in Solidity fixture %s: %v", fixturePath, err)
	}
	artifact.DeployedBytecode, err = decodeHexBytecode(artifact.DeployedBytecodeHex)
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("invalid deployed bytecode in Solidity fixture %s: %w", fixturePath, err)
	}
	return artifact, nil
}
