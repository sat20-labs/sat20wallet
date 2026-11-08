#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
/usr/bin/python3 - <<'PY'
import json, pathlib, re
base=pathlib.Path("data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028")
path=base/"wallet-full-mcp-v5-20261030.jsonl"
events=[]
for line in path.read_text().splitlines():
    try: events.append(json.loads(line))
    except json.JSONDecodeError: pass
names={e["Test"] for e in events if e.get("Action")=="run" and e.get("Test")}
parents={"/".join(n.split("/")[:i]) for n in names for i in range(1,len(n.split("/")))}
leaves=names-parents
terminal={e["Test"]:e["Action"] for e in events if e.get("Test") and e.get("Action") in ("pass","fail","skip")}
failed=[n for n,a in terminal.items() if a=="fail"]
result={"top_started":sum("/" not in n for n in names),"leaf_started":len(leaves),
        "leaf_outcomes":{a:sum(terminal.get(n)==a for n in leaves) for a in ("pass","fail","skip")},
        "unfinished":[n for n in names if n not in terminal],
        "failed":failed,
        "package_terminal":[e for e in events if not e.get("Test") and e.get("Action") in ("pass","fail","skip")],
        "failure_output":[e for e in events if e.get("Test") in failed and e.get("Action")=="output"][-100:],
        "tail":events[-3:]}
exitpath=base/"wallet-full-mcp-v5-20261030.exit"
if exitpath.exists(): result["exit"]=exitpath.read_text().strip()
(base/"wallet-full-mcp-v5-summary-20261030.json").write_text(json.dumps(result,ensure_ascii=False,indent=2))
diagnostics={}
for name in failed:
    if name not in leaves: continue
    lines="".join(e.get("Output","") for e in events if e.get("Test")==name).splitlines()
    selected=[]
    for i,line in enumerate(lines):
        if re.search(r"Error:|\.go:\d+:",line):
            selected.extend(lines[i:i+4])
    diagnostics[name]=selected
(base/"wallet-full-mcp-v5-diagnostics-20261030.json").write_text(json.dumps(diagnostics,ensure_ascii=False,indent=2))
result["diagnostics"]=diagnostics
result.pop("failure_output",None)
result["skipped"]=[{"name":n,"output":[e.get("Output","") for e in events if e.get("Test")==n and e.get("Action")=="output"][-4:]} for n,a in terminal.items() if a=="skip"]
result["panic_output"]=[e for e in events if re.search(r"panic:|fatal error:|SIGSEGV|FAILFAST",e.get("Output",""))]
(base/"wallet-full-mcp-v5-summary-20261030.json").write_text(json.dumps(result,ensure_ascii=False,indent=2))
print(json.dumps({k:v for k,v in result.items() if k not in ("skipped","diagnostics")},ensure_ascii=False))
PY
