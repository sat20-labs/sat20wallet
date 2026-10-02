#!/usr/bin/env python3
"""One-shot checked patch for PR #10; removed after applying and reviewing."""
from pathlib import Path
import subprocess

changes = {}

def replace(path, old, new):
    text = changes.get(path)
    if text is None:
        text = Path(path).read_text(encoding='utf-8')
    if text.count(old) != 1:
        raise RuntimeError(f'expected exactly one match in {path}: {old[:100]!r}')
    changes[path] = text.replace(old, new, 1)

replace('sdk/wallet/rgb11_transfer_test.go',
'''\t\t\tif len(state.TickerInfos) != 1 || state.TickerInfos[0].AssetKey != issued.AssetName.String() ||
\t\t\t\tstate.TickerInfos[0].ContractID != issued.ContractID || state.TickerInfos[0].Ticker != issued.AssetName.Ticker ||
\t\t\t\tstate.TickerInfos[0].Verified {''',
'''\t\t\texpectedLabel, err := rgb11wallet.BuildLocalDisplayName(test.request.Ticker, wallet.GetAddress(), "")
\t\t\tif err != nil {
\t\t\t\tt.Fatal(err)
\t\t\t}
\t\t\tif len(state.TickerInfos) != 1 || state.TickerInfos[0].AssetKey != issued.AssetName.String() ||
\t\t\t\tstate.TickerInfos[0].ContractID != issued.ContractID || state.TickerInfos[0].Ticker != expectedLabel ||
\t\t\t\tstate.TickerInfos[0].CanonicalName != "" || state.TickerInfos[0].Verified {''')

replace('pwa/composables/hooks/useL1Assets.ts',
'''\t  const canonicalName = String(info?.canonical_name || info?.CanonicalName ||
\t\t`${name.Protocol}:${name.Type || 'f'}:${name.Ticker || ''}`)
\t  if (known.has(canonicalName)) continue
\t  known.add(canonicalName)
\t  result.push({
\t\tid: canonicalName,
\t\tkey: canonicalName,''',
'''\t  const assetKey = `${name.Protocol}:${name.Type || 'f'}:${name.Ticker || ''}`
\t  if (known.has(assetKey)) continue
\t  known.add(assetKey)
\t  result.push({
\t\tid: assetKey,
\t\tkey: assetKey,''')
replace('pwa/composables/hooks/useL1Assets.ts',
'\t\tlabel: name.Ticker || canonicalName,',
'\t\tlabel: name.Ticker || assetKey,')

path = 'pwa/scripts/verify/rgb11-asset-naming.test.mjs'
text = Path(path).read_text(encoding='utf-8')
start = text.index('const source = await readFile(')
end = text.index('const compiled = ', start)
changes[path] = text[:start] + "const source = await readFile(new URL('../../composables/hooks/useRgb11Assets.ts', import.meta.url), 'utf8')\n" + text[end:]

for path, text in changes.items():
    Path(path).write_text(text, encoding='utf-8')
paths = sorted(set(changes) | {'sdk/wallet/rgb11_naming_test.go'})
subprocess.run(['gofmt', '-w', *[p for p in paths if p.endswith('.go')]], check=True)
subprocess.run(['git', 'diff', '--check'], check=True)
Path('.github/rgb11-naming-paths.txt').write_text('\n'.join(paths) + '\n', encoding='utf-8')
subprocess.run(['git', 'diff', '--stat'], check=True)
