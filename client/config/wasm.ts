// 测试环境，
const devConfig = {
    Env: "dev",
    Chain: "testnet",
    Mode: "client",
    Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@seed.sat20.org:19529",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@39.108.96.46:19529"
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
};
// 测试环境，
const testConfig = {
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
const prodConfig = {
    Env: "prod",
    Chain: "mainnet",
    Mode: "client",
    Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@seed.sat20.org:19529",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@39.108.96.46:19529"
    ],
    IndexerL1: {
        Scheme: "https",
        Host: "apiprd.sat20.org",
        Proxy: "btc/testnet"
    },
    IndexerL2: {
        Scheme: "https",
        Host: "apiprd.sat20.org",
        Proxy: "satsnet/testnet"
    },
    Log: "debug"
}
export const logLevel = 5 //0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace
export const config = {
    dev: devConfig,
    test: testConfig,
    prod: prodConfig,
}
export const getConfig = (env: string) => {
    return config[env as keyof typeof config]
}