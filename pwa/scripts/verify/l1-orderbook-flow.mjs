import * as bitcoin from 'bitcoinjs-lib';
import * as ecc from '@bitcoin-js/tiny-secp256k1-asmjs';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/';
const API = process.env.SAT20_L1_API || 'https://apiprd.ordx.market';
const MNEMONIC = process.env.SAT20_TEST_MNEMONIC || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';
const HTTP_TIMEOUT_MS = Number(process.env.SAT20_HTTP_TIMEOUT_MS || 30000);
const WALLET_HELPER_TIMEOUT_MS = Number(process.env.SAT20_WALLET_HELPER_TIMEOUT_MS || 30000);
const isEnabled = (value) => ['1', 'true', 'yes'].includes(String(value || '').toLowerCase());
const BROADCAST_DISABLED = isEnabled(process.env.SAT20_DRY_RUN)
  || isEnabled(process.env.SAT20_DISABLE_BROADCAST)
  || process.env.SAT20_ALLOW_ORDERBOOK_WRITE === '0'
  || process.env.SAT20_ALLOW_BUY_BROADCAST === '0';
// Real testnet verification broadcasts by default. Set SAT20_DRY_RUN=1 to use the
// old no-write path; the ALLOW_* variables remain accepted for compatibility.
const ALLOW_ORDERBOOK_WRITE = !BROADCAST_DISABLED;
const ALLOW_BUY_BROADCAST = !BROADCAST_DISABLED;
const EXISTING_DUMMY_TXID = process.env.SAT20_EXISTING_DUMMY_TXID || '';
const EXISTING_DUMMY_CHANGE = Number(process.env.SAT20_EXISTING_DUMMY_CHANGE || 0);

function integerEnv(name, fallback, minimum) {
  const raw = process.env[name];
  const value = raw === undefined || raw === '' ? fallback : Number(raw);
  if (!Number.isSafeInteger(value) || value < minimum) {
    throw new Error(`${name} must be a safe integer >= ${minimum}, got ${raw}`);
  }
  return value;
}

const SELLER_INDEX = integerEnv('SAT20_L1_SELLER_ACCOUNT_INDEX', 0, 0);
const BUYER_INDEX = integerEnv('SAT20_L1_BUYER_ACCOUNT_INDEX', 1, 0);
const ASSET_NAME = 'dogcoin';
const ORDER_ASSET_TYPE = 'ticker';
const ORDER_ASSET_NAME = 'dogcoin';
const SELL_AMOUNT = integerEnv('SAT20_L1_SELL_AMOUNT', 1000, 1);
const UNIT_PRICE = 1;
const SERVICE_FEE = 10;
const NETWORK_FEE = 10;
const DUMMY_UTXO_VALUE = 600;
const DUMMY_COUNT = 2;
const DUMMY_SPLIT_FEE = 500;
const BUY_TX_FEE = 1000;
const ORDER_PRICE = SELL_AMOUNT * UNIT_PRICE;
// The final buy consumes the dummy change to fund the seller output, a second
// set of dummy outputs, and the buy fee. The split source must additionally
// fund the first set of dummy outputs and the split fee.
const MIN_DUMMY_CHANGE = ORDER_PRICE + DUMMY_UTXO_VALUE * DUMMY_COUNT + BUY_TX_FEE;
const MIN_BUYER_SOURCE_UTXO = DUMMY_UTXO_VALUE * DUMMY_COUNT + DUMMY_SPLIT_FEE + MIN_DUMMY_CHANGE;
const SELL_UTXO_OVERRIDE = process.env.SAT20_L1_SELL_UTXO || '';
if (EXISTING_DUMMY_TXID && (!Number.isSafeInteger(EXISTING_DUMMY_CHANGE) || EXISTING_DUMMY_CHANGE < MIN_DUMMY_CHANGE)) {
  throw new Error(`Buyer account ${BUYER_INDEX} requires SAT20_EXISTING_DUMMY_CHANGE >= ${MIN_DUMMY_CHANGE} sats when SAT20_EXISTING_DUMMY_TXID is set, got ${EXISTING_DUMMY_CHANGE}`);
}
const SIGHASH_SINGLE_ANYONECANPAY = bitcoin.Transaction.SIGHASH_SINGLE | bitcoin.Transaction.SIGHASH_ANYONECANPAY;
bitcoin.initEccLib(ecc);

