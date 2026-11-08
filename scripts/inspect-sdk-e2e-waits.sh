#!/bin/bash
set -euo pipefail
/usr/bin/python3 - <<'PY'
import collections, datetime, json, re
from pathlib import Path
root = Path('/Users/yingfeng/github/sat20wallet/.cache/script-logs/test-runs/sat20wallet-sdk-native')
for run in ['20261103T081918Z-26557', '20261103T051814Z-10553']:
 p = root / (run + '.e2e.log')
 runs, ends, phases, slow, errors = {}, {}, [], [], []
 first = last = None
 with p.open(errors='replace') as stream:
  for raw in stream:
   try: e = json.loads(raw)
   except ValueError: continue
   stamp, test, action, out = e.get('Time'), e.get('Test'), e.get('Action'), e.get('Output', '')
   first = first or stamp
   last = stamp or last
   if action == 'run' and test: runs[test] = stamp
   if action in ('pass','fail','skip') and test:
    ends[test] = action
    if e.get('Elapsed',0) > 3: slow.append({'test':test,'action':action,'seconds':e['Elapsed'],'time':stamp})
   m = re.search(r'e2e-phase: name=(\S+) elapsed_seconds=([\d.]+)',out)
   if m: phases.append({'test':test,'phase':m[1],'seconds':float(m[2]),'time':stamp})
   if ('Error:' in out or 'panic:' in out or 'timed out' in out): errors.append({'test':test,'time':stamp,'text':out.strip()[:400]})
 sums = collections.defaultdict(lambda: [0,0,0])
 for ph in phases:
  v=sums[ph['phase']]
  v[0]+=1; v[1]+=ph['seconds'];v[2]=max(v[2],ph['seconds'])
 print(json.dumps({'run':run,'first':first,'last':last,'file_bytes':p.stat().st_size,
 'tests_started':len(runs),'terminal_counts':dict(collections.Counter(ends.values())),
 'unfinished':[{'test':t,'started':s} for t,s in runs.items() if t not in ends],
 'phase_totals':[{'phase':k,'count':v[0],'sum_seconds':round(v[1],3),'max_seconds':v[2]} for k,v in sorted(sums.items(),key=lambda kv:kv[1][1],reverse=True)],
 'slowest_tests':sorted(slow,key=lambda e:e['seconds'],reverse=True)[:35],
 'core_phases':[ph for ph in phases if ph['phase'].startswith('core_') or ph['phase'].endswith('_build')],
 'errors':errors[-20:]},ensure_ascii=False))
PY
