package e2e

import (
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/sat20-labs/satoshinet/chaincfg"
	"github.com/stretchr/testify/require"
)

const goPackageFingerprintSymbol = "go:link.pkghashbytes."
const btcLuckyPackage = "github.com/sat20-labs/indexer/share/btclucky"

type pwaBuildInputSnapshot struct {
	revisions map[string]string
	files     map[string]string
}

// This runs immediately before NodeReadiness in the cross-layer acceptance
// process. It builds the real Core/plugin pair once, verifies the linker
// fingerprints for every shared Go package, and refuses to continue if the
// shared source inputs changed while that pair was being built.
func TestSDKPWARealRegtestArtifactFingerprintGuard(t *testing.T) {
	if os.Getenv("SAT20_REAL_RGB_CROSS_LAYER") == "1" {
		require.Empty(t, os.Getenv(prebuiltE2EEnv), "RGB cross-layer guard must build every artifact with this run's GOFLAGS overlay")
		require.Equal(t, int32(32), chaincfg.RegressionNetParams.POSV2Height, "RGB cross-layer regtest must retain the POS v2 schedule")
		t.Logf("private regtest activation: POSV2Height=%d", chaincfg.RegressionNetParams.POSV2Height)
	}

	before, err := capturePWABuildInputs()
	require.NoError(t, err, "capture build inputs before Core/plugin build")

	artifacts := satoshinetCoreBuildArtifacts(t)

	after, err := capturePWABuildInputs()
	require.NoError(t, err, "capture build inputs after Core/plugin build")
	changes := changedPWABuildInputs(before, after)
	if len(changes) != 0 {
		shown := changes
		if len(shown) > 20 {
			shown = append(append([]string(nil), shown[:20]...), fmt.Sprintf("... and %d more", len(shown)-20))
		}
		t.Fatalf("shared Go build inputs changed while producing the Core/plugin pair; refusing to start nodes: %s -> %s; changed=%s",
			fingerprintPWABuildInputs(before), fingerprintPWABuildInputs(after), strings.Join(shown, ", "))
	}

	pluginFingerprints, err := readGoPackageFingerprints(artifacts.corePlugin)
	require.NoError(t, err, "read package fingerprints from %s", artifacts.corePlugin)
	coreFingerprints, err := readGoPackageFingerprints(artifacts.coreExecutable)
	require.NoError(t, err, "read package fingerprints from %s", artifacts.coreExecutable)
	require.Contains(t, pluginFingerprints, btcLuckyPackage,
		"Core plugin must contain the shared btclucky package fingerprint")
	require.Contains(t, coreFingerprints, btcLuckyPackage,
		"Core executable must contain the shared btclucky package fingerprint")

	shared := 0
	var mismatches []string
	for name, pluginFingerprint := range pluginFingerprints {
		coreFingerprint, ok := coreFingerprints[name]
		if !ok {
			continue
		}
		shared++
		if coreFingerprint != pluginFingerprint {
			mismatches = append(mismatches, fmt.Sprintf("%s plugin=%s core=%s", name, pluginFingerprint, coreFingerprint))
		}
	}
	sort.Strings(mismatches)
	require.Greater(t, shared, 0, "Core executable and plugin must share Go packages")
	require.Empty(t, mismatches,
		"Core executable and stpd.so were built with different shared package fingerprints")
	require.Equal(t, pluginFingerprints[btcLuckyPackage], coreFingerprints[btcLuckyPackage],
		"the btclucky fingerprint that previously failed plugin loading must match")

	t.Logf("artifact fingerprint guard passed: shared_packages=%d build_input_files=%d build_input_sha256=%s",
		shared, len(after.files), fingerprintPWABuildInputs(after))
}

