import { spawn } from 'node:child_process'
import { mkdtempSync, writeFileSync, readdirSync, rmSync, mkdirSync, existsSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { once } from 'node:events'
import { createRunnerResources } from './pwa-runner-resources.mjs'

test('each group owns a different writable Vite cache',async()=>{
 const a=createRunnerResources(),b=createRunnerResources()
 try {assert.notEqual(a.cacheDir,b.cacheDir)
 mkdirSync(a.cacheDir,{recursive:true});writeFileSync(join(a.cacheDir,'group-A-pending'),'A')
 assert.equal(existsSync(join(b.cacheDir,'group-A-pending')),false)
 await a.close(new Error('intentional A failure'))
 assert.equal(existsSync(b.cacheDir),false)
 mkdirSync(b.cacheDir,{recursive:true});writeFileSync(join(b.cacheDir,'B'),'B')
 assert.equal(existsSync(join(b.cacheDir,'B')),true)
 } finally {await a.close();await b.close()}
})
test('setup first error survives and later resources close after a close failure',async()=>{
 const scope=createRunnerResources(); const server=createServer((q,r)=>r.end('test'))
 server.listen(0,'127.0.0.1');await once(server,'listening')
 const first=new Error('setup-first'); const closed=[]
 scope.own('server',{async close(){await new Promise((r,j)=>server.close(e=>e?j(e):r()));closed.push('server')}})
 scope.own('broken',{async close(){closed.push('broken');throw new Error('close-second')}})
 try {await scope.close(first);assert.deepEqual(closed,['broken','server']);assert.equal(server.listening,false)}
 finally {if(server.listening)await new Promise(r=>server.close(r))}
})
test('cleanup errors fail a successful run after every resource was attempted',async()=>{
 const scope=createRunnerResources();const closed=[]
 scope.own('last',{async close(){closed.push('last')}})
 scope.own('broken',{async close(){throw new Error('close-failed')}})
 await assert.rejects(()=>scope.close(),/cleanup/);assert.deepEqual(closed,['last'])
})

test('actual runner setup failure closes DApp and Vite before exiting',async()=>{
 const root=mkdtempSync(join(tmpdir(),'sat20-runner-setup-'))
 const fixture=join(root,'fixture.json');writeFileSync(fixture,JSON.stringify({wasmPath:'intentional-incomplete-fixture',config:{Chain:'testnet',IndexerL2:{Scheme:'http',Host:'127.0.0.1:1',Proxy:'testnet'}}}))
 try {
  const child=spawn(process.execPath,['scripts/verify/account-management-e2e.mjs','--pos-fixture',fixture],{cwd:process.cwd(),env:{...process.env,TMPDIR:root},stdio:['ignore','pipe','pipe']})
  let stderr='';child.stderr.on('data',b=>stderr+=b);child.stdout.resume()
  const timer=setTimeout(()=>child.kill('SIGKILL'),15000)
  const [code,signal]=await once(child,'exit');clearTimeout(timer)
  assert.equal(signal,null,'setup error left an active owned resource');assert.equal(code,1)
  assert.match(stderr,/both current SDK WASM and its Go runtime are required/)
  assert.deepEqual(readdirSync(root),['fixture.json'],'temporary Vite cache must be reclaimed')
 } finally {rmSync(root,{recursive:true,force:true})}
})
