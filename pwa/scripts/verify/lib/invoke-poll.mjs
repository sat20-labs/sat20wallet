function errorMessage(error) {
  return error?.message || String(error);
}

export async function pollInvokeEvidence({
  attempts,
  sleep,
  delayForAttempt,
  getRawTx,
  getHistory,
  getStatus,
  findHistoryItem,
  isComplete,
}) {
  const result = {
    attempts: 0,
    completed: false,
    rawTx: undefined,
    history: undefined,
    historyItem: undefined,
    statusAfter: undefined,
    lastErrors: {},
  };

  for (let attempt = 0; attempt < attempts; attempt++) {
    result.attempts = attempt + 1;
    await sleep(delayForAttempt(attempt));

    try {
      result.rawTx = await getRawTx();
    } catch (error) {
      result.lastErrors.rawTx = errorMessage(error);
    }
    try {
      result.history = await getHistory();
      result.historyItem = findHistoryItem(result.history);
    } catch (error) {
      result.lastErrors.history = errorMessage(error);
    }
    try {
      result.statusAfter = await getStatus();
    } catch (error) {
      result.lastErrors.status = errorMessage(error);
    }

    if (isComplete(result)) {
      result.completed = true;
      break;
    }
  }

  return result;
}

export function formatInvokePollFailure(txId, result) {
  const errors = Object.entries(result.lastErrors)
    .map(([probe, message]) => `${probe}=${message}`)
    .join('; ');
  return `Invoke ${txId} did not become fully observable after ${result.attempts} attempts`
    + (errors ? `; last errors: ${errors}` : '');
}
