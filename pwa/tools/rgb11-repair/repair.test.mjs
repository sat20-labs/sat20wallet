import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
import {capture,hashText,applyPatch} from './repair.mjs';
if (!globalThis.crypto?.subtle) {
  Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });
}
const identity='prd|testnet|root|101|0|pubkey',prefix='rgb11-wallet-101-account-0-rgb11v2-';
class Storage { constructor(){this.map=new Map([[prefix+'pending-a','old'],[prefix+'proof-p','proof'],['secret-unrelated','do-not-read']]);this.writes=0;this.fail=false;}get length(){return this.map.size}key(i){return [...this.map.keys()][i]}getItem(k){assert.notEqual(k,'secret-unrelated');return this.map.get(k)??null}setItem(k,v){if(this.fail)throw Error('quota');this.writes++;this.map.set(k,v)}}
const store=new Storage();
const value=capture(store,identity,prefix),fingerprint=await hashText(JSON.stringify(value));
const patch={target:{identity,prefix,fingerprint,transfer_id:'a'},key:prefix+'pending-a',before:'old',after:'new'};
assert.equal(store.writes,0);
await assert.rejects(applyPatch(store,{...patch,key:prefix+'proof-p'},identity,fingerprint));
await assert.rejects(applyPatch(store,patch,'wrong',fingerprint));
await assert.rejects(applyPatch(store,patch,identity,'wrong'));
store.fail=true;await assert.rejects(applyPatch(store,patch,identity,fingerprint));assert.equal(store.getItem(patch.key),'old');assert.equal(store.writes,0);
store.fail=false;await applyPatch(store,patch,identity,fingerprint);assert.equal(store.writes,1);assert.equal(store.getItem(prefix+'proof-p'),'proof');
await assert.rejects(applyPatch(store,patch,identity,fingerprint));assert.equal(store.writes,1);
console.log('single-key maintenance storage checks passed');
