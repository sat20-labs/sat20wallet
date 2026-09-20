const kinds = ['pending-', 'transfer-', 'proof-', 'validation-', 'object-'];
export const orphanRepairJournalKey = 'sat20:maintenance:rgb11-orphan-repair:v1';
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

function normalizedSnapshot(snapshot) {
  if (!snapshot || snapshot.version !== 1 || typeof snapshot.wallet_id !== 'string' || !snapshot.wallet_id ||
      !Number.isInteger(snapshot.account_index) || snapshot.account_index < 0 ||
      typeof snapshot.engine_build_id !== 'string') throw Error('invalid wallet snapshot metadata');
  const records = (value, label) => {
    if (!Array.isArray(value)) throw Error(`invalid ${label} records`);
    let previous = '';
    const seen = new Set();
    return value.map(record => {
      if (!record || typeof record.key !== 'string' || !record.key || typeof record.value !== 'string' ||
          seen.has(record.key) || (previous && record.key <= previous)) throw Error(`invalid ${label} record order`);
      // SDK byte slices are JSON/base64 strings and the browser KV stores the
      // same bytes as base64. Reject non-canonical inputs before comparing them.
      let decoded;
      try { decoded = atob(record.value); } catch { throw Error(`invalid ${label} record value`); }
      let canonical = '';
      for (let i = 0; i < decoded.length; i += 3) {
        canonical += String.fromCharCode(...[...decoded.slice(i, i + 3)].map(ch => ch.charCodeAt(0)));
      }
      if (btoa(canonical) !== record.value) throw Error(`non-canonical ${label} record value`);
      seen.add(record.key); previous = record.key;
      return {key: record.key, value: record.value};
    });
  };
  return {
    version: snapshot.version,
    wallet_id: snapshot.wallet_id,
    account_index: snapshot.account_index,
    engine_build_id: snapshot.engine_build_id,
    projection_records: records(snapshot.projection_records, 'projection'),
    engine_records: records(snapshot.engine_records, 'engine'),
  };
}

async function snapshotHash(snapshot) {
  return hashText(JSON.stringify(normalizedSnapshot(snapshot)));
}

async function base64Hash(value) {
  let raw;
  try { raw = Uint8Array.from(atob(value), ch => ch.charCodeAt(0)); } catch { throw Error('invalid record base64'); }
  return [...new Uint8Array(await crypto.subtle.digest('SHA-256', raw))].map(x=>x.toString(16).padStart(2,'0')).join('');
}

function orphanPrefixes(projectionPrefix) {
  if (!/^rgb11-wallet-\d+-account-\d+-rgb11v2-$/.test(projectionPrefix)) throw Error('scope rejected');
  return {projection: projectionPrefix, engine: 'rgb11-engine-' + projectionPrefix.slice('rgb11-'.length)};
}

function captureFullSnapshot(storage, reference, projectionPrefix) {
  const normalized = normalizedSnapshot(reference), prefixes = orphanPrefixes(projectionPrefix);
  const scan = prefix => {
    const records = [];
    for (let i = 0; i < storage.length; i++) {
      const full = storage.key(i);
      if (typeof full !== 'string' || !full.startsWith(prefix)) continue;
      const value = storage.getItem(full);
      if (typeof value !== 'string') throw Error('storage changed during capture');
      records.push({key: full.slice(prefix.length), value});
    }
    records.sort((a, b) => a.key < b.key ? -1 : a.key > b.key ? 1 : 0);
    return records;
  };
  normalized.projection_records = scan(prefixes.projection);
  normalized.engine_records = scan(prefixes.engine);
  return normalizedSnapshot(normalized);
}

function recordMap(snapshot, store) {
  return new Map(snapshot[store === 'projection' ? 'projection_records' : 'engine_records'].map(record => [record.key, record.value]));
}