func capturePWABuildInputs() (pwaBuildInputSnapshot, error) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return pwaBuildInputSnapshot{}, fmt.Errorf("locate e2e source directory")
	}
	workspace := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", ".."))
	repositories := []struct {
		name   string
		prefix string
	}{
		{name: "sat20wallet", prefix: "sdk"},
		{name: "satoshinet"},
		{name: "indexer"},
		{name: "transcend"},
		{name: "xaplib", prefix: "chainindex"},
	}
	snapshot := pwaBuildInputSnapshot{revisions: make(map[string]string), files: make(map[string]string)}
	for _, repository := range repositories {
		root := filepath.Join(workspace, repository.name)
		revision, err := runGit(root, "rev-parse", "HEAD")
		if err != nil {
			return pwaBuildInputSnapshot{}, err
		}
		snapshot.revisions[repository.name] = revision
		args := []string{"ls-files", "-z", "--cached", "--others", "--exclude-standard"}
		if repository.prefix != "" {
			args = append(args, "--", repository.prefix)
		}
		listed, err := exec.Command("git", append([]string{"-C", root}, args...)...).Output()
		if err != nil {
			return pwaBuildInputSnapshot{}, fmt.Errorf("list build inputs in %s: %w", repository.name, err)
		}
		for _, relative := range strings.Split(string(listed), "\x00") {
			if relative == "" {
				continue
			}
			path := filepath.Join(root, filepath.FromSlash(relative))
			if !isGoBuildInput(path) {
				continue
			}
			digest, err := fileSHA256(path)
			if err != nil {
				return pwaBuildInputSnapshot{}, fmt.Errorf("fingerprint %s/%s: %w", repository.name, relative, err)
			}
			snapshot.files[repository.name+"/"+filepath.ToSlash(relative)] = digest
		}
	}

	for _, relative := range []string{
		"indexer/rpcserver/ord/static",
		"indexer/rpcserver/ord/templates",
		"transcend/rpc/web/manage.html",
	} {
		path := filepath.Join(workspace, filepath.FromSlash(relative))
		if err := addBuildInputPath(workspace, path, snapshot.files); err != nil {
			return pwaBuildInputSnapshot{}, err
		}
	}
	contracts := filepath.Join(workspace, "sat20wallet", "sdk", "e2e", "testdata", "contracts")
	if err := filepath.WalkDir(contracts, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		if filepath.Ext(path) != ".sol" && filepath.Ext(path) != ".json" {
			return nil
		}
		return addBuildInputPath(workspace, path, snapshot.files)
	}); err != nil && !os.IsNotExist(err) {
		return pwaBuildInputSnapshot{}, fmt.Errorf("fingerprint Solidity build inputs: %w", err)
	}
	if os.Getenv("SAT20_REAL_RGB_CROSS_LAYER") != "1" {
		return snapshot, nil
	}
	// The source overlay is a build input too: its JSON binds original paths
	// to replacement files, whose bytes need not exist in any Git checkout.
	flags := strings.Fields(os.Getenv("GOFLAGS"))
	var overlayPath string
	for i := 0; i < len(flags); i++ {
		flag := strings.Trim(flags[i], "\"'")
		if strings.HasPrefix(flag, "-overlay=") {
			overlayPath = strings.TrimPrefix(flag, "-overlay=")
		} else if flag == "-overlay" {
			i++
			if i >= len(flags) {
				return pwaBuildInputSnapshot{}, fmt.Errorf("GOFLAGS -overlay requires a path")
			}
			overlayPath = strings.Trim(flags[i], "\"'")
		}
	}
	if !filepath.IsAbs(overlayPath) {
		return pwaBuildInputSnapshot{}, fmt.Errorf("Go source overlay path must be absolute: %s", overlayPath)
	}
	data, err := os.ReadFile(overlayPath)
	if err != nil {
		return pwaBuildInputSnapshot{}, fmt.Errorf("read Go source overlay: %w", err)
	}
	var overlay struct {
		Replace map[string]string `json:"Replace"`
	}
	if err := json.Unmarshal(data, &overlay); err != nil {
		return pwaBuildInputSnapshot{}, fmt.Errorf("decode Go source overlay: %w", err)
	}
	if len(overlay.Replace) == 0 {
		return pwaBuildInputSnapshot{}, fmt.Errorf("RGB Go source overlay must contain source replacements")
	}
	digest := sha256.Sum256(data)
	snapshot.files["overlay:"+overlayPath] = hex.EncodeToString(digest[:])
	for original, replacement := range overlay.Replace {
		if replacement == "" { // A deletion is already captured by the mapping hash.
			continue
		}
		if !filepath.IsAbs(original) || !filepath.IsAbs(replacement) {
			return pwaBuildInputSnapshot{}, fmt.Errorf("Go source overlay paths must be absolute: %s -> %s", original, replacement)
		}
		digest, err := fileSHA256(replacement)
		if err != nil {
			return pwaBuildInputSnapshot{}, fmt.Errorf("fingerprint overlay replacement %s: %w", replacement, err)
		}
		snapshot.files["overlay:"+original+"->"+replacement] = digest
	}

	return snapshot, nil
}

func runGit(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git -C %s %s: %w", root, strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func isGoBuildInput(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	switch filepath.Ext(path) {
	case ".go", ".c", ".cc", ".cpp", ".cxx", ".h", ".hh", ".hpp", ".hxx", ".s", ".S", ".syso":
		return true
	default:
		return filepath.Base(path) == "go.mod" || filepath.Base(path) == "go.sum"
	}
}

func addBuildInputPath(workspace, path string, destination map[string]string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(path, func(child string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			return addBuildInputFile(workspace, child, destination)
		})
	}
	return addBuildInputFile(workspace, path, destination)
}

