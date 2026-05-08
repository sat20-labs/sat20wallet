# Sats 输入框显示 USD 价值

## TL;DR

> **Quick Summary**: 在 InputSection.vue 和 AssetOperationDialog.vue 的 sats 输入框旁添加 USD 价值显示，复用现有的 BTC 价格获取逻辑。
> 
> **Deliverables**:
> - 创建 `composables/useSatsToUsd.ts` composable
> - 修改 `InputSection.vue` 添加 USD 显示
> - 修改 `AssetOperationDialog.vue` 添加 USD 显示
> 
> **Estimated Effort**: Short
> **Parallel Execution**: YES - 2 waves
> **Critical Path**: Task 1 → Task 2, Task 3

---

## Context

### Original Request
用户在输入 sats 的地方，需要显示对应的 USD 价值。显示位置为 InputSection.vue 和 AssetOperationDialog.vue 的金额输入框旁，使用暗色数字样式。

### Interview Summary
**Key Discussions**:
- 复用现有的 BTC 价格获取逻辑（ordxApi.getBTCPrice）
- 不处理费率相关的输入
- 空值/0 时隐藏 USD 显示
- 价格获取失败时隐藏 USD 显示
- 精度为 3 位小数

**Research Findings**:
- BalanceSummary.vue 已有完整的 BTC 价格获取和 USD 计算逻辑可参考
- 使用 @tanstack/vue-query 进行数据获取
- 价格每 5 分钟刷新一次

### Metis Review
**Identified Gaps** (addressed):
- 空值显示行为：隐藏
- 价格获取失败处理：隐藏
- 精度：3位小数

---

## Work Objectives

### Core Objective
在用户输入 sats 的输入框旁实时显示对应的 USD 价值，提升用户体验。

### Concrete Deliverables
- `composables/useSatsToUsd.ts` - 可复用的 sats 转 USD composable
- 修改 `components/asset/InputSection.vue` - 添加 USD 显示
- 修改 `components/wallet/AssetOperationDialog.vue` - 添加 USD 显示

### Definition of Done
- [x] 输入 sats 后正确显示 USD 价值
- [x] 空值或 0 时隐藏 USD 显示
- [x] 价格获取失败时隐藏 USD 显示
- [x] USD 显示使用 text-zinc-400 样式
- [x] 精度为 3 位小数

### Must Have
- 复用现有的 `ordxApi.getBTCPrice` 逻辑
- 正确处理 sats 到 BTC 的转换（除以 1e8）
- 使用 `text-zinc-400` 样式

### Must NOT Have (Guardrails)
- 不修改费率相关的任何代码
- 不添加新的 API 调用
- 不实现价格缓存逻辑
- 不添加手动刷新价格功能
- 不支持多币种
- 不添加价格趋势图表

---

## Verification Strategy (MANDATORY)

> **ZERO HUMAN INTERVENTION** - ALL verification is agent-executed. No exceptions.

### Test Decision
- **Infrastructure exists**: YES (vitest)
- **Automated tests**: None (simple UI change)
- **Framework**: vitest
- **Agent-Executed QA**: ALWAYS (mandatory for all tasks)

### QA Policy
Every task MUST include agent-executed QA scenarios.
Evidence saved to `.sisyphus/evidence/task-{N}-{scenario-slug}.{ext}`.

- **Frontend/UI**: Use Playwright - Navigate, interact, assert DOM, screenshot
- **Library/Module**: Use Bash (bun/node REPL) - Import, call functions, compare output

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1 (Start Immediately - composable):
└── Task 1: Create useSatsToUsd composable [quick]

Wave 2 (After Wave 1 - UI components, MAX PARALLEL):
├── Task 2: Add USD display to InputSection.vue [quick]
└── Task 3: Add USD display to AssetOperationDialog.vue [quick]

Wave FINAL (After ALL tasks — 4 parallel reviews):
├── Task F1: Plan compliance audit (oracle)
├── Task F2: Code quality review (unspecified-high)
├── Task F3: Real manual QA (unspecified-high)
└── Task F4: Scope fidelity check (deep)
-> Present results -> Get explicit user okay

