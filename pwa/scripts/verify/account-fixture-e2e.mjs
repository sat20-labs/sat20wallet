import assert from 'node:assert/strict'
import { expect } from '@playwright/test'

export const recoveryQuestions = [
  ['book', 'book phrase', 'moonlight over the old bridge'],
  ['note', 'private note', 'yellow bicycle beside the river'],
  ['family', 'family phrase', 'meet under the tree at six'],
].map(([id, prompt, answer]) => ({ question: { id, prompt, case_sensitive: false, ignore_punctuation: true }, answer, confirmation: answer }))
export const recoveryAnswers = recoveryQuestions.map(q => ({ question_id: q.question.id, text: q.answer }))
export const recoveryShare = material => Buffer.from(material.files.find(file => file.name === 'user-share.txt').data, 'base64').toString('utf8')
export async function waitAccountBackup(page, accountCall) {
  await expect.poll(async () => (await accountCall(page, 'getAccountView')).backup_state,
    { timeout: 90000 }).toBe('backed_up')
}
// Public SDK preparation for tests whose actual subject is UI state/lifecycle.
export async function setupAccount(t, page, password = t.password) {
  const options = await t.accountCall(page, 'getStorageOptions', 100)
  const option = options.find(item => item.id === 'temporary')
  assert.equal(option?.available, true)
  await t.accountCall(page, 'confirmStorage', option)
  const material = await t.accountCall(page, 'createRecovery', {
    password, wallet_ids: (await t.catalog(page)).map(wallet => wallet.id), mode: '2of2', questions: recoveryQuestions,
  })
  await t.accountCall(page, 'activateRecovery', material.session_id, {
    password, saved_locator: material.locator, factors: { answers: recoveryAnswers, user_share: recoveryShare(material) },
  })
  await waitAccountBackup(page, t.accountCall)
  return material
}
export async function restoreAccount(t, page, material, password = t.password) {
  const session = await t.accountCall(page, 'loadRecovery', material.locator)
  await t.accountCall(page, 'previewRecovery', session.session_id, { answers: recoveryAnswers, user_share: recoveryShare(material) })
  const result = await t.accountCall(page, 'commitRecovery', session.session_id, password)
  assert.equal(result.complete, true)
  await page.reload(); await t.ready(page); await t.unlock(page, password)
  await waitAccountBackup(page, t.accountCall)
}
