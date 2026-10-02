#!/usr/bin/env python3
"""One-shot, checked refactor executed only on the RGB11 PR branch.

All replacements are prepared in memory before any existing source is written.
The workflow commits an explicit path allowlist, never the default branch.
This helper is removed after the resulting source diff is reviewed.
"""
from pathlib import Path
import os
import subprocess
import tempfile

sources = {}
changed = set()

def get(path):
    if path not in sources:
        sources[path] = Path(path).read_text(encoding="utf-8")
    return sources[path]

def put(path, text):
    sources[path] = text
    changed.add(path)

def replace(path, old, new):
    text = get(path)
    if text.count(old) != 1:
        raise RuntimeError(f"expected exactly one checked match in {path}: {old[:100]!r}")
    put(path, text.replace(old, new, 1))

def section(path, start, end, replacement):
    text = get(path)
    if text.count(start) != 1 or text.count(end) != 1:
        raise RuntimeError(f"ambiguous section in {path}: {start!r}")
    first, last = text.index(start), text.index(end)
    if last <= first:
        raise RuntimeError(f"invalid section order in {path}")
    put(path, text[:first] + replacement + text[last:])

types = "sdk/wallet/rgb11/types.go"
replace(types, '\t"crypto/sha256"\n\t"encoding/base32"', '\t"encoding/hex"')
section(types, '\n\t// DefaultFingerprintLength', '\n)\n\nvar (', '')
section(types, '// ContractFingerprint', 'type TickerExt struct {', '''// NewContractAssetKey is the immutable SDK projection identity. Its ticker is
// the complete 32-byte ContractID in lossless hex form, NOT a readable name or
// a digest prefix. Local aliases and SatoshiNet names are separate metadata.
func NewContractAssetKey(contractID, assetType string) (indexer.AssetName, error) {
	contract, err := consensus.ParseContractID(contractID)
	if err != nil {
		return indexer.AssetName{}, err
	}
	if assetType == "" {
		assetType = indexer.ASSET_TYPE_FT
	}
	if assetType != indexer.ASSET_TYPE_FT && assetType != indexer.ASSET_TYPE_NFT && assetType != "control" {
		return indexer.AssetName{}, ErrInvalidRGB11Asset
	}
	return indexer.AssetName{Protocol: Protocol, Type: assetType, Ticker: hex.EncodeToString(contract[:])}, nil
}

func ContractAssetKeyMatches(name indexer.AssetName, contractID string) bool {
	expected, err := NewContractAssetKey(contractID, name.Type)
	return err == nil && name == expected
}

''')
replace(types, '\tFingerprint      string            `json:"fingerprint,omitempty"`\n', '\tGenesisAddress   string            `json:"genesis_address,omitempty"`\n\tNamingStatus     string            `json:"naming_status,omitempty"`\n')
replace(types, '\tPrimaryVerified  bool              `json:"primary_verified,omitempty"`\n', '')

api = "sdk/wallet/rgb11/api_types.go"
section(api, 'type RGB11TickerInfo struct {', '// RGB11State exposes', '''type RGB11TickerInfo struct {
	*indexer.TickerInfo
	Ticker        string `json:"ticker"`
	AssetKey      string `json:"asset_key"`
	CanonicalName string `json:"canonical_name,omitempty"`
	ContractID    string `json:"contract_id"`
	GenesisAddress string `json:"genesis_address,omitempty"`
	NamingStatus  string `json:"naming_status"`
	Verified      bool   `json:"verified"`
}

''')

manager = "sdk/wallet/rgb11_manager.go"
replace(manager, '''	if err := validateRGB11TickerInfoName(info); err != nil {
		return err
	}
	if err := saveTickerInfo(p.db, info); err != nil {''', '''	if err := validateRGB11TickerInfoName(info); err != nil {
		return err
	}
	info, err := p.withRGB11NamingOrigin(info)
	if err != nil {
		return err
	}
	if err := saveTickerInfo(p.db, info); err != nil {''')
