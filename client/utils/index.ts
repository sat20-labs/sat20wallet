export const hideAddress = (
  str?: string | null,
  num: number = 6,
  placeholder = '*****',
) => {
  console.log('hideAddress', typeof str === 'string', str);

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
}: {
  network: string;
  path?: string;
  locale?: string;
}) => {
  const base = 'https://mempool.space';
  let url = base;
  if (locale) {
    url += `/${locale}`;
  }
  if (network === 'testnet') {
    url += '/testnet4';
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
