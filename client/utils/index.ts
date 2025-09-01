import { Chain, type Env } from '@/types/index';
import { Network } from '@/types/index';
import { ordxApi } from '@/apis';
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
  if (network === 'testnet') {
    url += '/testnet';
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

export function validateBTCAddress(address: string, network: string = 'mainnet'): boolean {
  if (!address || typeof address !== 'string') return false

  const trimmedAddress = address.trim()

  if (network === 'testnet') {
    // Testnet 地址格式：
    // 1. Legacy testnet addresses (以 2 开头)
    // 2. Bech32 testnet addresses (以 tb1 开头)
    const testnetRegex = /^(tb1[a-z0-9]{39,59}|2[a-km-zA-HJ-NP-Z1-9]{26,33})$/;
    return testnetRegex.test(trimmedAddress);
  } else {
    // Mainnet 地址格式：
    // 1. Legacy mainnet addresses (以 1 或 3 开头)
    // 2. Bech32 mainnet addresses (以 bc1 开头)
    const mainnetRegex = /^(bc1[a-z0-9]{39,59}|[13][a-km-zA-HJ-NP-Z1-9]{26,33})$/;
    return mainnetRegex.test(trimmedAddress);
  }
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

// 域名解析相关工具函数
export const isDomainName = (input: string, network: string = 'mainnet'): boolean => {
  // 简化的域名检测：只要不是比特币地址格式，就可能是域名
  if (!input || typeof input !== 'string') return false

  const trimmedInput = input.trim()

  // 如果是比特币地址格式，则不是域名
  if (validateBTCAddress(trimmedInput, network)) return false

  // 其他情况都可能是域名，让API来判断
  return true
}

export const resolveDomainName = async (name: string, network: string): Promise<{ address: string; name: string } | null> => {
  try {
    // 使用 OrdxApi 的 getNsName 方法

    const response = await ordxApi.getNsName({ name, network })

    if (response?.data?.address) {
      return {
        address: response.data.address,
        name: name
      }
    }

    return null
  } catch (error) {
    console.error(`Error resolving domain ${name}:`, error)
    return null
  }
}

export const validateAndResolveAddress = async (input: string, network: string): Promise<{
  isDomain: boolean
  resolvedAddress: string | null
  originalInput: string
  domainName: string | null
}> => {
  const trimmedInput = input.trim()

  // 如果是比特币地址，直接返回
  if (validateBTCAddress(trimmedInput, network)) {
    return {
      isDomain: false,
      resolvedAddress: trimmedInput,
      originalInput: trimmedInput,
      domainName: null
    }
  }

  // 对于非比特币地址格式的输入，尝试解析为域名
  const resolved = await resolveDomainName(trimmedInput, network)
  return {
    isDomain: resolved !== null,
    resolvedAddress: resolved?.address || null,
    originalInput: trimmedInput,
    domainName: resolved ? trimmedInput : null
  }
}

/*
测试用例示例：

// 主网地址测试
validateBTCAddress('1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa', 'mainnet') // true - Legacy mainnet
validateBTCAddress('bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4', 'mainnet') // true - Bech32 mainnet
validateBTCAddress('3J98t1WpEZ73CNmQviecrnyiWrnqRhWNLy', 'mainnet') // true - P2SH mainnet

// 测试网地址测试
validateBTCAddress('tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4', 'testnet') // true - Bech32 testnet
validateBTCAddress('2N1F1mBJLth1JUoUZBTfu2CfuWU5k5QENJA', 'testnet') // true - Legacy testnet

// 错误地址测试
validateBTCAddress('invalid-address', 'mainnet') // false
validateBTCAddress('tb1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4', 'mainnet') // false - testnet地址在mainnet
validateBTCAddress('bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4', 'testnet') // false - mainnet地址在testnet
*/

/**
 * 检测地址是否为非Taproot地址
 * 非Taproot地址格式：
 * - 主网：以 bc1q 开头或其他格式
 * - 测试网：以 tb1q 开头或其他格式
 */
export function isNonTaprootAddress(address: string, network: string = 'mainnet'): boolean {
  if (!address || typeof address !== 'string') return false
  
  const trimmedAddress = address.trim()
  
  if (network === 'testnet') {
    // 测试网非Taproot地址：不以 tb1p 开头
    return !trimmedAddress.startsWith('tb1p')
  } else {
    // 主网非Taproot地址：不以 bc1p 开头
    return !trimmedAddress.startsWith('bc1p')
  }
}

/**
 * 检测地址是否为非Taproot地址（自动检测网络）
 * 根据地址前缀自动判断网络类型
 */
export function isNonTaprootAddressAuto(address: string): boolean {
  if (!address || typeof address !== 'string') return false
  
  const trimmedAddress = address.trim()
  
  // 主网Taproot地址：以 bc1p 开头
  if (trimmedAddress.startsWith('bc1p')) {
    return false
  }
  
  // 测试网Taproot地址：以 tb1p 开头
  if (trimmedAddress.startsWith('tb1p')) {
    return false
  }
  
  return true
}