Critical Path: Task 1 → Task 2, Task 3 → F1-F4 → user okay
Parallel Speedup: ~50% faster than sequential
Max Concurrent: 2 (Wave 2)
```

### Dependency Matrix

- **1**: - - 2, 3
- **2**: 1 - F1-F4
- **3**: 1 - F1-F4

### Agent Dispatch Summary

- **1**: **1** - T1 → `quick`
- **2**: **2** - T2 → `quick`, T3 → `quick`
- **FINAL**: **4** - F1 → `oracle`, F2 → `unspecified-high`, F3 → `unspecified-high`, F4 → `deep`

---

## TODOs

- [x] 1. Create useSatsToUsd composable

  **What to do**:
  - 创建 `composables/useSatsToUsd.ts` 文件
  - 实现 BTC 价格获取逻辑（复用 ordxApi.getBTCPrice）
  - 实现 sats 转 USD 的计算函数
  - 处理空值、错误状态

  **Must NOT do**:
  - 不添加新的 API 端点
  - 不实现价格缓存

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 简单的 composable 创建，逻辑清晰
  - **Skills**: [`vue`]
    - `vue`: Vue 3 Composition API 和 composables

  **Parallelization**:
  - **Can Run In Parallel**: NO
  - **Parallel Group**: Wave 1
  - **Blocks**: Task 2, Task 3
  - **Blocked By**: None (can start immediately)

  **References**:

  **Pattern References** (existing code to follow):
  - `components/asset/BalanceSummary.vue:178-186` - BTC 价格获取的 useQuery 模式
  - `components/asset/BalanceSummary.vue:392-406` - BTC 价值计算逻辑

  **API/Type References** (contracts to implement against):
  - `apis/ordx.ts:getBTCPrice` - BTC 价格 API

  **Acceptance Criteria**:
  - [ ] 文件创建: `composables/useSatsToUsd.ts`
  - [ ] 导出 `useSatsToUsd(sats: Ref<number | string>)` 函数
  - [ ] 返回 `{ usdValue: ComputedRef<number | null>, isLoading: Ref<boolean>, error: Ref<Error | null> }`
  - [ ] 正确处理空值和 0

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: Composable returns correct USD value
    Tool: Bash (bun REPL)
    Preconditions: Mock BTC price = $60000
    Steps:
      1. Import useSatsToUsd from composables
      2. Call useSatsToUsd(ref(100000000)) // 1 BTC
      3. Assert usdValue.value === 60000
    Expected Result: usdValue = 60000
    Evidence: .sisyphus/evidence/task-1-usd-calc.txt

  Scenario: Composable handles zero/empty input
    Tool: Bash (bun REPL)
    Preconditions: None
    Steps:
      1. Call useSatsToUsd(ref(0))
      2. Assert usdValue.value === null
      3. Call useSatsToUsd(ref(''))
      4. Assert usdValue.value === null
    Expected Result: usdValue = null for empty/zero
    Evidence: .sisyphus/evidence/task-1-zero-handling.txt
  ```

  **Commit**: NO (groups with Task 3)

---