async function buildOrphanJournal(sourceValue, candidateValue, plan, binding) {
  const source = normalizedSnapshot(sourceValue), candidate = normalizedSnapshot(candidateValue);
  if (!binding?.exclusive || typeof binding.identity !== 'string' || !binding.identity.startsWith('prd|testnet|')) {
    throw Error('closed testnet maintenance session required');
  }
  const prefixes = orphanPrefixes(binding.projectionPrefix);
  if (!plan || plan.target?.wallet_id !== source.wallet_id || plan.target.account_index !== source.account_index ||
      candidate.wallet_id !== source.wallet_id || candidate.account_index !== source.account_index ||
      candidate.engine_build_id !== source.engine_build_id || candidate.version !== source.version) throw Error('repair identity mismatch');
  const target = plan.target;
  if (typeof target.request_id !== 'string' || !target.request_id || typeof target.transfer_id !== 'string' || !target.transfer_id ||
      typeof target.witness_txid !== 'string' || !target.witness_txid || typeof target.consignment_hash !== 'string' || !target.consignment_hash) {
    throw Error('incomplete orphan repair target');
  }
  const beforeHash = await snapshotHash(source), afterHash = await snapshotHash(candidate);
  if (plan.target.snapshot_hash !== beforeHash || plan.before_hash !== beforeHash || plan.after_hash !== afterHash || beforeHash === afterHash) {
    throw Error('approved snapshot hash mismatch');
  }
  if (!Array.isArray(plan.changes) || !Array.isArray(plan.changed_keys) || plan.changes.length < 3 || plan.changes.length > 4) {
    throw Error('unexpected repair changes');
  }
  const required = [
    `engine/wallet/receive/${target.request_id}`,
    `projection/transfer-${target.transfer_id}`,
    `projection/prepared-receive-${target.transfer_id}`,
  ];
  const allowed = new Set([...required, `projection/validation-${target.consignment_hash}`]);
  if (plan.changed_keys.some(key => !allowed.has(key)) || required.some(key => !plan.changed_keys.includes(key)) ||
      new Set(plan.changed_keys).size !== plan.changed_keys.length) throw Error('unexpected orphan repair key');
  const operations = [];
  const actualChanged = [];
  for (const store of ['projection', 'engine']) {
    const before = recordMap(source, store), after = recordMap(candidate, store);
    for (const key of new Set([...before.keys(), ...after.keys()])) {
      if (before.get(key) === after.get(key)) continue;
      actualChanged.push(`${store}/${key}`);
    }
  }
  if (JSON.stringify(actualChanged.sort()) !== JSON.stringify([...plan.changed_keys].sort())) throw Error('candidate changed-key mismatch');
  for (const change of plan.changes) {
    if (!['projection', 'engine'].includes(change.store) || typeof change.key !== 'string' || !change.key) throw Error('invalid repair change');
    const qualified = `${change.store}/${change.key}`;
    if (!allowed.has(qualified) || (qualified === required[0] ? change.delete : !change.delete)) throw Error('unexpected orphan repair operation');
    const before = recordMap(source, change.store).get(change.key);
    const after = recordMap(candidate, change.store).get(change.key);
    if (typeof before !== 'string' || await base64Hash(before) !== change.before_sha256) throw Error('repair before binding mismatch');
    if (change.delete) {
      if (after !== undefined || change.after_sha256) throw Error('repair deletion mismatch');
    } else if (typeof after !== 'string' || !change.after_sha256 || await base64Hash(after) !== change.after_sha256) {
      throw Error('repair after binding mismatch');
    }
    operations.push({key: prefixes[change.store] + change.key, before, after: change.delete ? null : after});
  }
  if (new Set(operations.map(operation => operation.key)).size !== operations.length || operations.length !== actualChanged.length) {
    throw Error('repair operation set mismatch');
  }
  return {
    version: 1, identity: binding.identity, projectionPrefix: binding.projectionPrefix,
    beforeHash, afterHash, source, candidate, operations, next: 0, phase: 'applying',
  };
}

function persistJournal(storage, journal) {
  const encoded = JSON.stringify(journal);
  storage.setItem(orphanRepairJournalKey, encoded);
  if (storage.getItem(orphanRepairJournalKey) !== encoded) throw Error('repair journal readback mismatch');
}

