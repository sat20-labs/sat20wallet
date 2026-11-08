package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

const prebuiltE2EEnv = "SAT20WALLET_E2E_ARTIFACTS"

// Only compiled binaries, compiler output and hashes belong in this bundle.
// A configured bundle must validate; it never falls back to remote builds.
type e2eArtifactBundle struct {
	GoVersion       string            `json:"go_version"`
	GOOS            string            `json:"goos"`
	GOARCH          string            `json:"goarch"`
	Files           map[string]string `json:"files"`
	SoliditySources map[string]string `json:"solidity_sources"`
	root            string
}

func readE2EArtifactBundle() (*e2eArtifactBundle, error) {
	root := os.Getenv(prebuiltE2EEnv)
	if root == "" {
		return nil, nil
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("%s must be an absolute artifact directory", prebuiltE2EEnv)
	}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("read E2E artifact manifest: %w", err)
	}
	var bundle e2eArtifactBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return nil, fmt.Errorf("decode E2E artifact manifest: %w", err)
	}
	if bundle.GoVersion != runtime.Version() || bundle.GOOS != runtime.GOOS || bundle.GOARCH != runtime.GOARCH {
		return nil, fmt.Errorf("E2E artifact target/toolchain mismatch: got %s %s/%s, running %s %s/%s",
			bundle.GoVersion, bundle.GOOS, bundle.GOARCH, runtime.Version(), runtime.GOOS, runtime.GOARCH)
	}
	bundle.root = root
	return &bundle, nil
}

func (bundle *e2eArtifactBundle) file(name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid E2E artifact name %q", name)
	}
	expected := bundle.Files[name]
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("missing or invalid E2E artifact hash for %s", name)
	}
	path := filepath.Join(bundle.root, clean)
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open E2E artifact %s: %w", name, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("E2E artifact %s is not a readable regular file", name)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash E2E artifact %s: %w", name, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected {
		return "", fmt.Errorf("E2E artifact hash mismatch: %s", name)
	}
	return path, nil
}
