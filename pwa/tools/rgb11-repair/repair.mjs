const kinds = ['pending-', 'transfer-', 'proof-', 'validation-', 'object-'];
export const hashText = async (text) => [...new Uint8Array(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(text)))].map(x=>x.toString(16).padStart(2,'0')).join('');
export function capture(storage, identity, prefix) {
  if (!identity.startsWith('prd|testnet|') || !/^rgb11-wallet-\d+-account-\d+-rgb11v2-$/.test(prefix)) throw Error('scope rejected');
  const records=[];
  for(let i=0;i<storage.length;i++) {const full=storage.key(i);if(!full.startsWith(prefix))continue;const key=full.slice(prefix.length);if(kinds.some(k=>key.startsWith(k)))records.push({key,value:storage.getItem(full)});}
  records.sort((a,b)=>a.key<b.key?-1:a.key>b.key?1:0);
  if (!records.length) throw Error('selected RGB namespace is empty');
  return {identity,prefix,records};
}
export async function applyPatch(storage, patch, identity, approvedExportHash) {
  const t=patch.target;
  if(t.identity!==identity||t.fingerprint!==approvedExportHash||patch.key!==t.prefix+'pending-'+t.transfer_id)throw Error('patch target mismatch');
  if(patch.before===patch.after)throw Error('empty patch');
  const current=capture(storage,identity,t.prefix);
  if(await hashText(JSON.stringify(current))!==approvedExportHash)throw Error('scope changed; export again');
  // Recheck synchronously after the await. The maintenance page must be the
  // only origin writer; no wallet runtime/service worker is loaded here.
  if(JSON.stringify(capture(storage,identity,t.prefix))!==JSON.stringify(current))throw Error('concurrent writer');
  if(storage.getItem(patch.key)!==patch.before)throw Error('old value mismatch');
  storage.setItem(patch.key,patch.after); // one atomic Web Storage operation
  if(storage.getItem(patch.key)!==patch.after)throw Error('storage readback mismatch');
  const after=capture(storage,identity,t.prefix);
  for(const r of current.records){if(t.prefix+r.key!==patch.key&&after.records.find(x=>x.key===r.key)?.value!==r.value)throw Error('unrelated record changed');}
  return 'Applied one key. No refresh, signing, broadcast or lock update was requested.';
}
