# SP SDK / PWA E2E 增量验证

日期：2026-10-22。范围为用户批准的六组测试，生产代码问题只记录，不实施修复。

## 测试范围

1. SDK：正常 Manager → 真实 Bitcoin Core 广播确认 → 生产 BaseIndexer/SP HTTP → Manager 扫描、精确余额和幂等刷新；使用两输入和普通找零。
2. SDK：正常初始化、内置 Provider、磁盘数据库、密码认证、关闭重开、子账户和网络隔离。
3. PWA：从首页收款入口访问 SP；独立验证页面启用、二维码、剪贴板及刷新恢复。
4. PWA：通过页面确认发送、重复点击只广播一次、Core 确认及接收方手动刷新。
5. PWA：延迟真实 HTTP 响应时切子账户、锁定；隔离 regtest 拒绝切换到公开网络。
6. PWA：连接中断保留已核实余额、重试、无效地址拒绝及取消不广播。

PWA 共 9 个独立叶子，复用 `account-management-e2e.mjs`。SDK 和 Core 用例复用原有 SP helper。普通 BTC 的测试读取适配只根据同一个生产 Base DB 与真实 Core 交易字节提供数据；SP、广播、签名和钱包业务不被替换。浏览器加载临时编译的当前源码 WASM，保留生产完整性校验和 IndexedDB。

## 执行状态

- SDK 生命周期：PASS，真实命中 1 个叶子。
- SDK Core 收发：首轮在准备 110 个成熟资金区块时超过 helper 的普通 5 秒 RPC 时限；仅为该批量准备请求设置独立 30 秒 HTTP client，普通请求仍使用原 helper。修正后执行中。
- PWA：等待 SDK Core 前置。
- 证据根目录：`/private/tmp/sat20-sp-e2e-validation-20261022`。日志和 fixture 为私密材料，不复制钱包密码、助记词或数据库到本文件。

## 生产问题

待浏览器和 SDK 生产路径断言结果确认后填写。已知首页收款使用 `ReceiveQRCode.vue`，SP 控件位于单独的 `Receive.vue`，本轮保留真实入口断言验证该接线。

## 验证边界

本轮不验证未实现的统一 BTC 余额/普通流水、通用 SP UTXO 选币及后台续扫。隔离 regtest 拒绝公开网络切换的测试，不等于允许主网/测试网切换时的迟到响应测试。没有远端服务重置、共享数据修改、生产代码修改或 Git 提交。
