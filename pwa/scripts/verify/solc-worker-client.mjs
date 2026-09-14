import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
const source=await readFile(new URL('../../utils/solcWorkerClient.ts',import.meta.url),'utf8')
const {outputText}=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}})
const {SolcWorkerClient}=await import(`data:text/javascript;base64,${Buffer.from(outputText).toString('base64')}`)
class FakeWorker {
 onmessage=null;onerror=null;onmessageerror=null;stopped=false;messages=[]
 postMessage(data){this.messages.push(data)}
 terminate(){this.stopped=true}
 reply(data){this.onmessage?.({data})}
}
const workers=[]
const client=new SolcWorkerClient(()=>{const w=new FakeWorker();workers.push(w);return w},100)
assert.equal(workers.length,0,'lazy startup')
let pending=client.compile('standard-json');let w=workers[0];const id=w.messages[0].id
assert.equal(w.messages[0].input,'standard-json')
await assert.rejects(client.compile('parallel'),/already running/)
w.reply({id:id+100,version:'wrong',output:'wrong'})
w.reply({id,version:'0.8.36+commit',output:'{"contracts":{}}'})
assert.deepEqual(await pending,{version:'0.8.36+commit',output:'{"contracts":{}}'})
pending=client.compile('next');assert.equal(workers.length,1)
w.reply({id,version:'late',output:'late'})
w.reply({id:w.messages[1].id,error:'compile failed'})
await assert.rejects(pending,/compile failed/);assert.equal(w.stopped,true)
const oldMessage=w.onmessage // cleared handlers are an additional cleanup check
assert.equal(oldMessage,null)
pending=client.compile('retry');w=workers[1]
const late=w.onmessage,lateError=w.onerror
client.dispose();await assert.rejects(pending,/cancelled/);assert.equal(w.stopped,true)
pending=client.compile('after dispose');const fresh=workers[2]
late({data:{id:fresh.messages[0].id,version:'old-worker',output:'wrong'}});lateError({})
fresh.reply({id:fresh.messages[0].id,version:'0.8.36',output:'ok'});assert.equal((await pending).output,'ok')
pending=client.compile('decode-error');fresh.onmessageerror({});await assert.rejects(pending,/could not be read/)
pending=client.compile('malformed');w=workers.at(-1);w.reply({id:w.messages[0].id,version:'',output:'bad'});await assert.rejects(pending,/Invalid/)
const timeoutWorkers=[];const timeoutClient=new SolcWorkerClient(()=>{const x=new FakeWorker();timeoutWorkers.push(x);return x},5)
await assert.rejects(timeoutClient.compile('slow'),/timed out/);assert.equal(timeoutWorkers[0].stopped,true)
await assert.rejects(new SolcWorkerClient(()=>{throw Error('unsupported')},20).compile('x'),/could not start/)
client.dispose();timeoutClient.dispose()
console.log('Solidity worker client lifecycle checks passed')
