const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://127.0.0.1:5173/';
const MNEMONIC = process.env.SAT20_TEST_MNEMONIC || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const DRY_RUN = ['1', 'true', 'yes', 'on'].includes(String(process.env.SAT20_DRY_RUN || '').toLowerCase());
const NOW = Number(process.env.SAT20_PREDICTION_NOW || Math.floor(Date.now() / 1000));
const DEPLOY_NONCE = Number(process.env.SAT20_DEPLOY_NONCE || Date.now() * 1000);
if (!Number.isSafeInteger(NOW) || NOW <= 0) {
  throw new Error('SAT20_PREDICTION_NOW must be a positive Unix timestamp');
}
if (!Number.isSafeInteger(DEPLOY_NONCE) || DEPLOY_NONCE <= 0) {
  throw new Error('SAT20_DEPLOY_NONCE must be a positive JavaScript safe integer');
}

const PREDICTION = {
  subtype: 'prediction',
  title: `PWA prediction verification ${NOW}`,
  description: 'Automated testnet prediction lifecycle verification',
  time_base: 'unix',
  event_time: NOW + 24 * 60 * 60,
  bet_deadline: NOW + 12 * 60 * 60,
  confirm_after: NOW + 25 * 60 * 60,
  source_url: 'https://sat20.org/',
  bet_asset: '::',
  min_bet_unit: '1000',
  outcomes: [
    { id: 'win', text: '法国赢' },
    { id: 'lost', text: '法国输' },
    { id: 'draw', text: '平手' },
  ],
};

function q(value) {
  return JSON.stringify(value);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

async function connect(wsUrl) {
  const ws = new WebSocket(wsUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });

  let id = 0;
  const callbacks = new Map();
  ws.addEventListener('message', (event) => {
    const msg = JSON.parse(event.data);
    if (!msg.id || !callbacks.has(msg.id)) return;
    const { resolve, reject, timer } = callbacks.get(msg.id);
    clearTimeout(timer);
    callbacks.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  });

  const send = (method, params = {}, timeout = 240000) => {
    const callId = ++id;
    ws.send(JSON.stringify({ id: callId, method, params }));
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        callbacks.delete(callId);
        reject(new Error(`CDP timeout: ${method}`));
      }, timeout);
      callbacks.set(callId, { resolve, reject, timer });
    });
  };

  return { ws, send };
}

async function getPage() {
  const pages = await fetch(`${CDP}/json/list`).then((r) => r.json());
  return pages.find((p) => p.type === 'page' && p.url.startsWith(PWA_URL))
    || pages.find((p) => p.type === 'page' && p.url === 'about:blank')
    || pages.find((p) => p.type === 'page');
}

async function evaluate(client, expression, timeout = 240000) {
  const result = await client.send('Runtime.evaluate', {
    expression,
    awaitPromise: true,
    returnByValue: true,
  }, timeout);
  if (result.exceptionDetails) {
    throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
  }
  return result.result.value;
}

async function waitForWasm(client) {
  const ready = await evaluate(client, `new Promise(async (resolve) => {
    for (let i = 0; i < 120; i++) {
      if (globalThis.sat20wallet_wasm) return resolve(true);
      await new Promise(r => setTimeout(r, 1000));
    }
    resolve(false);
  })`);
  if (!ready) throw new Error('PWA WASM did not load');
}

async function waitForVerifyApi(client) {
  const ready = await evaluate(client, `new Promise(async (resolve) => {
    for (let i = 0; i < 120; i++) {
      if (window.__SAT20_PWA_VERIFY__) return resolve(true);
      await new Promise(r => setTimeout(r, 1000));
    }
    resolve(false);
  })`);
  if (!ready) throw new Error('__SAT20_PWA_VERIFY__ did not load');
}

async function preparePwa(client, page) {
  await client.send('Page.enable');
  if (!page.url.startsWith(PWA_URL)) {
    await client.send('Page.navigate', { url: PWA_URL });
    await sleep(3000);
  }
  await waitForWasm(client);
  await evaluate(client, `(async () => {
    const { walletStorage } = await import('/lib/walletStorage.ts');
    await walletStorage.initializeState();
    await walletStorage.setValue('env', 'prd');
    await walletStorage.setValue('network', 'testnet');
    await walletStorage.setValue('chain', 'satnet');
    return true;
  })()`);
  await client.send('Page.reload', { ignoreCache: true });
  await sleep(4000);
  await waitForWasm(client);
  await waitForVerifyApi(client);
}

