# PWA 账户备份、恢复与 RGB 验收记录

更新时间：2026-11-06 UTC；纳入 21:39:06 后正确 STP 预检通过结果。工作目录：/Users/yingfeng/github/sat20wallet。
范围：账户管理备份/恢复与 RGB 资产管理。复用现有真实 regtest、Wallet1 和已完成交易；不重建链，不重发旧交易，不复制源码/数据库，不修改生产 SDK、用户 MCP 或浏览器安全设置。

## 1. 当前证据与冻结边界

- Git HEAD：fde00ec629f689839264bb328d0a08574d622ed0。工作树有大量既有已暂存/未暂存修改，不能用 HEAD 单独代表待构建输入，也不能 reset/checkout 清理。
- PWA 包版本：0.1.46；当前 public/version.json 仍是 buildId 20261106T002023Z。
- 21:09:47Z 磁盘 WASM：39,305,847 字节，SHA256 7df04d1925feffaaca043d135d9505ccc6a0b92df5faa98a7616e509f1f3644a。public/integrity-manifest.json 与 generated/wasm-integrity.ts 匹配它，但不能据此证明旧 UI 测试使用该二进制。
- 已安装 Go：1.24.13，与 sdk/go.mod 的 toolchain 相同。PWA wasm_exec.js 与 /usr/local/go/lib/wasm/wasm_exec.js 相同，SHA256 0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14。
- 已知 PWA 构建状态目录仅记录 00:20–00:21 的旧 regtest build，不能证明 21:09 WASM 的构建来源。该产物的精确构建输入来源仍待核。新构建只需当前 SDK 输入冻结，不必等待旧产物来源调查全部结束。
- 翻译/生产 PWA 输入于 21:34:55Z 冻结，清单 pwa/data/i18n-production-freeze-20261106.json，362 文件，treeSHA f237529525bcc51873850d76e51b09f515843cab5ea24a92e2e6228aac1ae23b。清单不包括 SDK、WASM、generated、输出、测试与 data。
- en.json SHA256 c2903cba1fa89462b3eb9768843b64035fe09d3e87a7a4dbc224d44bb12ff3df；zh.json SHA256 58c58d7c1b3a0365c626b35e64a9dc70b49a1815670512e937ab375b8a9bc7fa。
- 翻译方报告中英 2285/2285 key/插值一致、vue-tsc 通过、114 测试通过。这是静态/测试结果，不替代新版真实钱包界面验收。

## 2. 旧运行时验收基线

这些结果保留为旧运行时证据；新版必须另挂 buildId/WASM SHA，不把旧结果直接改标“新版通过”。

- Wallet1 Account1/index0 地址：
  bcrt1pwnqzyqxse06lr2vjep53wgdzcv5e7f6mkt54s46qmrmnm8uptw2s0yjywd
- Account2/index1 地址：
  bcrt1pp59vtp0facqyy5sfupqf8dpgkv3hv866988mc84sjmvdrgk57d8s8yjs75
- NIA RGBTST1 初始供应 1000；contract：
  rgb:02apjS5e-iXRV3tA-O0nSKeh-le93y3w-OtmBzFC-KRShvxo
- witness/OOB 已结算：tx b22145864d8bccc8b1aad1e57876ae07ed641206d277843eac0e4a98f03ce516，原块1110：
  712ccacd6248562d936c0a6e0f2fbea46be7ae0f4aca41e5baee644d1a2cecf7。
  异常结束后的同块恢复已完成，restore-original-block-1110.json 为 CONFIRMED，禁止再次运行恢复提交。
- blind 的 10,000 sats 普通 BTC 准备交易：
  f6825b2dabdbd74fd56c499eae1ab341cff81b725f2a826aa9620fdb24df3b7d，确认于1111，
  块 0a2e78167257de3b125a0ae0e592f6fce8fe4766f7cf2f89634860160b9a4a21。
