#!/bin/bash
set -euo pipefail
python3 - <<'PY'
import pathlib,json
roots=[pathlib.Path('/Users/yingfeng/github/sat20wallet/sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028'),pathlib.Path('/Users/yingfeng/github/transcend/data/sdk-acceptance-20261030')]
for root in roots:
    files=sorted((p for p in root.iterdir() if p.is_file()),key=lambda p:p.stat().st_mtime,reverse=True)[:18]
    for p in files:
        item={'name':p.name,'root':str(root),'bytes':p.stat().st_size,'mtime':p.stat().st_mtime}
        if p.suffix=='.exit':item['exit']=p.read_text().strip()
        if p.suffix=='.jsonl':
            recent=[];package=None
            with p.open() as f:
                for line in f:
                    try:e=json.loads(line)
                    except ValueError:continue
                    if e.get('Action') in ('run','pass','fail','skip'):
                        recent.append({k:e[k] for k in ('Time','Action','Test','Elapsed') if k in e});recent=recent[-6:]
                        if 'Test' not in e:package=recent[-1]
            item['recent']=recent;item['package']=package
        print(json.dumps(item))
PY
