const cfg = {
    Chain: "testnet4",
    Mode: "client",
    Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@192.168.10.102:9080",
        "s@033d7411f3023987d2c537e6a14bfc99368f8928627b5c845d5bcdafd14965525b@192.168.10.104:9060"
    ],
    RPC: {
        Scheme: "http",
        Host: "0.0.0.0:9080",
        Proxy: "testnet4"
    },

    IndexerL1: {
        Scheme: "http",
        Host: "192.168.10.104:8009"
    },
    IndexerL2: {
        Scheme: "http",
        Host: "192.168.10.104:8019"
    },
    Log: "debug"
};
const prodCfg =  {
    Chain: "testnet",
    Mode: "client",
    Peers: [
        "b@025fb789035bc2f0c74384503401222e53f72eefdebf0886517ff26ac7985f52ad@satstestnet-peer-p1.sat20.org",
        "s@0367f26af23dc40fdad06752c38264fe621b7bbafb1d41ab436b87ded192f1336e@satstestnet-peer-p0.sat20.org"
    ],
    RPC: {
        Scheme: "https",
        Host: "0.0.0.0:9080",
        Proxy: "testnet"
    },
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
const logLevel = 5 //0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace

export default {
  config: prodCfg,
  logLevel: logLevel,
}