- blind/OOB transfer：
  rgb:csg:Ys_5Op~s-pMz5kOr-2oP4kqV-NGLwygV-u4LB9lP-RuLFvXM#turbo-torso-bruce
- blind tx：
  51e8196cd15f408c51355ef52b2e7b8ae726557901c11a10e8a22af16dbd5900，fee154 sats，确认于1112，
  块 1d553de8222bb634962f2f6f37fbce222c7d7f0569ac433396e2acea4cdf58f5。
- 最后 UI 执行报告：A1=998、A2=2、合计1000，两端 settled/accepted-out-of-band，consistency ok；旧 witness 仍 settled。链确认有直接命令输出，UI 结果按原执行记录保留，不冒充此文重新操作页面所得。
- OOB 摘要匹配/人工接收确认不代表标准签名 ACK 或独立 Root 到 Root。
- RootA 原状态 Local to this device，账户激活、真实备份及冷恢复尚未完成验收。

## 3. L2 CORS 与当前环境

- 本次根目录：/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz。
- L1：Core53168，Indexer53169，Esplora53170；PWA5189。
- L2：bootstrap P2P/RPC/Indexer/STP/management = 30350/30351/30352/30353/30354；
  core = 30360/30361/30362/30363/30364。
- 30352/30362 是 Indexer，原本 OPTIONS200、Allow-Origin=*，不能用它们判定 STP 白名单是否生效。
- STP 正确预检：OPTIONS http://127.0.0.1:{30353或30363}/regtest/action/notify，
  Origin 分别为 http://127.0.0.1:5189 与 http://localhost:5189，
  Access-Control-Request-Method: POST，
  Access-Control-Request-Headers: content-type。
  四项应为204，Allow-Origin 精确等于该次 Origin，不是*。
- 用户已批准仅本次两节点追加 localhost:5189。配置已冻结：
  bootstrap/conf.yaml SHA256 4a41b99b0a49c178a5968cd08b3bb202993511b2c19435289eadf861b423dd59；
  core/conf.yaml SHA256 fa7647627675d530fbc35cbf750a12f1314226468c397850254bc8924d417ae3。
- 执行方报告新 run resume-l2-DrMOQw 于21:39:06Z READY，controller39366/bootstrap39372/core39375，保留高度4原hash及DKVS身份。后续原执行会话已完成正确STP端口四项预检：全部204且精确回显对应origin，未再改配置或重启。RootB的节点CORS前置已通过；不等同于RootB身份已建立。
- CORS及临时配置不属于生产源码修改；后续新版构建不需要再次重启L1/L2。

## 4. 最小构建与加载顺序

仅在 SDK 和 PWA 两方输入冻结、确认没有其他构建/写者后，由原执行会话统一进行。

1. 记录前置公共快照：当前链tip、旧交易确认、Wallet1身份、两账户余额、待协调/未确认操作。正常锁定相关页面；不要在未决广播或恢复commit状态切版本。
2. 正常停止仅本次5189服务，保留浏览器存储和全部链/节点数据。不要用旧PID盲杀。保持L1/L2。
3. 输入清单至少包括：
   - sdk/wasm、sdk/account、sdk/wallet、其余实际编译依赖的源码/嵌入文件；
   - sdk/go.mod/go.sum及本地replace所指向的实际依赖：satoshinet、indexer、xaplib/chainindex；
   - 上述362个PWA文件及中英资源；
   - Go版本、构建参数、GOFLAGS/GOWORK的相关重定向情况。不要输出完整环境变量。
   对本地依赖遵循 SDK 方冻结范围，不能以仅冻结两个入口文件代替完整编译输入。
