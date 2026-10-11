# 真实 Bitcoin L1 / SP E2E 联调方案

日期：2026-10-11；本方案沿用 sat20wallet/sdk/e2e 的现有测试入口，不另建一套虚拟网络。

## 现有环境调查

- sdk/e2e/harness_test.go 启动真正的 SatoshiNet bootstrap、core、miner 三个进程，实际使用 RPC、P2P 和 STP 插件；这三者的 L1 Indexer 地址指向 fakeL1Indexer。
- sdk/e2e/pos_pwa_l1_test.go 会真实地验证交易签名与资产变迁，但由内存夹具模拟 Bitcoin block、mempool、UTXO。
- sat20-labs/indexer 主分支已有 BaseIndexer 内建 SP Archive/Live 与生产 Gin RPC；indexer/base/silentpay_e2e_test.go 虽使用生产 Base/DB/HTTP，但 Bitcoin RPC 数据来源是模拟小链。
- 完整 indexer.NewIndexerMgr 的网络参数目前只明确支持 mainnet、testnet、testnet4。将完整 Indexer 直接配置成 regtest 会在其默认分支错误地落到 MainNetParams，所以不能仅改一份 YAML 就进行独立 Indexer 主进程的 regtest 验收。

## 第一阶段：真实 Bitcoin Core + 生产 L1 BaseIndexer + SDK

新增 sdk/e2e/silentpay_bitcoind_e2e_test.go，全部为普通 Go 测试，不加特殊 build tag。

1. 测试启动独立 bitcoind regtest 进程，使用 t.TempDir、随机空闲 localhost 端口、txindex=1 与全新 Bitcoin Core Wallet。先挖满成熟区块。
2. Core 真正通过 sendtoaddress 给 SDK 持有私钥的 P2WPKH 地址转 BTC；SDK 从最终选中的 UTXO 派生 BIP352 收款脚本，签名并 sendrawtransaction，之后 Core 真正挖矿确认。
3. 测试通过生产 bitcoin_rpc.InitBitconRpc、BaseIndexer.NewBaseIndexer(...RegressionNetParams)、SyncToChainTip、UpdateDB，构建实际 L1 Base 和 SP 数据库；不是把想要的 SP 记录直接写进 KVDB。
4. SDK 从生产 rpcserver/base.Service 和 rpcserver/bitcoind.Service 的 HTTP 接口读取公共 T、Taproot 输出、固定 Live 快照、真实 txid 与 rawtx；使用本地 Scan/Spend Key 找出属于账户的输出并派生花费私钥。
5. 用此私钥构造真实 Taproot 花费交易，再由 Bitcoin Core 的 sendrawtransaction 验证、挖矿。BaseIndexer 同步下一块后确保已全部花费的 Live 记录消失，并运行真实 CheckSelf。

执行入口：

    SAT20_E2E_REQUIRE_BITCOIND=1 go test ./e2e -run '^TestSPBitcoinCoreRegtestIndexerE2E$' -v -count=1 -timeout=15m

默认 go test ./e2e 会包含测试；本地缺少 bitcoind 时明确 Skip，CI 则以环境变量强制必须有真实 Core，不能静默跳过。CI 下载发行版 Bitcoin Core 29.2 Linux x86_64 包并核对官方 SHA256SUMS。没有主网接入或主网广播；所有数据只存在测试临时目录。全局 Bitcoin RPC singleton 在测试结束后恢复，此用例不并行。

当前构建使用的是生产 BaseIndexer 在 E2E 测试进程内运行，**不是完整 IndexerMgr 的独立服务进程**。这避免为了一个 SP 用例改动所有协议的 regtest 高度/参数处理。

## 第二阶段：纳入现有聪网虚拟节点和 PWA

SP 是 L1 BTC 功能，不需要聪网区块为其背书。现有聪网 E2E 中许多自定义 STP/资产初始化依赖 fakeL1Indexer，直接全部替换会让这些尚未真正部署到 Bitcoin regtest 的模拟资产失效。

建议第二阶段优先在 SDK 或测试专用 HTTP router 处拆清流量：真实 BTC/SP、原始交易、广播和 UTXO 查询统一使用 Bitcoin Core + BaseIndexer；仅保留原有 STP 资产初始化所需的模拟接口。不能让 mock 生成 SP Archive 或“已收到 BTC”的结果。这样可以让 PWA 的 WASM 执行真实 SP 收发验证，同时保留三节点聪网测试。

之后如需将完整 IndexerMgr 以独立进程运行在 regtest，先单独评估、批准添加 regtest 参数及其多资产协议高度；此项不在当前测试 PR 擅自修改。

## 尚未完成的 SP 上线验收

PR #11 仍是 Draft：已匹配 SP UTXO 接入通用 BTC 余额、选币、实际生产花费；匹配元数据本地加密；Archive/增量恢复；PWA Browser 完整验收尚未全部解决。即使真实 Core+Base E2E 通过，也不能替代这些功能。

## 实际执行结果（2026-10-11）

真实 Bitcoin Core + BaseIndexer + SDK 回路 **PASS**，并在修复 SDK Go module graph 后重复复验通过：

- 证据 1：[GitHub Actions run 38146136749](https://github.com/sat20-labs/sat20wallet/actions/runs/38146136749)，sp-bitcoin-core job 成功。日志包含 Bitcoin Core 29.2 SHA256 校验 OK、BaseIndexer.UpdateDB、BaseIndexer.CheckSelf 和 PASS: TestSPBitcoinCoreRegtestIndexerE2E (1.02s)。
- 证据 2：[GitHub Actions run 38146721490](https://github.com/sat20-labs/sat20wallet/actions/runs/38146721490)，修复 SP 源码 gofmt 后，该 job 再次成功，现有 RGB11 asset naming 格式检查也已通过该步骤。
- 比特币 regtest 测试数据与临时 DB 均在 GitHub Runner 上生成，结束后清理，不依赖本地 Mac 或主网服务。

为了确保 Pull Request 新增 workflow 尚未在主分支激活时能够获得实际证据，曾临时在 rgb11-asset-naming.yml 添加仅本 PR 运行的 SP job；验证完成后已删除这一临时 job，恢复原 workflow。长期 CI 入口为新建的 .github/workflows/silentpay.yml（包含真实 regtest 命令）。

本次实际通过的是 **真实 Core RPC + 原生 BaseIndexer + 真实 SP REST handler + SDK 的单项 E2E**；没有启动完整 IndexerMgr 守护进程、SatoshiNet 三节点与浏览器 PWA 的统一编排。不要用单项 PASS 代替第二阶段验收。
