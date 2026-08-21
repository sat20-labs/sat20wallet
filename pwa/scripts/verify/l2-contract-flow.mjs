import { formatInvokePollFailure, pollInvokeEvidence } from './lib/invoke-poll.mjs';
import { rankPwaPages, selectPwaPage } from './lib/pwa-page.mjs';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/';
const STP_API = process.env.SAT20_STP_API || 'https://apiprd.ordx.market/stp/testnet';
const SATSNET_INDEXER_API = process.env.SAT20_SATSNET_INDEXER_API || 'https://apiprd.ordx.market/satsnet/testnet';
const MNEMONIC = process.env.SAT20_TEST_MNEMONIC || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const CONTRACT_URL = process.env.SAT20_L2_CONTRACT_URL || '';
const INVOKE_KIND = process.env.SAT20_L2_INVOKE_KIND || 'swap-v2';
const HTTP_TIMEOUT_MS = Number(process.env.SAT20_HTTP_TIMEOUT_MS || 30000);
const TARGET_PROBE_TIMEOUT_MS = Number(process.env.SAT20_L2_TARGET_PROBE_TIMEOUT_MS || 5000);
const isEnabled = (value) => ['1', 'true', 'yes'].includes(String(value || '').toLowerCase());
const BROADCAST_DISABLED = isEnabled(process.env.SAT20_DRY_RUN)
  || isEnabled(process.env.SAT20_DISABLE_BROADCAST)
  || process.env.SAT20_ALLOW_L2_INVOKE === '0';
// Real testnet verification broadcasts by default. Set SAT20_DRY_RUN=1 to use
// the read-only path; SAT20_ALLOW_L2_INVOKE remains accepted for compatibility.
const ALLOW_L2_INVOKE = !BROADCAST_DISABLED;
const INVOKE_AMOUNT = process.env.SAT20_L2_INVOKE_AMOUNT || '1';
const INVOKE_UNIT_PRICE = process.env.SAT20_L2_INVOKE_UNIT_PRICE || '1';
const FEE_RATE = process.env.SAT20_L2_FEE_RATE || '1';
const MARKET_ROUNDTRIP = isEnabled(process.env.SAT20_L2_MARKET_ROUNDTRIP);
const SELL_ACCOUNT_INDEX = Number(process.env.SAT20_L2_SELL_ACCOUNT_INDEX ?? 0);
const BUY_ACCOUNT_INDEX = Number(process.env.SAT20_L2_BUY_ACCOUNT_INDEX ?? 1);
const SELL_AMOUNT = process.env.SAT20_L2_SELL_AMOUNT || '';
const SELL_UNIT_PRICE = process.env.SAT20_L2_SELL_UNIT_PRICE || '';
const BUY_AMOUNT = process.env.SAT20_L2_BUY_AMOUNT || '';
const BUY_UNIT_PRICE = process.env.SAT20_L2_BUY_UNIT_PRICE || '';
const PWA_STAGE_TIMEOUT_MS = Number(process.env.SAT20_PWA_STAGE_TIMEOUT_MS || 30000);

async function stpApi(path, options = {}, timeoutMs = HTTP_TIMEOUT_MS) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  let res;
  try {
    res = await fetch(STP_API + path, {
      ...options,
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(options.headers || {}),
      },
    });
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`HTTP timeout after ${timeoutMs}ms: ${STP_API + path}`);
    }
    throw error;
  } finally {
    clearTimeout(timer);
  }
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
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), HTTP_TIMEOUT_MS);
  let res;
  try {
    res = await fetch(SATSNET_INDEXER_API + path, { ...options, signal: controller.signal });
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`HTTP timeout after ${HTTP_TIMEOUT_MS}ms: ${SATSNET_INDEXER_API + path}`);
    }
    throw error;
  } finally {
    clearTimeout(timer);
  }
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

