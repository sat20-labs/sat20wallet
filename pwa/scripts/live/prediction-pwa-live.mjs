import { chromium } from '@playwright/test';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const CLIENT_MNEMONIC = 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const SERVER_MNEMONIC = 'uniform bulb body vital later special era tourist build chief devote annual';
const BOOTSTRAP_MNEMONIC = 'acquire pet news congress unveil erode paddle crumble blue fish match eye';
const SGAS_TOPUP_AMOUNT = '100000';
const SGAS_TOPUP_THRESHOLD = 1000;
const DEPLOYER_WALLET_INDEX = 1;
const MATCHES = [
  {
    title: '阿根廷 vs 奥地利',
    description: '2026世界杯 2026-06-23 01:00 比赛结果预测',
    source_url: 'https://worldcup.cctv.com/2026/schedule/index.shtml',
    bet_asset: '::',
    min_bet_unit: '1',
    time_base: 'unix',
    event_time: Math.floor(new Date('2026-06-23T01:00:00+08:00').getTime() / 1000),
    bet_deadline: Math.floor(new Date('2026-06-23T00:50:00+08:00').getTime() / 1000),
    confirm_after: Math.floor(new Date('2026-06-23T04:00:00+08:00').getTime() / 1000),
    outcomes: [
      { id: 'a', text: '阿根廷赢' },
      { id: 'b', text: '奥地利赢' },
      { id: 'c', text: '平局' },
    ],
  },
  {
    title: '法国 vs 伊拉克',
    description: '2026世界杯 2026-06-23 05:00 比赛结果预测',
    source_url: 'https://worldcup.cctv.com/2026/schedule/index.shtml',
    bet_asset: 'brc20:f:sgas',
    min_bet_unit: '1',
    time_base: 'unix',
    event_time: Math.floor(new Date('2026-06-23T05:00:00+08:00').getTime() / 1000),
    bet_deadline: Math.floor(new Date('2026-06-23T04:50:00+08:00').getTime() / 1000),
    confirm_after: Math.floor(new Date('2026-06-23T08:00:00+08:00').getTime() / 1000),
    outcomes: [
      { id: 'a', text: '法国赢' },
      { id: 'b', text: '伊拉克赢' },
      { id: 'c', text: '平局' },
    ],
  },
];

