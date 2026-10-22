# Solidity E2E fixtures

These files contain real solc output. E2E loads them directly and never invokes
solc, npx or an online compiler. Source SHA-256, contract name and compiler
profile are checked before deployment; stale or missing output fails the test.

Current compiler: solc 0.8.35+commit.47b9dedd. Profile: EVM paris, optimizer enabled,
200 runs. Keep the source and artifact together when changing a contract.

Regenerate offline from the SDK directory, once for each concrete contract:

```sh
go run ./e2e/cmd/solidity-compile --contract Counter e2e/testdata/contracts/Counter.sol > e2e/testdata/contracts/artifacts/Counter.Counter.json
```

Use the same command for each contract in AssetApps.sol, StandardApps.sol and
SDKReviewProbe.sol. Compilation is a fixture maintenance step, outside test runs.
