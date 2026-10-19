// spa/src/lib/pool/job-control.ts — which changes the 「⋯」 menu of a job offers, and to which daemon they go (plan "WWD-7 design", Jobs tab). The menu exists only where a daemon announces `jobs.control.v1`:
//   - cancel goes to a daemon that announces it — the origin's own first, else the first forwarder — with `?host=<origin>`, and the daemon forwards it to the origin's wwd when it is not the origin;
//   - the manual place (最前／最後／暫緩／恢復) goes only to the origin's own daemon, the one wwd sets it on; an origin the App has no daemon for (mserv, air19) offers cancel only.
// Cancelling a job that is going (claimed or running) asks first; a queued one is cancelled at once (user Q5).
import { useMemo } from 'react'
import { useHostStore } from '../../stores/useHostStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { liveTopology } from './jobs-driver-live'
import { JOBS_CONTROL_CAPABILITY, type PositionChoice } from './jobs-control-api'
import { usePoolStore } from './store'
import type { PoolJob } from './jobs-types'

/** The daemons a change goes to; null: no daemon can take it. */
export interface JobControl { cancelVia: string | null; positionVia: string | null }

export type MenuAction = { kind: 'position'; position: PositionChoice } | { kind: 'cancel'; confirm: boolean }

/** `announces(id)`: the daemon announces `jobs.control.v1` (and is connected, its capabilities read on this connection). */
export function controlVia(origin: string, topo: { ownDaemon: Readonly<Record<string, string>>; forwarders: readonly string[] }, announces: (id: string) => boolean): JobControl {
  const own = Object.hasOwn(topo.ownDaemon, origin) ? topo.ownDaemon[origin] : null
  const cancelVia = [...(own === null ? [] : [own]), ...topo.forwarders].find(announces) ?? null
  const positionVia = own !== null && announces(own) ? own : null
  return { cancelVia, positionVia }
}

/** The items for one job, in the menu's order. A place the job already has is not offered again; 恢復 only when it has one. */
export function menuActions(job: Pick<PoolJob, 'state' | 'manual'>, ctl: JobControl): MenuAction[] {
  const out: MenuAction[] = []
  if (job.state === 'queued') {
    if (ctl.positionVia !== null) {
      for (const p of ['front', 'back', 'hold'] as const) if (job.manual !== p) out.push({ kind: 'position', position: p })
      if (job.manual !== '') out.push({ kind: 'position', position: 'normal' })
    }
    if (ctl.cancelVia !== null) out.push({ kind: 'cancel', confirm: false })
  } else if ((job.state === 'claimed' || job.state === 'running') && ctl.cancelVia !== null) {
    out.push({ kind: 'cancel', confirm: true })
  }
  return out
}

/** The daemons for `origin`, worked out again when the capabilities, the connections or the pool's hosts move; the string keeps a change that moves nothing from re-rendering. */
export function useJobControl(origin: string): JobControl {
  const hosts = usePoolStore((s) => s.hosts)
  const caps = useNexHostStore((s) => s.byHost)
  const runtime = useHostStore((s) => s.runtime)
  const appHosts = useHostStore((s) => s.hosts)
  const sig = useMemo(() => {
    const { cancelVia, positionVia } = controlVia(origin, liveTopology(), (id) => caps[id]?.daemonCapabilities?.includes(JOBS_CONTROL_CAPABILITY) === true)
    return `${cancelVia ?? ''}\u0000${positionVia ?? ''}`
  // eslint-disable-next-line react-hooks/exhaustive-deps -- the four stores are what liveTopology and the capabilities read; they are the reason to work it out again
  }, [origin, hosts, caps, runtime, appHosts])
  return useMemo(() => {
    const [c, p] = sig.split('\u0000')
    return { cancelVia: c === '' ? null : c, positionVia: p === '' ? null : p }
  }, [sig])
}
