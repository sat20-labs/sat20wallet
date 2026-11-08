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
	sourceName := filepath.Base(sourcePath)
	fixturePath := filepath.Join(filepath.Dir(sourcePath), "artifacts",
		strings.TrimSuffix(sourceName, filepath.Ext(sourceName))+"."+contractName+".json")
	bundle, err := readE2EArtifactBundle()
	if err != nil {
		return SolidityArtifact{}, err
	}
	var sourceHash string
	if bundle == nil {
		source, err := os.ReadFile(sourcePath)
		if err != nil {
			return SolidityArtifact{}, err
		}
		sum := sha256.Sum256(source)
		sourceHash = hex.EncodeToString(sum[:])
	} else {
		// The Mac builder verifies source bytes against compiler output before
		// packaging. Only the compiled JSON and source hash cross to the runner.
		sourceHash = bundle.SoliditySources[filepath.ToSlash(filepath.Clean(sourcePath))]
		if decoded, err := hex.DecodeString(sourceHash); err != nil || len(decoded) != sha256.Size {
			return SolidityArtifact{}, fmt.Errorf("missing build-verified Solidity source hash: %s", sourcePath)
		}
		fixturePath, err = bundle.file("solidity/" + filepath.Base(fixturePath))
		if err != nil {
			return SolidityArtifact{}, err
		}
	}
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("read precompiled Solidity fixture (generate it offline): %w", err)
	}
	var artifact SolidityArtifact
	if err := json.Unmarshal(raw, &artifact); err != nil {
		return SolidityArtifact{}, fmt.Errorf("decode Solidity fixture: %w", err)
	}
	if artifact.SourceName != sourceName || artifact.ContractName != contractName ||
		artifact.SourceSHA256 != sourceHash {
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
