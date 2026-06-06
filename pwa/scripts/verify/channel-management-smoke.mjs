import { access, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, '../..');

const checks = [];

async function assertFile(relativePath, label) {
  const fullPath = path.join(root, relativePath);
  await access(fullPath);
  const info = await stat(fullPath);
  if (!info.isFile() || info.size <= 0) {
    throw new Error(`${label} is missing or empty: ${relativePath}`);
  }
  checks.push(`${label}: ${relativePath} (${info.size} bytes)`);
  return fullPath;
}

async function assertContains(relativePath, patterns, label) {
  const fullPath = path.join(root, relativePath);
  const content = await readFile(fullPath, 'utf8');
  for (const pattern of patterns) {
    const ok = pattern instanceof RegExp ? pattern.test(content) : content.includes(pattern);
    if (!ok) {
      throw new Error(`${label} missing pattern ${pattern.toString()} in ${relativePath}`);
    }
  }
  checks.push(`${label}: ${relativePath}`);
}

await assertFile('public/wasm/stpd.wasm', 'STP wasm');

await assertContains('public/service-worker.js', [
  'wasm/stpd.wasm',
  /sat20-wallet-pwa-v0\.1\.34-/,
], 'PWA offline precache');

await assertContains('utils/wasm.ts', [
  'loadStpWasm',
  'wasm/stpd.wasm',
  'satsnetStp.init',
], 'STP wasm loader');

await assertContains('utils/stp.ts', [
  'openChannel',
  'splicingIn',
  'splicingOut',
  'lockToChannel',
  'unlockFromChannel',
], 'STP wrapper API');

await assertContains('store/wallet.ts', [
  'satsnetStp.importWallet',
  'satsnetStp.unlockWallet',
  'satsnetStp.switchWallet',
  'satsnetStp.switchAccount',
], 'Wallet/STP state sync');

await assertContains('store/channel.ts', [
  'getAllChannels',
  'getCurrentChannel',
  'localbalanceL1',
], 'Channel store');

await assertContains('components/wallet/HomeHeader.vue', [
  'TranscendingMode',
  'showTranscendingMode',
], 'Mode switch entry');

await assertContains('components/wallet/AssetList.vue', [
  'ChannelCard',
  'splicing_in',
  'splicing_out',
  'lock',
  'unlock',
], 'Channel asset operations');

await assertContains('entrypoints/popup/pages/wallet/Setting.vue', [
  'EscapeHatch',
], 'Escape hatch settings entry');

await assertContains('components/approve/ApproveDeployContractRemote.vue', [
  "import sat20 from '@/utils/sat20'",
  'sat20.deployContract_Remote',
], 'Remote contract deploy uses SDK wallet wasm');

await assertContains('public/wasm/sat20wallet.wasm', [
  'deployContract_Remote',
  'stakeToBeMiner',
  'minerUnstake',
  'DeployRunes_Remote',
], 'Wallet wasm exports SDK action methods');

console.log(JSON.stringify({
  ok: true,
  checks,
}, null, 2));
