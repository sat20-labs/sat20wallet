import * as bitcoin from 'bitcoinjs-lib';
import * as ecc from '@bitcoin-js/tiny-secp256k1-asmjs';

const CDP = process.env.SAT20_CDP_URL || 'http://127.0.0.1:9223';
const PWA_URL = process.env.SAT20_PWA_URL || 'http://localhost:5173/';
const L1_API = process.env.SAT20_L1_API || 'https://apiprd.ordx.market';
const SATSNET_INDEXER_API = process.env.SAT20_SATSNET_INDEXER_API || 'https://apiprd.ordx.market/satsnet/testnet';
const MNEMONIC = process.env.SAT20_TEST_MNEMONIC || 'inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire';
const PASSWORD = process.env.SAT20_TEST_PASSWORD || '123456';

bitcoin.initEccLib(ecc);

async function api(baseUrl, path, options = {}) {
  const res = await fetch(baseUrl + path, {
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

async function waitForExpression(client, expression, label, timeoutMs = 20000, intervalMs = 250) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (await evaluate(client, expression, 30000)) return true;
    await sleep(intervalMs);
  }
  throw new Error(`timeout waiting for ${label}`);
}

async function waitForWasm(client) {
  const ready = await evaluate(client, `new Promise(async (resolve) => {
    for (let i = 0; i < 120; i++) {
      if (globalThis.sat20wallet_wasm && window.__SAT20_PWA_VERIFY__) return resolve(true);
      await new Promise(r => setTimeout(r, 1000));
    }
    resolve(false);
  })`);
  if (!ready) throw new Error('PWA WASM did not load');
}

async function walletCall(client, body) {
  const raw = await evaluate(client, `(async () => {
    const verify = window.__SAT20_PWA_VERIFY__;
    if (!verify) throw new Error('SAT20 PWA verify helpers are not available');
    const wallet = verify.useWalletStore();
    const { Chain, Network, walletStorage } = verify;
    const hashed = await verify.hashPassword(${q(PASSWORD)});
    if (!wallet.hasWallet) {
      const [importErr] = await wallet.importWallet(${q(MNEMONIC)}, hashed);
      if (importErr) throw importErr;
    } else if (wallet.locked) {
      const [unlockErr] = await wallet.unlockWallet(hashed);
      if (unlockErr) throw unlockErr;
    }
    await wallet.setPassword(hashed);
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.BTC);
    await walletStorage.setValue('env', 'prd');
    const sat20 = verify.sat20;
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
  if (!page.url.startsWith(PWA_URL)) {
    await client.send('Page.navigate', { url: PWA_URL });
    await sleep(3000);
  }
  await waitForWasm(client);
  await evaluate(client, `(async () => {
    const { walletStorage } = window.__SAT20_PWA_VERIFY__;
    await walletStorage.initializeState();
    await walletStorage.setValue('env', 'prd');
    await walletStorage.setValue('network', 'testnet');
    await walletStorage.setValue('chain', 'btc');
    return true;
  })()`);
  await client.send('Page.reload', { ignoreCache: true });
  await sleep(4000);
  await waitForWasm(client);
  await evaluate(client, `(() => {
    window.__SAT20_PWA_VERIFY__.useGlobalStore().autoLockTime = '30';
    return true;
  })()`);
}

function summarizeList(response, limit = 5) {
  if (!response) return response;
  const data = Array.isArray(response.data) ? response.data.slice(0, limit) : response.data;
  return {
    code: response.code,
    msg: response.msg,
    total: response.total ?? (Array.isArray(response.data) ? response.data.length : undefined),
    data,
  };
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

function buildSelfSpendPsbt(sourceUtxo, account) {
  const inputValue = Number(sourceUtxo.value);
  const fee = 500;
  if (inputValue <= fee + 330) {
    throw new Error(`selected UTXO is too small for self-spend signing test: ${inputValue}`);
  }

  const psbt = new bitcoin.Psbt({ network: bitcoin.networks.testnet });
  psbt.addInput({
    hash: sourceUtxo.txid,
    index: Number(sourceUtxo.vout),
    witnessUtxo: {
      script: outputScript(account.address),
      value: inputValue,
    },
    tapInternalKey: xOnlyPublicKey(account.pubKey),
  });
  psbt.addOutput({
    address: account.address,
    value: inputValue - fee,
  });
  return psbt.toHex();
}

async function findPlainL1Utxo(accounts) {
  for (const account of accounts) {
    const plainUtxos = await api(L1_API, `/btc/testnet/utxo/address/${account.address}/0`);
    const sourceUtxo = plainUtxos?.data?.find((utxo) => Number(utxo.value) > 1000);
    if (sourceUtxo) {
      return {
        account,
        sourceUtxo,
        plainUtxos: summarizeList(plainUtxos, 5),
      };
    }
  }
  return null;
}

async function runReceiveUiChecks(client) {
  const chainSetup = await walletCall(client, `
    await wallet.switchToAccount(0);
    await wallet.setNetwork(Network.TESTNET);
    await wallet.setChain(Chain.BTC);
    const btcAddress = wallet.address;
    await wallet.setChain(Chain.SATNET);
    const satnetAddress = wallet.address;
    await wallet.setChain(Chain.BTC);
    return JSON.stringify({ btcAddress, satnetAddress });
  `);

  await evaluate(client, `window.location.hash = '#/wallet'`);
  await waitForExpression(client, `document.body.innerText.includes('Receive') || document.body.innerText.includes('接收')`, 'wallet home');

  const checkReceiveForChain = async ({ tabLabel, chainName, address }) => {
    const raw = await evaluate(client, `(async () => {
      const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
      const includesAny = (text, labels) => labels.some((label) => text.includes(label));
      const waitFor = async (predicate, label, timeoutMs = 10000) => {
        const started = Date.now();
        while (Date.now() - started < timeoutMs) {
          if (predicate()) return true;
          await sleep(100);
        }
        throw new Error('timeout waiting for ' + label);
      };

      window.__sat20ClipboardWrites = [];
      delete window.__sat20ClipboardPatchError;
      try {
        const clipboard = navigator.clipboard || {};
        clipboard.writeText = async (text) => {
          window.__sat20ClipboardWrites.push(String(text));
        };
        Object.defineProperty(navigator, 'clipboard', {
          configurable: true,
          value: clipboard,
        });
      } catch (error) {
        window.__sat20ClipboardPatchError = error?.message || String(error);
      }

      const tabLabel = ${q(tabLabel)};
      const tabButton = Array.from(document.querySelectorAll('button, [role="tab"]'))
        .find((element) => element.textContent?.includes(tabLabel));
      if (!tabButton) {
        throw new Error('chain tab not found: ' + tabLabel);
      }
      tabButton.click();
      await sleep(300);

      const buttons = Array.from(document.querySelectorAll('button'));
      const receiveButton = buttons.find((button) => includesAny(button.textContent || '', ['Receive', '接收']));
      if (!receiveButton) {
        throw new Error('Receive button not found. Body: ' + document.body.innerText.slice(0, 500));
      }
      receiveButton.click();

      await waitFor(
        () => Array.from(document.querySelectorAll('button')).some((button) => includesAny(button.textContent || '', ['Copy Address', '复制地址'])),
        'receive dialog'
      );

      const dialog = Array.from(document.querySelectorAll('.fixed.inset-0'))
        .find((element) => includesAny(element.textContent || '', ['Copy Address', '复制地址']));
      if (!dialog) throw new Error('Receive dialog overlay not found');

      const canvas = dialog.querySelector('canvas');
      const copyButton = Array.from(dialog.querySelectorAll('button'))
        .find((button) => includesAny(button.textContent || '', ['Copy Address', '复制地址']));
      if (!copyButton) throw new Error('Copy Address button not found');

      const expectedAddress = ${q(address)};
      const maskedAddress = expectedAddress.replace(
        new RegExp('^(.{8}).+(.{8})$'),
        '$1*****$2'
      );
      const hasMaskedAddress = dialog.textContent.includes(maskedAddress);
      const hasActiveCopyToast = () => Array.from(document.querySelectorAll('[data-sonner-toast]'))
        .some((element) => {
          const text = element.textContent || '';
          const isCopyToast = includesAny(text, ['Copied to clipboard', 'Address copied successfully!']);
          return isCopyToast && element.getAttribute('data-removed') !== 'true';
        });

      copyButton.click();
      await waitFor(() => window.__sat20ClipboardWrites.length > 0, 'clipboard write');
      await waitFor(() => hasActiveCopyToast(), 'copy toast');
      const toastVisibleAfterCopy = hasActiveCopyToast();

      return JSON.stringify({
        chain: ${q(chainName)},
        expectedAddress,
        copiedAddress: window.__sat20ClipboardWrites[0] || '',
        clipboardPatchError: window.__sat20ClipboardPatchError || null,
        hasCanvas: Boolean(canvas),
        canvasWidth: canvas?.width || 0,
        canvasHeight: canvas?.height || 0,
        hasMaskedAddress,
        toastVisibleAfterCopy,
        toastDismissed: null,
      });
    })()`, 60000);

    const result = JSON.parse(raw);
    await sleep(8000);
    result.toastDismissed = await evaluate(client, `(() => {
      const includesAny = (text, labels) => labels.some((label) => text.includes(label));
      return !Array.from(document.querySelectorAll('[data-sonner-toast]'))
        .some((element) => {
          const text = element.textContent || '';
          const isCopyToast = includesAny(text, ['Copied to clipboard', 'Address copied successfully!']);
          return isCopyToast && element.getAttribute('data-removed') !== 'true';
        });
    })()`);
    await evaluate(client, `(() => {
      const dialog = Array.from(document.querySelectorAll('.fixed.inset-0'))
        .find((element) => (element.textContent || '').includes('Copy Address') || (element.textContent || '').includes('复制地址'));
      dialog?.querySelector('button')?.click();
      return true;
    })()`);

    if (result.copiedAddress !== address) {
      throw new Error(`${chainName} receive copy mismatch: ${result.copiedAddress} !== ${address}`);
    }
    if (!result.hasCanvas || result.canvasWidth <= 0 || result.canvasHeight <= 0) {
      throw new Error(`${chainName} receive QR canvas was not rendered`);
    }
    if (!result.hasMaskedAddress) {
      throw new Error(`${chainName} receive masked address was not rendered`);
    }
    if (!result.toastVisibleAfterCopy || !result.toastDismissed) {
      throw new Error(`${chainName} receive copy toast did not appear and dismiss correctly`);
    }
    return result;
  };

  return {
    bitcoin: await checkReceiveForChain({
      tabLabel: 'Bitcoin',
      chainName: 'bitcoin',
      address: chainSetup.btcAddress,
    }),
    satoshinet: await checkReceiveForChain({
      tabLabel: 'SatoshiNet',
      chainName: 'satoshinet',
      address: chainSetup.satnetAddress,
    }),
  };
}

async function setupBridgeHarness(client) {
  await evaluate(client, `(async () => {
    const { addAuthorizedOrigin, usePwaDappBridge } = window.__SAT20_PWA_VERIFY__;
    await addAuthorizedOrigin(window.location.origin);

    if (window.__sat20VerifyBridge?.stop) {
      window.__sat20VerifyBridge.stop();
    }
    document.getElementById('sat20-verify-bridge-frame')?.remove();

    const iframe = document.createElement('iframe');
    iframe.id = 'sat20-verify-bridge-frame';
    iframe.style.display = 'none';
    iframe.srcdoc = '<!doctype html><html><body></body></html>';
    document.body.appendChild(iframe);
    await new Promise((resolve) => {
      iframe.onload = resolve;
      setTimeout(resolve, 1000);
    });
    iframe.contentWindow.__sat20VerifyResponses = [];
    iframe.contentWindow.addEventListener('message', (event) => {
      if (event.data?.type === 'SAT20_DAPP_RESPONSE') {
        iframe.contentWindow.__sat20VerifyResponses.push(event.data);
      }
    });

    const bridge = usePwaDappBridge(() => iframe.contentWindow, () => window.location.href);
    bridge.start();
    window.__sat20VerifyBridge = {
      stop() {
        bridge.stop();
        iframe.remove();
        delete window.__sat20VerifyBridge;
      },
    };
    return true;
  })()`);
}

function makeBridgeRequest(origin, seq, action, params = [], extra = {}) {
  return {
    type: 'SAT20_DAPP_REQUEST',
    protocol: 'sat20-dapp-connect',
    requestId: extra.requestId || `verify-${seq}`,
    origin: extra.origin === undefined ? origin : extra.origin,
    action,
    params,
    network: 'testnet',
    nonce: extra.nonce || `nonce-${seq}`,
    expiresAt: extra.expiresAt || (Date.now() + 60000),
  };
}

async function postBridgeRequest(client, request) {
  return evaluate(client, `(() => {
    const iframe = document.getElementById('sat20-verify-bridge-frame');
    if (!iframe?.contentWindow?.__sat20VerifyResponses) {
      throw new Error('bridge harness iframe is not ready');
    }
    const startIndex = iframe.contentWindow.__sat20VerifyResponses.length;
    iframe.contentWindow.eval(${q(`parent.postMessage(${JSON.stringify(request)}, "*")`)});
    return startIndex;
  })()`);
}

async function waitForBridgeResponse(client, request, startIndex) {
  for (let i = 0; i < 150; i++) {
    const raw = await evaluate(client, `(() => {
      const iframe = document.getElementById('sat20-verify-bridge-frame');
      const response = iframe?.contentWindow?.__sat20VerifyResponses
        ?.slice(${Number(startIndex)})
        ?.find((item) => item.requestId === ${q(request.requestId)});
      return response ? JSON.stringify(response) : '';
    })()`);
    if (raw) return JSON.parse(raw);
    await sleep(100);
  }
  throw new Error(`bridge response timeout for ${request.action}`);
}

async function sendBridgeRequest(client, request) {
  const startIndex = await postBridgeRequest(client, request);
  return waitForBridgeResponse(client, request, startIndex);
}

async function getApprovalState(client) {
  const raw = await evaluate(client, `(async () => {
    const approveStore = window.__SAT20_PWA_VERIFY__.useApproveStore();
    return JSON.stringify({
      visible: approveStore.isVisible.value,
      action: approveStore.currentRequest.value?.action || null,
    });
  })()`);
  return JSON.parse(raw);
}

async function rejectCurrentApproval(client) {
  await evaluate(client, `(async () => {
    window.__SAT20_PWA_VERIFY__.useApproveStore().reject(new Error('verify rejection'));
    return true;
  })()`);
}

async function runBridgeChecks(client) {
  await setupBridgeHarness(client);
  const origin = await evaluate(client, 'window.location.origin');
  let seq = 0;
  const makeRequest = (action, params, extra) => makeBridgeRequest(origin, ++seq, action, params, extra);

  const accountsResponse = await sendBridgeRequest(client, makeRequest('getAccounts'));
  const publicKeyResponse = await sendBridgeRequest(client, makeRequest('getPublicKey'));
  const networkResponse = await sendBridgeRequest(client, makeRequest('getNetwork'));
  const walletAddress = accountsResponse?.result?.[0] || '';
  const amountResponse = await sendBridgeRequest(client, makeRequest('getAssetAmount', [walletAddress, '::']));

  const expiredResponse = await sendBridgeRequest(client, makeRequest('getNetwork', [], {
    requestId: 'verify-expired',
    nonce: 'nonce-expired',
    expiresAt: Date.now() - 1000,
  }));

  const duplicateRequest = makeRequest('getNetwork', [], {
    requestId: 'verify-duplicate',
    nonce: 'nonce-duplicate',
  });
  const duplicateFirst = await sendBridgeRequest(client, duplicateRequest);
  const duplicateSecond = await sendBridgeRequest(client, duplicateRequest);

  const originMismatchResponse = await sendBridgeRequest(client, makeRequest('getNetwork', [], {
    requestId: 'verify-origin-mismatch',
    nonce: 'nonce-origin-mismatch',
    origin: 'https://evil.example',
  }));

  const signRejectRequest = makeRequest('signMessage', ['sat20-pwa-verify-reject'], {
    requestId: 'verify-reject',
    nonce: 'nonce-reject',
  });
  const signRejectStartIndex = await postBridgeRequest(client, signRejectRequest);
  let rejectRequest = null;
  for (let i = 0; i < 50; i++) {
    const state = await getApprovalState(client);
    if (state.visible) {
      rejectRequest = state;
      break;
    }
    await sleep(100);
  }
  if (!rejectRequest?.visible) {
    throw new Error('signMessage approval did not become visible');
  }
  await rejectCurrentApproval(client);
  const rejectResponse = await waitForBridgeResponse(client, signRejectRequest, signRejectStartIndex);

  await evaluate(client, `(() => {
    window.__sat20VerifyBridge?.stop?.();
    return true;
  })()`);

  return {
    accountsResponse,
    publicKeyResponse,
    networkResponse,
    amountResponse,
    expiredResponse,
    duplicateFirst,
    duplicateSecond,
    originMismatchResponse,
    rejectAction: rejectRequest.action,
    rejectResponse,
  };
}

async function main() {
  const page = await getPage();
  if (!page?.webSocketDebuggerUrl) throw new Error('No debuggable PWA page');
  const client = await connect(page.webSocketDebuggerUrl);
  await client.send('Runtime.enable');
  console.log('[wallet-basics] preparing PWA');
  await preparePwa(client, page);

  console.log('[wallet-basics] checking wallet lifecycle');
  const lifecycle = await walletCall(client, `
    await wallet.switchToAccount(0);
    const before = {
      hasWallet: wallet.hasWallet,
      locked: wallet.locked,
      walletId: wallet.walletId,
      accountIndex: wallet.accountIndex,
      address: wallet.address,
      pubKey: wallet.publicKey,
      network: wallet.network,
      chain: wallet.chain,
      env: walletStorage.getValue('env'),
      wallets: wallet.wallets?.map((item) => ({
        id: item.id,
        name: item.name,
        accounts: item.accounts?.map((account) => ({
          index: account.index,
          address: account.address,
          pubKey: account.pubKey,
        })),
      })),
    };
    await wallet.setLocked(true);
    const lockedStored = walletStorage.getValue('locked');
    const [unlockErr] = await wallet.unlockWallet(hashed);
    if (unlockErr) throw unlockErr;
    const afterUnlock = {
      locked: wallet.locked,
      address: wallet.address,
      pubKey: wallet.publicKey,
      accountIndex: wallet.accountIndex,
      network: wallet.network,
      chain: wallet.chain,
    };
    return JSON.stringify({ before, lockedStored, afterUnlock });
  `);

  await client.send('Page.reload', { ignoreCache: true });
  await sleep(3000);
  await waitForWasm(client);

  console.log('[wallet-basics] checking accounts and wallet asset helpers');
  const accounts = await walletCall(client, `
    const rows = [];
    for (const accountIndex of [0, 1]) {
      await wallet.switchToAccount(accountIndex);
      const btcAddress = wallet.address;
      const btcPubKey = wallet.publicKey;
      await wallet.setChain(Chain.SATNET);
      const l2Address = wallet.address;
      rows.push({
        index: accountIndex,
        btcAddress,
        l2Address,
        pubKey: btcPubKey,
        l1Btc: await safe(async () => unwrap(await sat20.getAssetAmount(btcAddress, '::'))),
        l1Dog: await safe(async () => unwrap(await sat20.getAssetAmount(btcAddress, 'ordx:f:dogcoin'))),
        l2Sats: await safe(async () => unwrap(await sat20.getAssetAmount_SatsNet(l2Address, '::'))),
        l1UtxosWithBtc: await safe(async () => unwrap(await sat20.getUtxosWithAsset(btcAddress, '1', '::'))),
        l2UtxosWithSats: await safe(async () => unwrap(await sat20.getUtxosWithAsset_SatsNet(l2Address, '1', '::'))),
      });
      await wallet.setChain(Chain.BTC);
    }
    return JSON.stringify(rows);
  `);

  console.log('[wallet-basics] checking direct indexer queries');
  const indexerChecks = [];
  for (const account of accounts) {
    const l1PlainUtxos = await api(L1_API, `/btc/testnet/utxo/address/${account.btcAddress}/0`);
    const l2Summary = await api(SATSNET_INDEXER_API, `/v3/address/summary/${account.l2Address}`);
    const l2PlainUtxos = await api(SATSNET_INDEXER_API, `/utxo/address/${account.l2Address}/0`);
    indexerChecks.push({
      index: account.index,
      l1PlainUtxos: summarizeList(l1PlainUtxos, 5),
      l2Summary: summarizeList(l2Summary, 20),
      l2PlainUtxos: summarizeList(l2PlainUtxos, 5),
    });
  }

  console.log('[wallet-basics] checking message signing');
  const messageSigning = await walletCall(client, `
    await wallet.switchToAccount(0);
    await wallet.setChain(Chain.BTC);
    const btcSign = unwrap(await sat20.signMessage('sat20-pwa-verify-btc'));
    await wallet.setChain(Chain.SATNET);
    const satnetSign = unwrap(await sat20.signMessage('sat20-pwa-verify-satnet'));
    return JSON.stringify({
      btcSignatureLength: String(btcSign?.signature || btcSign || '').length,
      satnetSignatureLength: String(satnetSign?.signature || satnetSign || '').length,
    });
  `);

  console.log('[wallet-basics] checking L1 local PSBT signing without broadcast');
  const plainSource = await findPlainL1Utxo(accounts.map((account) => ({
    index: account.index,
    address: account.btcAddress,
    pubKey: account.pubKey,
  })));

  let l1PsbtSigning = { skipped: 'no plain L1 UTXO available for non-broadcast signing test' };
  if (plainSource) {
    const psbtHex = buildSelfSpendPsbt(plainSource.sourceUtxo, plainSource.account);
    l1PsbtSigning = await walletCall(client, `
      await wallet.switchToAccount(${plainSource.account.index});
      await wallet.setChain(Chain.BTC);
      const signed = unwrap(await sat20.signPsbt(${q(psbtHex)}, false));
      const signedPsbt = signed?.psbt || signed;
      const extracted = unwrap(await sat20.extractTxFromPsbt(signedPsbt));
      return JSON.stringify({
        accountIndex: ${plainSource.account.index},
        sourceOutpoint: ${q(`${plainSource.sourceUtxo.txid}:${plainSource.sourceUtxo.vout}`)},
        signedPsbtLength: signedPsbt.length,
        rawTxLength: extracted.tx.length,
        rawTxHex: extracted.tx,
      });
    `);
    const extractedTx = bitcoin.Transaction.fromHex(l1PsbtSigning.rawTxHex);
    l1PsbtSigning.rawTxid = extractedTx.getId();
    delete l1PsbtSigning.rawTxHex;
    l1PsbtSigning.plainUtxos = plainSource.plainUtxos;
  }

  console.log('[wallet-basics] checking receive QR and copy UI');
  const receiveUiChecks = await runReceiveUiChecks(client);

  console.log('[wallet-basics] checking DApp bridge envelope handling');
  const bridgeChecks = await runBridgeChecks(client);

  console.log(JSON.stringify({
    lifecycle,
    accounts,
    indexerChecks,
    messageSigning,
    l1PsbtSigning,
    receiveUiChecks,
    bridgeChecks,
  }, null, 2));

  client.ws.close();
}

main().catch((error) => {
  console.error(error.stack || error.message || error);
  process.exit(1);
});
