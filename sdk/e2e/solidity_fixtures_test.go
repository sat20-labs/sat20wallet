package e2e

import (
	"encoding/json"
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
	source, err := os.ReadFile("testdata/contracts/Counter.sol")
	require.NoError(t, err)
	raw, err := os.ReadFile("testdata/contracts/artifacts/Counter.Counter.json")
	require.NoError(t, err)
	for _, variant := range []string{"missing", "stale-source", "wrong-contract", "wrong-profile", "invalid-bytecode"} {
		t.Run(variant, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(dir, "artifacts"), 0700))
			var artifact SolidityArtifact
			require.NoError(t, json.Unmarshal(raw, &artifact))
			content := append([]byte(nil), source...)
			switch variant {
			case "stale-source":
				content = append(content, '\n')
			case "wrong-contract":
				artifact.ContractName = "AnotherContract"
			case "wrong-profile":
				artifact.EVMVersion = "shanghai"
			case "invalid-bytecode":
				artifact.BytecodeHex = "0xnot-hex"
			}
			require.NoError(t, os.WriteFile(filepath.Join(dir, "Counter.sol"), content, 0600))
			if variant != "missing" {
				encoded, err := json.Marshal(artifact)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(dir, "artifacts/Counter.Counter.json"), encoded, 0600))
			}
			_, err = LoadSolidityArtifact(filepath.Join(dir, "Counter.sol"), "Counter")
			require.Error(t, err, "invalid fixtures must fail without a compiler fallback")
		})
	}
}
