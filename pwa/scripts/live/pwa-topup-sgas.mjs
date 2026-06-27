import { chromium } from '@playwright/test';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/wallet';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const CLIENT_MNEMONIC = 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const SERVER_MNEMONIC = 'uniform bulb body vital later special era tourist build chief devote annual';
const BOOTSTRAP_MNEMONIC = 'acquire pet news congress unveil erode paddle crumble blue fish match eye';
const TOPUP_AMOUNT = process.env.SAT20_TOPUP_AMOUNT || '100000';

async function main() {
  const browser = await chromium.connectOverCDP(CDP);
  const context = browser.contexts()[0] || await browser.newContext();
  const page = context.pages()[0] || await context.newPage();
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(3000);

  const result = await page.evaluate(async ({ password, mnemonics, topupAmount }) => {
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
        throw err;
      }
    }

    await wallet.setPassword(hashed);
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await wallet.unlockWallet(hashed));
    await unwrap(await sat20.switchChain('testnet', hashed));

    const wallet1 = wallet.wallets[0];
    const wallet2 = wallet.wallets[1];
    const wallet1Addr = wallet1.accounts?.[0]?.address || '';

    await wallet.switchWallet(wallet2.id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));
    const [sendErr, sendRes] = await sat20.sendAssets_SatsNet(wallet1Addr, 'brc20:f:sgas', topupAmount, '');

    await new Promise((resolve) => setTimeout(resolve, 4000));

    await wallet.switchWallet(wallet1.id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));
    const wallet1Sgas = await unwrap(await sat20.getAssetAmount_SatsNet(wallet1Addr, 'brc20:f:sgas'));

    return {
      wallet1: { id: wallet1.id, address: wallet1Addr, sgas: wallet1Sgas },
      wallet2: { id: wallet2.id, address: wallet2.accounts?.[0]?.address || '' },
      send: sendErr ? { error: sendErr.message || String(sendErr) } : sendRes,
    };
  }, {
    password: PASSWORD,
    mnemonics: [CLIENT_MNEMONIC, SERVER_MNEMONIC, BOOTSTRAP_MNEMONIC],
    topupAmount: TOPUP_AMOUNT,
  });

  console.log(JSON.stringify(result, null, 2));
  await browser.close();
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
