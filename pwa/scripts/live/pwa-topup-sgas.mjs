import { chromium } from '@playwright/test';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/#/wallet';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const CLIENT_MNEMONIC = 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const SERVER_MNEMONIC = 'uniform bulb body vital later special era tourist build chief devote annual';
const BOOTSTRAP_MNEMONIC = 'acquire pet news congress unveil erode paddle crumble blue fish match eye';
const TOPUP_AMOUNT = process.env.SAT20_TOPUP_AMOUNT || '100000';

async function primeTestnetEnvironment(page) {
  const manifest = new URL('/manifest.webmanifest', PWA_URL);
  await page.goto(manifest.href, { waitUntil: 'domcontentloaded' });
  await page.evaluate(async () => {
    const db = await new Promise((resolve, reject) => {
      const request = indexedDB.open('sat20-wallet-pwa', 1);
      request.onupgradeneeded = () => {
        if (!request.result.objectStoreNames.contains('wallet-state')) {
          request.result.createObjectStore('wallet-state');
        }
      };
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
    await new Promise((resolve, reject) => {
      const transaction = db.transaction('wallet-state', 'readwrite');
      const store = transaction.objectStore('wallet-state');
      store.put(JSON.stringify('prd'), 'local:wallet_env');
      store.put(JSON.stringify('testnet'), 'local:wallet_network');
      store.put(JSON.stringify('satnet'), 'local:wallet_chain');
      transaction.oncomplete = resolve;
      transaction.onerror = () => reject(transaction.error);
    });
    db.close();
  });
  await page.goto(PWA_URL, { waitUntil: 'domcontentloaded' });
}

async function main() {
  const browser = await chromium.connectOverCDP(CDP);
  const context = browser.contexts()[0] || await browser.newContext();
  const page = context.pages()[0] || await context.newPage();
  await page.goto('about:blank', { waitUntil: 'domcontentloaded' });
  const session = await context.newCDPSession(page);
  await session.send('Storage.clearDataForOrigin', {
    origin: new URL(PWA_URL).origin,
    storageTypes: 'all',
  });
  await primeTestnetEnvironment(page);
  await page.waitForTimeout(3000);

  const result = await page.evaluate(async ({ password, mnemonics, topupAmount }) => {
    const walletMod = await import('/store/wallet.ts');
    const typeMod = await import('/types/index.ts');
    const sat20Mod = await import('/utils/sat20.ts');
    const { walletStorage } = await import('/lib/walletStorage.ts');

    const wallet = walletMod.useWalletStore();
    const { Chain, Network } = typeMod;
    const sat20 = sat20Mod.default;
		const credential = password;
    const unwrap = (tuple) => {
      if (tuple?.[0]) {
        throw tuple[0];
      }
      return tuple?.[1];
    };

    await walletStorage.initializeState();
    await walletStorage.setValue('env', 'prd');
    await walletStorage.setValue('network', 'testnet');
    await walletStorage.setValue('chain', 'satnet');

    for (const mnemonic of mnemonics) {
			const [err] = await wallet.importWallet(mnemonic, credential);
      if (err && /wallet already exists/i.test(err.message || String(err))) {
        await wallet.syncWalletCatalog();
        continue;
      }
      if (err) {
        throw err;
      }
    }

	await unwrap(await wallet.unlockWallet(credential));
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.SATNET);

    const wallet1 = wallet.wallets[0];
    const wallet2 = wallet.wallets[1];
    const wallet1Addr = wallet1.accounts?.[0]?.address || '';
    const wallet2Addr = wallet2.accounts?.[0]?.address || '';

    const candidates = [
      { wallet: wallet1, address: wallet1Addr },
      { wallet: wallet2, address: wallet2Addr },
    ];
    for (const candidate of candidates) {
      await wallet.switchWallet(candidate.wallet.id);
      await wallet.switchToAccount(0);
      await wallet.setChain(Chain.SATNET);
      await unwrap(await sat20.switchAccount(0));
      candidate.sgas = await unwrap(
        await sat20.getAssetAmount_SatsNet(candidate.address, 'brc20:f:sgas'),
      );
    }
    const required = BigInt(topupAmount);
    const senderIndex = candidates.findIndex((candidate) =>
      BigInt(candidate.sgas?.availableAmt || '0') >= required);
    if (senderIndex < 0) {
      throw new Error('neither top-up wallet has enough brc20:f:sgas');
    }
    const receiverIndex = senderIndex === 0 ? 1 : 0;
    const sender = candidates[senderIndex];
    const receiver = candidates[receiverIndex];
    await wallet.switchWallet(sender.wallet.id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));
    const [sendErr, sendRes] = await sat20.sendAssets_SatsNet(
      receiver.address, 'brc20:f:sgas', topupAmount, '',
    );

    await new Promise((resolve) => setTimeout(resolve, 4000));

    await wallet.switchWallet(receiver.wallet.id);
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    await unwrap(await sat20.switchAccount(0));
    const receiverSgas = await unwrap(
      await sat20.getAssetAmount_SatsNet(receiver.address, 'brc20:f:sgas'),
    );

    return {
      sender: { id: sender.wallet.id, address: sender.address, before: sender.sgas },
      receiver: { id: receiver.wallet.id, address: receiver.address, after: receiverSgas },
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