4. 复用标准Makefile的Go编译参数，但不要直接 make all：该目标还会覆盖 client/public/wasm，超出本次PWA更新范围。
   在 /Users/yingfeng/github/sat20wallet/sdk/wasm 执行：
   GOOS=js GOARCH=wasm GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=readonly -ldflags='-s -w' -o <本轮临时输出>/sat20wallet.wasm .
   临时输出可放本轮/private/tmp目录中新建唯一build目录，不复制源码/DB。缺缓存依赖或冻结变动就停报，不悄悄改go.mod。
   可用本地 Node 的 WebAssembly.validate 检验二进制格式，不运行Go/WASM钱包。记录长度/SHA。
   本次最小方案不依赖可选wasm-opt；若SDK方要求沿用优化器，另记录其版本和最终hash，不自动安装。
5. 只将已核候选原子发布到 pwa/public/wasm/sat20wallet.wasm。当前Go/runtime已匹配，wasm_exec.js无需改；若实际工具链变更必须同步匹配运行时并重新生成清单。
6. 在 pwa 目录指定唯一 SAT20_PWA_BUILD_ID 与本轮 SAT20_PWA_BUILD_STATUS_DIR，运行 npm run build:regtest。
   现成脚本顺序：write-version → write-integrity → vue-tsc --noEmit → vite build --mode regtest。
   write-integrity同时更新 public/integrity-manifest.json 和 generated/wasm-integrity.ts。不要手改生成清单。
   可追加 npm run verify:rgb11-l1 作为现有静态导出/源码门禁。不得把它当真实资产测试。
   不运行 verify:release-gate、全仓测试或会争用现有链的e2e harness。
7. 核输入前后hash不变、编译exit0、构建状态PASSED。核 version releaseId、public清单、generated清单和实际WASM/runtime大小/hash一致；dist-regtest清单也应自洽。
8. dist-regtest只用于此次编译门禁，不直接替换现有验收服务：它没有pwa-server.mjs注入的本次L1/L2端口transform。
   用原 pwa-server.mjs，从 cwd=/Users/yingfeng/github/sat20wallet/pwa 正常启动5189。
   必须重启服务：__SAT20_BUILD_ID__在Vite创建时读取；旧server配新integrity可能release mismatch，HMR不证明WASM已切换。
9. 先核HTTP版本/清单/实际WASM字节与冻结hash，及transform仍指向53169、30353/30363、30362。
   再在原IAB完整reload。以新release请求、完整性门禁通过、页面新版本证据为准，不读隐藏钱包状态或声称已有内存runtime自动更新。
10. 新版unlock会启动既有同步/链监控，账户激活后还可能自动备份/恢复待办。先核身份/余额/任务与快照一致；不为“刷新”重按Prepare、Broadcast或恢复Commit。意外错误先按对应已存在的任务/receipt核结果。


### 4.1 最短构建命令参考

以下命令仅在上面的当前输入冻结、钱包正常锁定与本次 5189 已停止条件满足后执行。这里是操作说明，不表示已经运行构建。

```sh
set -e
repo=/Users/yingfeng/github/sat20wallet
fixture=/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz
out=$(mktemp -d "$fixture/build-pwa-XXXXXX")
chmod 700 "$out"
cd "$repo/sdk/wasm"
GOOS=js GOARCH=wasm GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off go build -mod=readonly -ldflags='-s -w' -o "$out/sat20wallet.wasm" .
node -e 'const fs=require("fs"),crypto=require("crypto");const b=fs.readFileSync(process.argv[1]);if(!WebAssembly.validate(b))process.exit(1);console.log(JSON.stringify({bytes:b.length,sha256:crypto.createHash("sha256").update(b).digest("hex")}))' "$out/sat20wallet.wasm"
```

先比对构建输入前后清单不变。将候选文件原子替换到 `pwa/public/wasm/sat20wallet.wasm` 时使用目标同目录的唯一临时文件；保留目标权限，成功写完后再 rename，不能直接边写边向运行中的页面提供。不得附带覆盖 client 产物。

```sh
cd /Users/yingfeng/github/sat20wallet/pwa
SAT20_PWA_BUILD_ID="$(date -u +%Y%m%dT%H%M%SZ)-pwa" SAT20_PWA_BUILD_STATUS_DIR="$out/build-status" npm run build:regtest
npm run verify:rgb11-l1
```

