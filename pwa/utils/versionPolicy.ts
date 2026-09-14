export interface VersionRelease { version: string; buildId?: string; minVersion: string; forceUpdate: boolean; releaseNotes?: string; publishedAt?: string }
export function versionCompare(a: string, b: string): number | null {
  if (!/^\d+\.\d+\.\d+$/.test(a) || !/^\d+\.\d+\.\d+$/.test(b)) return null
  const aa=a.split('.').map(BigInt), bb=b.split('.').map(BigInt)
  for(let i=0;i<3;i++) if(aa[i]!==bb[i])return aa[i]>bb[i]?1:-1
  return 0
}
function buildTime(value = ''): number | null {
  const m=/^(\d{4})(\d{2})(\d{2})T(\d{2})(\d{2})(\d{2})Z$/.exec(value)
  if(!m)return null
  const iso=`${m[1]}-${m[2]}-${m[3]}T${m[4]}:${m[5]}:${m[6]}Z`, time=Date.parse(iso)
  return Number.isFinite(time)&&new Date(time).toISOString().replace('.000','')===iso?time:null
}
export class VersionPolicy {
  remote: VersionRelease | null = null
  blocked = false
  updating = false
  active = 0
  private waiters: (()=>void)[]=[]
  constructor(readonly local: string, readonly build: string, private changed: ()=>void=()=>{}) {}
  isNew(info: VersionRelease) {
    const comparison=versionCompare(info.version,this.local)
    if(comparison===1)return true
    const a=buildTime(info.buildId), b=buildTime(this.build)
    return comparison===0&&a!==null&&b!==null&&a>b
  }
  accept(value: unknown): value is VersionRelease {
    const info=value as VersionRelease
    if(!info||versionCompare(info.version,this.local)===null||versionCompare(info.minVersion,this.local)===null||typeof info.forceUpdate!=='boolean')return false
    this.remote={...info}
    // A published newer release may already be installed as a waiting worker.
    // Keep mutations on the embedded release blocked until that atomic update
    // is activated; reads and unlock remain available through begin().
    this.blocked=versionCompare(this.local,info.minVersion)===-1||this.isNew(info)
    this.changed();return true
  }
  assertAllowed(readOnly: boolean) {
    if(!readOnly&&(this.blocked||this.updating))throw Error('PWA update required before a new wallet operation')
  }
  begin(readOnly: boolean) {
    this.assertAllowed(readOnly)
    this.active++;this.changed();let ended=false
    return ()=>{if(ended)return;ended=true;this.active--;this.changed();if(!this.active){for(const done of this.waiters.splice(0))done()}}
  }
  async waitForUpdate() {
    this.updating=true;this.changed()
    if(this.active)await new Promise<void>(resolve=>this.waiters.push(resolve))
  }
  updateFailed(){this.updating=false;this.changed()}
}