replace(manager, '// canonical SAT20 asset name, including registry collision extensions.', '// immutable full-ContractID projection key; local labels are not identity.')
replace(manager, '''	if contractID == "" {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	if rgb11wallet.CanonicalAssetNameMatches''', '''	if contractID == "" {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	// This SDK is still L1-only. An imported metadata string must never
	// impersonate an authenticated SatoshiNet registry assignment.
	if ext.CanonicalName != "" {
		return rgb11wallet.ErrRGB11STPUnavailable
	}
	if rgb11wallet.CanonicalAssetNameMatches''')
replace(manager, '''		ticker, canonicalName, contractID, fingerprint, verified := p.rgb11TickerPresentation(info)
		tickers = append(tickers, &RGB11TickerInfo{
			TickerInfo:    info,
			Ticker:        ticker,
			CanonicalName: canonicalName,
			ContractID:    contractID,
			Fingerprint:   fingerprint,
			Verified:      verified,
		})''', '''		presentation, err := p.rgb11TickerPresentation(info)
		if err != nil {
			return nil, err
		}
		tickers = append(tickers, presentation)''')
replace(manager, '''	fingerprint, err := rgb11wallet.ContractFingerprint(container.ContractID, rgb11wallet.DefaultFingerprintLength)
	if err != nil {
		return nil, err
	}
''', '')
replace(manager, '''		CanonicalName: assetName.String(), NormalizedTicker: rgb11wallet.NormalizeTicker(metadata.Ticker),
		Fingerprint: fingerprint, DisplayTicker: rgb11wallet.DisplayTicker(metadata.Ticker, fingerprint, false),''', '''		NormalizedTicker: rgb11wallet.NormalizeTicker(metadata.Ticker),
		DisplayTicker: container.ContractID, NamingStatus: "origin-unavailable",''')
section(manager, 'func (p *rgb11Manager) rgb11TickerPresentation(', 'func (p *rgb11Manager) rgb11ContractIDForAssetName(', '''// withRGB11NamingOrigin runs on import/registration, never in UI observation.
// Failure to obtain a naming origin does not make a valid RGB contract invalid:
// the safe fallback is its complete ContractID, not an invented issuer/address.
func (p *rgb11Manager) withRGB11NamingOrigin(info *indexer.TickerInfo) (*indexer.TickerInfo, error) {
	if p == nil || p.Manager == nil || info == nil {
		return nil, ErrRGB11Inconsistent
	}
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil {
		return nil, err
	}
	if ext.ContractID == "" {
		ext.ContractID = ext.OriginalAssetID
	}
	ext.GenesisAddress = ""
	ext.DisplayTicker = ext.ContractID
	ext.NamingStatus = "origin-unavailable"
	if p.projectionStore != nil && p.evidence != nil && ext.ContractHash != "" {
		if raw, err := p.projectionStore.LoadObject(ext.ContractHash); err == nil {
			if container, err := coreconsignment.Decode(raw); err == nil &&
				rgb11wallet.ContractAssetKeyMatches(info.AssetName, container.ContractID) {
				if genesis, ok := container.Value.Field("genesis"); ok {
					if address, err := rgb11wallet.ResolveGenesisNamingAddress(context.Background(), genesis, p.evidence, GetChainParam()); err == nil {
						if local, err := rgb11wallet.BuildLocalDisplayName(ext.Ticker, address, ""); err == nil {
							ext.GenesisAddress = address
							ext.DisplayTicker = local
							ext.NamingStatus = "local-address"
						}
					}
				}
			}
		}
	}
	content, err := json.Marshal(ext)
	if err != nil {
		return nil, err
	}
	copy := *info
	copy.Content = content
	return &copy, nil
}

func (p *rgb11Manager) rgb11TickerPresentation(info *indexer.TickerInfo) (*RGB11TickerInfo, error) {
	if info == nil || p.projectionStore == nil {
		return nil, ErrRGB11Inconsistent
	}
	if err := validateRGB11TickerInfoName(info); err != nil {
		return nil, err
	}
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil {
		return nil, err
	}
	contractID := ext.ContractID
	if contractID == "" {
		contractID = ext.OriginalAssetID
	}
	label, status := ext.DisplayTicker, ext.NamingStatus
	if label == "" {
		label, status = contractID, "origin-unavailable"
	}
	if local, err := p.projectionStore.LoadLocalAssetName(contractID); err == nil {
		label, status = local, "local-custom"
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	return &RGB11TickerInfo{
		TickerInfo: info, Ticker: label, AssetKey: info.AssetName.String(),
		ContractID: contractID, GenesisAddress: ext.GenesisAddress, NamingStatus: status,
		// A local alias is never an authenticated SatoshiNet registration.
		CanonicalName: "", Verified: false,
	}, nil
}

func (p *rgb11Manager) SetRGB11LocalAssetName(contractID, name string) error {
	if p == nil || p.Manager == nil || p.projectionStore == nil {
		return ErrRGB11Inconsistent
	}
	var info *indexer.TickerInfo
	for _, assetType := range []string{indexer.ASSET_TYPE_FT, indexer.ASSET_TYPE_NFT} {
		key, err := rgb11wallet.NewContractAssetKey(contractID, assetType)
		if err != nil {
			return err
		}
		p.mutex.RLock()
		info = p.tickerInfoMap[key.String()]
		p.mutex.RUnlock()
		if info != nil {
			break
		}
	}
	if info == nil {
		return rgb11wallet.ErrInvalidRGB11Asset
	}
	var ext rgb11wallet.TickerExt
	if err := json.Unmarshal(info.Content, &ext); err != nil {
		return err
	}
	if ext.CanonicalName != "" {
		return rgb11wallet.ErrRegisteredNameFrozen
	}
	return p.projectionStore.SaveLocalAssetName(contractID, name)
}

''')

