# PWA 单 Manager 启动门禁候选

状态：仅供父线程 review 的方案草稿。未应用生产代码或测试代码，未运行测试。

## 1. 当前入口与判断

当前调用顺序是：

```text
index.html -> main.ts 静态导入整套 PWA
  -> clearDevelopmentServiceWorkerCache()
  -> loadWasm()
     -> walletStorage.initializeState() / 写 env、network
     -> instantiateGoWasm()
        -> WebAssembly.instantiate()
        -> walletManager.init()
           -> SDK Init: NewKVDB -> NewManager -> Start
  -> walletStorage.initializeState()
  -> installWalletIdentityCrossTabBoundary()
  -> mount Vue
```

所以只在 `installWalletIdentityCrossTabBoundary()` 处加互斥已经太晚：每页在到达它之前都可能初始化 PWA 存储并启动独立的 WASM/Manager。边界必须在 `index.html` 的应用入口处、加载 `main.ts` 和它的任何应用依赖之前。

`identity-boundary.ts` 的 `BroadcastChannel` 是身份变更后的通知/重载机制，不承担启动互斥。应把实例锁和该通知分开，身份切换仍保留原断言与实现。

## 2. 最小方案

对同一 origin 的 PWA 页面使用一个固定名的 Web Lock，例如 `sat20-wallet-manager-v1`，独占且不设超时、不使用 `steal`。在页面最初只渲染轻量等待壳，然后请求独占锁；持锁回调才 `import('./main.ts')`。默认排队行为使第二页不初始化钱包；当前页被销毁后，最早等待页自动取得锁并完整启动。

锁覆盖该 PWA 的应用模块加载，因此也覆盖 `loadWasm()`、Go `Init`、SDK IndexedDB、PWA IndexedDB、应用模块的 `localStorage`/`sessionStorage` 写入。`buffer-globals.ts` 只装载 Buffer polyfill，不访问钱包存储；可继续在 bootstrap 前加载，或由 bootstrap 静态导入。

入口草稿（展示执行边界，不是可直接合入的完整 UI）：

```ts
const lockName = 'sat20-wallet-manager-v1'
const runChan = typeof BroadcastChannel === 'function'
  ? new BroadcastChannel('sat20-wallet-run-v1')
  : null
let ownsLock = false
const holdLock = () => new Promise<void>(() => {})

renderWaitShell() // 纯 DOM；不导入 Vue、store、walletStorage 或 wasm.ts

if (!('locks' in navigator)) {
  renderLockError('This browser cannot enforce a single wallet instance.')
} else {
  runChan?.addEventListener('message', ({ data }) => {
    if (ownsLock && data?.type === 'focus') window.focus() // best effort only
  })
  document.querySelector('#walletFocus')?.addEventListener('click', () => {
    runChan?.postMessage({ type: 'focus' })
  })

  // A restored BFCache document restarts through the gated entry before use.
  window.addEventListener('pageshow', event => {
    if ((event as PageTransitionEvent).persisted) window.location.reload()
  })

  void navigator.locks.request(lockName, { mode: 'exclusive' }, async lock => {
    if (!lock) throw new Error('Exclusive wallet lock was not granted')
    ownsLock = true
    try {
      await import('./main.ts')
    } catch (error) {
      console.error(error)
      try { renderLockError(String(error)) } catch {}
    }
    // Do not release on wallet lock/logout, Manager.release(), UI unmount,
    // pagehide, or a startup error. Document teardown releases it.
    await holdLock()
  }).catch(error => renderLockError(String(error)))
}
```

入口调整候选：`index.html` 把 `main.ts` 的 script 改为 `wallet-bootstrap.ts`；该新 bootstrap 只持锁、渲染等待/错误状态、尝试聚焦原页，并在获锁后动态导入原 `main.ts`。`main.ts` 的应用启动主体仍沿原顺序运行。`renderWaitShell()` 要自己用最少 DOM 渲染，不能静态导入现有 Vue/i18n/startup helper，否则这些依赖会在取锁前执行。

`utils/wasm.ts` 的同页入口还应缓存一次初始化 Promise，避免同一 realm 重复调用 `loadWasm()` 再实例化 Go：

```ts
let wasmTask: Promise<void> | undefined
export const loadWasm = () => wasmTask ??= loadWalletWasm()
```

