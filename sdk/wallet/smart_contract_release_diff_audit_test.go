package wallet

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSmartContractReleaseDiffAudit(t *testing.T) {
	if !strings.Contains(flag.Lookup("test.run").Value.String(), t.Name()) { t.Skip("explicit source diff audit") }
	_, here, _, ok := runtime.Caller(0); if !ok { t.Fatal("locate source") }
	sdk := filepath.Dir(filepath.Dir(here)); root := filepath.Dir(filepath.Dir(sdk))
	baseline := filepath.Join(sdk, "review-evidence", "release-fix-sources-20261002T001718")
	allowed := map[string]map[string]bool{
		"sat20wallet/sdk/wallet/interface_contract_unified.go": {"evmGasAssetAmount":true},
		"satoshinet/contract/evm/backend.go": {"executeDeployTx":true},
		"satoshinet/contract/template/backend.go": {"executeDeployTx":true},
		"satoshinet/blockchain/validate.go": {"CheckTransactionSanity":true, "CheckTransactionInputs":true, "checkCoinbaseFees":true, "checkConnectBlock":true},
	}
	decls := func(data []byte) map[string]string {
		fs := token.NewFileSet(); parsed, err := parser.ParseFile(fs, "source.go", data, 0); if err != nil { t.Fatal(err) }
		result := map[string]string{}
		for i, d := range parsed.Decls {
			name := fmt.Sprintf("declaration-%d", i)
			if f, ok := d.(*ast.FuncDecl); ok { name = f.Name.Name }
			var buf bytes.Buffer; if err := format.Node(&buf, fs, d); err != nil { t.Fatal(err) }
			result[name] = buf.String()
		}
		return result
	}
	for name, permit := range allowed {
		old, err := os.ReadFile(filepath.Join(baseline, strings.ReplaceAll(name, "/", "__")+".before")); if err != nil { t.Fatal(err) }
		current, err := os.ReadFile(filepath.Join(root, name)); if err != nil { t.Fatal(err) }
		a,b := decls(old),decls(current)
		for symbol, text := range a { if b[symbol] != text { if !permit[symbol] { t.Errorf("unrelated declaration changed: %s %s", name, symbol) }; t.Logf("changed %s %s", name, symbol) } }
		for symbol := range b { if _, ok := a[symbol]; !ok && !permit[symbol] { t.Errorf("unexpected added declaration: %s %s", name, symbol) } }
	}
	// Bounded diagnostic source windows only; no wallet data or databases.
	data, err := os.ReadFile(filepath.Join(sdk, "e2e", "smart_contract_deploy_escrow_e2e_test.go")); if err != nil { t.Fatal(err) }
	lines := strings.Split(string(data), "\n")
	for i:=88; i<100 && i<len(lines); i++ { t.Logf("fixture %d: %s",i+1,lines[i]) }
}
