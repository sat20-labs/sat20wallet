import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
// Resources belong to this runner invocation. Never remove shared dependencies.
export function createRunnerResources() {
 const resources=[]
 const root=mkdtempSync(join(tmpdir(),'sat20-pwa-vite-'))
 let closing
 return { cacheDir:join(root,'cache'),
  own(name,resource){resources.push({name,resource});return resource},
  close(primary){return closing??=(async()=>{
   const failures=[]
   for(const {name,resource} of resources.reverse()) {
    try {await resource.close()} catch(error){failures.push(new Error(`cleanup ${name} failed`,{cause:error}))}
   }
   try {rmSync(root,{recursive:true,force:true})} catch(error){failures.push(new Error('cleanup cache failed',{cause:error}))}
   if(failures.length){
    if(primary) console.error(JSON.stringify({event:'runner-cleanup-errors',count:failures.length,primary_preserved:true}))
    else throw new AggregateError(failures,'runner cleanup failed')
   }
  })()}
 }
}
