
# Implementation Plan: Fix Compilation Errors

**Branch**: `001-` | **Date**: 2025-10-02 | **Spec**: [spec.md](./spec.md)
**Input**: Feature specification from `/specs/001-/spec.md`

## Execution Flow (/plan command scope)
```
1. Load feature spec from Input path
   → If not found: ERROR "No feature spec at {path}"
2. Fill Technical Context (scan for NEEDS CLARIFICATION)
   → Detect Project Type from file system structure or context (web=frontend+backend, mobile=app+api)
   → Set Structure Decision based on project type
3. Fill the Constitution Check section based on the content of the constitution document.
4. Evaluate Constitution Check section below
   → If violations exist: Document in Complexity Tracking
   → If no justification possible: ERROR "Simplify approach first"
   → Update Progress Tracking: Initial Constitution Check
5. Execute Phase 0 → research.md
   → If NEEDS CLARIFICATION remain: ERROR "Resolve unknowns"
6. Execute Phase 1 → contracts, data-model.md, quickstart.md, agent-specific template file (e.g., `CLAUDE.md` for Claude Code, `.github/copilot-instructions.md` for GitHub Copilot, `GEMINI.md` for Gemini CLI, `QWEN.md` for Qwen Code or `AGENTS.md` for opencode).
7. Re-evaluate Constitution Check section
   → If new violations: Refactor design, return to Phase 1
   → Update Progress Tracking: Post-Design Constitution Check
8. Plan Phase 2 → Describe task generation approach (DO NOT create tasks.md)
9. STOP - Ready for /tasks command
```

**IMPORTANT**: The /plan command STOPS at step 7. Phases 2-4 are executed by other commands:
- Phase 2: /tasks command creates tasks.md
- Phase 3-4: Implementation execution (manual or via tools)

## Summary
Fix TypeScript compilation errors in SignPsbt.vue component to restore build functionality. Research identified 4 main error categories: property access mismatches (confirm/cancel vs onConfirm/onCancel), variable redeclaration conflicts, missing store methods (signPsbt), and type conversion issues for URL query parameters. The approach involves targeted fixes to component template access patterns, proper variable scoping, store method implementation, and robust type conversion utilities.

## Technical Context
**Language/Version**: TypeScript 5.x with Vue 3
**Primary Dependencies**: Vue 3, WXT framework, Pinia store, TypeScript compiler
**Storage**: Local state via Pinia, browser extension storage
**Testing**: Vue Test Utils, Vitest, TypeScript compiler (vue-tsc)
**Target Platform**: Browser extension (Chrome/Firefox), Mobile via Capacitor
**Project Type**: Web application (browser extension + mobile app)
**Performance Goals**: TypeScript compilation < 5s, type safety 100% coverage
**Constraints**: Must maintain backward compatibility, no breaking API changes
**Scale/Scope**: Single component fixes, localized to SignPsbt.vue and related stores

## Constitution Check
*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

[Gates determined based on constitution file]

## Project Structure

### Documentation (this feature)
```
specs/[###-feature]/
├── plan.md              # This file (/plan command output)
├── research.md          # Phase 0 output (/plan command)
├── data-model.md        # Phase 1 output (/plan command)
├── quickstart.md        # Phase 1 output (/plan command)
├── contracts/           # Phase 1 output (/plan command)
└── tasks.md             # Phase 2 output (/tasks command - NOT created by /plan)
```

### Source Code (repository root)
```
app/
├── components/
│   ├── approve/           # PSBT signing components
│   │   └── SignPsbt.vue   # Target component for fixes
│   ├── wallet/            # Wallet-related components
│   └── setting/           # Settings components
├── store/                 # Pinia stores
│   ├── wallet.ts          # Wallet store with signPsbt method
│   ├── global.ts          # Global state management
│   └── index.ts           # Store configuration
├── composables/           # Vue composables
├── utils/                 # Utility functions
├── types/                 # TypeScript type definitions
└── entrypoints/           # Browser extension entry points
    └── popup/             # Popup interface
        └── pages/
            └── wallet/    # Wallet pages

tests/
├── unit/                  # Unit tests for components
└── integration/           # Integration tests
```

**Structure Decision**: Web application with Vue 3 components, Pinia state management, and browser extension architecture. The compilation errors are localized to the SignPsbt.vue component and its interaction with the wallet store.

