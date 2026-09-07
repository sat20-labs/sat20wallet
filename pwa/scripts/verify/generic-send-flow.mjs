const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/';
const L1_API = process.env.SAT20_L1_API || 'https://apiprd.ordx.market';
const L2_API = process.env.SAT20_SATSNET_INDEXER_API || 'https://apiprd.ordx.market/satsnet/testnet';
const TESTNET4_EXPLORER_API = process.env.SAT20_TESTNET4_EXPLORER_API || 'https://mempool.space/testnet4/api';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const DRY_RUN = ['1', 'true', 'yes', 'on'].includes(String(process.env.SAT20_DRY_RUN || '').toLowerCase());
const BATCH_MODE = ['1', 'true', 'yes', 'on'].includes(String(process.env.SAT20_BATCH_SEND || '').toLowerCase());
const NETWORK_SWITCH_ONLY = ['1', 'true', 'yes', 'on'].includes(String(process.env.SAT20_NETWORK_SWITCH_ONLY || '').toLowerCase());
const TRACE_NETWORK_SWITCH = ['1', 'true', 'yes', 'on'].includes(String(process.env.SAT20_TRACE_NETWORK_SWITCH || '').toLowerCase());
const EXISTING_L1_TXID = process.env.SAT20_EXISTING_L1_TXID || '';
const EXISTING_L2_TXID = process.env.SAT20_EXISTING_L2_TXID || '';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const q = (value) => JSON.stringify(value);