async function api(path, options = {}) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), HTTP_TIMEOUT_MS);
  let res;
  try {
    res = await fetch(API + path, {
      ...options,
      signal: controller.signal,
      headers: {
        'Content-Type': 'application/json',
        ...(options.headers || {}),
      },
    });
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`HTTP timeout after ${HTTP_TIMEOUT_MS}ms: ${API + path}`);
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

async function signedWalletHeaders(client, accountIndex) {
  const signed = await walletCall(client, `
    await withTimeout(wallet.switchToAccount(${accountIndex}), 'switchToAccount(${accountIndex})');
    const [err, res] = await sat20.signMessage('ordx-marketplace');
    if (err) throw err;
    return JSON.stringify({ publicKey: wallet.publicKey, signature: res?.signature || res });
  `);
  return {
    Publickey: signed.publicKey,
    Signature: signed.signature,
  };
}

async function signedSellerHeaders(client) {
  return signedWalletHeaders(client, SELLER_INDEX);
}

async function signedBuyerHeaders(client) {
  return signedWalletHeaders(client, BUYER_INDEX);
}

async function cancelOrder(client, sellerAddress, orderId) {
  return api('/testnet/ordx/CancelOrder', {
    method: 'POST',
    headers: await signedSellerHeaders(client),
    body: JSON.stringify({ address: sellerAddress, order_id: orderId }),
  });
}

async function unlockOrders(buyerAddress, orderIds) {
  return api('/testnet/ordx/UnlockBulkOrder', {
    method: 'POST',
    body: JSON.stringify({ address: buyerAddress, order_id: orderIds }),
  });
}

