package wallet

import (
    "crypto/sha256"
    "flag"
    "fmt"
    "go/ast"
    "go/parser"
    "go/token"
    "os"
    "path/filepath"
    "sort"
    "strings"
    "testing"
)

// Read-only, bounded, source-only evidence. Never visit databases or .git.
func TestSmartContractSourceReview(t *testing.T) {
    if !strings.Contains(flag.Lookup("test.run").Value.String(), "TestSmartContractSourceReview") {
        t.Skip("explicit source-evidence capture")
    }
    cwd, err := os.Getwd(); if err != nil { t.Fatal(err) }
    sdk := filepath.Dir(cwd)
    root := filepath.Dir(filepath.Dir(sdk))
    var out strings.Builder
    out.WriteString("# Smart-contract focused source evidence\n\nExact functions and bounded line windows only; this is not a whole-repository coverage claim.\n")
    emit := func(path string, names []string, windows []string) {
        raw, err := os.ReadFile(filepath.Join(root,path)); if err != nil { t.Fatal(err) }
        lines := strings.Split(string(raw),"\n")
        selected := map[int]bool{}
        fs := token.NewFileSet(); tree, err := parser.ParseFile(fs,path,raw,0); if err != nil { t.Fatal(err) }
        for _, decl := range tree.Decls {
            fn, ok := decl.(*ast.FuncDecl); if !ok { continue }
            for _, name := range names {
                if fn.Name.Name != name { continue }
                start, end := fs.Position(fn.Pos()).Line-1, fs.Position(fn.End()).Line
                // Very large functions are represented by relevant windows.
                if end-start > 260 { continue }
                for n:=start;n<end;n++ {selected[n]=true}
            }
        }
        for i,line := range lines {
            match:=false;for _,term:=range windows{if strings.Contains(line,term){match=true;break}}
            if !match{continue}
            for n:=max(0,i-5);n<=min(len(lines)-1,i+12);n++{selected[n]=true}
        }
        if len(selected)==0{return}
        fmt.Fprintf(&out,"\n## %s\nSHA-256: %x\n```text\n",path,sha256.Sum256(raw))
        indexes:=make([]int,0,len(selected));for i:=range selected{indexes=append(indexes,i)};sort.Ints(indexes)
        previous:=-2
        for _,i:=range indexes{if i!=previous+1{out.WriteString("...\n")};fmt.Fprintf(&out,"%d: %s\n",i+1,lines[i]);previous=i}
        out.WriteString("```\n")
    }
    emit("sat20wallet/sdk/wallet/interface_contract_unified.go",[]string{
        "firstFundingAsset","assetAmountStringToInt64","convertTemplateInvokeParam","templateInvokeParamTemplate",
        "selectUnifiedContractFundingWithWallet","selectEVMDefaultContractFunding","queryEVMInvokeFee","invokeEVMContract"},nil)
    emit("satoshinet/rpcserver.go",[]string{"handleGetContract","handleGetContractState","contractStateAtTip"},nil)
    emit("satoshinet/indexer/rpcserver/indexer/handler.go",[]string{
        "getContract","getContractState","filterRuntimeExistingContractSummaries","contractRuntimeExists","contractStateResponseExists"},nil)
    emit("satoshinet/contract/evm/runtime.go",[]string{"Deploy"},nil)
    emit("satoshinet/contract/evm/backend.go",[]string{"executeWorkBackend","executeInvokeTx"},[]string{"ValidateInvokeTxBasic","ErrCallAdmission"})
    emit("satoshinet/contract/template/autopay.go",[]string{"CheckInvoke"},[]string{"InvokeAPICancel"})
    emit("satoshinet/contract/template_codec.go",[]string{"IsTemplateInvokeActionSupported"},[]string{"TemplateInvokeAPICancel"})
    for _,dir:=range []string{"satoshinet/contract","satoshinet/contract/evm","satoshinet/contract/framework"}{
        entries,err:=os.ReadDir(filepath.Join(root,dir));if err!=nil{t.Fatal(err)}
        for _,entry:=range entries{
            if entry.IsDir()||!strings.HasSuffix(entry.Name(),".go")||strings.HasSuffix(entry.Name(),"_test.go"){continue}
            path:=filepath.Join(dir,entry.Name())
            raw,err:=os.ReadFile(filepath.Join(root,path));if err!=nil{t.Fatal(err)}
            if !strings.Contains(string(raw),"func ValidateInvokeGasLimit") && !strings.Contains(string(raw),"func ValidateInvokeTxBasic"){continue}
            emit(path,[]string{"ValidateInvokeGasLimit","ValidateInvokeTxBasic"},nil)
        }
    }
    emit("satoshinet/mempool/mempool.go",[]string{"isContractTx"},[]string{"contractcommon.","ValidateInvoke", "CheckTransactionInputs"})
    emit("satoshinet/mining/mining.go",nil,[]string{"contract result builder", "addContractResultsToTemplate"})
    emit("satoshinet/blockchain/validate.go",nil,[]string{"BindingSat", "TxAssets", "Assets.Equal", "AssetsMap", "asset precision", "asset conservation"})
    emit("transcend/stp/contract_invoke.go",nil,[]string{"CONTENT_TYPE_INVOKECONTRACT", "WitnessV0ScriptHashTy", "SaveReservationWithLock", "HandleReorg"})
    emit("transcend/stp/contract_deploy.go",nil,[]string{"CONTENT_TYPE_DEPLOYCONTRACT", "SaveReservation", "DeployContract"})
    emit("transcend/stp/contractmgr.go",[]string{"SaveReservation","SaveReservationWithLock"},nil)
    path:=filepath.Join(sdk,"review-evidence","smart-contract-source-excerpts.md")
    if err:=os.WriteFile(path,[]byte(out.String()),0600);err!=nil{t.Fatal(err)}
}