随后核实际字节、public 和 generated 清单、version 的 releaseId 一致。只从 PWA cwd 启动本次服务：

```sh
cd /Users/yingfeng/github/sat20wallet/pwa
node /private/tmp/sat20-pwa-rgb-20261106-qdcd3whz/pwa-server.mjs
```

这是持续服务；正常启动后不等待它退出才继续 UI 验收。若出现端口占用、输入漂移或部分发布失败，保留现状并报告，不自动启第二服务或还原其它工作。

## 5. 已测/未测与新版验收矩阵

每个新版本结果填写下方记录表；状态只使用“未执行 / 进行中 / 通过 / 失败 / 阻塞 / 不在本轮范围”。旧结果只保留在旧版本栏。恢复因素、密码、份额和原始备份内容不得写入此文。

| 项目 | 旧运行时证据 | 新版最小验收及判定 |
|---|---|---|
| 版本切换、现存钱包迁移 | 原Wallet1、两账户及两笔RGB转移通过 | 新buildId/hash；相同账户地址、contract及998/2；旧tx仍确认；不能以重新发行代替迁移 |
| 账户激活、首备份 | 未验，原Root为Local to this device | 临时存储+2of2；核account_id/root_fingerprint、recovery_configured；目录与两个rgb11 scope完成backed_up，无dirty/error/conflict；pending=0或恢复码已生成不等于完成 |
| 激活中断/续办 | 未验 | 在明确未提交阶段Cancel/关闭/刷新，验证授权或activation_pending可续办或安全失效；不重复package/目录写入；不靠杀节点制造故障 |
| 独立只读恢复演练 | 未验 | 现成 /restore-account?mode=rehearsal；不足知识因素、错误答案/份额拒绝；正确预览account_id一致且不能Commit；取消不改身份/资产 |
| 真正冷恢复 | 未验 | 空白origin中locator→知识因素→用户份额→最新目录预览→新设备密码→一次Commit→unlock；比对稳定account_id/fingerprint，不比较root_wallet_id |
| RGB恢复材料覆盖 | 未验 | 同contract、A1/A2持有量、已导入零余额contract、RGB proof/validation/receive seal和必要活跃任务闭包；seed+链UTXO不足证明RGB恢复 |
| 恢复后可花费 | 未验 | 先核资产/锁定/待办一致，再安排一笔单独编号的最小新转移证明签名及资产控制；绝不重发旧settled交易 |
| 发行/导入导出 | NIA RGBTST1供应1000及同contract导入导出通过 | 新版单独复验；如需发行新case用明确新contract，不覆盖旧资产证据；NIA初始分配不是IFA增发通过 |
| witness/OOB | 旧链1110和两端结算通过 | 先迁移复核；若标新版“实际转移通过”，须另有一笔新case证据，不能只引用旧receipt |
| blind/OOB | 旧链1112、fee154、998/2通过 | 同上；已有RGB witness输出不能当新的plain seal。新准备先核当前UTXO，不重复10,000 sats旧准备 |
| 独立Root地址转移 | 子账户接收不支持的负例已证；独立Root正向未验 | 不同存储+不同fingerprint；合法Root绑定；原A1与RootB均真实在线。Root自转/恢复A都不能替代 |
| 中英文完整流程 | 翻译静态检查及编译通过 | 账户激活/恢复预览/等待/错误/终态，RGB Issue/Import/Receive/Send/ACK现场切换；无raw key、误译、输入或身份丢失 |
| IFA/UDA | 未验 | 当前UI提供NIA/IFA/UDA，不提供CFA；IFA增发、UDA单独列范围，不能由NIA通过概括 |
| 2of3、付费、TTL/故障注入 | 未验 | 需要独立条件与范围，不由临时2of2通过推断；本次最小优先路径不引入第三方guardian/付款 |

