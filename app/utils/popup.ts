import { Browser } from '@capacitor/browser';

export const createPopup = async (url: string): Promise<void> => {
  await Browser.open({ url });
};