func addBuildInputFile(workspace, path string, destination map[string]string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	digest, err := fileSHA256(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(workspace, path)
	if err != nil {
		return err
	}
	destination[filepath.ToSlash(relative)] = digest
	return nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func fingerprintPWABuildInputs(snapshot pwaBuildInputSnapshot) string {
	hasher := sha256.New()
	for _, name := range sortedMapKeys(snapshot.revisions) {
		fmt.Fprintf(hasher, "revision\x00%s\x00%s\x00", name, snapshot.revisions[name])
	}
	for _, name := range sortedMapKeys(snapshot.files) {
		fmt.Fprintf(hasher, "file\x00%s\x00%s\x00", name, snapshot.files[name])
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func sortedMapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func changedPWABuildInputs(before, after pwaBuildInputSnapshot) []string {
	var changed []string
	for name, revision := range before.revisions {
		if after.revisions[name] != revision {
			changed = append(changed, name+"/@HEAD")
		}
	}
	for name, digest := range after.revisions {
		if before.revisions[name] != digest {
			changed = append(changed, name+"/@HEAD")
		}
	}
	for name, digest := range before.files {
		if after.files[name] != digest {
			changed = append(changed, name)
		}
	}
	for name := range after.files {
		if _, ok := before.files[name]; !ok {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func readGoPackageFingerprints(path string) (map[string]string, error) {
	if binary, err := macho.Open(path); err == nil {
		defer binary.Close()
		if binary.Symtab == nil {
			return nil, fmt.Errorf("%s has no Mach-O symbol table", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		fingerprints := make(map[string]string)
		for _, symbol := range binary.Symtab.Syms {
			name := strings.TrimPrefix(symbol.Name, "_")
			if !strings.HasPrefix(name, goPackageFingerprintSymbol) {
				continue
			}
			if symbol.Sect == 0 || int(symbol.Sect) > len(binary.Sections) {
				return nil, fmt.Errorf("package fingerprint symbol %q has invalid Mach-O section", name)
			}
			section := binary.Sections[int(symbol.Sect)-1]
			if symbol.Value < section.Addr || symbol.Value-section.Addr+8 > section.Size {
				return nil, fmt.Errorf("package fingerprint symbol %q is outside its Mach-O section", name)
			}
			offset := uint64(section.Offset) + symbol.Value - section.Addr
			fingerprint, err := readFingerprintAt(file, offset)
			if err != nil {
				return nil, fmt.Errorf("read package fingerprint %q: %w", name, err)
			}
			if err := setPackageFingerprint(fingerprints, strings.TrimPrefix(name, goPackageFingerprintSymbol), fingerprint); err != nil {
				return nil, err
			}
		}
		if len(fingerprints) == 0 {
			return nil, fmt.Errorf("%s has no Go package fingerprints", path)
		}
		return fingerprints, nil
	}

	binary, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s as Mach-O or ELF: %w", path, err)
	}
	defer binary.Close()
	symbols, err := binary.Symbols()
	if err != nil {
		return nil, fmt.Errorf("read ELF symbols from %s: %w", path, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	fingerprints := make(map[string]string)
	for _, symbol := range symbols {
		if !strings.HasPrefix(symbol.Name, goPackageFingerprintSymbol) {
			continue
		}
		sectionIndex := int(symbol.Section)
		if sectionIndex <= 0 || sectionIndex >= len(binary.Sections) {
			return nil, fmt.Errorf("package fingerprint symbol %q has invalid ELF section", symbol.Name)
		}
		section := binary.Sections[sectionIndex]
		if symbol.Value < section.Addr || symbol.Value-section.Addr+8 > section.Size {
			return nil, fmt.Errorf("package fingerprint symbol %q is outside its ELF section", symbol.Name)
		}
		offset := section.Offset + symbol.Value - section.Addr
		fingerprint, err := readFingerprintAt(file, offset)
		if err != nil {
			return nil, fmt.Errorf("read package fingerprint %q: %w", symbol.Name, err)
		}
		if err := setPackageFingerprint(fingerprints, strings.TrimPrefix(symbol.Name, goPackageFingerprintSymbol), fingerprint); err != nil {
			return nil, err
		}
	}
	if len(fingerprints) == 0 {
		return nil, fmt.Errorf("%s has no Go package fingerprints", path)
	}
	return fingerprints, nil
}

func readFingerprintAt(file *os.File, offset uint64) (string, error) {
	bytes := make([]byte, 8)
	if _, err := file.ReadAt(bytes, int64(offset)); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func setPackageFingerprint(destination map[string]string, packageName, fingerprint string) error {
	if previous, exists := destination[packageName]; exists && previous != fingerprint {
		return fmt.Errorf("conflicting Go package fingerprints for %s: %s and %s", packageName, previous, fingerprint)
	}
	destination[packageName] = fingerprint
	return nil
}
