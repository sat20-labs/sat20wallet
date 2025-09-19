import { type ClassValue, clsx } from 'clsx'
import { twMerge } from 'tailwind-merge'
import { Message } from '@/types/message'

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

export async function sendAccountsChangedEvent(data: any, from = Message.MessageFrom.POPUP, to = Message.MessageTo.INJECTED) {
  // No-op in Capacitor
}

export async function sendNetworkChangedEvent(network: string, from = Message.MessageFrom.POPUP, to = Message.MessageTo.INJECTED) {
  // No-op in Capacitor
}