import { Network } from '@/types'
interface NetworkConfig {
  Env: string
  Chain: string
  Mode: string
  Peers: string[]
  IndexerL1: {
    Scheme: string
    Host: string
    Proxy: string
  }
  IndexerL2: {
    Scheme: string
    Host: string
    Proxy: string
  }
  Log: string
}

interface EnvConfig {
  [Network.LIVENET]: NetworkConfig
  [Network.TESTNET]: NetworkConfig
}

interface Config {
  dev: EnvConfig
  test: EnvConfig
  prd: EnvConfig
}

const config: Config = {
  dev: {
    [Network.LIVENET]: {
      Env: "dev",
      Chain: "mainnet",
      Mode: "client",
      Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://satstestnet-peer-p1.sat20.org",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@https://satstestnet-peer-p0.sat20.org"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apidev.sat20.org",
        Proxy: "btc/testnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apidev.sat20.org",
        Proxy: "satsnet/testnet"
      },
      Log: "debug"
    },
    [Network.TESTNET]: {
      Env: "dev",
      Chain: "testnet",
      Mode: "client",
      Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://satstestnet-peer-p1.sat20.org",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@https://satstestnet-peer-p0.sat20.org"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apidev.sat20.org",
        Proxy: "btc/testnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apidev.sat20.org",
        Proxy: "satsnet/testnet"
      },
      Log: "debug"
    }
  },
  test: {
    [Network.LIVENET]: {
      Env: "test",
      Chain: "mainnet",
      Mode: "client",
      Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@seed.sat20.org:19529",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@39.108.96.46:19529"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apitest.sat20.org",
        Proxy: "btc/testnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apitest.sat20.org",
        Proxy: "satsnet/testnet"
      },
      Log: "debug"
    },
    [Network.TESTNET]: {
      Env: "test",
      Chain: "testnet",
      Mode: "client",
      Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@seed.sat20.org:19529",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@39.108.96.46:19529"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apitest.sat20.org",
        Proxy: "btc/testnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apitest.sat20.org",
        Proxy: "satsnet/testnet"
      },
      Log: "debug"
    }
  },
  prd: {
    [Network.LIVENET]: {
      Env: "prd",
      Chain: "mainnet",
      Mode: "client",
      Peers: [
        "b@03ab606f4dffd65965b4a9db957361800f8b03ed16acac11d5a4672801554596d0@https://apiprd.sat20.org/stp/mainnet",
        "s@022ab2945f61304f117f55d469c341d606ceb729de436c80c0e6ad7819cdd53ce7@https://apiprd.ordx.market/stp/mainnet"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apiprd.ordx.market",
        Proxy: "btc/mainnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apiprd.ordx.market",
        Proxy: "satsnet/mainnet"
      },    
      Log: "error"
    },
    [Network.TESTNET]: {
      Env: "prd",
      Chain: "testnet",
      Mode: "client",
      Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@https://apiprd.sat20.org/stp/testnet",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@https://apiprd.ordx.market/stp/testnet"
      ],
      IndexerL1: {
        Scheme: "https",
        Host: "apiprd.ordx.market",
        Proxy: "btc/testnet"
      },
      IndexerL2: {
        Scheme: "https",
        Host: "apiprd.ordx.market",
        Proxy: "satsnet/testnet"
      },
      Log: "error"
    }
  }
}

export const logLevel = 2 //0: Panic, 1: Fatal, 2: Error, 3: 
export const getConfig = (env: string, network: Network): NetworkConfig => {
  const envConfig = config[env as keyof Config]
  if (!envConfig) {
    throw new Error(`Invalid env: ${env}`)
  }
  const networkConfig = envConfig[network]
  if (!networkConfig) {
    throw new Error(`Invalid network: ${network}`)
  }
  return networkConfig
}