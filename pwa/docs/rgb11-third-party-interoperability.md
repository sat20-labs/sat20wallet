# RGB11 第三方钱包互操作验收

## 固定测试基线

- 钱包：Iris Wallet Android
- 版本：`0.3.1`，version code `73`
- 官方仓库：`RGB-Tools/iris-wallet-android`
- 源码 commit：`5e4c1f9e8a5484b6072c381b3c281aa6aff167ab`
- 构建 variant：`bitcoinTestnet4Debug`
- Android package：`com.iriswallet.testnet4.debug`
- RGB 依赖：`org.rgbtools:rgb-lib-android:0.3.0-beta.4`
- Bitcoin 网络：Testnet4
- Electrum：`ssl://electrum.iriswallet.com:50053`

该版本的 NIA、CFA、UDA schema ID 与 SAT20 PWA 使用的
`rgb-lib 0.3.0-beta.7` / RGB `0.11.1-rc.11` 一致。IFA 不在当前
RGB11/NIA 验收范围内。

不要使用 RGB-WG `rgb-wallet 0.11.0-beta.7` 替代 Iris。该版本属于
更早的 RGB-WG 技术栈，不是 PWA 当前采用的 RGB v0.11.1 互操作基线。

## 构建说明

本地构建可以做以下两项纯构建适配，不修改钱包或 RGB 业务逻辑：

1. Java/Kotlin toolchain 从 18 调整为本机可用的 21。
2. 按 Iris README 提供空的 faucet key 实现。互操作测试不依赖 Iris 水龙头。

2026-07-29 本地验证产物：

- APK：`app/build/outputs/apk/bitcoinTestnet4/debug/app-bitcoinTestnet4-debug.apk`
- package：`com.iriswallet.testnet4.debug`
- version：`0.3.1-testnet4-debug` / `73`
- SHA-256：`40700866e0c8a49797d3c04b373fd46ac1c013e025454118bd6c534db0c1f484`

Debug APK 的签名和构建产物不要求字节级可复现。每次测试必须重新核对源码
commit、variant、package、版本及 RGB 依赖。

## 验收范围

第三方互操作必须使用标准 RGB 流程，不使用 SAT20 DKVS 传递第三方数据：

1. PWA 发行 NIA/RGB11 资产。
2. Iris 导入 PWA 发行的资产合约。
3. Iris 创建 invoice，PWA 生成并发送 consignment。
4. Iris 验证并接受 consignment，完成 acknowledgment。
5. PWA 创建 invoice，Iris 向 PWA 反向转账。
6. PWA 验证并接受 Iris 的 consignment。
7. Iris 发行 NIA 资产，PWA 导入该资产合约并重复双向转账。

每个方向记录 contract ID、invoice、consignment、anchor transaction、
接收状态和最终余额。Bitcoin Testnet4 确认较慢时，每 10 分钟检查一次。

## 标准文件要求

- 合约文件使用 RGB 标准二进制封装，文件头为 `RGB\0CON`，扩展名 `.rgb`。
- 转移 consignment 使用 RGB 标准二进制封装，文件头为 `RGB\0TFR`，
  扩展名 `.rgbc`。
- 文件导入接口只接受上述标准封装，不接受裸 strict payload、ASCII armor
  或 SAT20 自定义 JSON 包装。
- 标准 RGB JSON-RPC proxy 上传和下载的 consignment 同样使用 `.rgbc`
  二进制内容；SAT20 DKVS 仅用于 SAT20 钱包之间的管理路径。

## 2026-07-30 验收记录

### 标准合约文件

- PWA 发行测试资产：`FS7BJLFI`
- contract ID：
  `rgb:Xd6kZPJC-2mD_W62-WjMF7rx-vny0k4u-yv_XyLX-85Zxz~A`
- 导出文件头：`RGB\0CON`
- 独立测试钱包通过严格文件接口导入成功，导入后的 contract ID 一致。
- 接收钱包 `projected=0`，没有把发行钱包的 allocation 错记为本地余额。

### PWA 到 Iris

- 资产：`IS7AX94S`
- contract ID：
  `rgb:bJg~o5Xd-~~3CitN-mpIl2JD-Jn8tnW9-LBYwUXk-4I8mQA8`
- transfer ID：
  `rgb:csg:gg9~Q99W-LhjvN8f-xWwnMgy-hJ5BfJw-xK__Dta-qdYaetM#critic-egypt-shrink`
- anchor transaction：
  `877f400b26807eb3c10801524836e36580b090c732cd0cce7c97e5be0d4dec0c`
- 数量：`10`
- PWA 上传标准 `RGB\0TFR` 文件；Iris 已识别同一 contract ID，显示余额
  `10`，并返回 accepted acknowledgment。
- Iris 实际保存的 `rcv_compose.rgbc` 为 `5341` 字节，前 7 字节是
  `52 47 42 00 54 46 52`，即 `RGB\0TFR`。
- 当前状态：交易仍在 Testnet4 mempool，Iris 显示
  `WAITING_CONFIRMATIONS`，确认前没有可花余额。

### Iris 到 PWA

等待上述 anchor transaction 确认后，由 Iris 使用可花余额向 PWA 标准
invoice 转回 `2`。确认前 Iris 会拒绝发送，提示没有 spendable balance。

`f58932783c82eccadfcca27178a37dbcc5b81bc3c71502bd9df8e731b536fbd7`
是 SAT20 PWA 测试钱包之间的标准 proxy 转移，不计入 Iris 第三方互操作结果。

## 2026-08-03 回滚 3422 后验收

测试网回滚到 `3422` 后，重新部署并激活 AUTOPAY，再清理本地 PWA 钱包数据并
重新导入两个测试钱包。账户管理、DKVS 同步和 RGB11 两条 PWA 内部转账路径
均通过。

### SAT20 钱包路径

- 发行 `RSCNUFZB`，规范资产名：`rgb11:f:rscnufzb@cnetsdrz`。
- 第二个 PWA 钱包通过 SAT20 relay 接收 `10`，广播交易：
  `4131de3769d2a67cce3e7e93861a68cb47fbd0d19c48278c69a356f63d01495a`。
- 接收方资产保持 pending，未确认前不能再次花费；余额和 carrier UTXO 锁定行为符合预期。

### 标准 RGB out-of-band 路径

- 发行 `RSCNWKMY`，规范资产名：`rgb11:f:rscnwkmy@dyrpdlqu`。
- 使用标准 RGB invoice、consignment 和 ack 完成 PWA 到 PWA 转账，广播交易：
  `66e6f272c9e1f7ee1e0877a50f5003ef40ded90ff7239352b05af78c8b292051`。
- 接收方保持 pending，未确认前不能花费；两笔交易在本次记录时均未确认。

### Iris 第三方钱包

本次回滚后的测试尚未重新执行：当前测试机没有连接 Android 设备或模拟器，
也没有固定基线的 Iris APK。2026-07-30 的 PWA 到 Iris 记录仍保留，但属于
回滚前的历史结果，不计入本轮验收。后续仍使用本文固定的 Iris `0.3.1` / version
code `73` / `bitcoinTestnet4Debug` 基线完成双向互操作。
