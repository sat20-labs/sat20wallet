import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
const base=new URL('../../', import.meta.url).pathname
const password=readFileSync(base+'components/common/WalletPasswordDialog.vue','utf8')
const confirm= password.slice(password.indexOf('const confirm = async'), password.indexOf('const unsubscribe ='))
const wrapper=readFileSync(base+'utils/sat20.ts','utf8')
const handle=wrapper.slice(wrapper.indexOf('  private async _handleRequest'),wrapper.indexOf('  async createWallet'))
const transform=source=>ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText
const settle=async()=>{for(let i=0;i<15;i++)await Promise.resolve()}
for(const blocked of ['begin-log','warm-unlock','finish-log']) {
 let resume; const pending=new Promise(resolve=>resume=resolve)
 let wasmCalls=0, finishes=0
 const events=[]
 const traceSource=readFileSync(base+'utils/walletAwaitTrace.ts','utf8').replaceAll('export ', '').replaceAll('import.meta.env.VITE_WALLET_AWAIT_TRACE', '"1"')
 const traceContext={Date,window:{dispatchEvent:e=>events.push(e.detail)},CustomEvent:class {constructor(type,init){this.detail=init.detail}}}
 vm.runInNewContext(transform(traceSource+'\nglobalThis.startTrace=beginWalletAwaitTrace'),traceContext)
 const ctx={console:{error(){}},Error,String,
  beginWalletAwaitTrace:traceContext.startTrace,
  walletRequestSessionGuard:()=>()=>{},
  tryit:fn=>async()=>{try{return [undefined,await fn()]}catch(error){return [error,undefined]}},
  beginPwaWalletOperation:async()=>{if(blocked==='begin-log')await pending;return {}},
  finishPwaOperation:async()=>{if(blocked==='finish-log')await pending},
  beginVersionDispatch:()=>()=>{},
  sat20wallet_wasm:{unlockWallet:async()=>{wasmCalls++;if(blocked==='warm-unlock')await pending;return {code:0,data:{walletId:'existing'}}}},
  busy:{value:false},error:{value:''},passwordInput:{value:{value:'test-only'}},resolve:()=>{},
  limiter:{assertAllowed(){},recordFailure(){},reset(){}},t:key=>key,
  finish:()=>{finishes++},
 }
 vm.runInNewContext(transform('class Probe { '+handle+' }\nglobalThis.walletManager = {unlockWallet: p => new Probe()._handleRequest("unlockWallet",p)};\n'+confirm+'\nglobalThis.start=confirm;'),ctx)
 const task=ctx.start();await settle()
 assert.equal(events.at(-1).phase,{'begin-log':'begin-log','warm-unlock':'dispatch','finish-log':'finish-log'}[blocked])
 assert.ok(events.every(e=>Object.keys(e).sort().join(',')==='elapsedMs,method,phase,request'))
 assert.equal(ctx.busy.value,true);assert.equal(finishes,0)
 assert.equal(wasmCalls,blocked==='begin-log'?0:1)
 resume();await task;assert.equal(finishes,1)
 console.log(`${blocked}: password modal remains awaiting; business import callback not reached; resumes when await resolves`)
}
const policy=readFileSync(base+'utils/versionPolicy.ts','utf8').replaceAll('export ','')
const ctx={};vm.runInNewContext(transform(policy+'\nglobalThis.Policy=VersionPolicy'),ctx)
const p=new ctx.Policy('0.1.38','20260913T160637Z');p.blocked=true
p.begin(true)()
assert.throws(()=>p.begin(false),/update required/)
console.log('version blocked: unlock/read permitted, import/write rejected; banner is a write-test precondition blocker')

console.log('Diagnostic events contain only stage, method, request number and elapsed time; no credentials/results')

const disabledSource=readFileSync(base+'utils/walletAwaitTrace.ts','utf8').replaceAll('export ', '').replaceAll('import.meta.env.VITE_WALLET_AWAIT_TRACE', 'undefined')
const disabled={};vm.runInNewContext(transform(disabledSource+'\nglobalThis.startTrace=beginWalletAwaitTrace'),disabled)
assert.doesNotThrow(()=>disabled.startTrace('unlockWallet')('begin-log'), 'disabled diagnostics do not access window or emit')