async function pushRawTx(rawTxHex) {
  return api('/btc/testnet/btc/tx', {
    method: 'POST',
    body: JSON.stringify({ SignedTxHex: rawTxHex }),
  });
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

  const send = (method, params = {}, timeout = 180000) => {
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

async function evaluate(client, expression, timeout = 180000) {
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
  await evaluate(client, `new Promise(async (resolve) => {
    for (let i = 0; i < 90; i++) {
      if (globalThis.sat20wallet_wasm) return resolve(true);
      await new Promise(r => setTimeout(r, 1000));
    }
    resolve(false);
  })`);
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
    await wallet.setChain(Chain.BTC);
    const unwrap = (tuple) => {
      if (tuple?.[0]) throw tuple[0];
      return tuple?.[1];
    };
    const withTimeout = (promise, label, ms = ${WALLET_HELPER_TIMEOUT_MS}) => Promise.race([
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
    await walletStorage.setValue('chain', 'btc');
    return true;
  })()`);
  await client.send('Page.reload', { ignoreCache: true });
  await sleep(4000);
  await waitForWasm(client);
}

function withPrice(utxoInfo, price) {
  return { ...utxoInfo, Price: price };
}

function buildL1SellOrderPsbt(utxoInfo, seller) {
  const [txid, vout] = utxoInfo.Outpoint.split(':');
  const psbt = new bitcoin.Psbt({ network: bitcoin.networks.testnet });
  const pubkey = Buffer.from(seller.pubKey, 'hex');

  psbt.addInput({
    hash: txid,
    index: Number(vout),
    witnessUtxo: {
      script: Buffer.from(utxoInfo.PkScript, 'base64'),
      value: Number(utxoInfo.Value),
    },
    sighashType: SIGHASH_SINGLE_ANYONECANPAY,
    tapInternalKey: pubkey.length === 33 ? pubkey.subarray(1, 33) : pubkey,
  });
  psbt.addOutput({
    address: seller.address,
    value: Number(utxoInfo.Price),
  });

  return psbt.toHex();
}

function xOnlyPublicKey(publicKey) {
  const pubkey = Buffer.from(publicKey, 'hex');
  if (pubkey.length === 33) return pubkey.subarray(1, 33);
  if (pubkey.length === 32) return pubkey;
  throw new Error('invalid taproot public key');
}

function outputScript(address) {
  return bitcoin.address.toOutputScript(address, bitcoin.networks.testnet);
}

function buildDummySplitPsbt(sourceUtxo, buyer, outputCount) {
  const inputValue = Number(sourceUtxo.value);
  const dummyTotal = DUMMY_UTXO_VALUE * outputCount;
  const change = inputValue - dummyTotal - DUMMY_SPLIT_FEE;
  if (change <= DUMMY_UTXO_VALUE) {
    throw new Error(`selected UTXO is too small for dummy split: ${inputValue}`);
  }

  const psbt = new bitcoin.Psbt({ network: bitcoin.networks.testnet });
  psbt.addInput({
    hash: sourceUtxo.txid,
    index: Number(sourceUtxo.vout),
    witnessUtxo: {
      script: outputScript(buyer.address),
      value: inputValue,
    },
    tapInternalKey: xOnlyPublicKey(buyer.pubKey),
  });
  for (let i = 0; i < outputCount; i++) {
    psbt.addOutput({
      address: buyer.address,
      value: DUMMY_UTXO_VALUE,
    });
  }
  psbt.addOutput({
    address: buyer.address,
    value: change,
  });

  return {
    psbtHex: psbt.toHex(),
    change,
  };
}

function buildL1BuyPsbt({ signedOrderRaw, dummyTxid, dummyCount, dummyChange, buyer }) {
  const signedOrder = bitcoin.Psbt.fromHex(signedOrderRaw, {
    network: bitcoin.networks.testnet,
  });
  const sellerInput = {
    hash: signedOrder.txInputs[0].hash,
    index: signedOrder.txInputs[0].index,
    witnessUtxo: signedOrder.data.inputs[0].witnessUtxo,
    finalScriptWitness: signedOrder.data.inputs[0].finalScriptWitness,
  };
  const assetOutputValue = Number(signedOrder.data.inputs[0].witnessUtxo.value);
  const sellerOutput = signedOrder.txOutputs[0];
  const buyerScript = outputScript(buyer.address);
  const tapInternalKey = xOnlyPublicKey(buyer.pubKey);
  const dummyTotal = DUMMY_UTXO_VALUE * dummyCount;
  const outputWithoutChange =
    dummyTotal +
    assetOutputValue +
    Number(sellerOutput.value) +
    dummyTotal;
  const inputTotal = dummyTotal + assetOutputValue + dummyChange;
  const change = inputTotal - outputWithoutChange - BUY_TX_FEE;
  if (change < 0) {
    throw new Error(`insufficient buyer change for buy tx: ${change}`);
  }

  const psbt = new bitcoin.Psbt({ network: bitcoin.networks.testnet });
  for (let i = 0; i < dummyCount; i++) {
    psbt.addInput({
      hash: dummyTxid,
      index: i,
      witnessUtxo: {
        script: buyerScript,
        value: DUMMY_UTXO_VALUE,
      },
      tapInternalKey,
    });
  }
  psbt.addInput(sellerInput);
  psbt.addInput({
    hash: dummyTxid,
    index: dummyCount,
    witnessUtxo: {
      script: buyerScript,
      value: dummyChange,
    },
    tapInternalKey,
  });

  psbt.addOutput({
    address: buyer.address,
    value: dummyTotal,
  });
  psbt.addOutput({
    address: buyer.address,
    value: assetOutputValue,
  });
  psbt.addOutput({
    script: sellerOutput.script,
    value: Number(sellerOutput.value),
  });
  for (let i = 0; i < dummyCount; i++) {
    psbt.addOutput({
      address: buyer.address,
      value: DUMMY_UTXO_VALUE,
    });
  }
  if (change > DUMMY_UTXO_VALUE) {
    psbt.addOutput({
      address: buyer.address,
      value: change,
    });
  }

  return {
    psbtHex: psbt.toHex(),
    change,
  };
}

async function findSubmittedOrder(sellerAddress, sellUtxo) {
  const orders = await api(`/testnet/ordx/GetOrders?offset=0&size=20&sort=4&type=1&assets_name=${ASSET_NAME}&assets_type=ticker`);
  const list = orders?.data?.order_list || [];
  return list.find((o) => o.address === sellerAddress && o.utxo === sellUtxo);
}

async function main() {
  console.log('[l1-orderbook] locating PWA page');
  const page = await getPage();
  if (!page?.webSocketDebuggerUrl) throw new Error('No debuggable PWA page');
  const client = await connect(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  console.log('[l1-orderbook] preparing PWA');
  await preparePwa(client, page);

  console.log('[l1-orderbook] checking seller and buyer balances');
  const accounts = [];
  for (const accountIndex of new Set([SELLER_INDEX, BUYER_INDEX])) {
    console.log(`[l1-orderbook] account ${accountIndex} balance helper`);
    accounts.push(await walletCall(client, `
      await withTimeout(wallet.switchToAccount(${accountIndex}), 'switchToAccount(${accountIndex})');
      return JSON.stringify({
        index: ${accountIndex},
        address: wallet.address,
        pubKey: wallet.publicKey,
        dog: await safe(async () => unwrap(await sat20.getAssetAmount(wallet.address, 'ordx:f:dogcoin'))),
        btc: await safe(async () => unwrap(await sat20.getAssetAmount(wallet.address, '::'))),
      });
    `));
  }

  const seller = accounts.find((account) => account.index === SELLER_INDEX);
  const buyer = accounts.find((account) => account.index === BUYER_INDEX);
  const totalPay = ORDER_PRICE + SERVICE_FEE + NETWORK_FEE;
  const dummyCount = DUMMY_COUNT;

  if (!seller || !buyer) {
    throw new Error(`Expected seller account ${SELLER_INDEX} and buyer account ${BUYER_INDEX}`);
  }
  const sellerAvailable = Number(seller.dog?.availableAmt || 0);
  if (sellerAvailable < SELL_AMOUNT) {
    throw new Error(`Seller account ${SELLER_INDEX} has insufficient ${ASSET_NAME}: need available >= ${SELL_AMOUNT}, got ${sellerAvailable}`);
  }
  const buyerAvailable = Number(buyer.btc?.availableAmt || 0);
  if (!EXISTING_DUMMY_TXID && buyerAvailable < MIN_BUYER_SOURCE_UTXO) {
    throw new Error(`Buyer account ${BUYER_INDEX} has insufficient BTC for dummy split and buy: need available >= ${MIN_BUYER_SOURCE_UTXO} sats and one plain UTXO of at least that value, got available ${buyerAvailable}`);
  }

  const sellerDogUtxos = await walletCall(client, `
    await withTimeout(wallet.switchToAccount(${SELLER_INDEX}), 'switchToAccount(${SELLER_INDEX})');
    const selected = await safe(async () => unwrap(await sat20.getUtxosWithAsset(wallet.address, ${q(String(SELL_AMOUNT))}, 'ordx:f:dogcoin')));
    return JSON.stringify(selected);
  `);
  if (sellerDogUtxos?.error) {
    throw new Error(`Seller asset UTXO helper failed: ${sellerDogUtxos.error}`);
  }
  console.log('[l1-orderbook] selecting seller UTXO and signing order');
  const sellUtxo = SELL_UTXO_OVERRIDE || sellerDogUtxos?.utxos?.[0];
  if (!sellUtxo) {
    throw new Error(`Seller account ${SELLER_INDEX} has no ${ASSET_NAME} UTXO available for required amount ${SELL_AMOUNT}: ${JSON.stringify(sellerDogUtxos)}`);
  }

  const sellInfoRes = await api('/btc/testnet/v3/utxo/info/' + sellUtxo);
  if (sellInfoRes.code !== 0 || !sellInfoRes.data) throw new Error('Failed to get sell UTXO info');
  const sellInfo = withPrice(sellInfoRes.data, SELL_AMOUNT * UNIT_PRICE);
  const sellPsbt = buildL1SellOrderPsbt(sellInfo, seller);

  const signedOrders = await walletCall(client, `
    await withTimeout(wallet.switchToAccount(${SELLER_INDEX}), 'switchToAccount(${SELLER_INDEX})');
    const signed = unwrap(await sat20.signPsbt(${q(sellPsbt)}, false));
    const signedPsbt = signed?.psbt || signed;
    return JSON.stringify([signedPsbt]);
  `);

  const summary = {
    seller: seller.address,
    buyer: buyer.address,
    sellUtxo,
    amount: SELL_AMOUNT,
    totalPay,
    signedOrderCount: Array.isArray(signedOrders) ? signedOrders.length : 0,
  };

  if (!ALLOW_ORDERBOOK_WRITE) {
    console.log(JSON.stringify({
      ...summary,
      dryRun: true,
      next: 'Unset SAT20_DRY_RUN/SAT20_DISABLE_BROADCAST (and legacy ALLOW_* = 0) to use the default broadcast path.',
    }, null, 2));
    client.ws.close();
    return;
  }

  const submitRes = await api('/testnet/ordx/SubmitBatchOrders', {
    method: 'POST',
    body: JSON.stringify({
      address: seller.address,
      order_query: signedOrders.map((raw) => ({
        assets_type: ORDER_ASSET_TYPE,
        assets_name: ORDER_ASSET_NAME,
        raw,
      })),
    }),
  });
  console.log('[l1-orderbook] order submitted; waiting for visibility');

  if (submitRes.code !== 200) {
    throw new Error(`SubmitBatchOrders failed: ${JSON.stringify(submitRes)}`);
  }

  let submittedOrder;
  for (let i = 0; i < 10 && !submittedOrder; i++) {
    await sleep(2000);
    submittedOrder = await findSubmittedOrder(seller.address, sellUtxo);
  }
  if (!submittedOrder) {
    console.log(JSON.stringify({
      ...summary,
      submitRes,
      orderVisible: false,
    }, null, 2));
    client.ws.close();
    return;
  }

  const orderId = Number(submittedOrder.order_id);
  const lockRes = await api('/testnet/ordx/LockBulkOrder', {
    method: 'POST',
    body: JSON.stringify({ address: buyer.address, order_id: [orderId] }),
  });
  console.log('[l1-orderbook] order locked; entering default broadcast buy path');
  if (lockRes.code !== 200) throw new Error(`LockBulkOrder failed: ${JSON.stringify(lockRes)}`);
  const raw = lockRes?.data?.[0]?.raw || lockRes?.data?.raw || lockRes?.raw;
  if (!raw) throw new Error(`LockBulkOrder did not return raw: ${JSON.stringify(lockRes)}`);

  if (!ALLOW_BUY_BROADCAST) {
    const unlockRes = await unlockOrders(buyer.address, [orderId]);
    const cancelRes = await cancelOrder(client, seller.address, orderId);

    console.log(JSON.stringify({
      ...summary,
      orderId,
      lockRawLength: raw.length,
      unlockRes,
      cancelRes,
      dryRunBuy: true,
      next: 'Unset SAT20_DRY_RUN/SAT20_DISABLE_BROADCAST (and legacy ALLOW_* = 0) to sign and broadcast the L1 buy transaction.',
    }, null, 2));
    client.ws.close();
    return;
  }

  let buyRes;
  let txSummary;
  let broadcastAttempted = false;
  try {
    let dummyTxid = EXISTING_DUMMY_TXID;
    let dummyChange = EXISTING_DUMMY_CHANGE;
    let dummyPushRes = { skipped: 'using existing dummy tx' };
    let dummySignedPsbtLength = 0;

    if (!dummyTxid) {
      const plainUtxos = await api(`/btc/testnet/utxo/address/${buyer.address}/0`);
      const sourceUtxo = plainUtxos?.data?.find((utxo) => Number(utxo.value) >= MIN_BUYER_SOURCE_UTXO);
      if (!sourceUtxo) {
        throw new Error(`Buyer account ${BUYER_INDEX} has no plain BTC UTXO >= ${MIN_BUYER_SOURCE_UTXO} sats required for sell amount ${SELL_AMOUNT}: ${JSON.stringify(plainUtxos)}`);
      }

      const dummySplit = buildDummySplitPsbt(sourceUtxo, buyer, dummyCount);
      const dummyTx = await walletCall(client, `
        await withTimeout(wallet.switchToAccount(${BUYER_INDEX}), 'switchToAccount(${BUYER_INDEX})');
        const signed = unwrap(await sat20.signPsbt(${q(dummySplit.psbtHex)}, false));
        const signedPsbt = signed?.psbt || signed;
        const extracted = unwrap(await sat20.extractTxFromPsbt(signedPsbt));
        return JSON.stringify({ signedPsbtLength: signedPsbt.length, rawTxHex: extracted.tx });
      `);
      dummySignedPsbtLength = dummyTx.signedPsbtLength;
      dummyTxid = bitcoin.Transaction.fromHex(dummyTx.rawTxHex).getId();
      dummyChange = dummySplit.change;
      dummyPushRes = await pushRawTx(dummyTx.rawTxHex);
      if (dummyPushRes?.code !== 0) {
        throw new Error(`dummy split broadcast failed: ${JSON.stringify(dummyPushRes)}`);
      }
    }

    const buyPsbt = buildL1BuyPsbt({
      signedOrderRaw: raw,
      dummyTxid,
      dummyCount,
      dummyChange,
      buyer,
    });

    const buyTx = await walletCall(client, `
      await withTimeout(wallet.switchToAccount(${BUYER_INDEX}), 'switchToAccount(${BUYER_INDEX})');
      const signed = unwrap(await sat20.signPsbt(${q(buyPsbt.psbtHex)}, false));
      const signedPsbt = signed?.psbt || signed;
      const extracted = unwrap(await sat20.extractTxFromPsbt(signedPsbt));
      return JSON.stringify({ signedPsbtLength: signedPsbt.length, rawTxHex: extracted.tx });
    `);

    buyRes = await api('/testnet/ordx/BulkBuyOrder', {
      method: 'POST',
      headers: await signedBuyerHeaders(client),
      body: JSON.stringify({
        address: buyer.address,
        order_ids: [orderId],
        raw: buyTx.rawTxHex,
      }),
    });
    if (buyRes?.code !== 200) {
      throw new Error(`BulkBuyOrder failed: ${JSON.stringify(buyRes)}`);
    }
    broadcastAttempted = true;
    txSummary = {
      dummyTxid,
      dummyPushRes,
      dummySignedPsbtLength,
      buySignedPsbtLength: buyTx.signedPsbtLength,
      buyRawTxid: bitcoin.Transaction.fromHex(buyTx.rawTxHex).getId(),
      buyRawTxLength: buyTx.rawTxHex.length,
      buyChange: buyPsbt.change,
    };
  } catch (error) {
    const unlockRes = broadcastAttempted
      ? { skipped: 'buy broadcast was attempted' }
      : await unlockOrders(buyer.address, [orderId]).catch((unlockError) => ({
        error: unlockError.message || String(unlockError),
      }));
    const cancelRes = broadcastAttempted
      ? { skipped: 'buy broadcast was attempted' }
      : await cancelOrder(client, seller.address, orderId).catch((cancelError) => ({
        error: cancelError.message || String(cancelError),
      }));
    console.log(JSON.stringify({
      ...summary,
      orderId,
      lockRawLength: raw.length,
      error: error.message || String(error),
      unlockRes,
      cancelRes,
    }, null, 2));
    throw error;
  }

  console.log(JSON.stringify({
    broadcastMode: BROADCAST_DISABLED ? 'disabled' : 'default-broadcast',
    ...summary,
    orderId,
    lockRawLength: raw.length,
    txSummary,
    buyRes,
  }, null, 2));
  client.ws.close();
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
