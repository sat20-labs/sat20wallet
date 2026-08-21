import assert from 'node:assert/strict';
import test from 'node:test';

import { isExactPwaRoot, selectPwaPage } from './pwa-page.mjs';

const page = (url, id) => ({ id, type: 'page', url, webSocketDebuggerUrl: `ws://cdp/${id}` });

test('prefers the exact PWA root over an earlier unlock route', () => {
  const selected = selectPwaPage([
    page('http://localhost:5173/unlock#/wallet', 'unlock'),
    page('http://localhost:5173/', 'root'),
  ], 'http://localhost:5173/');

  assert.equal(selected.webSocketDebuggerUrl, 'ws://cdp/root');
});

test('returns a routed PWA page when it is the only PWA target', () => {
  const selected = selectPwaPage([
    page('http://localhost:5173/unlock#/wallet', 'unlock'),
    page('about:blank', 'blank'),
  ], 'http://localhost:5173/');

  assert.equal(selected.webSocketDebuggerUrl, 'ws://cdp/unlock');
  assert.equal(isExactPwaRoot(selected.url, 'http://localhost:5173/'), false);
});

test('prefers a healthy routed PWA over an exact-root error target', () => {
  const selected = selectPwaPage([
    page('http://localhost:5173/', 'error-root'),
    page('http://localhost:5173/unlock#/wallet', 'ready-unlock'),
  ], 'http://localhost:5173/', new Set(['ready-unlock']));

  assert.equal(selected.webSocketDebuggerUrl, 'ws://cdp/ready-unlock');
});

test('respects a non-root PWA base path', () => {
  const selected = selectPwaPage([
    page('http://localhost:4173/unlock', 'outside'),
    page('http://localhost:4173/pwa/#/wallet', 'route'),
    page('http://localhost:4173/pwa/', 'root'),
  ], 'http://localhost:4173/pwa/');

  assert.equal(selected.webSocketDebuggerUrl, 'ws://cdp/root');
});