async function getIndexedRawTx(txId) {
  const rawTx = await indexerApi(`/btc/rawtx/${txId}`);
  if (rawTx?.code !== 0) {
    throw new Error(rawTx?.msg || `Transaction ${txId} is not indexed yet`);
  }
  return rawTx;
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
  const readyPageIds = new Set();
  for (const page of rankPwaPages(pages, PWA_URL)) {
    let probe;
    try {
      probe = await connect(page.webSocketDebuggerUrl);
      await probe.send('Runtime.enable', {}, 5000);
      const ready = await evaluate(probe, `Boolean(globalThis.sat20wallet_wasm && window.__SAT20_PWA_VERIFY__)`, 5000);
      if (ready) {
        readyPageIds.add(page.id);
        break;
      }
    } catch {
      // A stale/error CDP target is not a usable PWA page. Try the next target.
    } finally {
      probe?.ws.close();
    }
  }
  const selected = selectPwaPage(pages, PWA_URL, readyPageIds);
  return selected && { ...selected, pwaReady: readyPageIds.has(selected.id) };
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function q(value) {
  return JSON.stringify(value);
}

function decimalParts(value, label) {
  const normalized = String(value).trim();
  const match = /^(0|[1-9][0-9]*)(?:\.([0-9]+))?$/.exec(normalized);
  if (!match) throw new Error(`${label} must be a non-negative decimal string: ${value}`);
  return { integer: BigInt(match[1] + (match[2] || '')), scale: (match[2] || '').length };
}

function multiplyDecimals(left, right) {
  const a = decimalParts(left, 'amount');
  const b = decimalParts(right, 'unit price');
  const scale = a.scale + b.scale;
  const digits = (a.integer * b.integer).toString().padStart(scale + 1, '0');
  if (scale === 0) return digits;
  const result = `${digits.slice(0, -scale)}.${digits.slice(-scale)}`.replace(/\.?0+$/, '');
  return result || '0';
}

function compareDecimals(left, right) {
  const a = decimalParts(left, 'left decimal');
  const b = decimalParts(right, 'right decimal');
  const scale = Math.max(a.scale, b.scale);
  const ai = a.integer * (10n ** BigInt(scale - a.scale));
  const bi = b.integer * (10n ** BigInt(scale - b.scale));
  return ai === bi ? 0 : ai > bi ? 1 : -1;
}

function availableBalance(balance, label) {
  if (!balance || balance.error) throw new Error(`${label} balance unavailable: ${balance?.error || 'empty result'}`);
  const value = balance.availableAmt ?? balance.amount ?? balance.totalAmt ?? balance.total;
  if (value === undefined || value === null) {
    throw new Error(`${label} balance has no available amount: ${JSON.stringify(balance)}`);
  }
  return String(value);
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

async function waitForWasm(client, stage) {
  const ready = await evaluate(client, `new Promise(async (resolve) => {
    const deadline = Date.now() + ${PWA_STAGE_TIMEOUT_MS};
    while (Date.now() < deadline) {
      if (globalThis.sat20wallet_wasm && window.__SAT20_PWA_VERIFY__) return resolve(true);
      await new Promise(r => setTimeout(r, 250));
    }
    resolve(false);
  })`, PWA_STAGE_TIMEOUT_MS + 5000);
  if (!ready) throw new Error(`${stage}: PWA WASM/verify helpers did not load within ${PWA_STAGE_TIMEOUT_MS}ms`);
}

async function walletCall(client, body) {
  const raw = await evaluate(client, `(async () => {
    const verify = window.__SAT20_PWA_VERIFY__;
    if (!verify) throw new Error('SAT20 PWA verify helpers are not available');
    const { Chain, Network, hashPassword, sat20, useWalletStore } = verify;
    const wallet = useWalletStore();
    const hashed = await hashPassword(${q(PASSWORD)});
    if (!wallet.hasWallet) {
      const [importErr] = await wallet.importWallet(${q(MNEMONIC)}, hashed);
      if (importErr) throw importErr;
    } else if (wallet.locked) {
      const [unlockErr] = await wallet.unlockWallet(hashed);
      if (unlockErr) throw unlockErr;
    }
    await wallet.setPassword(hashed);
    if (wallet.network !== Network.TESTNET) await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.SATNET);
    const unwrap = (tuple) => {
      if (tuple?.[0]) throw tuple[0];
      return tuple?.[1];
    };
    const withTimeout = (promise, label, ms = 30000) => Promise.race([
      promise,
      new Promise((_, reject) => setTimeout(() => reject(new Error(label + ' timed out after ' + ms + 'ms')), ms)),
    ]);
    const safe = async (fn) => {
      try {
        return await withTimeout(fn(), 'wallet helper call');
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
  console.log(`[l2-contract] selected CDP page: ${page.url} (${page.pwaReady ? 'ready' : 'navigate required'})`);
  if (!page.pwaReady) {
    console.log('[l2-contract] navigating selected page to PWA root');
    await client.send('Page.navigate', { url: PWA_URL });
    await sleep(3000);
  }
  console.log('[l2-contract] waiting for initial PWA runtime');
  await waitForWasm(client, 'initial PWA runtime');
  console.log('[l2-contract] applying testnet/SatoshiNet storage settings');
  await evaluate(client, `(async () => {
    const { walletStorage } = window.__SAT20_PWA_VERIFY__ || {};
    if (!walletStorage) throw new Error('PWA walletStorage verify helper is unavailable');
    await walletStorage.initializeState();
    await walletStorage.setValue('env', 'prd');
    await walletStorage.setValue('network', 'testnet');
    await walletStorage.setValue('chain', 'satnet');
    return true;
  })()`, PWA_STAGE_TIMEOUT_MS);
  console.log('[l2-contract] reloading PWA with normalized settings');
  await client.send('Page.reload', { ignoreCache: true });
  await sleep(4000);
  console.log('[l2-contract] waiting for PWA runtime after reload');
  await waitForWasm(client, 'PWA runtime after reload');
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

async function getContractStatus(url, timeoutMs = HTTP_TIMEOUT_MS) {
  const data = await stpApi(`/info/contract/${encodeURIComponent(url)}`, {}, timeoutMs);
  return parseContractStatus(data);
}

async function selectTargetContract(deployedUrls) {
  const heightResponse = await indexerApi('/bestheight');
  const currentHeight = Number(heightResponse?.data?.height ?? heightResponse?.height);
  if (!Number.isFinite(currentHeight)) {
    throw new Error(`Unable to determine current SatoshiNet height: ${JSON.stringify(heightResponse)}`);
  }

  const candidates = CONTRACT_URL
    ? [CONTRACT_URL]
    : deployedUrls.filter((url) => typeof url === 'string' && url.endsWith(contractSuffixForKind(INVOKE_KIND)));
  const rejected = [];
  for (const url of candidates) {
    try {
      const status = await getContractStatus(url, TARGET_PROBE_TIMEOUT_MS);
      const statusCode = Number(status?.status);
      const contractHeight = Number(status?.currentBlock);
      if (statusCode >= 100 && statusCode < 200 && (!Number.isFinite(contractHeight) || contractHeight <= currentHeight)) {
        return { targetUrl: url, targetStatus: status, currentHeight };
      }
      rejected.push(`${url} (status=${statusCode}, currentBlock=${contractHeight})`);
    } catch (error) {
      rejected.push(`${url} (${error.message || String(error)})`);
    }
  }

  throw new Error(
    `No active ${contractSuffixForKind(INVOKE_KIND)} contract is usable at current SatoshiNet height ${currentHeight}. `
      + `Candidates: ${rejected.join('; ')}`
  );
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
  const paymentAmount = multiplyDecimals(INVOKE_AMOUNT, INVOKE_UNIT_PRICE);
  return {
    invoke,
    walletExpression: (targetUrl) => `
      const res = unwrap(await sat20.invokeContractV2_SatsNet(
        ${q(targetUrl)},
        ${q(invoke)},
        '::',
        ${q(paymentAmount)},
        ${q(FEE_RATE)}
      ));
      return JSON.stringify(res);
    `,
  };
}

function buildSwapLeg(orderType, targetAsset, amount, unitPrice) {
  const invoke = JSON.stringify({
    action: 'swap',
    param: JSON.stringify({ orderType, assetName: targetAsset, amt: amount, unitPrice }),
  });
  return {
    orderType,
    amount,
    unitPrice,
    invoke,
    inputAsset: orderType === 1 ? targetAsset : '::',
    inputAmount: orderType === 1 ? amount : multiplyDecimals(amount, unitPrice),
  };
}

async function waitForInvoke(client, targetUrl, txId, baselineStatus) {
  const evidence = await pollInvokeEvidence({
    attempts: 18,
    sleep,
    delayForAttempt: (attempt) => attempt === 0 ? 3000 : 10000,
    getRawTx: () => getIndexedRawTx(txId),
    getHistory: async () => parseContractHistory(await stpApi(`/info/contract/history/${encodeURIComponent(targetUrl)}?start=0&limit=50`)),
    getStatus: () => getContractStatus(targetUrl),
    findHistoryItem: (history) => history?.data?.find((item) => String(item?.InUtxo || '').startsWith(`${txId}:`)),
    isComplete: ({ rawTx, historyItem, statusAfter }) => Boolean(
      rawTx && historyItem && Number(statusAfter?.invokeCount) > Number(baselineStatus?.invokeCount || 0)
    ),
  });
  if (!evidence.completed) throw new Error(formatInvokePollFailure(txId, evidence));
  return {
    txId,
    rawTx: evidence.rawTx?.code === 0
      ? { code: evidence.rawTx.code, msg: evidence.rawTx.msg, rawLength: String(evidence.rawTx.data || '').length }
      : evidence.rawTx,
    matchedHistory: evidence.historyItem,
    statusAfter: evidence.statusAfter,
    pollAttempts: evidence.attempts,
    lastPollErrors: evidence.lastErrors,
  };
}

async function invokeSwapLeg(client, targetUrl, accountIndex, leg) {
  return walletCall(client, `
    await withTimeout(wallet.switchToAccount(${accountIndex}), 'switchToAccount(${accountIndex})', 30000);
    await wallet.setChain(Chain.SATNET);
    const res = unwrap(await sat20.invokeContractV2_SatsNet(
      ${q(targetUrl)}, ${q(leg.invoke)}, ${q(leg.inputAsset)}, ${q(leg.inputAmount)}, ${q(FEE_RATE)}
    ));
    return JSON.stringify(res);
  `);
}

async function main() {
  console.log('[l2-contract] loading deployed contract list');
  const deployed = await stpApi('/info/contracts/deployed');
  const deployedUrls = Array.isArray(deployed?.url) ? deployed.url : [];
  const { targetUrl, targetStatus, currentHeight } = await selectTargetContract(deployedUrls);
  const targetAsset = parseAssetFromContractUrl(targetUrl);
  const targetContractType = parseContractTypeFromUrl(targetUrl);
  if (!targetAsset) throw new Error(`Failed to parse asset from contract URL: ${targetUrl}`);
  const templateAction = templateActionForKind(INVOKE_KIND);
  if (MARKET_ROUNDTRIP) {
    if (INVOKE_KIND !== 'swap-v2') throw new Error('Market roundtrip only supports swap-v2');
    if (!CONTRACT_URL) throw new Error('SAT20_L2_CONTRACT_URL is required for market roundtrip');
    if (![SELL_ACCOUNT_INDEX, BUY_ACCOUNT_INDEX].every(Number.isInteger) || SELL_ACCOUNT_INDEX === BUY_ACCOUNT_INDEX) {
      throw new Error('Market roundtrip requires two distinct integer account indexes');
    }
    for (const [name, value] of Object.entries({ SELL_AMOUNT, SELL_UNIT_PRICE, BUY_AMOUNT, BUY_UNIT_PRICE })) {
      if (!value || compareDecimals(value, '0') <= 0) throw new Error(`${name} must be explicitly set to a positive decimal`);
    }
    if (compareDecimals(BUY_AMOUNT, SELL_AMOUNT) > 0) throw new Error('Buy amount cannot exceed sell amount');
    if (compareDecimals(BUY_UNIT_PRICE, SELL_UNIT_PRICE) < 0) throw new Error('Buy unit price must cross the sell unit price');
  }

  const page = await getPage();
  if (!page?.webSocketDebuggerUrl) throw new Error('No debuggable PWA page');
  const client = await connect(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  console.log('[l2-contract] preparing PWA');
  await preparePwa(client, page);

  console.log(`[l2-contract] reading contract status and invoke fee at height ${currentHeight}`);
  const { invoke, walletExpression } = buildInvoke(INVOKE_KIND, targetAsset);
  const feeRes = await stpApi('/info/contract/invokefee', {
    method: 'POST',
    body: JSON.stringify({ url: targetUrl, parameter: invoke }),
  });

  const accountSummary = await walletCall(client, `
    const assets = Array.from(new Set(['::', ${q(targetAsset)}]));
    const rows = [];
    for (const accountIndex of Array.from(new Set([${SELL_ACCOUNT_INDEX}, ${BUY_ACCOUNT_INDEX}]))) {
      await withTimeout(wallet.switchToAccount(accountIndex), 'switchToAccount(' + accountIndex + ')', 30000);
      await withTimeout(wallet.setChain(Chain.SATNET), 'setChain(SATNET)', 10000);
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

  console.log('[l2-contract] checking direct SatoshiNet indexer state');
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
    await withTimeout(wallet.switchToAccount(0), 'switchToAccount(0)', 30000);
    await wallet.setChain(Chain.SATNET);
    return JSON.stringify({
      supportedContracts: await safe(async () => unwrap(await sat20.getSupportedContracts())),
      targetContractStatus: await safe(async () => unwrap(await sat20.getDeployedContractStatus(${q(targetUrl)}))),
      invokeParamTemplate: await safe(async () => unwrap(await sat20.getParamForInvokeContract(${q(templateAction.templateName)}, ${q(templateAction.action)}))),
      invokeFee: await safe(async () => unwrap(await sat20.getFeeForInvokeContract(${q(targetUrl)}, ${q(invoke)}))),
    });
  `);

  console.log('[l2-contract] default broadcast mode:', BROADCAST_DISABLED ? 'disabled' : 'enabled');
  if (MARKET_ROUNDTRIP) {
    const sellLeg = buildSwapLeg(1, targetAsset, SELL_AMOUNT, SELL_UNIT_PRICE);
    const buyLeg = buildSwapLeg(2, targetAsset, BUY_AMOUNT, BUY_UNIT_PRICE);
    const sellAccount = accountSummary.find((row) => row.index === SELL_ACCOUNT_INDEX);
    const buyAccount = accountSummary.find((row) => row.index === BUY_ACCOUNT_INDEX);
    if (!sellAccount || !buyAccount) throw new Error('Unable to load both market accounts');
    const sellerAssetAvailable = availableBalance(sellAccount.balances[targetAsset], 'seller target asset');
    const sellerSatsAvailable = availableBalance(sellAccount.balances['::'], 'seller sats');
    const buyerSatsAvailable = availableBalance(buyAccount.balances['::'], 'buyer sats');
    if (compareDecimals(sellerAssetAvailable, sellLeg.inputAmount) < 0) throw new Error('Seller target asset balance is insufficient');
    if (compareDecimals(sellerSatsAvailable, '0') <= 0) throw new Error('Seller has no sats available for fees');
    if (compareDecimals(buyerSatsAvailable, buyLeg.inputAmount) <= 0) throw new Error('Buyer sats balance cannot cover notional plus fees');

    const sellFee = await stpApi('/info/contract/invokefee', {
      method: 'POST', body: JSON.stringify({ url: targetUrl, parameter: sellLeg.invoke }),
    });
    const buyFee = await stpApi('/info/contract/invokefee', {
      method: 'POST', body: JSON.stringify({ url: targetUrl, parameter: buyLeg.invoke }),
    });
    const baseline = targetStatus;
    let roundtrip = { skipped: 'broadcast disabled by SAT20_DRY_RUN=1/SAT20_DISABLE_BROADCAST=1' };
    if (ALLOW_L2_INVOKE) {
      const sellResult = await invokeSwapLeg(client, targetUrl, SELL_ACCOUNT_INDEX, sellLeg);
      if (!sellResult?.txId) throw new Error(`Sell invoke returned no txid: ${JSON.stringify(sellResult)}`);
      const sellCheck = await waitForInvoke(client, targetUrl, sellResult.txId, baseline);
      const buyResult = await invokeSwapLeg(client, targetUrl, BUY_ACCOUNT_INDEX, buyLeg);
      if (!buyResult?.txId || buyResult.txId === sellResult.txId) {
        throw new Error(`Buy invoke returned invalid txid: ${JSON.stringify(buyResult)}`);
      }
      const buyCheck = await waitForInvoke(client, targetUrl, buyResult.txId, sellCheck.statusAfter);
      if (Number(buyCheck.statusAfter?.TotalDealTx || 0) <= Number(baseline?.TotalDealTx || 0)) {
        throw new Error('Buy was accepted but TotalDealTx did not increase');
      }
      const balancesAfter = await walletCall(client, `
        const rows = [];
        for (const accountIndex of [${SELL_ACCOUNT_INDEX}, ${BUY_ACCOUNT_INDEX}]) {
          await withTimeout(wallet.switchToAccount(accountIndex), 'switchToAccount(' + accountIndex + ')', 30000);
          await wallet.setChain(Chain.SATNET);
          rows.push({ index: accountIndex, address: wallet.address,
            targetAsset: unwrap(await sat20.getAssetAmount_SatsNet(wallet.address, ${q(targetAsset)})),
            sats: unwrap(await sat20.getAssetAmount_SatsNet(wallet.address, '::')) });
        }
        return JSON.stringify(rows);
      `);
      roundtrip = { sellLeg, buyLeg, sellFee, buyFee, sellResult, sellCheck, buyResult, buyCheck, balancesAfter };
    }
    console.log(JSON.stringify({
      broadcastMode: BROADCAST_DISABLED ? 'disabled' : 'default-broadcast',
      mode: 'market-roundtrip', currentHeight, targetUrl, targetAsset,
      contractStatus: baseline, accountsBefore: accountSummary, indexerChecks, walletContractChecks, roundtrip,
    }, null, 2));
    client.ws.close();
    return;
  }

  let invokeRes = { skipped: 'broadcast disabled by SAT20_DRY_RUN=1/SAT20_DISABLE_BROADCAST=1' };
  let postInvokeChecks = null;
  if (ALLOW_L2_INVOKE) {
    invokeRes = await walletCall(client, `
      await withTimeout(wallet.switchToAccount(0), 'switchToAccount(0)', 30000);
      await wallet.setChain(Chain.SATNET);
      ${walletExpression(targetUrl)}
    `);
    if (invokeRes?.txId) {
      const evidence = await pollInvokeEvidence({
        attempts: 12,
        sleep,
        delayForAttempt: (attempt) => attempt === 0 ? 3000 : 10000,
        getRawTx: () => getIndexedRawTx(invokeRes.txId),
        getHistory: async () => parseContractHistory(await stpApi(`/info/contract/history/${encodeURIComponent(targetUrl)}?start=0&limit=20`)),
        getStatus: () => getContractStatus(targetUrl),
        findHistoryItem: (history) => history?.data?.find((item) => String(item?.InUtxo || '').startsWith(`${invokeRes.txId}:`)),
        isComplete: ({ rawTx, historyItem, statusAfter }) => Boolean(rawTx && historyItem && statusAfter),
      });
      if (!evidence.completed) {
        throw new Error(formatInvokePollFailure(invokeRes.txId, evidence));
      }
      postInvokeChecks = {
        rawTx: evidence.rawTx?.code === 0
          ? { code: evidence.rawTx.code, msg: evidence.rawTx.msg, rawLength: String(evidence.rawTx.data || '').length }
          : evidence.rawTx,
        matchedHistory: evidence.historyItem ?? null,
        latestHistory: evidence.history?.data?.[0] ?? null,
        statusAfter: evidence.statusAfter && {
          currentBlock: evidence.statusAfter.currentBlock,
          invokeCount: evidence.statusAfter.invokeCount,
          CheckPoint: evidence.statusAfter.CheckPoint,
          TotalDealTx: evidence.statusAfter.TotalDealTx,
          TotalInputSats: evidence.statusAfter.TotalInputSats,
          TotalOutputAssets: evidence.statusAfter.TotalOutputAssets,
        },
        pollAttempts: evidence.attempts,
        lastPollErrors: evidence.lastErrors,
      };
    }
  }

  console.log(JSON.stringify({
    broadcastMode: BROADCAST_DISABLED ? 'disabled' : 'default-broadcast',
    invokeKind: INVOKE_KIND,
    currentHeight,
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