function validateJournalOperations(journal) {
  const prefixes = orphanPrefixes(journal.projectionPrefix), expected = new Map();
  for (const store of ['projection', 'engine']) {
    const before = recordMap(journal.source, store), after = recordMap(journal.candidate, store);
    for (const key of new Set([...before.keys(), ...after.keys()])) {
      if (before.get(key) !== after.get(key)) expected.set(prefixes[store] + key, {before: before.get(key) ?? null, after: after.get(key) ?? null});
    }
  }
  if (expected.size !== journal.operations.length || !Number.isInteger(journal.next) || journal.next < 0 || journal.next > journal.operations.length ||
      !['applying', 'verified'].includes(journal.phase)) throw Error('repair journal operation set corrupted');
  const seen = new Set();
  for (const operation of journal.operations) {
    const wanted = expected.get(operation?.key);
    if (!wanted || seen.has(operation.key) || operation.before !== wanted.before || operation.after !== wanted.after) {
      throw Error('repair journal operation set corrupted');
    }
    seen.add(operation.key);
  }
}

async function continueOrphanJournal(storage, journal, binding) {
  if (!binding?.exclusive || journal.identity !== binding.identity || journal.projectionPrefix !== binding.projectionPrefix) {
    throw Error('repair journal scope mismatch');
  }
  if (journal.version !== 1 || journal.beforeHash !== await snapshotHash(journal.source) ||
      journal.afterHash !== await snapshotHash(journal.candidate) || !Array.isArray(journal.operations)) throw Error('repair journal corrupted');
  validateJournalOperations(journal);
  while (journal.next < journal.operations.length) {
    const operation = journal.operations[journal.next], current = storage.getItem(operation.key);
    if (current !== operation.after) {
      if (current !== operation.before) throw Error(`repair CAS mismatch at ${operation.key}`);
      if (operation.after === null) storage.removeItem(operation.key); else storage.setItem(operation.key, operation.after);
      if (storage.getItem(operation.key) !== operation.after) throw Error(`repair readback mismatch at ${operation.key}`);
    }
    journal.next++;
    persistJournal(storage, journal);
  }
  const live = captureFullSnapshot(storage, journal.candidate, journal.projectionPrefix);
  if (await snapshotHash(live) !== journal.afterHash) throw Error('repaired snapshot verification failed');
  journal.phase = 'verified';
  persistJournal(storage, journal);
  storage.removeItem(orphanRepairJournalKey);
  if (storage.getItem(orphanRepairJournalKey) !== null) throw Error('repair journal cleanup failed');
  return 'Applied approved RGB11 orphan repair and verified the complete wallet snapshot.';
}

// Applies only an offline-approved orphan repair artifact. Each Web Storage
// mutation is CAS checked. The durable journal makes an interrupted run
// idempotently resume on this maintenance tool without invoking wallet code.
export async function applyOrphanSnapshotRepair(storage, source, candidate, plan, binding) {
  if (storage.getItem(orphanRepairJournalKey) !== null) throw Error('unfinished repair journal exists; resume it first');
  const {journal} = await validateOrphanSnapshotRepair(storage, source, candidate, plan, binding);
  persistJournal(storage, journal);
  return continueOrphanJournal(storage, journal, binding);
}

export async function validateOrphanSnapshotRepair(storage, source, candidate, plan, binding) {
  if (storage.getItem(orphanRepairJournalKey) !== null) throw Error('unfinished repair journal exists; resume it first');
  const journal = await buildOrphanJournal(source, candidate, plan, binding);
  const live = captureFullSnapshot(storage, source, binding.projectionPrefix);
  if (await snapshotHash(live) !== journal.beforeHash) throw Error('scope changed; export again');
  // Recheck after async hashes. The dedicated maintenance session must be the
  // only origin writer and must not load the wallet runtime or service worker.
  if (JSON.stringify(captureFullSnapshot(storage, source, binding.projectionPrefix)) !== JSON.stringify(live)) throw Error('concurrent writer');
  return {journal, beforeHash: journal.beforeHash, afterHash: journal.afterHash, operationCount: journal.operations.length,
    requestID: plan.target.request_id, transferID: plan.target.transfer_id, witnessTxID: plan.target.witness_txid};
}

export async function resumeOrphanSnapshotRepair(storage, binding) {
  const encoded = storage.getItem(orphanRepairJournalKey);
  if (encoded === null) return 'No unfinished RGB11 orphan repair journal.';
  let journal;
  try { journal = JSON.parse(encoded); } catch { throw Error('repair journal corrupted'); }
  return continueOrphanJournal(storage, journal, binding);
}
