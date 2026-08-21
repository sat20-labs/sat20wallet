import { createHash } from 'node:crypto'
import { chmodSync, renameSync, writeFileSync } from 'node:fs'
import { chromium } from '@playwright/test'

const legacyPrefix = 'dkvs-batch-outbox-v1:'
const expectedNamespace = 'prd:testnet:https:apiprd.ordx.market:satsnet/testnet'
const expectedHeight = 39791n
const expectedTTL = 144n

const option = (name, fallback) => {
  const prefix = `--${name}=`
  const value = process.argv.find((argument) => argument.startsWith(prefix))
  return value ? value.slice(prefix.length) : fallback
}

const apply = process.argv.includes('--apply')
const cdp = option('cdp', 'http://127.0.0.1:9223')
const origin = option('origin', 'http://localhost:5173')
const timestamp = new Date().toISOString().replaceAll(':', '-').replaceAll('.', '-')
const backup = option('backup', `/private/tmp/sat20-dkvs-outbox-39791-${timestamp}.json`)
if (!backup.startsWith('/private/tmp/')) {
  throw new Error('--backup must be an explicit /private/tmp path')
}

const digest = (value) => createHash('sha256').update(value).digest('hex')

const legacyEntryID = (entry) => {
  const hash = createHash('sha256')
  for (const mutation of entry.mutations || []) {
    hash.update(Buffer.from(mutation.record || '', 'base64'))
    hash.update(Buffer.from([0]))
    hash.update(mutation.expected_hash || '', 'utf8')
    hash.update(Buffer.from([mutation.expect_absent ? 1 : 0]))
  }
  for (const condition of entry.path_preconditions || []) {
    hash.update(condition.path || '', 'utf8')
    hash.update(condition.expected_root || '', 'utf8')
    const generation = Buffer.alloc(8)
    generation.writeBigUInt64BE(BigInt(condition.expected_generation || 0))
    hash.update(generation)
  }
  hash.update(entry.endpoint_id || '', 'utf8')
  return hash.digest('hex')
}

const readCompactSize = (bytes, cursor) => {
  if (cursor.offset >= bytes.length) throw new Error('unexpected end of compact size')
  const marker = bytes[cursor.offset++]
  if (marker < 0xfd) return BigInt(marker)
  const width = marker === 0xfd ? 2 : marker === 0xfe ? 4 : 8
  if (cursor.offset + width > bytes.length) throw new Error('truncated compact size')
  let value = 0n
  for (let index = 0; index < width; index++) {
    value |= BigInt(bytes[cursor.offset + index]) << BigInt(index * 8)
  }
  cursor.offset += width
  if (marker === 0xfd && value < 0xfdn || marker === 0xfe && value <= 0xffffn ||
      marker === 0xff && value <= 0xffffffffn) {
    throw new Error('non-canonical compact size')
  }
  return value
}

const readBytes = (bytes, cursor) => {
  const length = Number(readCompactSize(bytes, cursor))
  if (!Number.isSafeInteger(length) || cursor.offset + length > bytes.length) {
    throw new Error('invalid variable bytes length')
  }
  const value = bytes.slice(cursor.offset, cursor.offset + length)
  cursor.offset += length
  return value
}

const readU32 = (bytes, cursor) => {
  if (cursor.offset + 4 > bytes.length) throw new Error('truncated uint32')
  const value = new DataView(bytes.buffer, bytes.byteOffset + cursor.offset, 4).getUint32(0, true)
  cursor.offset += 4
  return value
}

const readU64 = (bytes, cursor) => {
  if (cursor.offset + 8 > bytes.length) throw new Error('truncated uint64')
  const value = new DataView(bytes.buffer, bytes.byteOffset + cursor.offset, 8).getBigUint64(0, true)
  cursor.offset += 8
  return value
}

const decodeRecord = (encoded) => {
  const bytes = Uint8Array.from(Buffer.from(encoded, 'base64'))
  const cursor = { offset: 0 }
  const version = readU32(bytes, cursor)
  const key = new TextDecoder().decode(readBytes(bytes, cursor))
  readBytes(bytes, cursor) // value
  readBytes(bytes, cursor) // public key
  readBytes(bytes, cursor) // signature
  const seq = readU64(bytes, cursor)
  const issueHeight = readU64(bytes, cursor)
  const ttl = readU64(bytes, cursor)
  readBytes(bytes, cursor) // fee proof
  const flags = readU32(bytes, cursor)
  if (cursor.offset !== bytes.length) throw new Error('trailing DKVS record bytes')
  return { version, key, seq, issueHeight, ttl, flags }
}

