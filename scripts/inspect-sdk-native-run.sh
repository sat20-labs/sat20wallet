#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
/usr/bin/python3 - <<'PY'
import collections, json, pathlib, re, subprocess
root = pathlib.Path('.cache/script-logs/test-runs/sat20wallet-sdk-native')
choices = sorted(p for p in root.glob('*.status') if re.fullmatch(r'\d{8}T\d{6}Z-\d+\.status', p.name))
if not choices:
    print('No SDK native run found')
    raise SystemExit(0)
status = choices[-1]
run = status.stem
st = dict(line.split('=', 1) for line in status.read_text().splitlines() if '=' in line)
events = root / (run + '.events.jsonl')
ended = {}
active = set()
pack_ends = {}
tail = {}
notes = collections.defaultdict(lambda: collections.deque(maxlen=12))
wanted = {'TestSDKAccountReview','TestBTCLuckyRegtestE2E','TestBTCLuckyManagerRegtestE2E','TestSDKNativeInterfaces','TestSDKUIFlows','TestSPBitcoinCoreRegtestIndexerE2E','TestSPManagerBitcoinCoreSendReceiveE2E','TestSDKSmartContractsFunding'}
if events.is_file():
    with events.open(errors='replace') as f:
        for line in f:
            try:
                e = json.loads(line)
            except ValueError:
                continue
            action, pkg, name = (e.get(key) for key in ('Action', 'Package', 'Test'))
            tail = {key: e.get(key) for key in ('Time', 'Action', 'Package', 'Test')}
            if name:
                key = (pkg, name)
                top = name.split('/', 1)[0]
                out = str(e.get('Output') or '').strip()
                if top in wanted and action == 'output' and re.search(r'Error:|Error Trace:|Messages:|context cancel|unexpected|not available|executable file|requires|not found|exit status|timed out|[.]go:\d+:', out, re.I) and not re.search(r'password|mnemonic|secret|private.?key|token|cookie|seed|credential', out, re.I):
                    notes[top].append(out[:250])
                if action == 'run':
                    active.add(key)
                if action in ('pass', 'fail', 'skip'):
                    active.discard(key)
                    ended[key] = action
            elif action in ('pass', 'fail', 'skip'):
                pack_ends[pkg] = action
pid = int(run.rsplit('-', 1)[1])
ps = subprocess.run(['/bin/ps','-axo','pid=,ppid=,state=,etime=,comm='], capture_output=True, text=True)
rows = {}
for line in ps.stdout.splitlines():
    fields = line.split(None,4)
    if len(fields)==5 and all(v.isdigit() for v in fields[:2]):
        rows[int(fields[0])] = fields
owned = {pid} if pid in rows else set()
while True:
    implied = owned | {k for k, v in rows.items() if int(v[1]) in owned}
    if owned == implied:
        break
    owned = implied
print(json.dumps(dict(run=run,state=st.get('state'),exit_code=st.get('exit_code'),last_event=tail,
    packages=collections.Counter(pack_ends.values()),
    top_level=collections.Counter(a for (_,n),a in ended.items() if '/' not in n),
    failures=[k for k,a in ended.items() if a=='fail'][-30:],
    active=sorted(active)[-20:],failure_diagnostics={t:list(v) for t,v in notes.items() if any(n.split('/',1)[0] == t and a == 'fail' for (_,n),a in ended.items())},ps_ok=ps.returncode==0,
    launcher_tree=[rows[k] for k in sorted(owned)][:30]), ensure_ascii=False))
PY