关键语义：
- 激活向导最后的 rehearse 调用 ActivateAccountManagement，是实际激活；与 mode=rehearsal 的只读恢复预览不同。
- 恢复提交成功后UI若刷新失败，只重新加载已提交结果，不再Commit。RestoreAccount.vue已有独立提交后状态。
- SDK备份含账户目录、RGB proofs/objects/必要pending、receive/reservation闭包；部分终态ACK、轮询游标、本地profile/outbox按设计不原样备份。不能把所有历史记录逐字段相同作为未经产品支持的恢复标准。
- 所有恢复因素/密码/份额只在已授权测试UI流程内处理，不进入聊天、公共截图、报告或完整日志。


### 5.1 新版本可填写证据表

构建记录：

| 字段 | 实际值 |
|---|---|
| SDK 冻结时刻、输入清单路径及 SHA256 | 协调方报告五仓/2901文件/HEAD稳定；本轮Go构建输入 `/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz/build-pwa-5zmsf09x/sdk-inputs-before.json` SHA256 `1d8c6d0711502844076b275de7c66c13f5b80ad6300bcb085d01db1c21bff4a8`，327 packages / 328 modules / 2764 files；before/after去除phase和自校验字段后的fingerprint均为 `a564f2213980c244f12b8cd378ff78ef06d4deb0e89d6104418dff3677cae64c` |
| PWA 冻结清单与 treeSHA 复核 | `pwa/data/i18n-production-freeze-20261106.json` SHA256 `c4f69f6164f997fad4b333c5a2aae1bf9d2e3ff8f67ec5f4a07408564b0fa3aa`；362 files，treeSHA256 `f237529525bcc51873850d76e51b09f515843cab5ea24a92e2e6228aac1ae23b`，已核362/362匹配 |
| 新 buildId / releaseId | `20261106T225010Z-pwa` / `0.1.46-20261106T225010Z-pwa` |
| WASM 字节数 / SHA256 / Go 版本 | 39,305,847 / `830a890ae161aab5912d2a74e4fc0d2aaf89e57fb14584acf0361315ad76c6c8` / Go 1.24.13 darwin/amd64，GOOS=js GOARCH=wasm |
| wasm_exec.js SHA256 | `0c949f4996f9a89698e4b5c586de32249c3b69b7baadb64d220073cc04acba14` |
| 构建日志路径 / exit code / PASSED 时刻 | `/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz/build-pwa-5zmsf09x/build-status/regtest-2026-11-06T22-50-10-524Z.log`；status `PASSED`，exit 0，2026-11-06T22:50:50.076Z |
| 5189 服务 cwd / 启动时刻 | 旧实例记录：cwd `/Users/yingfeng/github/sat20wallet/pwa`，PID 46004，exec session 39775，精确UTC启动时刻未记录；本轮同 cwd 新启动，exec session 16825，命令返回 `PWA READY http://127.0.0.1:5189`，服务PID/精确UTC启动时刻未记录 |
| 浏览器实际加载 releaseId / WASM 请求证据 | 同源Wallet1旧页面曾加载 `0.1.46-20261106T225010Z-pwa`；本轮服务HTTP `/version.json` 200返回同buildId/commit，`/integrity-manifest.json` 200，实际GET `/wasm/sat20wallet.wasm` 为39,305,847字节/SHA `830a890ae161aab5912d2a74e4fc0d2aaf89e57fb14584acf0361315ad76c6c8`。本轮未重开钱包页、未取得浏览器网络面板的独立WASM请求记录；HTTP产物匹配不能替代浏览器请求证据 |
| 复验起始链高 / hash / 待办数 | Core regtest 1112 / `1d553de8222bb634962f2f6f37fbce222c7d7f0569ac433396e2acea4cdf58f5`。Wallet1 Transfer monitor显示2条且均Settled；未读取SDK私有outbox/pending计数 |

