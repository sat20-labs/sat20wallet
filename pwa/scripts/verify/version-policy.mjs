import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
const transpile=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
const url=s=>`data:text/javascript;base64,${Buffer.from(s).toString('base64')}`
const source=await readFile(new URL('../../utils/versionPolicy.ts',import.meta.url),'utf8')
const policyURL=url(transpile(source));const {VersionPolicy}=await import(policyURL)
const make=()=>new VersionPolicy('1.2.3','20260909T010000Z')
const release={version:'1.2.3',buildId:'20260909T010000Z',minVersion:'1.0.0',forceUpdate:false}
let p=make();assert.equal(p.blocked,false);p.begin(false)(); // first unknown retains capability
assert.equal(p.accept({...release,minVersion:'2.0.0'}),true);assert.equal(p.blocked,true)
assert.equal(p.accept({version:'bad'}),false);assert.equal(p.blocked,true) // invalid/offline never clears known
for(const info of [{...release,buildId:'20260909T020000Z'},{...release,version:'1.2.4'}]){p=make();assert.equal(p.accept(info),true);assert.equal(p.blocked,true);assert.throws(()=>p.begin(false));p.begin(true)()}
for(const [build,blocked] of [['20260909T020000Z',true],['20260909T000000Z',false],['garbage',false],['20260230T010000Z',false],['20260909T010000Z',false]]){p=make();p.accept({...release,buildId:build,forceUpdate:true});assert.equal(p.blocked,blocked)}
p=make();p.accept({...release,version:'1.2.4',forceUpdate:true});assert.equal(p.blocked,true)
p=make();const finish=p.begin(false);let updated=false;const wait=p.waitForUpdate().then(()=>{updated=true});assert.throws(()=>p.begin(false));assert.equal(updated,false);finish();await wait;assert.equal(updated,true);p.updateFailed();assert.equal(p.updating,false)
// Exercise the actual shared cache module across reload/environment/release.
const values=new Map();globalThis.localStorage={getItem:k=>values.get(k)??null,setItem:(k,v)=>values.set(k,v)}
const sharedSource=await readFile(new URL('../../utils/pwaVersionPolicy.ts',import.meta.url),'utf8')
const shared=async(build='20260909T010000Z',mode='production',nonce='')=>{
 let text=transpile(sharedSource).replace(/import \{ ref \} from 'vue';/, 'const ref=value=>({value});').replace("from './versionPolicy'",`from '${policyURL}'`).replaceAll('__SAT20_APP_VERSION__',"'1.2.3'").replaceAll('__SAT20_BUILD_ID__',JSON.stringify(build)).replaceAll('import.meta.env.VITE_SAT20_VERSION_URL',"''").replaceAll('import.meta.env.BASE_URL',"'/pwa/'").replaceAll('import.meta.env.MODE',JSON.stringify(mode));
 return import(url(text+'\n//'+nonce))
}
let singleton=await shared();singleton.acceptVersionPolicy({...release,minVersion:'2.0.0'})
assert.equal((await shared(undefined,undefined,'reload')).versionPolicy.blocked,true)
assert.equal((await shared('20260909T030000Z')).versionPolicy.blocked,false)
assert.equal((await shared(undefined,'development')).versionPolicy.blocked,false)
// Real facade modules, mocked WASM/logging only. Boundary guards remain real.
globalThis.fixturePolicy=singleton
let pauseLog=null,finishLog=null,calls=0
const helpers=`const {beginVersionDispatch}=globalThis.fixturePolicy;
const tryit=fn=>async(...args)=>{try{return [undefined,await fn(...args)]}catch(e){return [e,undefined]}};
const walletRequestSessionGuard=()=>()=>{};
const beginWalletAwaitTrace=()=>()=>{};
const beginPwaWalletOperation=async()=>{if(globalThis.pauseLog)await globalThis.pauseLog;return {}};
const beginAccountManagementOperation=beginPwaWalletOperation;
const finishPwaOperation=async()=>{if(globalThis.finishLog)await globalThis.finishLog};\n`
const facades=[]
for(const name of ['sat20','stp','rgb11Address','accountManagement']){
 let text=transpile(await readFile(new URL(`../../utils/${name}.ts`,import.meta.url),'utf8'))
 text=text.replace(/^import .*?;\n/gm,'')
 facades.push((await import(url(helpers+text))).default)
}
const mutations=[()=>facades[0].sendAssets('a','::','330',1),()=>facades[1].openChannel('a','1','1'),()=>facades[2].prepareTransfer({}),()=>facades[3].fundAutopay()]
let resolveWasm;globalThis.sat20wallet_wasm=new Proxy({}, {get:()=>async()=>{calls++;return {code:0,data:{ok:true}}}});globalThis.sat20account_wasm=globalThis.sat20wallet_wasm
for(const mutate of mutations){const before=calls;try{const result=await mutate();assert.ok(result[0] instanceof Error)}catch(e){assert.match(e.message,/update required/)}assert.equal(calls,before)}
await facades[0].getVersion();await facades[3].status();assert.equal(calls,2)
// Policy arrives while async operation logging is pending: no dispatch.
singleton.acceptVersionPolicy(release);let resumeLog;globalThis.pauseLog=new Promise(r=>resumeLog=r);const racing=mutations[0]();singleton.acceptVersionPolicy({...release,minVersion:'2.0.0'});resumeLog();await racing;assert.equal(calls,2);globalThis.pauseLog=null
// Policy after dispatch cannot discard result. Update also waits for result log.
singleton.acceptVersionPolicy(release);globalThis.sat20wallet_wasm.sendAssets=undefined
// Replace Proxy with a controllable real method.
globalThis.sat20wallet_wasm={sendAssets:async()=>{calls++;return new Promise(r=>resolveWasm=r)}}
const inFlight=mutations[0]();await new Promise(r=>setTimeout(r,0));singleton.acceptVersionPolicy({...release,minVersion:'2.0.0'})
let releaseLog;globalThis.finishLog=new Promise(r=>releaseLog=r)
let drained=false;const drain=singleton.versionPolicy.waitForUpdate().then(()=>drained=true)
resolveWasm({code:0,data:{txId:'preserved'}});await new Promise(r=>setTimeout(r,0));assert.equal(drained,false)
releaseLog();const [error,result]=await inFlight;await drain;assert.equal(error,undefined);assert.equal(result.txId,'preserved');assert.equal(drained,true)
console.log('Version policy, cache and four facade boundary checks passed')
