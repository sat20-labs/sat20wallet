import { access, readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(__dirname, '../..');

const checks = [];

const readJson = async (relativePath) => {
  const fullPath = path.join(root, relativePath);
  return JSON.parse(await readFile(fullPath, 'utf8'));
};

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

async function assertNotContains(relativePath, patterns, label) {
  const fullPath = path.join(root, relativePath);
  const content = await readFile(fullPath, 'utf8');
  for (const pattern of patterns) {
    const found = pattern instanceof RegExp ? pattern.test(content) : content.includes(pattern);
    if (found) {
      throw new Error(`${label} contains obsolete pattern ${pattern.toString()} in ${relativePath}`);
    }
  }
  checks.push(`${label}: ${relativePath}`);
}

await assertFile('public/wasm/sat20wallet.wasm', 'Wallet wasm');

const version = await readJson('public/version.json');
if (!version.version || !version.buildId) {
  throw new Error('public/version.json must contain version and buildId');
}
const expectedCacheName = `sat20-wallet-pwa-v${version.version}-${version.buildId}`;

await assertContains('public/service-worker.js', [
  'wasm/sat20wallet.wasm',
  expectedCacheName,
], 'PWA offline precache');

await assertContains('utils/wasm.ts', [
  'wasm/sat20wallet.wasm',
  'walletManager.init',
], 'Wallet wasm loader');

await assertContains('utils/stp.ts', [
  'sat20wallet_wasm',
  'openChannel',
  'splicingIn',
  'splicingOut',
  'lockToChannel',
  'unlockFromChannel',
  'parseLockExpandRequiredAmount',
], 'Channel wrapper API');

await assertContains('composables/useAssetActions.ts', [
  'parseLockExpandRequiredAmount',
  'lockUtxoWithExpand',
], 'Capacity-aware channel lock action');

await assertContains('components/wallet/LockWithExpandConfirmDialog.vue', [
  'lockExpandFeeInfo',
  "@click.prevent=\"$emit('confirm')\"",
], 'Paid channel expansion confirmation');

await assertContains('store/channel.ts', [
  'getCurrentChannel',
  'getCurrentChannelPromise',
  'isMissingCurrentChannelError',
  'clearChannelState()',
  'localbalanceL1',
], 'Channel store');

await assertNotContains('store/channel.ts', [
  'satsnetStp.getAllChannels()',
  'channels.length === 0',
], 'Channel store uses current channel only');

await assertContains('entrypoints/popup/pages/wallet/index.vue', [
  "case 'channelrestored':",
  "case 'expanded':",
  'await channelStore.getCurrentChannel()',
], 'Recovered channel refresh callback');

await assertNotContains('entrypoints/popup/pages/wallet/index.vue', [
  "case 'expanded\"':",
], 'Expanded channel callback spelling');

await assertContains('components/setting/EscapeHatch.vue', [
  ':disabled="loading"',
  '@click="requestForceClose"',
  'v-model:open="showForceCloseConfirm"',
  'await satsnetStp.safetySnapshot(id)',
  'assessStpValueMovementSafety(snapshot)',
  'await satsnetStp.closeChannel(id, btcFeeRate.value, false)',
  'await satsnetStp.closeChannel(id, btcFeeRate.value, true)',
  'finally',
], 'Escape hatch explicit safety-gated force close');

const escapeHatchSource = await readFile(path.join(root, 'components/setting/EscapeHatch.vue'), 'utf8');
const cooperativeCloseStart = escapeHatchSource.indexOf('const closeChannel = async () =>');
const cooperativeCloseEnd = escapeHatchSource.indexOf('const readSafeForceCloseSnapshot = async');
if (cooperativeCloseStart < 0 || cooperativeCloseEnd <= cooperativeCloseStart) {
  throw new Error('Unable to locate cooperative close function boundaries');
}
const cooperativeCloseSource = escapeHatchSource.slice(cooperativeCloseStart, cooperativeCloseEnd);
if (cooperativeCloseSource.includes('closeChannel(id, btcFeeRate.value, true)')) {
  throw new Error('Cooperative close must not automatically fall back to force close');
}
checks.push('Escape hatch cooperative close has no force-close fallback');

await assertNotContains('entrypoints/popup/pages/wallet/index.vue', [
  'channelStore.getAllChannels',
], 'Wallet page uses current channel only');

await assertNotContains('store/wallet.ts', [
  'channelStore.getAllChannels',
], 'Wallet store uses current channel only');

await assertNotContains('composables/useAssetActions.ts', [
  'channelStore.getAllChannels',
], 'Asset actions use current channel only');

await assertNotContains('components/wallet/ChannelCard.vue', [
  'channelStore.getAllChannels',
], 'Channel card uses current channel only');

await assertNotContains('utils/stp.ts', [
  "_handleRequest('start')",
], 'Obsolete STP start wrapper removed');

await assertNotContains('store/wallet.ts', [
  'satsnetStp.start()',
], 'Obsolete STP start calls removed');

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
  'LockWithExpandConfirmDialog',
  'confirmLockWithExpand',
], 'Channel asset operations');

await assertContains('components/asset/BalanceSummary.vue', [
  'LockWithExpandConfirmDialog',
  'confirmLockWithExpand',
], 'Balance channel lock expansion flow');

await assertContains('entrypoints/popup/pages/wallet/Setting.vue', [
  'EscapeHatch',
], 'Escape hatch settings entry');

await assertContains('components/approve/ApproveDeployContractRemote.vue', [
  "import sat20 from '@/utils/sat20'",
  'sat20.deployContract_Remote',
], 'Remote contract deploy uses SDK wallet wasm');

await assertContains('public/wasm/sat20wallet.wasm', [
  'getCommitTxAssetInfo',
  'safetySnapshot',
  'commitmentExport',
  'punishStatus',
  'punishBuild',
  'punishBroadcast',
  'forceClosePlan',
  'sweepBuild',
  'deployContract_Remote',
  'stakeToBeMiner',
  'minerUnstake',
  'DeployRunes_Remote',
], 'Wallet wasm exports SDK action methods');

console.log(JSON.stringify({
  ok: true,
  checks,
}, null, 2));
