package e2e

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSolidityFixturesLoadWithoutCompiler(t *testing.T) {
	// A deliberately empty PATH proves loading cannot silently invoke solc.
	t.Setenv("PATH", t.TempDir())
	paths, err := filepath.Glob("testdata/contracts/artifacts/*.json")
	require.NoError(t, err)
	require.NotEmpty(t, paths)
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var fixture SolidityArtifact
			require.NoError(t, json.Unmarshal(raw, &fixture))
			artifact, err := LoadSolidityArtifact(filepath.Join("testdata/contracts", fixture.SourceName), fixture.ContractName)
			require.NoError(t, err)
			require.NotEmpty(t, artifact.Bytecode)
			require.NotEmpty(t, artifact.DeployedBytecode)
		})
	}
}

func TestSolidityFixturesRejectStaleOrInvalidOutput(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	// Exercise the source-hash path even when other E2E tests use a bundle.
	t.Setenv(prebuiltE2EEnv, "")
	// Independent loader fixture, not a copy of a repository contract. The
	// hand-assembled init code deploys STOP, matching this payable fallback.
	source := []byte("pragma solidity ^0.8.0; contract LoaderFixture { fallback() external payable { assembly { stop() } } }\n")
	fixture := SolidityArtifact{
		SourceName: "LoaderFixture.sol", SourceSHA256: fmt.Sprintf("%x", sha256.Sum256(source)),
		ContractName: "LoaderFixture", CompilerVersion: "synthetic-loader-fixture",
		EVMVersion: defaultSolidityEVMVersion, OptimizerEnabled: true, OptimizerRuns: 200,
		ABI:         json.RawMessage(`[{"stateMutability":"payable","type":"fallback"}]`),
		BytecodeHex: "0x6001600c60003960016000f300", DeployedBytecodeHex: "0x00",
	}
	baseline := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(baseline, "artifacts"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(baseline, fixture.SourceName), source, 0600))
	raw, err := json.Marshal(fixture)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(baseline, "artifacts/LoaderFixture.LoaderFixture.json"), raw, 0600))
	loaded, err := LoadSolidityArtifact(filepath.Join(baseline, fixture.SourceName), fixture.ContractName)
	require.NoError(t, err, "unmodified synthetic fixture must load before testing rejection")
	require.NotEmpty(t, loaded.Bytecode)
	require.Equal(t, []byte{0}, loaded.DeployedBytecode)
	for _, variant := range []string{"missing", "stale-source", "wrong-contract", "wrong-profile", "invalid-bytecode"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "artifacts"), 0700))
			var artifact SolidityArtifact
			require.NoError(t, json.Unmarshal(raw, &artifact))
			content := append([]byte(nil), source...)
			expectedError := ""
			switch variant {
			case "missing":
				expectedError = "read precompiled Solidity fixture"
			case "stale-source":
				content = append(content, '\n')
				expectedError = "stale or mismatched Solidity fixture"
			case "wrong-contract":
				artifact.ContractName = "AnotherContract"
				expectedError = "stale or mismatched Solidity fixture"
			case "wrong-profile":
				artifact.EVMVersion = "shanghai"
				expectedError = "unexpected compiler profile"
			case "invalid-bytecode":
				artifact.BytecodeHex = "0xnot-hex"
				expectedError = "invalid deploy bytecode"
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, fixture.SourceName), content, 0600))
			if variant != "missing" {
				encoded, err := json.Marshal(artifact)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "artifacts/LoaderFixture.LoaderFixture.json"), encoded, 0600))
			}
			_, err = LoadSolidityArtifact(filepath.Join(dir, fixture.SourceName), fixture.ContractName)
			require.Error(t, err, "invalid fixtures must fail without a compiler fallback")
			require.ErrorContains(t, err, expectedError)
		})
	}
}