| Case | 范围 | 新版本状态 | 公开身份、tx/transfer/确认高度等结果 | 证据路径/截图与 UTC 时间 | 问题及下一步 |
|---|---|---|---|---|---|
| P01 | 新版加载与现存 Wallet1 迁移 | 进行中 | Wallet1原数据解锁成功；RGB 0.11.1，Consistency: ok；contract `rgb:02apjS5e-iXRV3tA-O0nSKeh-le93y3w-OtmBzFC-KRShvxo`；先前UI快照A1=998/Available=998。旧witness与blind交易在本轮重启后重新核为活动链，分别3/1确认（见第8节）；A2余额、account_id/fingerprint仍未复核 | build status/latest.json、public/version.json；Core/Indexer/Esplora恢复receipt与只读状态；IAB旧页面记录 | 仍缺稳定账户身份/A2读数及浏览器独立WASM请求证据；服务/链恢复不等于P01通过 |
| P02 | 原 Root 账户激活、首次完整备份 | 进行中 | 最近一次可读取快照曾为Local to this device、管理未激活、恢复未配置、无备份；已选2of2及Temporary cache并到第3步恢复问题表单。临时缓存保留7200块、估计到7204，不代表长期备份。本轮按用户要求未读或改变表单，当前是否完成备份未知 | 旧IAB账户管理步骤3快照；本轮未查看页面 | 用户需亲自完成恢复因素的输入、确认和备份提交；不在聊天中传送答案或份额。完成后再只读核验备份状态 |
| P03 | 激活中断、取消、恢复续办 | 未执行 | 待填 | 待填 | 待填 |
| P04 | 只读恢复演练、错误因素拒绝 | 未执行 | 待填 | 待填 | 待填 |
| P05 | 空白存储冷恢复、一次 Commit | 未执行 | 待填 | 待填 | 待填 |
| P06 | RGB 材料与资产恢复闭环 | 未执行 | 待填 | 待填 | 待填 |
| P07 | 恢复后资产可花费 | 未执行 | 待填 | 待填 | 待填 |
| P08 | NIA 发行及同合约导入/导出 | 未执行 | 待填 | 待填 | 待填 |
| P09 | witness/OOB 新版复验 | 未执行 | 待填 | 待填 | 待填 |
| P10 | blind/OOB 新版复验 | 未执行 | 待填 | 待填 | 待填 |
| P11 | 独立 Root 地址转移 | 未执行 | 待填 | 待填 | 待填 |
| P12 | 中英文关键流程、等待和错误态 | 未执行 | 待填 | 待填 | 待填 |
| E01 | IFA 增发 / UDA | 未执行，待排期 | 待填 | 待填 | 不由 NIA 结果替代 |
| E02 | 2of3 / 付费 / TTL / 故障注入 | 不在本轮最小范围 | 无 | 无 | 不由临时 2of2 结果替代 |

同一新交易可同时覆盖 P07 与一种转移模式，但需分别说明证明了什么，不重复发送来凑条目。P08 若只做旧资产迁移核对，应明确“旧发行资产迁移通过，新版发行未执行”。P09/P10 同理区分旧交易状态复核与新版新交易。

## 6. 独立origin的安排

http://localhost:5189 与 http://127.0.0.1:5189 标准origin隔离，SDK和PWA均使用该origin的IndexedDB/localStorage，Web Locks及BroadcastChannel也分离。现有5189已HEAD200，Vite允许localhost；实际IAB的安全上下文/锁能力仍需正常页面启动验证，失败不得禁用安全机制。

用户新重点优先备份/恢复：
- 若把localhost用于RootA冷恢复，成功条件是与RootA相同fingerprint。
- 若把localhost用于新RootB，成功条件是不同fingerprint。
- 一个profile不能同时满足两者。不得把同Root恢复称为双Root，不得无说明清掉已恢复数据换身份。
- 建议先完成新版原Root备份与只读演练，再明确选择localhost冷恢复或RootB；另一项需要真正空白存储的后续安排。不得因此阻塞已获准的构建。
- RootB仅按合法UI新建合成测试身份并使用临时存储+2of2。收件映射本身无需先充BTC；发送方已有plain资金承担费用。后续每个新交易独立记录。

