# 本机真实 E2E 固定夹具

`wallets.json` 是已在仓库测试代码中公开使用的四个合成测试身份。
ordinary-1 的 account 0 对应 regtest 默认 AUTOPAY deployer；bootstrap/core
对应现有 STP 节点夹具。它不包含人工验收环境导出的秘密材料。

统一入口为 `pwa/scripts/verify/workspace-e2e.mjs`。每个独立用例组复制此
文件到自己的运行目录，并通过真实 SDK、Bitcoin Core 和 L1 indexer
生成钱包、资金、资产、通道、恢复包和 user-share。组间不得复制链库、
钱包库、恢复包或已发行资产的快照。

存在顺序依赖的发送/返回、通道生命周期用例在同组执行；准备步骤和业务
步骤分别记录。只复用二进制、编译缓存和本目录的固定向量。完整日志及结果
摘要保存在本轮目录，不纳入版本控制。独立用例或依赖组通过后立即停止自有
进程，把运行目录内的节点、STP 和浏览器日志归档到该组的 `logs/retained/`，
再删除链、钱包及资产等运行数据（含数据库 WAL）；异常时保留现场。
