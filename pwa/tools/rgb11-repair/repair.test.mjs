import assert from 'node:assert/strict';
import {webcrypto} from 'node:crypto';
import {readFile} from 'node:fs/promises';
import {capture,hashText,applyPatch,validateOrphanSnapshotRepair,applyOrphanSnapshotRepair,resumeOrphanSnapshotRepair,orphanRepairJournalKey} from './repair.mjs';
if (!globalThis.crypto?.subtle) {
  Object.defineProperty(globalThis, 'crypto', { value: webcrypto, configurable: true });
}
const identity='prd|testnet|root|101|0|pubkey',prefix='rgb11-wallet-101-account-0-rgb11v2-';
class Storage { constructor(){this.map=new Map([[prefix+'pending-a','old'],[prefix+'proof-p','proof'],['secret-unrelated','do-not-read']]);this.writes=0;this.fail=false;}get length(){return this.map.size}key(i){return [...this.map.keys()][i]}getItem(k){assert.notEqual(k,'secret-unrelated');return this.map.get(k)??null}setItem(k,v){if(this.fail)throw Error('quota');this.writes++;this.map.set(k,v)}removeItem(k){this.map.delete(k)}}
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

const b64 = value => Buffer.from(value).toString('base64');
const sha256Base64 = async value => [...new Uint8Array(await crypto.subtle.digest('SHA-256', Buffer.from(value, 'base64')))].map(x=>x.toString(16).padStart(2,'0')).join('');
const enginePrefix='rgb11-engine-'+prefix.slice('rgb11-'.length);
const source={
  version:1,wallet_id:'rgb11-contract-wallet',account_index:0,engine_build_id:'engine-v1',
  projection_records:[
    {key:'pending-t',value:b64('sender-rejected')},
    {key:'prepared-receive-t',value:b64('request-r')},
    {key:'transfer-t',value:b64('awaiting-broadcast')},
    {key:'validation-h',value:b64('receipt-t-h')},
  ],
  engine_records:[{key:'wallet/receive/r',value:b64('acknowledged-t-h')}],
};
const candidate={...source,
  projection_records:[source.projection_records[0]],
  engine_records:[{key:'wallet/receive/r',value:b64('prepared-empty')}],
};
const beforeHash=await hashText(JSON.stringify(source)),afterHash=await hashText(JSON.stringify(candidate));
const changes=[
  {store:'engine',key:'wallet/receive/r',before_sha256:await sha256Base64(source.engine_records[0].value),after_sha256:await sha256Base64(candidate.engine_records[0].value)},
  {store:'projection',key:'transfer-t',before_sha256:await sha256Base64(source.projection_records[2].value),delete:true},
  {store:'projection',key:'prepared-receive-t',before_sha256:await sha256Base64(source.projection_records[1].value),delete:true},
  {store:'projection',key:'validation-h',before_sha256:await sha256Base64(source.projection_records[3].value),delete:true},
];
const plan={target:{wallet_id:source.wallet_id,account_index:0,snapshot_hash:beforeHash,request_id:'r',transfer_id:'t',witness_txid:'witness',consignment_hash:'h'},before_hash:beforeHash,after_hash:afterHash,
  changed_keys:['engine/wallet/receive/r','projection/transfer-t','projection/prepared-receive-t','projection/validation-h'],changes};
const binding={identity,projectionPrefix:prefix,exclusive:true};
class RepairStorage {
  constructor(failAt=0){
    this.map=new Map([['unrelated','untouched']]); this.mutations=0; this.failAt=failAt;
    for(const record of source.projection_records)this.map.set(prefix+record.key,record.value);
    for(const record of source.engine_records)this.map.set(enginePrefix+record.key,record.value);
  }
  get length(){return this.map.size} key(i){return [...this.map.keys()][i]} getItem(k){return this.map.get(k)??null}
  mutate(){this.mutations++;if(this.failAt===this.mutations)throw Error('simulated crash')}
  setItem(k,v){this.mutate();this.map.set(k,v)} removeItem(k){this.mutate();this.map.delete(k)}
}
const repaired=new RepairStorage();
const dry=await validateOrphanSnapshotRepair(repaired,source,candidate,plan,binding);
assert.equal(dry.operationCount,4);assert.equal(dry.beforeHash,beforeHash);assert.equal(dry.afterHash,afterHash);assert.equal(repaired.mutations,0);
await applyOrphanSnapshotRepair(repaired,source,candidate,plan,binding);
assert.equal(repaired.getItem(enginePrefix+'wallet/receive/r'),candidate.engine_records[0].value);
assert.equal(repaired.getItem(prefix+'transfer-t'),null);assert.equal(repaired.getItem(prefix+'prepared-receive-t'),null);assert.equal(repaired.getItem(prefix+'validation-h'),null);
assert.equal(repaired.getItem(prefix+'pending-t'),source.projection_records[0].value);assert.equal(repaired.getItem('unrelated'),'untouched');assert.equal(repaired.getItem(orphanRepairJournalKey),null);

for(const failAt of [2,3,5,7]){
  const interrupted=new RepairStorage(failAt);
  await assert.rejects(applyOrphanSnapshotRepair(interrupted,source,candidate,plan,binding),/simulated crash/);
  interrupted.failAt=0;
  if(interrupted.getItem(orphanRepairJournalKey)===null)await applyOrphanSnapshotRepair(interrupted,source,candidate,plan,binding);
  else await resumeOrphanSnapshotRepair(interrupted,binding);
  assert.equal(interrupted.getItem(enginePrefix+'wallet/receive/r'),candidate.engine_records[0].value);
  assert.equal(interrupted.getItem(prefix+'transfer-t'),null);assert.equal(interrupted.getItem(prefix+'pending-t'),source.projection_records[0].value);
  assert.equal(interrupted.getItem(orphanRepairJournalKey),null);assert.equal(interrupted.getItem('unrelated'),'untouched');
}
const changed=new RepairStorage();changed.map.set(prefix+'pending-t',b64('concurrent-change'));
await assert.rejects(applyOrphanSnapshotRepair(changed,source,candidate,plan,binding),/scope changed/);
const corrupt=new RepairStorage(2);
await assert.rejects(applyOrphanSnapshotRepair(corrupt,source,candidate,plan,binding),/simulated crash/);
corrupt.failAt=0;
const journal=JSON.parse(corrupt.getItem(orphanRepairJournalKey));journal.operations[0].key=prefix+'pending-t';corrupt.map.set(orphanRepairJournalKey,JSON.stringify(journal));
await assert.rejects(resumeOrphanSnapshotRepair(corrupt,binding),/operation set corrupted/);
const maintenancePage=await readFile(new URL('./index.html',import.meta.url),'utf8');
for(const required of ['orphanSource','orphanSourceHash','orphanCandidate','orphanCandidateHash','orphanPlan','orphanPlanHash','orphanDry','orphanApply','orphanResume'])assert.match(maintenancePage,new RegExp(`id="${required}"`));
assert.match(maintenancePage,/three\/four-record/);
for(const handler of ['validateOrphanSnapshotRepair','applyOrphanSnapshotRepair','resumeOrphanSnapshotRepair'])assert.match(maintenancePage,new RegExp(handler));
console.log('orphan snapshot journal apply and crash-resume checks passed');