## Phase 0: Outline & Research
1. **Extract unknowns from Technical Context** above:
   - For each NEEDS CLARIFICATION → research task
   - For each dependency → best practices task
   - For each integration → patterns task

2. **Generate and dispatch research agents**:
   ```
   For each unknown in Technical Context:
     Task: "Research {unknown} for {feature context}"
   For each technology choice:
     Task: "Find best practices for {tech} in {domain}"
   ```

3. **Consolidate findings** in `research.md` using format:
   - Decision: [what was chosen]
   - Rationale: [why chosen]
   - Alternatives considered: [what else evaluated]

**Output**: research.md with all NEEDS CLARIFICATION resolved

## Phase 1: Design & Contracts
*Prerequisites: research.md complete*

1. **Extract entities from feature spec** → `data-model.md`:
   - Entity name, fields, relationships
   - Validation rules from requirements
   - State transitions if applicable

2. **Generate API contracts** from functional requirements:
   - For each user action → endpoint
   - Use standard REST/GraphQL patterns
   - Output OpenAPI/GraphQL schema to `/contracts/`

3. **Generate contract tests** from contracts:
   - One test file per endpoint
   - Assert request/response schemas
   - Tests must fail (no implementation yet)

4. **Extract test scenarios** from user stories:
   - Each story → integration test scenario
   - Quickstart test = story validation steps

5. **Update agent file incrementally** (O(1) operation):
   - Run `.specify/scripts/bash/update-agent-context.sh claude`
     **IMPORTANT**: Execute it exactly as specified above. Do not add or remove any arguments.
   - If exists: Add only NEW tech from current plan
   - Preserve manual additions between markers
   - Update recent changes (keep last 3)
   - Keep under 150 lines for token efficiency
   - Output to repository root

**Output**: data-model.md, /contracts/*, failing tests, quickstart.md, agent-specific file

## Phase 2: Task Planning Approach
*This section describes what the /tasks command will do - DO NOT execute during /plan*

**Task Generation Strategy**:
- Load `.specify/templates/tasks-template.md` as base
- Generate tasks from Phase 1 design docs (contracts, data model, quickstart)
- Each compilation error category → specific fix task
- Each component contract → component fix task [P]
- Each data model entity → type definition task [P]
- Each validation scenario → test task

**Ordering Strategy**:
- Error resolution order: Property access → Variable scope → Store methods → Type conversion
- Test-first approach: Type validation tests before implementation
- Dependency order: Component fixes before integration tests
- Mark [P] for parallel execution (independent error categories)

**Estimated Output**: 12-15 numbered, ordered tasks in tasks.md
- Property access fixes (2-3 tasks)
- Variable scope resolution (2-3 tasks)
- Store method implementation (3-4 tasks)
- Type conversion utilities (2-3 tasks)
- Integration testing (2-3 tasks)

**IMPORTANT**: This phase is executed by the /tasks command, NOT by /plan

## Phase 3+: Future Implementation
*These phases are beyond the scope of the /plan command*

**Phase 3**: Task execution (/tasks command creates tasks.md)  
**Phase 4**: Implementation (execute tasks.md following constitutional principles)  
**Phase 5**: Validation (run tests, execute quickstart.md, performance validation)

## Complexity Tracking
*Fill ONLY if Constitution Check has violations that must be justified*

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| [e.g., 4th project] | [current need] | [why 3 projects insufficient] |
| [e.g., Repository pattern] | [specific problem] | [why direct DB access insufficient] |


## Progress Tracking
*This checklist is updated during execution flow*

**Phase Status**:
- [x] Phase 0: Research complete (/plan command)
- [x] Phase 1: Design complete (/plan command)
- [x] Phase 2: Task planning complete (/plan command - describe approach only)
- [ ] Phase 3: Tasks generated (/tasks command)
- [ ] Phase 4: Implementation complete
- [ ] Phase 5: Validation passed

**Gate Status**:
- [x] Initial Constitution Check: PASS
- [x] Post-Design Constitution Check: PASS
- [x] All NEEDS CLARIFICATION resolved
- [x] Complexity deviations documented

---
*Based on Constitution v2.1.1 - See `/memory/constitution.md`*