async function connect(wsUrl) {
  const ws = new WebSocket(wsUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener('open', resolve, { once: true });
    ws.addEventListener('error', reject, { once: true });
  });
  let id = 0;
  const callbacks = new Map();
  ws.addEventListener('message', (event) => {
    const message = JSON.parse(event.data);
    const pending = callbacks.get(message.id);
    if (!pending) return;
    clearTimeout(pending.timer);
    callbacks.delete(message.id);
    if (message.error) pending.reject(new Error(JSON.stringify(message.error)));
    else pending.resolve(message.result);
  });
  const send = (method, params = {}, timeout = 180000) => {
    const callID = ++id;
    ws.send(JSON.stringify({ id: callID, method, params }));
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        callbacks.delete(callID);
        reject(new Error(`CDP timeout: ${method}`));
      }, timeout);
      callbacks.set(callID, { resolve, reject, timer });
    });
  };
  return { ws, send };
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
      if (globalThis.sat20wallet_wasm && window.__SAT20_PWA_VERIFY__) return resolve(true);
      await new Promise(r => setTimeout(r, 500));
    }
    resolve(false);
  })`);
  if (!ready) throw new Error('PWA WASM did not load');
}

async function preparePwa(client, page) {
  await client.send('Page.enable');
  if (!page.url.startsWith(PWA_URL)) {
    await client.send('Page.navigate', { url: PWA_URL });
    await sleep(2500);
  }
  await waitForWasm(client);
  await evaluate(client, `(async () => {
    const verify = window.__SAT20_PWA_VERIFY__;
    const wallet = verify.useWalletStore();
		const credential = ${q(PASSWORD)};
    if (!wallet.hasWallet) throw new Error('test wallet is not imported');
    if (wallet.locked) {
			const [error] = await wallet.unlockWallet(credential);
      if (error) throw error;
    }
	const [sessionUnlockErr] = await wallet.unlockWallet(credential);
	if (sessionUnlockErr) throw sessionUnlockErr;
    await verify.walletStorage.setValue('env', 'prd');
    if (wallet.network === verify.Network.TESTNET &&
        !String(wallet.address || '').startsWith('tb1')) {
      await wallet.setNetwork(verify.Network.MAINNET);
    }
    if (wallet.network !== verify.Network.TESTNET) {
      await wallet.setNetwork(verify.Network.TESTNET);
    }
    if (!String(wallet.address || '').startsWith('tb1')) {
      throw new Error('testnet wallet identity is not active');
    }
    return true;
  })()`);
}

async function walletCall(client, body) {
  const raw = await evaluate(client, `(async () => {
    const verify = window.__SAT20_PWA_VERIFY__;
    const wallet = verify.useWalletStore();
    const { Chain, Network } = verify;
    const sat20 = verify.sat20;
    const unwrap = (tuple) => {
      if (tuple?.[0]) throw new Error(tuple[0].message || String(tuple[0]));
      return tuple?.[1];
    };
    ${body}
  })()`);
  return JSON.parse(raw);
}

async function fetchJSON(url) {
  const response = await fetch(url);
  const text = await response.text();
  try {
    return JSON.parse(text);
  } catch {
    return { status: response.status, text };
  }
}

async function waitForRawTx(base, path, txid) {
  let last;
  for (let i = 0; i < 12; i++) {
    if (i > 0) await sleep(5000);
    try {
      last = await fetchJSON(`${base}${path}${txid}`);
    } catch (error) {
      last = { error: error?.message || String(error) };
      continue;
    }
    if (last?.code === 0 && last?.data) {
      return { code: last.code, msg: last.msg, rawLength: String(last.data).length };
    }
  }
  return last;
}

async function waitForTestnet4RawTx(txid) {
  let last;
  for (let i = 0; i < 12; i++) {
    if (i > 0) await sleep(5000);
    try {
      const response = await fetch(`${TESTNET4_EXPLORER_API}/tx/${txid}/hex`);
      const raw = await response.text();
      last = { status: response.status, rawLength: raw.length };
      if (response.ok && /^[0-9a-f]+$/i.test(raw)) return last;
    } catch (error) {
      last = { error: error?.message || String(error) };
    }
  }
  return last;
}

async function main() {
  const pages = await fetch(`${CDP}/json/list`).then((response) => response.json());
  const page = pages.find((item) => item.type === 'page' && item.url.startsWith(PWA_URL))
    || pages.find((item) => item.type === 'page' && item.url === 'about:blank')
    || pages.find((item) => item.type === 'page');
  if (!page) throw new Error('no browser page available');
  const client = await connect(page.webSocketDebuggerUrl);
  await preparePwa(client, page);

  const accounts = await walletCall(client, `
    await wallet.switchToAccount(1);
    const destination = wallet.address;
    await wallet.switchToAccount(0);
    return JSON.stringify({ source: wallet.address, destination });
  `);

  if (NETWORK_SWITCH_ONLY) {
    const networkSwitch = await walletCall(client, `
      await wallet.switchToAccount(0);
      const before = { network: wallet.network, address: wallet.address };
      const trace = [];
      const originalFetch = window.fetch;
      const originals = {};
      if (${TRACE_NETWORK_SWITCH}) {
        window.fetch = async (...args) => {
          const started = performance.now();
          const input = args[0];
          const rawUrl = typeof input === 'string' ? input : input?.url;
          let endpoint = String(rawUrl || '');
          try {
            const parsed = new URL(endpoint, window.location.href);
            endpoint = parsed.host + parsed.pathname;
          } catch {}
          try {
            const response = await originalFetch(...args);
            trace.push({ type: 'fetch', endpoint, method: args[1]?.method || input?.method || 'GET', status: response.status, elapsedMs: Math.round(performance.now() - started) });
            return response;
          } catch (error) {
            trace.push({ type: 'fetch', endpoint, method: args[1]?.method || input?.method || 'GET', error: error?.message || String(error), elapsedMs: Math.round(performance.now() - started) });
            throw error;
          }
        };
        for (const name of ['switchChain', 'release', 'init', 'getAllChannels']) {
          if (typeof sat20[name] !== 'function') continue;
          originals[name] = sat20[name];
          sat20[name] = async function (...args) {
            const started = performance.now();
            try {
              return await originals[name].apply(this, args);
            } finally {
              trace.push({ type: 'method', name, elapsedMs: Math.round(performance.now() - started) });
            }
          };
        }
      }
      try {
        const startedMainnet = Date.now();
        await wallet.setNetwork(Network.MAINNET);
        const mainnet = {
          network: wallet.network,
          address: wallet.address,
          elapsedMs: Date.now() - startedMainnet,
        };
        const startedTestnet = Date.now();
        await wallet.setNetwork(Network.TESTNET);
        const restored = {
          network: wallet.network,
          address: wallet.address,
          elapsedMs: Date.now() - startedTestnet,
        };
        return JSON.stringify({ before, mainnet, restored, trace });
      } finally {
        if (${TRACE_NETWORK_SWITCH}) {
          window.fetch = originalFetch;
          for (const [name, original] of Object.entries(originals)) sat20[name] = original;
        }
      }
    `);
    if (!String(networkSwitch.mainnet.address || '').startsWith('bc1')) {
      throw new Error(`mainnet address is invalid: ${networkSwitch.mainnet.address}`);
    }
    if (networkSwitch.restored.network !== 'testnet' ||
        networkSwitch.restored.address !== networkSwitch.before.address) {
      throw new Error(`testnet wallet state was not restored: ${JSON.stringify(networkSwitch)}`);
    }
    console.log(JSON.stringify({ mode: 'network-switch-read-only', networkSwitch }, null, 2));
    client.ws.close();
    return;
  }

  if (DRY_RUN) {
    console.log(JSON.stringify({ mode: 'dry-run', accounts }, null, 2));
    client.ws.close();
    return;
  }

  let l2Txid = EXISTING_L2_TXID;
  if (!l2Txid) {
    console.log(`[generic-send] broadcasting L2 ${BATCH_MODE ? 'two 1-sat outputs' : '1 sat'} from account 0 to account 1`);
    const l2 = await walletCall(client, `
      await wallet.switchToAccount(0);
      await wallet.setChain(Chain.SATNET);
      const result = unwrap(await sat20.${BATCH_MODE ? 'batchSendAssets_SatsNet' : 'sendAssets_SatsNet'}(
        ${q(accounts.destination)}, '::', '1'${BATCH_MODE ? ', 2' : ", ''"}));
      return JSON.stringify(result);
    `);
    l2Txid = l2?.txId || l2;
    console.log(`[generic-send] L2 txid: ${l2Txid}`);
  }
  const l2RawTx = await waitForRawTx(L2_API, '/btc/rawtx/', l2Txid);

  let l1Txid = EXISTING_L1_TXID;
  if (!l1Txid) {
    console.log(`[generic-send] broadcasting L1 ${BATCH_MODE ? 'two 600-sat outputs' : '600 sats'} from account 0 to account 1`);
    const l1 = await walletCall(client, `
      await wallet.switchToAccount(0);
      await wallet.setChain(Chain.BTC);
      const result = unwrap(await sat20.${BATCH_MODE ? 'batchSendAssets' : 'sendAssets'}(
        ${q(accounts.destination)}, '::', '600'${BATCH_MODE ? ", 2, '1'" : ", '1'"}));
      return JSON.stringify(result);
    `);
    l1Txid = l1?.txId || l1;
    console.log(`[generic-send] L1 txid: ${l1Txid}`);
  }
  const l1RawTx = await waitForTestnet4RawTx(l1Txid);

  console.log(JSON.stringify({
    mode: BATCH_MODE ? 'default-broadcast-batch' : 'default-broadcast',
    accounts,
    l2: { txid: l2Txid, rawTx: l2RawTx },
    l1: { txid: l1Txid, rawTx: l1RawTx },
  }, null, 2));
  client.ws.close();
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