public_api = "sdk/wallet/rgb11_api.go"
put(public_api, get(public_api) + '''
// SetRGB11LocalAssetName changes SDK-local metadata only. Contract identity,
// balances, transfer proofs and SatoshiNet registered names are never renamed.
func (p *Manager) SetRGB11LocalAssetName(contractID, name string) error {
	if p == nil {
		return ErrRGB11Inconsistent
	}
	release := p.beginRGB11Operation()
	defer release()
	manager, err := p.synchronizedRGB11Manager()
	if err != nil {
		return err
	}
	return manager.SetRGB11LocalAssetName(contractID, name)
}
''')

for path in ["sdk/wallet/rgb11/snapshot_ticker.go", "sdk/wallet/rgb11/validation_native_test.go"]:
    replace(path, 'metadata, err := schemas.ExtractGenesisAssetMetadata(', '_, err = schemas.ExtractGenesisAssetMetadata(')

# All checked textual edits have succeeded. No unrelated source is overwritten.
for path in sorted(changed):
    Path(path).write_text(sources[path], encoding="utf-8")

# Rewrite call expressions with Go's parser rather than textual comma splitting.
# It removes only the obsolete ticker argument, including multiline calls.
rewrite_go = r'''package main
import (
 "bytes"
 "go/ast"
 "go/format"
 "go/parser"
 "go/token"
 "os"
 "path/filepath"
 "strings"
 "fmt"
)
func main() {
 err := filepath.WalkDir("sdk", func(path string, d os.DirEntry, err error) error {
  if err != nil { return err }
  if d.IsDir() || !strings.HasSuffix(path, ".go") { return nil }
  raw, err := os.ReadFile(path); if err != nil { return err }
  if !bytes.Contains(raw, []byte("NewCanonicalAssetName")) && !bytes.Contains(raw, []byte("CanonicalAssetNameMatches")) { return nil }
  fset := token.NewFileSet()
  f, err := parser.ParseFile(fset, path, raw, parser.ParseComments); if err != nil { return err }
  changed := false
  ast.Inspect(f, func(node ast.Node) bool {
   call, ok := node.(*ast.CallExpr); if !ok { return true }
   var ident *ast.Ident
   switch fun := call.Fun.(type) { case *ast.Ident: ident = fun; case *ast.SelectorExpr: ident = fun.Sel }
   if ident == nil { return true }
   switch ident.Name {
   case "NewCanonicalAssetName":
    if len(call.Args) != 3 { panic("unexpected constructor arity in " + path) }
    ident.Name = "NewContractAssetKey"; call.Args = []ast.Expr{call.Args[0], call.Args[2]}; changed = true
   case "CanonicalAssetNameMatches":
    if len(call.Args) != 3 { panic("unexpected matcher arity in " + path) }
    ident.Name = "ContractAssetKeyMatches"; call.Args = call.Args[:2]; changed = true
   }
   return true
  })
  if changed {
   var out bytes.Buffer
   if err := format.Node(&out, fset, f); err != nil { return err }
   if err := os.WriteFile(path, out.Bytes(), 0644); err != nil { return err }
   fmt.Println(path)
  }
  return nil
 })
 if err != nil { panic(err) }
}
'''
with tempfile.TemporaryDirectory() as tmp:
    script = Path(tmp) / "rewrite.go"
    script.write_text(rewrite_go, encoding="utf-8")
    env = dict(os.environ, GO111MODULE="off", GOWORK="off")
    output = subprocess.check_output(["go", "run", str(script)], env=env, text=True)
    changed.update(line for line in output.splitlines() if line)