async function walletCall(client, body) {
  const raw = await evaluate(client, `(async () => {
    const verify = window.__SAT20_PWA_VERIFY__;
    if (!verify) throw new Error('__SAT20_PWA_VERIFY__ is not available');
    const wallet = verify.useWalletStore();
		const { Chain, Network, sat20 } = verify;
		const credential = ${q(PASSWORD)};
    const unwrap = (tuple) => {
      if (tuple?.[0]) throw tuple[0];
      return tuple?.[1];
    };
    const safe = async (fn) => {
      try {
        return await fn();
      } catch (error) {
        return { error: error?.message || String(error) };
      }
    };
    const debug = {};
    const wasmExists = await safe(async () => unwrap(await sat20.isWalletExist()));
    debug.wasmExistsBefore = wasmExists;
    if (!wasmExists?.exists) {
			const [importErr] = await wallet.importWallet(${q(MNEMONIC)}, credential);
      debug.importErr = importErr?.message || String(importErr || '');
      if (importErr) throw importErr;
    } else {
			const [unlockErr, unlockRes] = await sat20.unlockWallet(credential);
      debug.directUnlockErr = unlockErr?.message || String(unlockErr || '');
      debug.directUnlockRes = unlockRes;
      if (unlockErr && !String(unlockErr.message || unlockErr).includes('wallet has been unlocked')) throw unlockErr;
    }
    debug.wasmExistsAfter = await safe(async () => unwrap(await sat20.isWalletExist()));
    const activeWalletId = debug.directUnlockRes?.walletId;
    if (activeWalletId) {
		debug.switchWallet = await safe(async () => unwrap(await sat20.switchWallet(String(activeWalletId), credential)));
    }
	debug.switchChain = 'disabled: PWA setNetwork recreates the manager';
    debug.switchAccount = await safe(async () => unwrap(await sat20.switchAccount(0)));
    debug.address0 = await safe(async () => unwrap(await sat20.getWalletAddress(0)));
	const [sessionUnlockErr] = await wallet.unlockWallet(credential);
	if (sessionUnlockErr) throw sessionUnlockErr;
    await wallet.setChain(Chain.SATNET);
    ${body}
  })()`);
  return JSON.parse(raw);
}

async function main() {
  console.log(`[prediction] deploy nonce: ${DEPLOY_NONCE}`);
  const page = await getPage();
  if (!page?.webSocketDebuggerUrl) throw new Error('No debuggable PWA page');
  const client = await connect(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  await preparePwa(client, page);

  const result = await walletCall(client, `
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    const prediction = ${q(PREDICTION)};
    const built = unwrap(await sat20.buildUnifiedContractContent('agent', 'prediction', JSON.stringify(prediction)));
    const req = {
      ContractType: 'agent',
      SubType: 'prediction',
      DeployNonce: ${DEPLOY_NONCE},
      ContractContent: built.content,
      ContentEncoding: built.contentEncoding,
    };
    const version = await safe(async () => unwrap(await sat20.getVersion()));
    const deploy = await safe(async () => unwrap(await sat20.${DRY_RUN ? 'estimateDeployUnifiedContract' : 'deployUnifiedContract'}(req)));
    return JSON.stringify({
      mode: ${q(DRY_RUN ? 'dry-run-estimate' : 'default-broadcast')},
      address: wallet.address,
      network: wallet.network,
      chain: wallet.chain,
      debug,
      version,
      prediction,
      contentEncoding: built.contentEncoding,
      contentLength: built.content.length,
      deployNonce: ${DEPLOY_NONCE},
      deploy,
    }, null, 2);
  `);

  if (result.deploy?.error) {
    throw new Error(`prediction ${DRY_RUN ? 'estimate' : 'deploy'} failed: ${result.deploy.error}`);
  }
  if (!DRY_RUN && result.deploy?.contractAddress) {
    let indexed = null;
    for (let i = 0; i < 12; i++) {
      if (i > 0) await sleep(5000);
      indexed = await walletCall(client, `
        const query = await safe(async () => unwrap(await sat20.queryContract({
          ContractType: 'agent',
          Query: 'info',
          Contract: ${q(result.deploy.contractAddress)},
        })));
        return JSON.stringify(query);
      `);
      if (indexed?.result) break;
    }
    result.indexed = indexed;
  }

  console.log(JSON.stringify(result, null, 2));
  client.ws.close();
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