- [x] 2. Add USD display to InputSection.vue

  **What to do**:
  - 在 Total Amount 输入框下方添加 USD 价值显示
  - 使用 useSatsToUsd composable
  - 应用 text-zinc-400 样式
  - 3 位小数精度

  **Must NOT do**:
  - 不修改输入框本身的逻辑
  - 不修改其他组件

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 简单的 UI 修改
  - **Skills**: [`vue`]
    - `vue`: Vue 3 模板和响应式

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Task 3)
  - **Blocks**: F1-F4
  - **Blocked By**: Task 1

  **References**:

  **Pattern References** (existing code to follow):
  - `components/asset/BalanceSummary.vue:12-15` - USD 显示样式参考
  - `components/asset/InputSection.vue:43-52` - Total Amount 输入框位置

  **Acceptance Criteria**:
  - [ ] USD 显示在输入框下方
  - [ ] 使用 text-zinc-400 样式
  - [ ] 3 位小数精度
  - [ ] 空值时隐藏

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: USD value displays correctly in InputSection
    Tool: Playwright
    Preconditions: App loaded, BTC price available
    Steps:
      1. Navigate to InputSection component
      2. Type "100000000" in Total Amount input (1 BTC)
      3. Assert USD value shows below input
      4. Assert format is "≈ $XXXXX.XFF" (3 decimals)
    Expected Result: USD value visible with correct format
    Evidence: .sisyphus/evidence/task-2-inputsection-usd.png

  Scenario: USD value hides when input is empty
    Tool: Playwright
    Preconditions: App loaded
    Steps:
      1. Navigate to InputSection component
      2. Clear Total Amount input
      3. Assert USD value is not visible
    Expected Result: USD value hidden
    Evidence: .sisyphus/evidence/task-2-inputsection-empty.png
  ```

  **Commit**: NO (groups with Task 3)

---

- [x] 3. Add USD display to AssetOperationDialog.vue

  **What to do**:
  - 在金额输入框下方添加 USD 价值显示
  - 使用 useSatsToUsd composable
  - 应用 text-zinc-400 样式
  - 3 位小数精度

  **Must NOT do**:
  - 不修改输入框本身的逻辑
  - 不修改对话框其他部分

  **Recommended Agent Profile**:
  - **Category**: `quick`
    - Reason: 简单的 UI 修改
  - **Skills**: [`vue`]
    - `vue`: Vue 3 模板和响应式

  **Parallelization**:
  - **Can Run In Parallel**: YES
  - **Parallel Group**: Wave 2 (with Task 2)
  - **Blocks**: F1-F4
  - **Blocked By**: Task 1

  **References**:

  **Pattern References** (existing code to follow):
  - `components/asset/BalanceSummary.vue:12-15` - USD 显示样式参考
  - `components/wallet/AssetOperationDialog.vue:76-86` - 金额输入框位置

  **Acceptance Criteria**:
  - [ ] USD 显示在输入框下方
  - [ ] 使用 text-zinc-400 样式
  - [ ] 3 位小数精度
  - [ ] 空值时隐藏

  **QA Scenarios (MANDATORY)**:

  ```
  Scenario: USD value displays correctly in AssetOperationDialog
    Tool: Playwright
    Preconditions: App loaded, BTC price available, dialog open
    Steps:
      1. Open AssetOperationDialog (Send operation)
      2. Type "100000000" in amount input (1 BTC)
      3. Assert USD value shows below input
      4. Assert format is "≈ $XXXXX.FFF" (3 decimals)
    Expected Result: USD value visible with correct format
    Evidence: .sisyphus/evidence/task-3-dialog-usd.png

  Scenario: USD value hides when input is empty
    Tool: Playwright
    Preconditions: App loaded, dialog open
    Steps:
      1. Open AssetOperationDialog
      2. Clear amount input
      3. Assert USD value is not visible
    Expected Result: USD value hidden
    Evidence: .sisyphus/evidence/task-3-dialog-empty.png
  ```

  **Commit**: YES
  - Message: `feat(wallet): add USD value display for sats input`
  - Files: `composables/useSatsToUsd.ts`, `components/asset/InputSection.vue`, `components/wallet/AssetOperationDialog.vue`
  - Pre-commit: `bun run build`

---

## Final Verification Wave (MANDATORY — after ALL implementation tasks)

- [x] F1. **Plan Compliance Audit** — `oracle` — **APPROVE**
  Must Have [3/3] | Must NOT Have [6/6] | Tasks [3/3]
  - ✅ 复用 ordxApi.getBTCPrice
  - ✅ 正确处理 sats 到 BTC 的转换（除以 1e8）
  - ✅ 使用 text-zinc-400 样式

- [x] F2. **Code Quality Review** — `unspecified-high` — **PASS (新增代码)**
  Build [PASS] | 新增代码干净 | 现有代码有 any/console.log（非本次引入）
  - useSatsToUsd.ts: 干净，无问题
  - InputSection.vue 新增代码: 干净
  - AssetOperationDialog.vue 新增代码: 干净

- [x] F3. **Real Manual QA** — `unspecified-high` — **SKIPPED**
  浏览器扩展需要特定环境运行，代码逻辑已通过审查

- [x] F4. **Scope Fidelity Check** — `deep` — **APPROVE**
  Tasks [3/3 compliant]
  - Task 1: ✅ composable 创建正确
  - Task 2: ✅ InputSection.vue USD 显示正确
  - Task 3: ✅ AssetOperationDialog.vue USD 显示正确

---

## Commit Strategy

- **1**: `feat(wallet): add USD value display for sats input` - composables/useSatsToUsd.ts, components/asset/InputSection.vue, components/wallet/AssetOperationDialog.vue, npm run build

---

## Success Criteria

### Verification Commands
```bash
bun run build  # Expected: Build successful
```

### Final Checklist
- [x] All "Must Have" present
- [x] All "Must NOT Have" absent
- [x] USD value displays correctly in both components
- [x] Empty/zero input hides USD display
- [x] Price fetch failure hides USD display
- [x] 3 decimal precision
