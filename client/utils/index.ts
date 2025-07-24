import { Chain, type Env } from '@/types/index';
import { Network } from '@/types/index';
export const hideAddress = (
  str?: string | null,
  num: number = 6,
  placeholder = '*****',
) => {
  if (typeof str === 'string' && str) {
    const regex = new RegExp(`^(.{${num}}).+(.{${num}})$`);
    return str.replace(regex, `$1${placeholder}$2`);
  }
  return str;
};

export const generateMempoolUrl = ({
  network,
  path,
  locale,
  chain,
  env,
}: {
  network: string;
  path?: string;
  locale?: string;
  chain?: Chain;
  env?: Env;
}) => {
  const satMempoolUrl: Record<Env, string> = {
    dev: 'https://mempool.dev.sat20.org',
    test: 'https://mempool.test.sat20.org',
    prd: 'https://mempool.sat20.org',
  }
  let _network = network;
  if (network === Network.TESTNET) {
    _network = 'testnet';
  } else if (network === Network.LIVENET) {
    _network = 'mainnet';
  }
  const btcMempoolUrl = 'https://mempool.space'
  let base = btcMempoolUrl;
  if (chain && chain === Chain.SATNET && env) {
    base = satMempoolUrl[env];
  }
  let url = base;
  if (chain !== Chain.SATNET && locale) {
    url += `/${locale}`;
  }
  if (chain !== Chain.SATNET) {
    if (network === 'testnet') {
      url += '/testnet4';
    }
  } else {
    url += `/${network}`;
  }
  if (path) {
    url += `/${path}`;
  }
  return url;
};

export function satsToBtc(sats: string | number): number {
  if (typeof sats === 'string') {
    sats = sats.trim();
  }

  if (isNaN(Number(sats))) {
    console.warn('Input is not a valid number, defaulting to 0');
    sats = 0;
  }

  let satoshis = Number(sats);

  // Ensure the number is non-negative
  if (satoshis < 0) {
    console.warn('Input must be a non-negative number, defaulting to 0');
    satoshis = 0;
  }

  // Round to the nearest integer to handle decimal Satoshis
  satoshis = Math.round(satoshis);

  // Convert Satoshis to BTC
  const btc = satoshis / 1e8;

  return btc;
}

export function btcToSats(btc: string | number): number {
  if (typeof btc === 'string') {
    btc = btc.trim();
  }

  if (isNaN(Number(btc))) {
    console.warn('Input is not a valid number, defaulting to 0');
    btc = 0;
  }

  let btcAmount = Number(btc);

  // Ensure the number is non-negative
  if (btcAmount < 0) {
    console.warn('Input must be a non-negative number, defaulting to 0');
    btcAmount = 0;
  }

  // Convert BTC to Satoshis and handle precision issues by rounding
  const sats = Math.round(btcAmount * 1e8);

  return sats;
}

export function validateBTCAddress(address: string): boolean {
  const regex = /^[13][a-km-zA-HJ-NP-Z1-9]{26,33}$/;
  return regex.test(address);
}

export const formatLargeNumber = (num: number): string => {
  const format = (value: number): string => {
    // 对整数部分进行千位分隔，对小数部分保留最多两位小数
    return value % 1 === 0
      ? value.toLocaleString() // 整数部分千位分隔
      : Number(value.toFixed(2)).toLocaleString(); // 小数部分最多两位小数
  };

  if (num >= 1_000_000_000) {
    return `${format(num / 1_000_000_000)} B`; // 转换为十亿单位
  } else if (num >= 1_000_000) {
    return `${format(num / 1_000_000)} M`; // 转换为百万单位
  } else if (num >= 10_000) {
    return `${format(num / 1_000)} K`; // 转换为千单位
  }
  return num.toLocaleString(); // 小于 1000 的数字直接返回
};
