package wallet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// This native-Go source contract covers only the WASM bridge structure. It
// does not execute syscall/js, the PWA store, or an end-to-end recovery flow.
// Success must be delegated to the ordinary SDK SwitchAccount, which owns
// same-account heartbeat wakes; the bridge must not acknowledge it early.
func TestWASMSwitchAccountDelegatesBeforeAnySuccessReturn(t *testing.T) {
	source, err := parser.ParseFile(token.NewFileSet(), "../wasm/main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var callback *ast.FuncLit
	for _, declaration := range source.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "switchAccount" {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			name, ok := call.Fun.(*ast.Ident)
			if !ok || name.Name != "createAsyncJsHandler" || len(call.Args) != 1 {
				return true
			}
			callback, _ = call.Args[0].(*ast.FuncLit)
			return false
		})
	}
	if callback == nil {
		t.Fatal("switchAccount async callback not found")
	}

	for _, statement := range callback.Body.List {
		delegates := false
		ast.Inspect(statement, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "SwitchAccount" || len(call.Args) != 1 {
				return true
			}
			manager, ok := selector.X.(*ast.Ident)
			argument, argumentOK := call.Args[0].(*ast.Ident)
			delegates = ok && manager.Name == "_mgr" && argumentOK && argument.Name == "id"
			return true
		})
		if delegates {
			return
		}
		ast.Inspect(statement, func(node ast.Node) bool {
			result, ok := node.(*ast.ReturnStmt)
			if !ok || len(result.Results) != 3 {
				return true
			}
			code, ok := result.Results[1].(*ast.BasicLit)
			if ok && code.Kind == token.INT && code.Value == "0" {
				t.Fatal("WASM switchAccount returns success before Manager.SwitchAccount(id); same-account recovery wake is bypassed")
			}
			return true
		})
	}
	t.Fatal("WASM switchAccount callback does not delegate to Manager.SwitchAccount(id)")
}
