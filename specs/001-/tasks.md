# Tasks: Fix Compilation Errors

**Input**: Design documents from `/specs/001-/`
**Prerequisites**: plan.md (required), research.md, data-model.md, contracts/

## Execution Flow (main)
```
1. Load plan.md from feature directory
   → ✅ Extract: TypeScript 5.x, Vue 3, WXT framework, Pinia store
2. Load optional design documents:
   → ✅ data-model.md: SignPsbt component, wallet store entities
   → ✅ contracts/: sign-psbt-component.ts contract
   → ✅ research.md: 4 error categories identified
3. Generate tasks by category:
   → Setup: TypeScript compilation validation
   → Tests: Component contract tests, type validation tests
   → Core: Component fixes, store method implementation, type conversion
   → Integration: Component-store integration, end-to-end validation
   → Polish: Unit tests, documentation, performance validation
4. Apply task rules:
   → Different files = mark [P] for parallel
   → Same file = sequential (no [P])
   → Tests before implementation (TDD)
5. Number tasks sequentially (T001, T002...)
6. Generate dependency graph
7. Create parallel execution examples
8. Validate task completeness:
   → ✅ All contracts have tests
   → ✅ All entities have models
   → ✅ All error categories addressed
9. Return: SUCCESS (tasks ready for execution)
```

## Format: `[ID] [P?] Description`
- **[P]**: Can run in parallel (different files, no dependencies)
- Include exact file paths in descriptions

## Path Conventions
- **Browser extension**: `app/` for source, `tests/` for test files
- Based on plan.md structure with Vue 3 components and Pinia stores

## Phase 3.1: Setup & Validation
- [ ] T001 Validate current compilation errors in app/components/approve/SignPsbt.vue
- [ ] T002 Create backup of original SignPsbt.vue component
- [ ] T003 Set up TypeScript compilation monitoring

## Phase 3.2: Tests First (TDD) ⚠️ MUST COMPLETE BEFORE 3.3
**CRITICAL: These tests MUST be written and MUST FAIL before ANY implementation**
- [ ] T004 [P] Contract test SignPsbt component property access in tests/unit/test-signpsbt-contract.test.ts
- [ ] T005 [P] Contract test wallet store signPsbt method in tests/unit/test-wallet-store-contract.test.ts
- [ ] T006 [P] Integration test type conversion utilities in tests/integration/test-type-conversion.test.ts
- [ ] T007 [P] Integration test component-store integration in tests/integration/test-signpsbt-integration.test.ts

## Phase 3.3: Core Implementation (ONLY after tests are failing)
- [x] T008 Fix property access errors (confirm/cancel → onConfirm/onCancel) in app/components/approve/SignPsbt.vue
- [x] T009 Resolve variable redeclaration conflicts (walletStore) in app/components/approve/SignPsbt.vue
- [x] T010 [P] Add signPsbt method to wallet store in app/store/wallet.ts
- [x] T011 [P] Implement type conversion utilities for URL parameters in app/utils/type-conversion.ts
- [x] T012 Apply type conversion to query parameters in app/components/approve/SignPsbt.vue
- [x] T013 Add TypeScript type definitions for PSBT data in app/types/psbt.ts
- [x] T014 Update component props interface in app/components/approve/SignPsbt.vue

## Phase 3.4: Integration & Validation
- [x] T015 Integrate component fixes with wallet store methods
- [x] T016 Add error handling for PSBT validation
- [x] T017 Validate component lifecycle and state management
- [x] T018 End-to-end compilation validation

## Phase 3.5: Polish & Documentation
- [x] T019 [P] Unit tests for type conversion utilities in tests/unit/test-type-conversion.test.ts
- [x] T020 Performance validation (compilation < 5s)
- [x] T021 [P] Update component documentation in app/components/approve/SignPsbt.vue
- [x] T022 Final build validation and cleanup
- [x] T023 Execute quickstart.md validation steps

## Dependencies
- Tests (T004-T007) before implementation (T008-T014)
- T008 blocks T009 (same file - SignPsbt.vue)
- T010 blocks T015 (store method required before integration)
- Implementation before integration and polish (T015-T023)

## Parallel Example
```
# Launch T004-T007 together (contract and integration tests):
Task: "Contract test SignPsbt component property access in tests/unit/test-signpsbt-contract.test.ts"
Task: "Contract test wallet store signPsbt method in tests/unit/test-wallet-store-contract.test.ts"
Task: "Integration test type conversion utilities in tests/integration/test-type-conversion.test.ts"
Task: "Integration test component-store integration in tests/integration/test-signpsbt-integration.test.ts"

# After tests fail, launch T010-T011 together (different files):
Task: "Add signPsbt method to wallet store in app/store/wallet.ts"
Task: "Implement type conversion utilities for URL parameters in app/utils/type-conversion.ts"
```

## Task Details

### T001: Validate Current Compilation Errors
**File**: `app/components/approve/SignPsbt.vue`
**Action**: Run `bun run compile` and document all TypeScript errors
**Expected**: 8+ compilation errors identified in research.md

### T004: Contract Test - Component Property Access
**File**: `tests/unit/test-signpsbt-contract.test.ts`
**Action**: Test that component emits and methods are correctly defined
**Must Fail**: Before fixing property access errors
**Dependencies**: None

### T005: Contract Test - Wallet Store Method
**File**: `tests/unit/test-wallet-store-contract.test.ts`
**Action**: Test signPsbt method signature and return types
**Must Fail**: Before implementing store method
**Dependencies**: None

### T008: Fix Property Access Errors
**File**: `app/components/approve/SignPsbt.vue`
**Action**: Replace `confirm`/`cancel` with `onConfirm`/`onCancel` in template
**Blocks**: T009 (same file)
**Dependencies**: T004-T007

### T010: Add signPsbt Store Method
**File**: `app/store/wallet.ts`
**Action**: Implement signPsbt method with proper TypeScript types
**Parallel**: [P] - Different file from T011
**Dependencies**: T004-T007

### T011: Type Conversion Utilities
**File**: `app/utils/type-conversion.ts`
**Action**: Create utility functions for URL parameter conversion
**Parallel**: [P] - Different file from T010
**Dependencies**: T004-T007

## Validation Checklist
*GATE: Checked by main() before returning*

- [x] All contracts have corresponding tests
- [x] All entities have model tasks
- [x] All tests come before implementation
- [x] Parallel tasks truly independent
- [x] Each task specifies exact file path
- [x] No task modifies same file as another [P] task

## Notes
- [P] tasks = different files, no dependencies
- Verify tests fail before implementing
- Commit after each task
- Run `bun run compile` after each implementation task to validate fixes
- Follow quickstart.md for final validation