## 7. 主要源码依据

- sdk/wasm/Makefile：WASM编译与额外client复制副作用。
- pwa/scripts/build-with-status.cjs、write-version.cjs、write-integrity-manifest.cjs：版本/构建/双清单生成。
- pwa/utils/wasm.ts：releaseId、MIME、大小、SHA-256校验；runtime/load任务缓存。
- pwa/vite.config.ts：启动时注入version/buildId；regtest配置。
- pwa/entrypoints/popup/pages/RestoreAccount.vue：恢复预览、只读演练、单次提交和提交后刷新。
- pwa/entrypoints/popup/pages/wallet/settings/account-management/Index.vue：临时/付费存储、激活、重试与演练入口。
- sdk/wasm/account_management.go：激活rehearsal、最新目录恢复、Commit。
- sdk/wallet/account_backup_wait.go：目录/模块confirmed计数和恢复/冲突门禁。
- sdk/docs/account-rgb-backup-scope-20261030.md及实际provider/recovery源码：恢复闭包边界；文档不是现场通过证据。
- transcend/rpc/router.go：STP允许origin、204预检及启动时加载；indexer/rpcserver/router.go：独立Indexer通配CORS。

## 8. 2026-11-07 原链区块耐久恢复与服务重启

本节记录恢复既有测试链数据，不是新版钱包交易验收。未创建交易、未挖新块、未重放旧交易，也未触碰钱包恢复表单。

### 8.1 原区块提交、逐块flush与receipt

恢复工具 `/Users/yingfeng/github/.test-tools/sat20-real-tools-20261106/restore-original-blocks-durable.py`，SHA256 `0025c34557645538c5db04d94addb9d01b6ccd160ab63b32ed3b97cbb3d1899e`。普通沙箱只读 `--plan` 因 `/bin/ps` 返回 `Operation not permitted` 停止；经正式权限机制执行同一个 `--plan` 后返回 `VALIDATED_READ_ONLY`，初始tip为1109，工具和原块前置校验通过。

唯一一次 `--apply` 产生receipt `/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz/restore-original-blocks-1110-1112-20261107.json`，SHA256 `70e6c94ca48e13d52aedf9c19e194649a3197a7beb41347ce5404161378bfdaa`，最后事件为 `FLUSHED_RESTART_REQUIRED`，tip高度1112。该状态名描述apply后的必需动作；下节的独立新Core进程证据证明重启已完成。旧receipt `/private/tmp/sat20-pwa-rgb-20261106-qdcd3whz/restore-original-block-1110.json`未改，SHA256仍为`5ccbbe82337be4eaaac6840f4c869c922623d843941a811eafa348f0a4ccdfe7`。

| 高度 | 原始区块hash | 该块原交易txid | apply时状态 | 新Core重启后确认数 |
|---:|---|---|---|---:|
| 1109 | `637e770031bf6c7ed710aad31a1e0d617b9d7df622a63c3f254abd1f42889cf8` | funding `65d184a630bf6d3a0595875cedd0ca28b429710ee33a9fcc7b781184fcadc41a` | 原块保留且活动 | 4 |
| 1110 | `712ccacd6248562d936c0a6e0f2fbea46be7ae0f4aca41e5baee644d1a2cecf7` | witness `b22145864d8bccc8b1aad1e57876ae07ed641206d277843eac0e4a98f03ce516` | `BLOCK_ACTIVE_AND_FLUSHED` | 3 |
| 1111 | `0a2e78167257de3b125a0ae0e592f6fce8fe4766f7cf2f89634860160b9a4a21` | plain准备 `f6825b2dabdbd74fd56c499eae1ab341cff81b725f2a826aa9620fdb24df3b7d` | `BLOCK_ACTIVE_AND_FLUSHED` | 2 |
| 1112 | `1d553de8222bb634962f2f6f37fbce222c7d7f0569ac433396e2acea4cdf58f5` | blind `51e8196cd15f408c51355ef52b2e7b8ae726557901c11a10e8a22af16dbd5900` | `BLOCK_ACTIVE_AND_FLUSHED` | 1 |

