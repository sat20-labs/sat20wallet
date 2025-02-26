const cfg = {
    Chain: "testnet4",
    Mode: "client",
    Peers: [
        "b@03258dd933765d50bc88630c6584726f739129d209bfeb21053c37a3b62e7a4ab1@192.168.10.102:9080",
        "s@033d7411f3023987d2c537e6a14bfc99368f8928627b5c845d5bcdafd14965525b@192.168.10.104:9060"
    ],
    RPC: {
        Scheme: "http",
        Host: "0.0.0.0:9080",
        Proxy: "testnet4"
    },
    Btcd: {
        Host: "192.168.10.102:28332",
        User: "jacky",
        Password: "123456",
        Zmqpubrawblock: "tcp://192.168.10.102:58332",
        Zmqpubrawtx: "tcp://192.168.10.102:58333"
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
    Chain: "testnet4",
    Mode: "client",
    Peers: [
        "b@03258dd933765d50bc88630c6584726f739129d209bfeb21053c37a3b62e7a4ab1@satstestnet-peer-p0.sat20.org",
        "s@037b4acf9a21829b74b194edf09dc8ae0b024c484ba5c9ec8bda098fbe2676c278@satstestnet-peer-p1.sat20.org"
    ],
    RPC: {
        Scheme: "https",
        Host: "0.0.0.0:9080",
        Proxy: "testnet4"
    },
    Btcd: {
        Host: "192.168.10.102:28332",
        User: "jacky",
        Password: "123456",
        Zmqpubrawblock: "tcp://satstestnet-btcd-zb.sat20.org",
        Zmqpubrawtx: "tcp://satstestnet-btcd-zt.sat20.org"
    },
    IndexerL1: {
        Scheme: "https",
        Host: "indexer-testnet.sat20.org"
    },
    IndexerL2: {
        Scheme: "https",
        Host: "indexer-satstestnet.sat20.org"
    },
    Log: "debug"
};
const logLevel = 5 //0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace

export default {
  config: prodCfg,
  logLevel: logLevel,
}