保持失败 Promise，要求通过整页重载重试，避免一次初始化部分失败后在同页启动第二个 WASM runtime。`utils/sat20.ts` 的 `WalletManager.init()` 也可缓存 in-flight Promise，成功 `release()` 后清空；这样 facade 上的重复初始化会合并到一次调用。现有 SDK `initManager()` 会拒绝已存在的 `_mgr`，但同一 realm 两个并发原始 `init()` 若都在异步 `NewKVDB/NewManager` 完成前进入，当前检查并不明显地串行化它们。若 `sat20wallet_wasm.init` 可从其他 PWA 路径直调，则 SDK 侧还需初始化中 guard；正常 PWA 启动应只通过单一 `loadWasm()`，这一边界需由父线程 review 后测试确认。

### 生命周期规则

| 情况 | 候选行为 |
|---|---|
| 第二页打开 | 等待壳 + 可选“回到已打开钱包”按钮；不导入 `main.ts`，不加载 Go runtime/WASM，不打开业务 IndexedDB，不写钱包存储。锁请求排队，无抢占。 |
| 原页面刷新 | 旧 document 销毁后 Web Lock 自动释放，排队页之一取锁。刷新页也走 bootstrap 并排队；任一时刻只允许一个获锁回调导入应用。 |
| 关闭窗口/标签页 | 不依赖 `beforeunload`/`unload` 清理；document 销毁时浏览器终止该 document 的锁请求。 |
| 浏览器/renderer 崩溃 | document/agent 终止后释放锁；未终止但后台挂起时不抢锁，其他页继续等待，避免双活。 |
| BFCache | 不在 `pagehide` 手动释放。Web Locks 规范把持锁 document 的 unload 清理作为锁终止/BFCache 不恢复原因；若浏览器恢复 persisted document，候选 `pageshow.persisted` 处理会立即整页重载，通过 bootstrap 重新排队。目标浏览器仍需验证。 |
| 钱包 UI 锁屏/退出登录 | 仅锁业务会话，不释放实例锁；否则另一页可能在 Manager 仍运行时取得锁。 |
| SDK `releaseManager` | 只关闭 Go Manager，不是页面实例终止；不释放 Web Lock。此页若仍打开，第二页继续等待。相同页要重建 Manager，复用已启动的 Go runtime，并由单例 init Promise 串行。 |
| 不支持 Web Locks / API 拒绝 | fail closed：显示不支持/锁错误与重试提示，不导入应用，也不以 localStorage 心跳或超时强抢作为后备。 |

