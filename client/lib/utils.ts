import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'
import { Message } from '@/types/message'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export async function sendAccountsChangedEvent(data: any, from = Message.MessageFrom.POPUP, to = Message.MessageTo.INJECTED) {
  if (typeof browser !== 'undefined' && browser.runtime?.sendMessage) {
    const currWin = await browser.windows.getCurrent()
    browser.runtime.sendMessage({
      type: Message.MessageType.EVENT,
      event: 'accountsChanged',
      data,
      metadata: {
        from,
        to,
        windowId: currWin.id,
      },
    })
  }
}

export async function sendNetworkChangedEvent(network: string, from = Message.MessageFrom.POPUP, to = Message.MessageTo.INJECTED) {
  if (typeof browser !== 'undefined' && browser.runtime?.sendMessage) {
    const currWin = await browser.windows.getCurrent()
    await browser.runtime.sendMessage({
      type: Message.MessageType.EVENT,
      event: 'networkChanged',
      data: network,
      metadata: {
        from,
        to,
        windowId: currWin.id,
      },
    })
  }
}
