J: test-only stages, explicit successful-preview heading and visible password inputs, then exact-label fills with the existing test password. Real all-wallet restore, catalog equality and no-secrets assertions retained. Failure logs redact known secrets and payload-like strings; no input values or raw API payloads are read/logged. No product edits, API overrides, forced visibility/navigation, preview bypass or timeout extension. Existing focused Go entry reused.

D: five exact-case wrappers only in account_management_browser_e2e_test.go. Existing TestPWAAccountSwitch and all helper/fixture/assertion code retained.

Separate patches: exact-change.patch (J), d-entries/exact-change.patch (D). Review/hash evidence: review.json. Exact execution selectors: handoff.json. No compile, syntax test, browser or business tests executed.
