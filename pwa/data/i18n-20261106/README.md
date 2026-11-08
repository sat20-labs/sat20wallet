# PWA 中英文资源验收记录

生产源码冻结时间：2026-11-06 21:34:55 UTC。冻结后只修改现有测试夹具和验收记录，没有再修改生产源码。未提交代码，原有暂存和未暂存工作均保留。

## 发现与处理

初始扫描覆盖 334 个 PWA Vue/TS 源文件：中英文资源分别为 1207/1206 项，71 处静态调用引用了缺失资源（44 个唯一 key）；558 处模板文本/可见属性候选分布于 59 个文件，其中包含品牌、单位和协议标识。

问题最集中的位置：
- 账户管理与恢复：settings/account-management/Index.vue（128 处模板候选）、RestoreAccount.vue（33 处），以及 AccountManagementSetting.vue。
- 钱包、通道和 RGB：PoolManager.vue（37 处）、ClosedChannelRecovery.vue（25 处）、ChannelCard.vue（24 处）、L1AssetsTabs.vue 及 RGB11 弹窗。
- 设置和授权：NodeSetting、ReferrerSetting、密码页、Agent/DApp/合约授权页；合约授权还存在整组缺失 key。
- 通用流程：安装、创建/导入、收款、DID、挖矿、操作日志，以及通用确认和表单错误组件。

修复沿用现有 vue-i18n 与 locales/en.json、locales/zh.json；没有增加语言框架。补齐资源并替换按钮、说明、风险提示、占位文本、无障碍标签、Toast、校验错误和已知动态状态。插值使用带参数资源。语言切换不改变状态码、金额、地址、账户或原始业务数据。

账户备份/恢复为优先范围：恢复问题提示、Guardian、托管方式、付款确认、恢复演练及已知 SDK 显示消息均有中英文资源。未知 SDK 诊断原文保留。

RGB 为优先范围：资产和发送/接收操作的本地提示、状态枚举和日志界面标签已本地化。协议标识、交易 ID、用户输入和未知原始错误保持不变。

最终资源：中英文各 2285 项，key 集合完全一致；静态缺失 key 为 0，插值参数不一致为 0，消息编译错误为 0。修正了既有 nodeSetting.jionNodeGuide 拼写不一致、splitAsset.balance 缺英文、txDetail.noAssets 缺中文插值等问题。

## 已通过验证

- npm run compile：完整 vue-tsc --noEmit 通过。
- 全量 2285 对资源：键集合、静态调用引用、参数集合及 @intlify/message-compiler 检查通过。
- 10 个现有测试文件合并运行：114/114 通过，0 失败、0 跳过。
  - 账户存储恢复会话、AUTOPAY 到期显示、Service Worker 更新。
  - 钱包导入、RGB 操作日志/当前状态、RGB 预定恢复。
  - 已批准运行时保护、钱包锁边界、密码修改导航、推荐人状态。
- agent-safety-preflight 通过。
- 闭通道恢复实际 Vue 组件内存渲染：原有 F3 45/45 矩阵通过。
- 双语切换：5 个场景各执行 en → zh → en，共 15 个渲染检查点通过。覆盖风险提示、按钮、40000 + 770 = 40770 / 5 sat/vB、提款数量 / 500 sats / parent ID、动态注资和提款提交状态。切换语言时调用次数、报价原始字节、身份和协议上下文保持不变。
- git diff --check 通过。

测试适配仅接入真实中英文资源和 vue-i18n，未放宽业务断言。另补齐 wallet-lock-boundary 原有夹具缺失的真实 wasmError 依赖，不改生产代码。

## 仍需真实浏览器验收

隔离 Playwright 在启动 Chromium 时被 macOS MachPort 注册权限拒绝（Permission denied 1100），因此没有运行页面用例，不能算浏览器通过。临时 5198 Vite 服务已关闭。真实钱包的备份/恢复及 RGB 双语主流程交给现有 PWA 验收会话，在统一更新当前 SDK WASM 与 PWA 后执行；本次没有进行真实资金动作。

原有 SDK 未知诊断、历史日志正文、用户输入、品牌/单位/协议值保留原文。Tools 开发者界面仍有少量 HTTP/unsupported-encoding 等拼接诊断文案，属于后续尾项；当前冻结不再热修改，避免干扰钱包验收。不能据此宣称整个 PWA 所有后端错误均已翻译。

## 冻结和证据

生产指纹清单：../i18n-production-freeze-20261106.json，覆盖 362 个前端源码、资源、构建配置和依赖清单文件；排除 SDK、WASM/generated、构建输出、测试、data 和秘密配置。

树 SHA-256：f237529525bcc51873850d76e51b09f515843cab5ea24a92e2e6228aac1ae23b

- en.json：c2903cba1fa89462b3eb9768843b64035fe09d3e87a7a4dbc224d44bb12ff3df
- zh.json：58c58d7c1b3a0365c626b35e64a9dc70b49a1815670512e937ab375b8a9bc7fa

此目录保留 resources-check.json、regression-114.log、typecheck.log、agent-safety.log、closed-recovery-bilingual.log 及 locale-render-evidence.json。
