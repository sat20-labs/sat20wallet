const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/';
const STP_API = process.env.SAT20_STP_API || 'https://apiprd.ordx.market/stp/testnet';
const SATSNET_INDEXER_API = process.env.SAT20_SATSNET_INDEXER_API || 'https://apiprd.ordx.market/satsnet/testnet';
const MNEMONIC = process.env.SAT20_TEST_MNEMONIC || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const CONTRACT_URL = process.env.SAT20_L2_CONTRACT_URL || '';
const INVOKE_KIND = process.env.SAT20_L2_INVOKE_KIND || 'swap-v2';
const ALLOW_L2_INVOKE = process.env.SAT20_ALLOW_L2_INVOKE === '1';
const INVOKE_AMOUNT = process.env.SAT20_L2_INVOKE_AMOUNT || '1';
const INVOKE_UNIT_PRICE = process.env.SAT20_L2_INVOKE_UNIT_PRICE || '1';
const FEE_RATE = process.env.SAT20_L2_FEE_RATE || '1';

async function stpApi(path, options = {}) {
  const res = await fetch(STP_API + path, {
    ...options,
    headers: {
      'Content-Type': 'application/json',
      ...(options.headers || {}),
    },
  });
  const text = await res.text();
  let data;
  try {
    data = JSON.parse(text);
  } catch {
    data = text;
  }
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}: ${text}`);
  }
  return data;
}

async function indexerApi(path, options = {}) {
  const res = await fetch(SATSNET_INDEXER_API + path, options);
  const text = await res.text();
  let data;
  try {
    data = JSON.parse(text);
  } catch {
    data = text;
  }
  if (!res.ok) {
    throw new Error(`${res.status} ${res.statusText}: ${text}`);
  }
  return data;
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

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function q(value) {
  return JSON.stringify(value);
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

async function walletCall(client, body) {
  const raw = await evaluate(client, `(async () => {
    const walletMod = await import('/store/wallet.ts');
    const typeMod = await import('/types/index.ts');
    const sat20Mod = await import('/utils/sat20.ts');
    const cryptoMod = await import('/utils/crypto.ts');
    const wallet = walletMod.useWalletStore();
    const { Chain, Network } = typeMod;
    const hashed = await cryptoMod.hashPassword(${q(PASSWORD)});
    if (!wallet.hasWallet) {
      const [importErr] = await wallet.importWallet(${q(MNEMONIC)}, hashed);
      if (importErr) throw importErr;
    } else if (wallet.locked) {
      const [unlockErr] = await wallet.unlockWallet(hashed);
      if (unlockErr) throw unlockErr;
    }
    await wallet.setPassword(hashed);
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.SATNET);
    const sat20 = sat20Mod.default;
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
    ${body}
  })()`);
  return JSON.parse(raw);
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
}

function parseAssetFromContractUrl(url) {
  const parts = url.split('_');
  if (parts.length < 3) return '';
  return parts.slice(1, -1).join('_');
}

function parseContractTypeFromUrl(url) {
  return url.split('_').at(-1) || '';
}

function templateActionForKind(kind) {
  if (kind === 'launchpool-mint') {
    return { templateName: 'launchpool.tc', action: 'mint' };
  }
  if (kind === 'amm-swap') {
    return { templateName: 'amm.tc', action: 'swap' };
  }
  return { templateName: 'swap.tc', action: 'swap' };
}

function parseContractStatus(data) {
  if (!data?.status) return null;
  if (typeof data.status !== 'string') return data.status;
  return JSON.parse(data.status);
}

function parseContractHistory(data) {
  if (!data?.status) return null;
  if (typeof data.status !== 'string') return data.status;
  return JSON.parse(data.status);
}

function summarizeResponseList(response, limit = 5) {
  if (!response || !Array.isArray(response.data)) return response;
  return {
    ...response,
    total: response.total ?? response.data.length,
    data: response.data.slice(0, limit),
  };
}

async function getContractStatus(url) {
  const data = await stpApi(`/info/contract/${encodeURIComponent(url)}`);
  return parseContractStatus(data);
}

function contractSuffixForKind(kind) {
  if (kind === 'launchpool-mint') return '_launchpool.tc';
  if (kind === 'amm-swap') return '_amm.tc';
  return '_swap.tc';
}

function buildInvoke(kind, targetAsset) {
  if (kind === 'launchpool-mint') {
    return {
      invoke: JSON.stringify({
        action: 'mint',
        param: INVOKE_AMOUNT,
      }),
      walletExpression: (targetUrl) => `
        const res = unwrap(await sat20.invokeContract_SatsNet(
          ${q(targetUrl)},
          ${q(JSON.stringify({ action: 'mint', param: INVOKE_AMOUNT }))},
          ${q(FEE_RATE)}
        ));
        return JSON.stringify(res);
      `,
    };
  }

  const invokeParam = {
    action: 'swap',
    param: JSON.stringify({
      orderType: 2,
      assetName: targetAsset,
      amt: INVOKE_AMOUNT,
      unitPrice: INVOKE_UNIT_PRICE,
    }),
  };
  const invoke = JSON.stringify(invokeParam);
  return {
    invoke,
    walletExpression: (targetUrl) => `
      const res = unwrap(await sat20.invokeContractV2_SatsNet(
        ${q(targetUrl)},
        ${q(invoke)},
        '::',
        ${q(INVOKE_AMOUNT)},
        ${q(FEE_RATE)}
      ));
      return JSON.stringify(res);
    `,
  };
}

async function main() {
  const deployed = await stpApi('/info/contracts/deployed');
  const deployedUrls = Array.isArray(deployed?.url) ? deployed.url : [];
  const matchingUrls = deployedUrls.filter((url) => typeof url === 'string' && url.endsWith(contractSuffixForKind(INVOKE_KIND)));
  const targetUrl = CONTRACT_URL || matchingUrls[0];
  if (!targetUrl) throw new Error(`No ${contractSuffixForKind(INVOKE_KIND)} contract found in deployed contract list`);
  const targetAsset = parseAssetFromContractUrl(targetUrl);
  const targetContractType = parseContractTypeFromUrl(targetUrl);
  if (!targetAsset) throw new Error(`Failed to parse asset from contract URL: ${targetUrl}`);
  const templateAction = templateActionForKind(INVOKE_KIND);

  const page = await getPage();
  if (!page?.webSocketDebuggerUrl) throw new Error('No debuggable PWA page');
  const client = await connect(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  await preparePwa(client, page);

  const targetStatus = await getContractStatus(targetUrl);
  const { invoke, walletExpression } = buildInvoke(INVOKE_KIND, targetAsset);
  const feeRes = await stpApi('/info/contract/invokefee', {
    method: 'POST',
    body: JSON.stringify({ url: targetUrl, parameter: invoke }),
  });

  const accountSummary = await walletCall(client, `
    const assets = Array.from(new Set(['::', ${q(targetAsset)}]));
    const rows = [];
    for (const accountIndex of [0, 1]) {
      await wallet.switchToAccount(accountIndex);
      await wallet.setChain(Chain.SATNET);
      const balances = {};
      for (const asset of assets) {
        balances[asset] = await safe(async () => unwrap(await sat20.getAssetAmount_SatsNet(wallet.address, asset)));
      }
      rows.push({
        index: accountIndex,
        address: wallet.address,
        pubKey: wallet.publicKey,
        balances,
        satsUtxos: await safe(async () => unwrap(await sat20.getUtxosWithAsset_SatsNet(wallet.address, '1', '::'))),
      });
    }
    return JSON.stringify(rows);
  `);

  const indexerChecks = [];
  for (const account of accountSummary) {
    const summary = await indexerApi(`/v3/address/summary/${account.address}`);
    const targetAssetUtxos = await indexerApi(`/v3/address/asset/${account.address}/${encodeURIComponent(targetAsset)}?start=0&limit=5`);
    const plainUtxos = await indexerApi(`/utxo/address/${account.address}/0`);
    indexerChecks.push({
      index: account.index,
      address: account.address,
      summary: summarizeResponseList(summary, 20),
      targetAssetUtxos: summarizeResponseList(targetAssetUtxos, 5),
      plainUtxos: summarizeResponseList(plainUtxos, 5),
    });
  }

  const walletContractChecks = await walletCall(client, `
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.SATNET);
    return JSON.stringify({
      supportedContracts: await safe(async () => unwrap(await sat20.getSupportedContracts())),
      targetContractStatus: await safe(async () => unwrap(await sat20.getDeployedContractStatus(${q(targetUrl)}))),
      invokeParamTemplate: await safe(async () => unwrap(await sat20.getParamForInvokeContract(${q(templateAction.templateName)}, ${q(templateAction.action)}))),
      invokeFee: await safe(async () => unwrap(await sat20.getFeeForInvokeContract(${q(targetUrl)}, ${q(invoke)}))),
    });
  `);

  let invokeRes = { skipped: 'set SAT20_ALLOW_L2_INVOKE=1 to submit a real SatoshiNet contract invoke' };
  let postInvokeChecks = null;
  if (ALLOW_L2_INVOKE) {
    invokeRes = await walletCall(client, `
      await wallet.switchToAccount(0);
      await wallet.setChain(Chain.SATNET);
      ${walletExpression(targetUrl)}
    `);
    if (invokeRes?.txId) {
      let rawTx;
      let history;
      let historyItem;
      let statusAfter;
      for (let i = 0; i < 12; i++) {
        await sleep(i === 0 ? 3000 : 10000);
        rawTx = await indexerApi(`/btc/rawtx/${invokeRes.txId}`);
        history = parseContractHistory(await stpApi(`/info/contract/history/${encodeURIComponent(targetUrl)}?start=0&limit=20`));
        historyItem = history?.data?.find((item) => String(item?.InUtxo || '').startsWith(`${invokeRes.txId}:`));
        statusAfter = await getContractStatus(targetUrl);
        if (historyItem) break;
      }
      postInvokeChecks = {
        rawTx: rawTx?.code === 0 ? { code: rawTx.code, msg: rawTx.msg, rawLength: String(rawTx.data || '').length } : rawTx,
        matchedHistory: historyItem ?? null,
        latestHistory: history?.data?.[0] ?? null,
        statusAfter: statusAfter && {
          currentBlock: statusAfter.currentBlock,
          invokeCount: statusAfter.invokeCount,
          CheckPoint: statusAfter.CheckPoint,
          TotalDealTx: statusAfter.TotalDealTx,
          TotalInputSats: statusAfter.TotalInputSats,
          TotalOutputAssets: statusAfter.TotalOutputAssets,
        },
      };
    }
  }

  console.log(JSON.stringify({
    invokeKind: INVOKE_KIND,
    targetUrl,
    targetContractType,
    targetAsset,
    contractStatus: targetStatus && {
      status: targetStatus.status,
      enableBlock: targetStatus.enableBlock,
      currentBlock: targetStatus.currentBlock,
      AssetAmtInPool: targetStatus.AssetAmtInPool,
      SatsValueInPool: targetStatus.SatsValueInPool,
      TotalDealTx: targetStatus.TotalDealTx,
    },
    invoke,
    feeRes,
    accounts: accountSummary,
    indexerChecks,
    walletContractChecks,
    invokeRes,
    postInvokeChecks,
  }, null, 2));

  client.ws.close();
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
