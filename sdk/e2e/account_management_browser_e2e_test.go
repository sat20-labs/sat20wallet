package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Browser dependencies are explicit; the ordinary SDK gate remains independent
// of Node/Chromium. The browser uses the same real temporary node fixture.
func TestSDKAccountPWAConnectedBrowser(t *testing.T) {
	if os.Getenv("SAT20_RUN_PWA_E2E") != "1" {
		t.Skip("run with SAT20_RUN_PWA_E2E=1; see pwa/scripts/verify/README.md")
	}
	network := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t)).Network
	waitForDKVSPeerReady(t, network)
	config, _ := accountReviewConfig(t, network)
	encoded, err := json.Marshal(config)
	require.NoError(t, err)
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	pwa := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "pwa"))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/verify/account-management-e2e.mjs")
	command.Dir = pwa
	command.Env = append(os.Environ(), "SAT20_ACCOUNT_E2E_CONFIG="+string(encoded))
	var output bytes.Buffer
	writer := io.MultiWriter(&output, os.Stdout)
	command.Stdout, command.Stderr = writer, writer
	require.NoError(t, command.Run(), "PWA account gate failed:\n%s", output.String())
}
