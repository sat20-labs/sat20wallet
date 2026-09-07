import soljsonModule from 'solc/soljson.js'

type SoljsonModule = {
  cwrap: (
    name: string,
    returnType: string,
    argumentTypes: string[]
  ) => (...args: unknown[]) => string
}

const soljson = soljsonModule as unknown as SoljsonModule
if (!soljson || typeof soljson.cwrap !== 'function') {
  throw new Error('Solidity compiler module is unavailable')
}

// solc 0.8.x exposes the standard JSON compiler through solidity_compile.
// Passing null callbacks is sufficient here because Tools.vue provides all
// sources inline and deliberately does not support external imports.
const compileStandard = soljson.cwrap(
  'solidity_compile',
  'string',
  ['string', 'number', 'number']
)
const compilerVersion = soljson.cwrap('solidity_version', 'string', [])

const compile = (input: string) => compileStandard(input, 0, 0)
const version = () => compilerVersion()

// Match the subset of solc-js used by Tools.vue. The normal solc wrapper also
// includes remote-version/network helpers that pull Node http/stream modules
// into the browser bundle; those helpers are intentionally absent here.
export default { compile, version }
