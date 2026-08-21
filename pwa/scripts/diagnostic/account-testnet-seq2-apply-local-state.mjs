export function classifyLocalPatchHash(currentHash, oldHash, targetHash) {
  const current = String(currentHash || '').toLowerCase()
  const old = String(oldHash || '').toLowerCase()
  const target = String(targetHash || '').toLowerCase()
  if (!current || !old || !target) return 'invalid'
  if (current === target) return 'already_applied'
  if (current === old) return 'apply'
  return 'third_hash'
}