const inspectLegacyEntry = (storageKey, storageValue) => {
  if (!storageKey.startsWith(legacyPrefix)) return null
  let entry
  try {
    entry = JSON.parse(Buffer.from(storageValue, 'base64').toString('utf8'))
  } catch {
    return null
  }
  const expectedKeyPrefix = `${legacyPrefix}${digest(expectedNamespace)}:`
  if (entry?.namespace !== expectedNamespace || entry?.state !== 'terminal' ||
      entry?.last_error_code !== 'DKVS_INVALID_RECORD' || !entry?.id ||
      storageKey !== `${expectedKeyPrefix}${entry.id}` ||
      entry.id !== legacyEntryID(entry) || entry?.recovery_of) {
    return null
  }
  if (!Array.isArray(entry.mutations) || entry.mutations.length !== 2) return null
  const records = entry.mutations.map((mutation) => {
    if (!mutation?.expect_absent || mutation?.expected_hash) {
      throw new Error(`legacy candidate ${storageKey} has unexpected precondition`)
    }
    return decodeRecord(mutation.record)
  })
  const state = records.find((record) => /^\/personal\/[0-9a-f]{64}\/account\/state$/.test(record.key))
  const blob = records.find((record) => /^\/blob\/[0-9a-f]{64}\/account-managed-data$/.test(record.key))
  if (!state || !blob) return null
  const stateAccount = state.key.split('/')[2]
  const blobAccount = blob.key.split('/')[2]
  if (stateAccount !== blobAccount || records.some((record) =>
    record.issueHeight !== expectedHeight || record.ttl !== expectedTTL || record.seq !== 1n)) {
    return null
  }
  return {
    storageKey,
    storageValue,
    valueSHA256: digest(storageValue),
    accountID: stateAccount,
    mutationKeys: records.map((record) => record.key),
    issueHeights: records.map((record) => record.issueHeight.toString()),
    ttls: records.map((record) => record.ttl.toString()),
  }
}

const browser = await chromium.connectOverCDP(cdp)
try {
  const context = browser.contexts()[0]
  const page = context?.pages().find((candidate) => candidate.url().startsWith(origin))
  if (!page) throw new Error(`page not found for ${origin}`)

  const snapshot = await page.evaluate(async (prefix) => {
    const chromeLocal = globalThis.chrome?.storage?.local
    if (chromeLocal) {
      const all = await new Promise((resolve, reject) => {
        chromeLocal.get(null, (values) => {
          const error = globalThis.chrome?.runtime?.lastError
          if (error) reject(new Error(error.message))
          else resolve(values || {})
        })
      })
      return {
        kind: 'chrome.storage.local',
        values: Object.entries(all).filter(([key]) => key.startsWith(prefix)),
      }
    }
    const values = []
    for (let index = 0; index < localStorage.length; index++) {
      const key = localStorage.key(index)
      if (key?.startsWith(prefix)) values.push([key, localStorage.getItem(key)])
    }
    return { kind: 'localStorage', values }
  }, legacyPrefix)

  const candidates = snapshot.values
    .filter(([, value]) => typeof value === 'string')
    .map(([key, value]) => inspectLegacyEntry(key, value))
    .filter(Boolean)
  if (candidates.length !== 1) {
    throw new Error(`expected exactly one verified 39791 outbox record, found ${candidates.length}`)
  }
  const candidate = candidates[0]
  const report = {
    storage: snapshot.kind,
    origin,
    key: candidate.storageKey,
    value_sha256: candidate.valueSHA256,
    account_id: candidate.accountID,
    mutation_keys: candidate.mutationKeys,
    issue_heights: candidate.issueHeights,
    ttls: candidate.ttls,
  }
  if (!apply) {
    console.log(JSON.stringify({ mode: 'dry-run', ...report }))
    process.exit(0)
  }

  const temporary = `${backup}.partial`
  writeFileSync(temporary, JSON.stringify({ ...report, storage_value: candidate.storageValue }), {
    encoding: 'utf8', mode: 0o600,
  })
  chmodSync(temporary, 0o600)
  renameSync(temporary, backup)
  chmodSync(backup, 0o600)

  await page.evaluate(async ({ kind, key, expectedValue }) => {
    if (kind === 'chrome.storage.local') {
      const chromeLocal = globalThis.chrome?.storage?.local
      const current = await new Promise((resolve, reject) => {
        chromeLocal.get(key, (values) => {
          const error = globalThis.chrome?.runtime?.lastError
          if (error) reject(new Error(error.message))
          else resolve(values?.[key])
        })
      })
      if (current !== expectedValue) throw new Error('outbox value changed before deletion')
      await new Promise((resolve, reject) => {
        chromeLocal.remove(key, () => {
          const error = globalThis.chrome?.runtime?.lastError
          if (error) reject(new Error(error.message))
          else resolve()
        })
      })
      const remaining = await new Promise((resolve, reject) => {
        chromeLocal.get(key, (values) => {
          const error = globalThis.chrome?.runtime?.lastError
          if (error) reject(new Error(error.message))
          else resolve(values?.[key])
        })
      })
      if (remaining !== undefined) throw new Error('outbox key still exists after deletion')
      return
    }
    if (localStorage.getItem(key) !== expectedValue) {
      throw new Error('outbox value changed before deletion')
    }
    localStorage.removeItem(key)
    if (localStorage.getItem(key) !== null) throw new Error('outbox key still exists after deletion')
  }, { kind: snapshot.kind, key: candidate.storageKey, expectedValue: candidate.storageValue })

  console.log(JSON.stringify({ mode: 'deleted', backup, ...report }))
} finally {
  // Exiting disconnects from CDP without terminating the user's browser.
}
process.exit(0)
