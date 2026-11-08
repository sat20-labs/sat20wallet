#!/bin/sh
set -u
cd /Users/yingfeng/github/sat20wallet/sdk || exit 2
out=data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028
/usr/local/go/bin/go test -mod=readonly -list '^(Test|Example)' ./wallet > "$out/wallet-test-inventory-20261030.txt" 2>&1 || exit $?
/usr/bin/python3 - <<'PY'
import json,pathlib
p=pathlib.Path("data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028")
expected={s.strip() for s in (p/"wallet-test-inventory-20261030.txt").read_text().splitlines() if s.startswith(("Test","Example"))}
events=[json.loads(s) for s in (p/"wallet-full-mcp-v5-20261030.jsonl").read_text().splitlines()]
started={e["Test"] for e in events if e.get("Action")=="run" and e.get("Test") and "/" not in e["Test"]}
result={"expected":len(expected),"started":len(started),"missing":sorted(expected-started),"removed_or_renamed_since_run":sorted(started-expected)}
(p/"wallet-full-mcp-v5-inventory-20261030.json").write_text(json.dumps(result,indent=2))
print(json.dumps(result))
PY