### 8.2 L1正常停机、同库重启与索引一致性

旧L1 run `resume-l1-N4kDpv`（controller PID 53188，Core PID 53200）通过自身唯一release文件正常停机：session `57911` 返回 `STOPPED`，status记录Core/indexer/electrs子进程exit 0；`bitcoind.log`在2026-11-07T01:05:09Z记录`Shutdown done`。随后复核原锁已移除、子进程不存在、53168–53172及18444端口空闲、5个原数据库目录无打开句柄。

同一数据库由原冻结L1脚本重新启动一次：新run `resume-l1-KKKvAC`，exec session `50828`，controller PID 54712、Core PID 54723。恢复工具`--status`只读返回`differentCoreProcessObserved=true`；receipt中的四笔交易均`active=true`，Core tip高度1112/hash `1d553de8222bb634962f2f6f37fbce222c7d7f0569ac433396e2acea4cdf58f5`。Core `getindexinfo`显示txindex同步到1112；Indexer bestheight为1112，代理查询1112区块hash与Core一致；Esplora tip高度/hash相同。

两把原遗留锁未删除，其内容以同inode方式保存在各自旧run目录：`resume-l1-Tgb1FP/resume-l1.lock.stale-20261107T004715Z-011afa.json` SHA256 `8ac383c831f0d0c151b5ed0169ec0756f8e79b5039acd77a83e072b0264fccb3`；`resume-l2-DrMOQw/resume-l2.lock.stale-20261107T004715Z-0b0ffa.json` SHA256 `7031a80d06645a90ddf8eb76c249c9db11ef162cf1f4497cb0c4e3a47e56efe6`。

### 8.3 L2与PWA恢复

原L2同库脚本启动run `resume-l2-AcRucw`，exec session `42500`，controller PID 54817，bootstrap/core PID 54825/54826。两节点tip均为原高度4/hash `9a6595c68793932eb0ff0b966fc0a91019fbcdd845f836f35ceb537e215ee477`；两Manager与STP health均ready。DKVS endpoint identity校验匹配原身份（30352后缀`6ac7985f52ad`，30362后缀`ded192f1336e`）。STP 30353、30363分别对`http://127.0.0.1:5189`和`http://localhost:5189`执行OPTIONS，四项均204，`Access-Control-Allow-Origin`精确回显请求Origin。

原PWA服务从`/Users/yingfeng/github/sat20wallet/pwa` cwd启动，exec session `16825`返回`PWA READY http://127.0.0.1:5189`。HTTP `/version.json`为200，release `0.1.46-20261106T225010Z-pwa`、buildId `20261106T225010Z-pwa`、commit `fde00ec629f6`；HTTP `/integrity-manifest.json`为200。实际HTTP GET `/wasm/sat20wallet.wasm`返回200、39,305,847字节，SHA256 `830a890ae161aab5912d2a74e4fc0d2aaf89e57fb14584acf0361315ad76c6c8`。本轮没有重新打开浏览器或钱包页，因此未取得浏览器实际WASM请求记录。

### 8.4 验收边界

- 本轮恢复验证了既有链、索引、L2身份和HTTP静态WASM产物；不等于新版钱包迁移或新版交易通过。P09/P10的旧witness/blind交易重启后确认恢复，但新版新增交易仍未执行。
- P01仍进行中：尚缺A2及公开账户`account_id`/`fingerprint`核验、浏览器独立WASM请求证据。
- P02仍进行中：为保护用户输入，本轮未读取或改变恢复表单；当前账户备份完成状态未知，待用户完成新恢复因素的输入、确认和提交后再验证。