Web Locks 的 callback Promise 决定锁的持有时长，document unload cleanup 会清理其锁；默认排队实现关闭后交接，`steal` 会破坏被抢持有者仍在执行时的互斥保证，故不采用。见 [W3C Web Locks](https://www.w3.org/TR/web-locks/) 和 [HTML 标准的 BFCache 阻止原因](https://html.spec.whatwg.org/multipage/nav-history-apis.html#back-forward-cache)。

注意范围：此门禁覆盖同一浏览器中参与该入口的 PWA 页面及其 PWA 存储调用。`tools/rgb11-repair/index.html` 等同源独立维护页可直接操作 `localStorage`/IndexedDB，未经过 PWA bootstrap；若要声称“任何同源钱包写入都互斥”，这些写者也必须加入相同锁，或明确要求先关闭正式钱包。不同 origin、扩展上下文、浏览器 profile/存储分区、不同浏览器和不同设备不由该锁协调。

## 3. 尝试回到原页的边界

普通 tab 可用同源 `BroadcastChannel` 向唯一持锁页发焦点请求，让原页调用 `window.focus()`。这是尽力尝试；浏览器不承诺切换系统窗口/标签页。等待页需明确提示“钱包已在另一个窗口打开；如未自动切换，请手动切回。当前窗口会在原窗口关闭后继续启动”，不能显示“已成功切换”。

当前 manifest 是 `display: standalone`，因此安装 PWA 可能以独立 app window 启动；同一 origin、同一浏览器存储分区下仍应竞争同一 Web Lock。现有 SW 可用于枚举匹配 scope 的 `WindowClient` 并调用 `focus()`，但前提是该页受控且浏览器把安装窗口暴露为可枚举 client；规范还要求 origin 内至少有一个 window 处于 transient activation，否则 `WindowClient.focus()` 会拒绝。若走 SW 路径，第二页按钮点击后才发送请求，并保留手动切换提示。普通标签、安装 PWA 和目标移动系统的 focus 行为需要分别实测，不能承诺一定跳转。见 [W3C Service Worker 的 Client.focus 规则](https://www.w3.org/TR/service-workers/#client-focus)。

## 4. SharedWorker 方案为何本轮不选

真正共享 Manager 要把 Go WASM runtime、Manager、SDK IndexedDB 接口和所有 WASM 方法移入一个 SharedWorker，再给每个 Window 暴露经 `MessagePort` 的 RPC facade。这样才是共享 Manager，而不是共享 DB。

目前代码直接由 Window 中的 `utils/wasm.ts` 执行 `new Go()`、动态插入 runtime script、`WebAssembly.instantiate()`；Go SDK 的数据更新回调会向 `window` dispatch `CustomEvent`；`utils/sat20.ts` 逐个代理大量 WASM 方法，账户通知、DApp bridge、Vue store 和批准流程都在 Window。SharedWorker 没有 DOM/`window`，必须逐项改成 port RPC、结果/错误结构化克隆、事件广播和 window 侧更新；函数 callback 不能直接跨 MessagePort 传递。IndexedDB 在 Worker 中可用，但现有 JS/WASM 桥并非 Worker client。SharedWorker 也有独立生命周期与浏览器支持边界；不可用时若退回每页 Worker/Manager 就破坏目标，需继续 fail closed。

因此它是可行的后续架构方向，但本轮要完成“同一 PWA 本地数据不出现多个 Manager”，在入口加一个 Web Lock 更窄、可由真实双页测试直接验证。参见 [HTML SharedWorker 规范](https://html.spec.whatwg.org/multipage/workers.html#shared-workers-and-the-sharedworker-interface)；Worker 的独立 global 与 DOM 限制也见同一标准。

## 5. 真实 Playwright 测试草稿

基于现有 `scripts/verify/account-management-usage-e2e.mjs` 的 `using(page)`、`device(page.context())`、`ready(page)` 和真实 PWA/WASM/IndexedDB 流程。第二页是同一 BrowserContext 的真实同源 page，不复制 Manager、不注入 Manager 私有状态。`device(sharedContext)` 当前会立即调用 `ready()`；建议只为此类测试增加 `skipReady`/wait shell 选项，让测试先拿到仍在排队的 Page，再自行等待获锁后的 `ready()`。

建议新增 scenario（不运行）：

1. `using(page)` 生成/解锁真实 Manager A 后，在同一 context 安装 init-script 计数器，再创建第二个真实页面 B。
2. 计数器只观测，不替换真实实现：统计 B 的 `WebAssembly.instantiate`、`indexedDB.open`、`IDBObjectStore` 写操作和 `Storage.setItem/removeItem/clear`。B 应显示等待状态，计数全为 0，且 `window.__SAT20_PWA_VERIFY__` 不存在；A 的真实 Manager 与存储可正常使用。
3. 锁住 A 的钱包 UI，B 仍不得开始。可在 A 调用真实 `walletManager.release()`，B 也仍应等待；这证明实例锁不跟着会话锁或 SDK release 释放。该断言不要求 A 的 Manager 继续可用。
4. 关闭 A 的 Page 但保留 BrowserContext。B 应自动取得锁，完整走原 `main.ts -> loadWasm -> walletManager.init -> ready()`，随后以真实密码解锁并读到现存目录/已选身份；B 的计数出现一次 WASM 初始化与真实 IndexedDB 启动。这个交接证明 Manager 由实际浏览器启动路径从持久数据重建。
5. 独立刷新场景：A 运行、B 排队时刷新 A；最终所有同 context pages 中最多一个挂载 `__SAT20_PWA_VERIFY__`/可响应 Manager API。刷新全过程不得出现第二次并发 Manager；不以 `beforeunload` flag 或人工释放来让断言通过。
6. 身份/密码交接：A 先真实提交子账户/钱包切换或改密码，B 仍处于等待壳；关闭 A 后 B 获锁并从持久存储完整启动，核对当前被提交身份，且旧密码失败、新密码成功。对密码 case 还保留当前单页“密码更改后刷新并拒绝旧密码”的既有断言。
7. BFCache 场景由实际目标浏览器验收：从持锁页导航离开再返回，同时保留一页等待者；验证最多一个可操作 Manager。若出现 `pageshow.persisted`，页面应先重载并重新参加锁队列。进程强杀可做手工/专项验收；不能靠卸载事件证明锁释放。

测试只改断言和锁等待交互，不伪造缓存 Manager。新用例要在现有 `TestSDKAccountPWAConnectedBrowser` 使用的真实 PWA runner 注册，并保留设备间独立 storage 的既有测试；设备/浏览器之间的锁不共享。

## 6. 当前四个同 context 多页 case 的替换映射

| 现有 case | 单 Manager 下的处理 | 必须保留的安全性质 |
|---|---|---|
| `usage: shared IndexedDB tabs invalidate pending writes when another tab locks` | 双 Manager 互相广播锁屏已不可达；替换为 B 在 A 锁屏及 `release()` 后仍无 WASM/DB访问，A 关闭后才交接。 | 现有单页 `locked account writes and requests interrupted by lock cannot dispatch` 继续保留；不得删除会话代际检查。 |
| `usage: shared IndexedDB subaccount switch discards another tab previous-identity response` | 另一个 Manager 的旧身份响应失去前提；替换为 A 提交账户切换，等待页 B 获锁后仅从持久状态加载新账户，并保留已有同页切换/后台刷新用例。 | 旧响应不得在任何现存身份切换断言中被接受；不能删掉代际/身份检查。 |
| `usage: shared IndexedDB wallet switch survives another tab rebuilding before commit` | 另一 Manager 在切换提交前重建的竞争失去前提；替换为 A 完成钱包切换后关闭，再由 B 完整启动并读到已提交 wallet selection。 | 仍断言重载后的 manager/store selection 与实际 SDK 选中钱包一致，不能只检查 UI。 |
| `usage: shared IndexedDB password change cannot authorize an old-password tab` | “第二个 Manager 缓存旧密文”场景不再成立；改为改密后 B 保持无 Manager，A 关闭后 B 从 SDK IndexedDB 启动，旧密码失败、新密码成功。 | 保留既有 `PWA password change survives page reload and retains selection` 旧密码拒绝、新密码成功和账户目录断言。 |

这些 case 在 `scripts/verify/account-management-e2e.mjs` 的全量必跑名单里；`sdk/e2e/account_management_browser_e2e_test.go` 另有 `TestPWATabAccountSwitch`、`TestPWATabWalletSwitch` 两个定向 wrapper。父线程核准替换用例及映射前，不删除、跳过、改弱任何旧断言或 wrapper。使用不同独立 context/device 的 DKVS 同步、恢复和冷启动测试不属于多 Manager 同 DB，继续保留。

## 7. 依据、源码指纹与工作区边界

Web Locks 规范说明：同 origin 脚本可协调同名资源，锁一直持有到回调 Promise settle，document unload cleanup 终止锁；默认请求排队。`steal` 会使原持有者在没有独占保证下继续执行，所以禁止使用。Worker 的 IndexedDB、SharedWorker 与 focus 依据见上方标准链接。

读关键入口前记录的 SHA-256（UTC `2026-10-31T06:22:57Z`）：

| 源码 | SHA-256 |
|---|---|
| `pwa/main.ts` | `a697661e412bbd3db522506489f380e1b271926eadfc9398d820134c8391e604` |
| `pwa/utils/wasm.ts` | `aab3f8e259b95bbc38937ad4b1233c93736e51f66bd0728505f3a63c2d14ea22` |
| `pwa/lib/identity-boundary.ts` | `fbde36dc891bc2472309c91c64e2be108bd06559f6dd8eac2351943b18ee951c` |
| `pwa/scripts/verify/account-management-usage-e2e.mjs` | `3406201794eb008d29f600cc182ab2542aa6e621515e075d0da542c5bbec00aa` |
| `sdk/wasm/main.go` | `5bc357afdc146349c19ee187f0a07c66d2c48d4ed928fd1cfe5a95140aabbb82` |
| `sdk/e2e/account_management_browser_e2e_test.go` | `63e895bc67e6fe257b57a30d36bab95de22f0329e9fe86a41e2178437ce48c14` |

读取时 PWA 根 `AGENTS.md` 的简化/安全和修改授权原则已检查；仓库 `.agents/skills` 目录/技能文件不存在。共享工作区里 SDK `wasm/main.go`、`e2e/account_management_browser_e2e_test.go` 及 SDK 的 verify runner 已显示为其他工作修改；本草稿没有编辑这些文件，也没有编辑任何测试或生产源码。唯一预期改动是新增本 Markdown 文件。