# The regression still explicitly exercises local metadata changes after the
# constructor is renamed to make its identity-only purpose unambiguous.
regression = "sdk/wallet/rgb11/naming_identity_regression_test.go"
text = Path(regression).read_text(encoding="utf-8")
text = text.replace('before, err := NewContractAssetKey(id,', 'local := LocalNameMetadata{ContractID: id, LocalName: "usdt@123456789012"}\n\trenamed, err := local.Rename("usdt@alice")\n\tif err != nil || renamed.ContractID != id || renamed.LocalName == local.LocalName {\n\t\tt.Fatalf("local rename failed: %+v %v", renamed, err)\n\t}\n\tbefore, err := NewContractAssetKey(local.ContractID,', 1)
text = text.replace('after, err := NewContractAssetKey(id,', 'after, err := NewContractAssetKey(renamed.ContractID,', 1)
Path(regression).write_text(text, encoding="utf-8")
changed.add(regression)

# Existing issuance assertions named the old internal key CanonicalName.
# Registered canonical names must now remain absent in an L1-only wallet.
transfer_test = Path("sdk/wallet/rgb11_transfer_test.go")
text = transfer_test.read_text(encoding="utf-8")
if '.CanonicalName' in text:
    text = text.replace('.CanonicalName', '.AssetKey')
    transfer_test.write_text(text, encoding="utf-8")
    changed.add(str(transfer_test))

# Format this PR's new files as well as the touched integration files.
new_files = [
 "sdk/wallet/rgb11/naming.go", "sdk/wallet/rgb11/naming_test.go",
 "sdk/wallet/rgb11/naming_origin.go", "sdk/wallet/rgb11/naming_origin_test.go",
 "sdk/wallet/rgb11/local_name_store.go", "sdk/wallet/rgb11/local_name_store_test.go",
 "sdk/wallet/rgb11/types_test.go",
]
changed.update(new_files)
subprocess.run(["gofmt", "-w", *sorted(p for p in changed if p.endswith(".go"))], check=True)
for path in sorted(changed):
    if not path.startswith("sdk/wallet/") or not path.endswith(".go"):
        raise RuntimeError(f"unexpected change outside reviewed SDK paths: {path}")
Path(".github/rgb11-naming-paths.txt").write_text("\n".join(sorted(changed)) + "\n", encoding="utf-8")
subprocess.run(["git", "diff", "--check"], check=True)
subprocess.run(["git", "diff", "--stat"], check=True)
