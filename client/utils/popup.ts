interface PopupOptions {
  width?: number
  height?: number
}

export const createPopup = (
  url: string,
  options: PopupOptions = {}
): Promise<chrome.windows.Window | undefined> => {
  const { width = 375, height = 700 } = options
  return new Promise((resolve) => {
    browser.windows.create(
      {
        url,
        type: 'popup',
        width,
        height,
      },
      (newWindow) => {
        resolve(newWindow)
      }
    )
  })
}
