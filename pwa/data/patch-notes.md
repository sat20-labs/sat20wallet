# PWA 单 Manager 补丁草稿

状态：仅生成候选 patch，未应用、未提交、未运行测试。`git apply --check` 只验证 patch 对当前 worktree 可应用，不执行测试，也不改动源码。

## 交付物

- `production.patch`：在 `index.html` 把应用入口改成轻量 bootstrap；bootstrap 先取得同源独占 Web Lock，持锁后才动态导入 `main.ts`。等待页不导入 Vue/store/WASM，提供 focus 尝试、刷新重试和自动排队接管；刷新只销毁当前请求并重新排队，不抢锁。不使用 heartbeat、TTL、`steal` 或 `beforeunload` 释放。补上同页 WASM runtime/Manager init 去重及 release 后可重建 Manager。
- `tests.patch`：给 `device(sharedContext)` 增加默认保持原行为的 `waitForReady = true` 与可选 `beforeGoto` 参数；新 `waitingDevice(context)` 在导航前用 `page.addInitScript` 安装 WASM、IndexedDB、Web Storage 探针。新增同 context 第二页、UI 锁定仍持锁、Manager release/reinit 不产生第二 runtime、持锁页关闭后等待页完整初始化并解锁，以及刷新排队交接的 Playwright 用例。等待 UI 断言使用稳定 `data-testid` / `data-state`，不依赖语言文案。新增可独立调用的 `TestPWASingleManager`。
- `sdk-init.patch`：独立的 Go/WASM 原始 `init` 并发保护候选，用 `managerInitializing` 防止两个 raw init 同时通过 `_mgr == nil` 检查。参数校验后的单次 reservation 和 `managerAsyncTasks.Add` 在同一 lifecycle mutex 下完成；异步任务不重复检查自身 reservation。预约之前的早退不设置标志，预约后的 error/panic/成功路径都通过 defer 清标志并平衡 WaitGroup。`releaseManager` 在 `_mgr` 尚未发布时拒绝关闭；发布后设置 closing 并等待 init task 完成后再关闭。它与 PWA Web Lock/facade 去重分开，父线程可独立决定是否应用。
- `single-manager-20261031.md`：此前的入口/生命周期方案说明。

## 旧多标签页用例替换映射

| 旧用例 | 新用例 | 保留的性质 |
|---|---|---|
| `shared IndexedDB tabs invalidate pending writes when another tab locks` | `active Manager rejects a pending account write after same-page lock` | 捕获后、dispatch 前发生锁定时，旧 session guard 必须拒绝，断言业务调用没有 dispatch。第二个 Manager 不再作为锁定源。 |
| `shared IndexedDB subaccount switch discards another tab previous-identity response` | `same-page subaccount switch discards a stale account response` | 仍检查账户切换使旧代际响应失效，并核对最后选中的账户地址/公钥。 |
| `shared IndexedDB wallet switch survives another tab rebuilding before commit` | `committed wallet selection survives single Manager handoff` | 等待页在切换提交前不启动；原页提交真实 SDK 切换并关闭后，新 Manager 从持久状态打开已提交钱包。 |
| `shared IndexedDB password change cannot authorize an old-password tab` | `password changes apply on single Manager handoff` | 密码变更后关闭原 Manager；接管页必须拒绝旧密码、接受新密码，并恢复同一 Guardian 身份。 |

现有 Go wrappers `TestPWATabAccountSwitch` 和 `TestPWATabWalletSwitch` 保留并改为选择新用例；另加 `TestPWATabPasswordHandoff`。新增单例 selector 覆盖双页生命周期、刷新交接和同页锁定期间的 pending request。没有整段删除旧的 case wrapper。

## 入口覆盖与边界

PWA 锁名是 `sat20-wallet-manager-v1`。同源正常应用入口会在加载 `main.ts` 前排队；关闭/刷新由浏览器 document teardown 释放锁，等待页随后才加载钱包。UI lock 和 Manager `release()` 都不释放页面锁。拿不到 Web Locks 或 request 出错时 fail closed；持锁期间页面只是被冻结/挂起时不让其他页强抢。

