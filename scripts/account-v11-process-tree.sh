#!/bin/bash
set -eu
# Read-only process metadata for this exact E2E run; no command arguments or environment.
/usr/bin/python3 - <<'PY'
import subprocess
rows = []
for line in subprocess.check_output(['/bin/ps','-axo','pid=,ppid=,etime=,%cpu=,state=,comm='], text=True).splitlines():
    parts = line.strip().split(None, 5)
    if len(parts) == 6:
        rows.append((int(parts[0]), int(parts[1]), parts))
selected = {59989}
while True:
    found = selected | {pid for pid, parent, _ in rows if parent in selected}
    if found == selected:
        break
    selected = found
for pid, parent, parts in rows:
    if pid in selected:
        print(' '.join(parts))
PY
