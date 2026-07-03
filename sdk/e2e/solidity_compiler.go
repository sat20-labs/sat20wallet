package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/sha3"
)

const defaultSolcBinary = "solc"
const defaultSolidityEVMVersion = "paris"

type SolidityArtifact struct {
	SourceName          string          `json:"sourceName"`
	ContractName        string          `json:"contractName"`
	ABI                 json.RawMessage `json:"abi"`
	Bytecode            []byte          `json:"-"`
	BytecodeHex         string          `json:"bytecode"`
	DeployedBytecode    []byte          `json:"-"`
	DeployedBytecodeHex string          `json:"deployedBytecode"`
	Warnings            []string        `json:"warnings,omitempty"`
}

type SolidityCompileOptions struct {
	SolcPath   string
	EVMVersion string
	Timeout    time.Duration
}

func CompileSolidityFile(path, contractName string, opts SolidityCompileOptions) (SolidityArtifact, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return SolidityArtifact{}, err
	}
	sourceName := filepath.Base(path)
	return CompileSoliditySource(sourceName, string(content), contractName, opts)
}

func CompileSoliditySource(sourceName, source, contractName string, opts SolidityCompileOptions) (SolidityArtifact, error) {
	if sourceName == "" {
		sourceName = "Contract.sol"
	}
	if strings.TrimSpace(source) == "" {
		return SolidityArtifact{}, errors.New("missing solidity source")
	}
	solc := opts.SolcPath
	if solc == "" {
		solc = defaultSolcBinary
	}
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	evmVersion := opts.EVMVersion
	if evmVersion == "" {
		evmVersion = defaultSolidityEVMVersion
	}

	input := solcStandardInput{
		Language: "Solidity",
		Sources: map[string]solcSource{
			sourceName: {Content: source},
		},
		Settings: solcSettings{
			Optimizer: solcOptimizer{
				Enabled: true,
				Runs:    200,
			},
			EVMVersion: evmVersion,
			OutputSelection: map[string]map[string][]string{
				"*": {
					"*": {
						"abi",
						"evm.bytecode.object",
						"evm.deployedBytecode.object",
					},
				},
			},
		},
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return SolidityArtifact{}, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, solc, "--standard-json")
	cmd.Stdin = bytes.NewReader(payload)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return SolidityArtifact{}, fmt.Errorf("solc timed out after %s", timeout)
	}
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("solc failed: %w\n%s", err, string(output))
	}

	var parsed solcStandardOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return SolidityArtifact{}, fmt.Errorf("decode solc output: %w\n%s", err, string(output))
	}
	warnings, err := solcDiagnostics(parsed.Errors)
	if err != nil {
		return SolidityArtifact{}, err
	}

	contracts := parsed.Contracts[sourceName]
	if len(contracts) == 0 {
		return SolidityArtifact{}, fmt.Errorf("solc output has no contracts for %s", sourceName)
	}
	if contractName == "" {
		if len(contracts) != 1 {
			names := make([]string, 0, len(contracts))
			for name := range contracts {
				names = append(names, name)
			}
			return SolidityArtifact{}, fmt.Errorf("contract name is required, candidates: %s", strings.Join(names, ", "))
		}
		for name := range contracts {
			contractName = name
		}
	}
	compiled, ok := contracts[contractName]
	if !ok {
		return SolidityArtifact{}, fmt.Errorf("contract %s not found in %s", contractName, sourceName)
	}
	bytecode, err := decodeHexBytecode(compiled.EVM.Bytecode.Object)
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("decode deploy bytecode: %w", err)
	}
	if len(bytecode) == 0 {
		return SolidityArtifact{}, fmt.Errorf("contract %s has empty deploy bytecode", contractName)
	}
	deployed, err := decodeHexBytecode(compiled.EVM.DeployedBytecode.Object)
	if err != nil {
		return SolidityArtifact{}, fmt.Errorf("decode deployed bytecode: %w", err)
	}

	return SolidityArtifact{
		SourceName:          sourceName,
		ContractName:        contractName,
		ABI:                 append(json.RawMessage(nil), compiled.ABI...),
		Bytecode:            bytecode,
		BytecodeHex:         "0x" + hex.EncodeToString(bytecode),
		DeployedBytecode:    deployed,
		DeployedBytecodeHex: "0x" + hex.EncodeToString(deployed),
		Warnings:            warnings,
	}, nil
}

func SolidityFunctionSelector(signature string) []byte {
	hasher := sha3.NewLegacyKeccak256()
	_, _ = hasher.Write([]byte(signature))
	var digest [32]byte
	hasher.Sum(digest[:0])
	return append([]byte(nil), digest[:4]...)
}

func SolidityArtifactID(artifact SolidityArtifact) string {
	sum := sha256.Sum256(artifact.Bytecode)
	return hex.EncodeToString(sum[:])
}

type solcStandardInput struct {
	Language string                `json:"language"`
	Sources  map[string]solcSource `json:"sources"`
	Settings solcSettings          `json:"settings"`
}

type solcSource struct {
	Content string `json:"content"`
}

type solcSettings struct {
	Optimizer       solcOptimizer                  `json:"optimizer,omitempty"`
	EVMVersion      string                         `json:"evmVersion,omitempty"`
	OutputSelection map[string]map[string][]string `json:"outputSelection"`
}

type solcOptimizer struct {
	Enabled bool `json:"enabled"`
	Runs    int  `json:"runs"`
}

type solcStandardOutput struct {
	Errors    []solcError                              `json:"errors,omitempty"`
	Contracts map[string]map[string]solcContractOutput `json:"contracts,omitempty"`
}

type solcError struct {
	Severity         string `json:"severity"`
	Type             string `json:"type"`
	FormattedMessage string `json:"formattedMessage"`
	Message          string `json:"message"`
}

type solcContractOutput struct {
	ABI json.RawMessage `json:"abi"`
	EVM struct {
		Bytecode struct {
			Object string `json:"object"`
		} `json:"bytecode"`
		DeployedBytecode struct {
			Object string `json:"object"`
		} `json:"deployedBytecode"`
	} `json:"evm"`
}

func solcDiagnostics(errors []solcError) ([]string, error) {
	var warnings []string
	var fatal []string
	for _, item := range errors {
		msg := item.FormattedMessage
		if msg == "" {
			msg = item.Message
		}
		msg = strings.TrimSpace(msg)
		if item.Severity == "error" {
			fatal = append(fatal, msg)
		} else if msg != "" {
			warnings = append(warnings, msg)
		}
	}
	if len(fatal) != 0 {
		return warnings, fmt.Errorf("solc errors:\n%s", strings.Join(fatal, "\n"))
	}
	return warnings, nil
}

func decodeHexBytecode(value string) ([]byte, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	if value == "" {
		return nil, nil
	}
	return hex.DecodeString(value)
}