检查到 `tools/rgb11-repair/index.html` 是 `pwa/tools` 中直接打开 `sat20-wallet-pwa` IndexedDB 的独立维护入口。`production.patch` 让它以相同 lock 名 `ifAvailable` 取得锁并在整个维护页生命周期持有；钱包页已活跃或 Web Locks 不可用时，维护操作拒绝继续。当前 `repair.mjs` 的模块顶层仅定义函数；数据库打开和写入发生在 `guard()` 之后。这个锁只覆盖更新后的同源 PWA/维护页，不能约束旧版未升级页面、不同 origin、不同浏览器 profile、扩展、原生 SDK 或其他设备。

focus 通过 `BroadcastChannel` 尝试通知持锁页调用 `window.focus()`；浏览器可拒绝窗口聚焦，等待页必须保留手动切回提示。安装 PWA 窗口和普通 tab 只有在同浏览器 profile、同 origin/storage partition 时才共享 Web Lock；Web Lock 不负责跨浏览器、跨 profile 或跨设备同步。

SharedWorker 可以把 Go WASM/Manager 搬入共享 worker，使多个页面通过 MessagePort 共用同一进程；这需要重写 `window.sat20wallet_wasm`/facade 调用、请求序号与错误返回、Manager 事件/身份通知及 worker 生命周期桥。当前应用主要由 Window/Vue 页面驱动。为本轮先解决同一 profile 双启动，Web Lock 改动范围更小；SharedWorker 作为后续架构选项，不包含在 patch 内。

## Patch 基线与验证

生成前记录的源码 SHA-256（这些值也可用于核对草稿针对的共享 worktree 版本）：

| 文件 | SHA-256 |
|---|---|
| `pwa/index.html` | `f383a200aed4a6236cd4850c00a1d1d61e49e0b0720c57df82406dc21d1d355c` |
| `pwa/main.ts` | `a697661e412bbd3db522506489f380e1b271926eadfc9398d820134c8391e604` |
| `pwa/utils/wasm.ts` | `aab3f8e259b95bbc38937ad4b1233c93736e51f66bd0728505f3a63c2d14ea22` |
| `pwa/utils/sat20.ts` | `2f3f37ff75d41d5b6d8675ff8ed881a36b14ab209815f1ea09604b40071ae122` |
| `pwa/tools/rgb11-repair/index.html` | `67369425bc9fcd0fbe1aedd85d3d65bbc509301a2da64d1df1525a8307374309` |
| `pwa/scripts/verify/account-management-e2e.mjs` | `3b2edd17441f4577eb2c0dd4b156b724cef2e6777eb5fd6ae816f446f2f2540f` |
| `pwa/scripts/verify/account-management-usage-e2e.mjs` | `3406201794eb008d29f600cc182ab2542aa6e621515e075d0da542c5bbec00aa` |
| `sdk/wasm/main.go` | `5bc357afdc146349c19ee187f0a07c66d2c48d4ed928fd1cfe5a95140aabbb82` |
| `sdk/e2e/account_management_browser_e2e_test.go` | `63e895bc67e6fe257b57a30d36bab95de22f0329e9fe86a41e2178437ce48c14` |

`git apply --check` 对三个 patch 均通过。候选完整 PWA 副本的 `vue-tsc --noEmit` 通过，Vite production build 通过；构建只有依赖 PURE 注释和大 chunk 的非阻塞 warning。两个候选 `.mjs` runner 通过 `node --check`。SDK 的 `GOOS=js GOARCH=wasm go build ./wasm` 使用 Go overlay 编译候选 `main.go` 成功；默认 Go cache 路径受 sandbox 写限制，改用 `/tmp` 下独立 `GOCACHE` 后成功。没有运行浏览器或其他业务测试。 这些 patch 建立在共享且已有其他修改的 worktree 上；若父线程或其他线程继续改动目标文件，需要先核对以上哈希/上下文，再决定是否重基 patch。当前草稿只在 `pwa/data` 新增文件，不覆盖其他线程的 SDK、WASM 或测试改动。
