export const awaitAccountChannelRefresh = async (
  refresh: () => Promise<void>,
  onError: (error: unknown) => void,
) => {
  try {
    await refresh()
  } catch (error) {
    onError(error)
  }
}