async function setupWallets(page) {
  return page.evaluate(async ({ password, mnemonics, matches, sgasTopupAmount, sgasTopupThreshold, deployerWalletIndex }) => {
    const walletMod = await import('/store/wallet.ts');
    const typeMod = await import('/types/index.ts');
    const cryptoMod = await import('/utils/crypto.ts');
    const sat20Mod = await import('/utils/sat20.ts');
    const { walletStorage } = await import('/lib/walletStorage.ts');

    const wallet = walletMod.useWalletStore();
    const { Chain, Network } = typeMod;
    const sat20 = sat20Mod.default;
    const hashed = await cryptoMod.hashPassword(password);
    const unwrap = (tuple) => {
      if (tuple?.[0]) {
        throw tuple[0];
      }
      return tuple?.[1];
    };

    await walletStorage.clear();
    await walletStorage.initializeState();
    await walletStorage.setValue('env', 'prd');
    await walletStorage.setValue('network', 'testnet');
    await walletStorage.setValue('chain', 'satnet');

    for (const mnemonic of mnemonics) {
      const [err] = await wallet.importWallet(mnemonic, hashed);
      if (err) {
        throw new Error(err.message || String(err));
      }
    }

    await wallet.setPassword(hashed);
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await wallet.unlockWallet(hashed));
    await unwrap(await sat20.switchChain('testnet', hashed));
    await wallet.switchWallet(wallet.wallets[0].id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));

    const details = [];
    for (const item of wallet.wallets) {
      const address = item.accounts?.[0]?.address || '';
      const [satsErr, satsRes] = await sat20.getAssetAmount_SatsNet(address, '::');
      const [sgasErr, sgasRes] = await sat20.getAssetAmount_SatsNet(address, 'brc20:f:sgas');
      details.push({
        id: item.id,
        name: item.name,
        address,
        sats: satsErr ? { error: satsErr.message || String(satsErr) } : satsRes,
        sgas: sgasErr ? { error: sgasErr.message || String(sgasErr) } : sgasRes,
      });
    }
    const topup = {};
    if (Number(details[0]?.sgas?.availableAmt || 0) < sgasTopupThreshold) {
      await wallet.switchWallet(wallet.wallets[1].id);
      await wallet.switchToAccount(0);
      await wallet.setChain(Chain.SATNET);
      await unwrap(await sat20.switchAccount(0));
      const [sendErr, sendRes] = await sat20.sendAssets_SatsNet(details[0].address, 'brc20:f:sgas', sgasTopupAmount, 'pwa live prediction prep');
      topup.fromWalletId = wallet.walletId;
      topup.toAddress = details[0].address;
      topup.amount = sgasTopupAmount;
      topup.result = sendErr ? { error: sendErr.message || String(sendErr) } : sendRes;
      await new Promise((resolve) => setTimeout(resolve, 3000));
    }

    await wallet.switchWallet(wallet.wallets[deployerWalletIndex].id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));

    const activeAddressRes = await unwrap(await sat20.getWalletAddress(0));
    const activeSatsRes = await unwrap(await sat20.getAssetAmount_SatsNet(activeAddressRes.address, '::'));
    const activeSgasRes = await unwrap(await sat20.getAssetAmount_SatsNet(activeAddressRes.address, 'brc20:f:sgas'));
    const activeSgasUtxos = await unwrap(await sat20.getUtxosWithAsset_SatsNet(activeAddressRes.address, '1', 'brc20:f:sgas'));

    const deployed = [];
    for (const prediction of matches) {
      const [contentErr, contentRes] = await sat20.buildUnifiedContractContent(
        'agent',
        'prediction',
        JSON.stringify(prediction),
      );
      if (contentErr || !contentRes?.content) {
        throw new Error(contentErr?.message || 'buildUnifiedContractContent failed');
      }
      const req = {
        ContractType: 'agent',
        SubType: 'prediction',
        ContractContent: contentRes.content,
        ContentEncoding: contentRes.contentEncoding || 'base64',
        GasLimit: undefined,
      };
      const [estimateErr, estimateRes] = await sat20.estimateDeployUnifiedContract(req);
      if (estimateErr) {
        throw new Error(estimateErr.message || String(estimateErr));
      }
      const [deployErr, deployRes] = await sat20.deployUnifiedContract(req);
      if (deployErr) {
        deployed.push({
          prediction,
          estimate: estimateRes,
          deployError: deployErr.message || String(deployErr),
        });
        break;
      }
      deployed.push({
        prediction,
        estimate: estimateRes,
        deploy: deployRes,
      });
    }

    return {
      active: {
        walletId: wallet.walletId,
        address: wallet.address,
        network: wallet.network,
        chain: wallet.chain,
        accountIndex: wallet.accountIndex,
        wasmAddress: activeAddressRes.address,
        sats: activeSatsRes,
        sgas: activeSgasRes,
        sgasUtxos: activeSgasUtxos,
      },
      wallets: wallet.wallets.map((item) => ({
        id: item.id,
        name: item.name,
      })),
      details,
      topup,
      deployed,
      chain: wallet.chain,
      network: wallet.network,
    };
  }, {
    password: PASSWORD,
    mnemonics: [CLIENT_MNEMONIC, SERVER_MNEMONIC, BOOTSTRAP_MNEMONIC],
    matches: MATCHES,
    sgasTopupAmount: SGAS_TOPUP_AMOUNT,
    sgasTopupThreshold: SGAS_TOPUP_THRESHOLD,
    deployerWalletIndex: DEPLOYER_WALLET_INDEX,
  });
}

async function main() {
  const browser = await chromium.connectOverCDP(CDP);
  const context = browser.contexts()[0] || await browser.newContext();
  let page = context.pages().find((item) => item.url() === 'about:blank');
  if (!page) {
    page = await context.newPage();
  }
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);
  const setup = await setupWallets(page);
  await page.goto('http://localhost:5173/#/wallet', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(5000);
  console.log(JSON.stringify({
    setup,
    title: await page.title(),
    url: page.url(),
    body: (await page.locator('body').innerText()).slice(0, 1000),
  }, null, 2));
  await browser.close();
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
