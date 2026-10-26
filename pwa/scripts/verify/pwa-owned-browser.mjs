import { writeSync } from 'node:fs'
// Node writes only to the anonymous pipe inherited from this Go invocation.
export async function registerOwnedBrowser(browser) {
 const raw=process.env.SAT20_PWA_BROWSER_OWNER_FD
 if(!raw) return
 const fd=Number(raw)
 if(!Number.isInteger(fd)||fd<3) throw new Error('invalid owner pipe descriptor')
 const session=await browser.newBrowserCDPSession()
 try {
  const {processInfo}=await session.send('SystemInfo.getProcessInfo')
  const pid=Number(processInfo.find(p=>p.type==='browser')?.id)
  if(!Number.isSafeInteger(pid)||pid<=1) throw new Error('missing owned browser PID')
  writeSync(fd,JSON.stringify({node:process.pid,browser:pid,pids:processInfo.map(p=>Number(p.id))})+'\n')
 } finally { await session.detach() }
}
