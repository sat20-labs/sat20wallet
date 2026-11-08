package e2e

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func runPrebuiltCoreChild(ctx context.Context, command *exec.Cmd, converterPath string, childLog *os.File) error {
	reader, writer, err := os.Pipe()
	if err != nil {
		return err
	}
	defer reader.Close()
	defer writer.Close()
	// Own both processes directly so cancelling the context cannot leave a
	// wallet child behind a killed test2json wrapper. OS pipes need no copier.
	converter := exec.CommandContext(ctx, converterPath, "-t", "-p", "github.com/sat20-labs/sat20wallet/sdk/wallet")
	converter.Stdin, converter.Stdout, converter.Stderr = reader, childLog, childLog
	if err := converter.Start(); err != nil {
		return err
	}
	_ = reader.Close()
	command.Stdout, command.Stderr = writer, writer
	childErr := command.Run()
	closeErr := writer.Close()
	convertErr := converter.Wait()
	return errors.Join(childErr, closeErr, convertErr)
}

func prebuiltSPWalletFixture(t *testing.T) string {
	t.Helper()
	// These are the existing public synthetic harness roles, not copied keys
	// or state from the original persistent regtest wallet fixture.
	roles := []struct{ Role, Mnemonic string }{
		{Role: "bootstrap", Mnemonic: bootstrapMnemonic},
		{Role: "core", Mnemonic: coreMnemonic},
	}
	raw, err := json.Marshal(roles)
	require.NoError(t, err)
	path := filepath.Join(testCaseDirectory(t), "synthetic-wallets.json")
	require.NoError(t, os.WriteFile(path, raw, 0600))
	return path
}

func stagePrebuiltSatoshiNetArtifacts(t *testing.T, variant string, coreOnly bool) (satoshinetArtifacts, bool) {
	t.Helper()
	bundle, err := readE2EArtifactBundle()
	require.NoError(t, err)
	if bundle == nil {
		return satoshinetArtifacts{}, false
	}
	defer e2ePhase(t, "node_prebuilt_stage")()
	require.Contains(t, []string{"normal", "dkvs"}, variant)
	output := filepath.Join(e2eRuntimeDirectory(t), variant)
	require.NoError(t, os.MkdirAll(output, 0700))
	coreName, minerName := "satoshinet-core-rpctest", "satoshinet-miner-rpctest"
	if variant == "dkvs" {
		coreName, minerName = "satoshinet-core-dkvs-rpctest", "satoshinet-miner-dkvs-rpctest"
	}
	artifacts := satoshinetArtifacts{
		coreExecutable: filepath.Join(output, coreName),
		corePlugin:     filepath.Join(output, "stpd.so"),
	}
	files := map[string]string{
		"satoshinet-core-rpctest": artifacts.coreExecutable,
		"stpd-" + variant + ".so": artifacts.corePlugin,
	}
	if !coreOnly {
		artifacts.minerExecutable = filepath.Join(output, minerName)
		artifacts.minerPlugin = filepath.Join(output, "wallet.so")
		files["satoshinet-miner-rpctest"] = artifacts.minerExecutable
		files["wallet.so"] = artifacts.minerPlugin
	}
	for name, target := range files {
		source, err := bundle.file(name)
		require.NoError(t, err)
		copySatoshiNetRuntimeFile(t, source, target)
		t.Logf("artifact: variant=%s file=%s source=prebuilt sha256=%s", variant, name, bundle.Files[name])
	}
	return artifacts, true
}

// This transport smoke uses the existing temporary-node fixture. Unlike the
// normal non-generating miner, this miner must actually load wallet.so too.
func TestSatoshiNetPrebuiltPluginsLoad(t *testing.T) {
	if os.Getenv(prebuiltE2EEnv) == "" {
		t.Skip("explicit precompiled-artifact transport smoke")
	}
	for _, variant := range []string{"normal", "dkvs"} {
		t.Run(variant, func(t *testing.T) {
			bootstrap := keyFromMnemonic(t, bootstrapMnemonic, 0)
			core := keyFromMnemonic(t, coreMnemonic, 0)
			_, script, err := getP2WSHScript(bootstrap.PubKey().SerializeCompressed(), core.PubKey().SerializeCompressed())
			require.NoError(t, err)
			fakeL1 := newFakeL1Indexer(t, hex.EncodeToString(bootstrap.PubKey().SerializeCompressed()), script, nil)
			minerArgs := append([]string{"--generate"}, dkvsMinerArgs(t)...)
			var network *realSatoshiNet
			if variant == "dkvs" {
				network = newDKVSNoPluginNetworkWithArgs(t, fakeL1, nil, nil, minerArgs)
			} else {
				network = newRealSatoshiNetWithArgs(t, fakeL1, nil, nil, minerArgs)
			}
			for _, node := range network.Nodes {
				if node.role == "miner" {
					// wallet.Manager has no core-management HTTP server. The host
					// emits this positive event only after calling StartWalletMgr
					// successfully through the loaded wallet.so symbol.
					require.Eventually(t, func() bool {
						raw, err := os.ReadFile(node.logFile)
						return err == nil && strings.Contains(string(raw), "StartWalletMgr completed")
					}, rpcReadyTimeout, 200*time.Millisecond, "wallet plugin did not finish startup")
					_, err := node.Client.GetBlockCount()
					require.NoError(t, err, "wallet-plugin node RPC is not alive")
				} else {
					waitSDKPWARegtestManagerHealth(t, node)
				}
				t.Logf("prebuilt plugin ready: variant=%s role=%s pid=%d", variant, node.role, node.NodePID())
			}
		})
	}
}
