package wallet

import (
 "bufio"
 "bytes"
 "context"
 "encoding/json"
 "flag"
 "os"
 "os/exec"
 "path/filepath"
 "runtime"
 "strings"
 "testing"
 "time"
)

// Explicit entry point for the existing fixed MCP test profile. Default
// go test ./... runs the E2E parent directly and must not recursively start it.
func TestSDKCoreModulesValidation(t *testing.T) {
 if !strings.Contains(flag.Lookup("test.run").Value.String(),"TestSDKCoreModulesValidation") {
  t.Skip("explicit isolated core-module E2E runner")
 }
 cwd,err:=os.Getwd();coreRequire(t,"locate SDK",err)
 ctx,cancel:=context.WithTimeout(context.Background(),9*time.Minute);defer cancel()
 cmd:=exec.CommandContext(ctx,filepath.Join(runtime.GOROOT(),"bin","go"),"test","-json","./e2e","-run","^TestSDKCoreModulesE2E$","-count=1","-timeout=8m")
 cmd.Dir=filepath.Dir(cwd)
 for _,entry:=range os.Environ(){
  if !strings.HasPrefix(entry,"SAT20WALLET_RUN_LIVE_NETWORK_TESTS=") && !strings.HasPrefix(entry,"SAT20WALLET_CORE_E2E_CONFIG="){cmd.Env=append(cmd.Env,entry)}
 }
 started:=time.Now()
 raw,runErr:=cmd.CombinedOutput()
 type event struct{Action,Test,Output string;Elapsed float64}
 var verdicts []event
 var diagnostics []string
 scanner:=bufio.NewScanner(bytes.NewReader(raw));scanner.Buffer(make([]byte,65536),4<<20)
 for scanner.Scan(){
  var e event
  if json.Unmarshal(scanner.Bytes(),&e)!=nil{continue}
  if e.Action=="pass" || e.Action=="fail" || e.Action=="skip"{e.Output="";verdicts=append(verdicts,e)}
  if strings.Contains(e.Output,"core-e2e:") || strings.Contains(e.Output,"Error Trace:") || strings.Contains(e.Output,"Error:") || strings.Contains(e.Output,"panic:") {
   diagnostics=append(diagnostics,strings.TrimSpace(e.Output))
  }
 }
 report:=map[string]any{"started_at":started.Format(time.RFC3339),"finished_at":time.Now().Format(time.RFC3339),
  "passed":runErr==nil,"timed_out":ctx.Err()!=nil,"verdicts":verdicts,"diagnostics":diagnostics}
 data,err:=json.MarshalIndent(report,"","  ");coreRequire(t,"encode E2E execution evidence",err)
 dir:=filepath.Join(filepath.Dir(cwd),"review-evidence");coreRequire(t,"create evidence directory",os.MkdirAll(dir,0700))
 coreRequire(t,"save E2E execution evidence",os.WriteFile(filepath.Join(dir,"core-e2e-run-latest.json"),append(data,'\n'),0600))
 if runErr!=nil {
  // Show only bounded reviewed diagnostics, not every fixture log line.
  for i,line:=range diagnostics{if i<24{t.Log(line)}}
  if len(diagnostics)==0 && len(raw)<8192{t.Log(string(raw))}
  t.Fatalf("core-e2e: scoped suite failed: %v",runErr)
 }
}
