#!/bin/bash
set -euo pipefail
python3 - <<'PY'
import json
from pathlib import Path
roots = [
 Path('/Users/yingfeng/github/transcend/data/sdk-acceptance-20261030'),
 Path('/Users/yingfeng/github/sat20wallet/sdk/data/contract-rgb11-dkvs-20261028/unified-acceptance-20261028'),
]
patterns = ['transcend-testall-v4-20261030.jsonl', 'pwa-rgb-nia-workflow-v*-20261030.jsonl', 'wallet-dkvs-affected-group-v3-20261030.jsonl', 'wallet-uncovered*.jsonl']
for root in roots:
 for pat in patterns:
  paths = list(root.glob(pat))
  if not paths:
   continue
  path = max(paths, key=lambda p:p.stat().st_mtime)
  runs, ends, failed, package, last = set(), {}, [], None, None
  panic = False
  errors = []
  capture = 0
  last_finished = None
  last_time = None
  with path.open(errors='replace') as f:
   for line in f:
    try: event=json.loads(line)
    except (ValueError, TypeError): continue
    name, action=event.get('Test'),event.get('Action')
    last_time = event.get('Time', last_time)
    if action=='run' and name: runs.add(name)
    if action in ('pass','fail','skip'):
     if name:
      ends[name]=action
      if action=='fail': failed.append(name)
     else: package={'action':action,'elapsed':event.get('Elapsed')}
    if name: last=name
    out = event.get('Output','')
    if 'finished' in out and out.strip().startswith('Test'):
     last_finished={'time':event.get('Time'),'message':out.strip()[:160]}
    if 'panic:' in out: panic=True
    if any(mark in out for mark in ('Error Trace:', 'Error:', 'panic:')): capture = 5
    if capture and out:
     errors.append(out.strip()[:220])
     capture -= 1
     errors = errors[-15:]
  exit_path=path.with_suffix('.exit')
  print(json.dumps({'file':str(path),'bytes':path.stat().st_size,'started':len(runs),'terminal':len(ends),
   'counts':{s:sum(v==s for v in ends.values()) for s in ('pass','fail','skip')},
   'unfinished':sorted(runs-set(ends))[-12:],'failed':failed[-15:],
   'last_time':last_time,'last_finished':last_finished,'last_test':last,'package':package,'panic_seen':panic,'recent_error_lines':errors,
   'exit':exit_path.read_text().strip() if exit_path.exists() else None},ensure_ascii=False))
PY
