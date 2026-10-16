import { useCallback, useEffect, useRef, useState } from 'react'
import { useFleetDaemons } from '../../lib/fleet/use-fleet-daemons'
import { checkMarks, runProbeCheck, type CheckView, type MarkView } from '../../lib/fleet/probe-check'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../lib/host-look'

type T = (k: string, p?: Record<string, string>) => string
type Line =
  | { kind: 'running' }
  | { kind: 'view'; view: CheckView }
  | { kind: 'text'; key: string; params?: Record<string, string> }

const labelOf = (id: string) => hostLabel(id, hostLookOf(id))
const GLYPH = { ok: '✓', failed: '✗', unknown: '—' } as const

function Mark({ id, which, label, m, t }: { id: string; which: 'ssh' | 'measure'; label: string; m: MarkView; t: T }) {
  const tone = m.mark === 'ok' ? 'text-green-400' : m.mark === 'failed' ? 'text-red-400' : 'text-text-muted'
  return (
    <span data-testid={`probe-check-${id}-${which}`} className="inline-flex items-center gap-1">
      <span className="text-text-secondary">{label}</span>
      <span className={tone}>{GLYPH[m.mark]}</span>
      {m.reasonKey && <span className="text-text-muted">{t(m.reasonKey)}</span>}
    </span>
  )
}

/**
 * 「連線檢查」 (fleet spec §8.2): one line per fleet daemon host, each reaching the probe host through its OWN alias
 * (`aliases`, by App host id — the SAVED ones). Runs on open, when the saved aliases change, and on 「重新檢查」.
 */
export function ProbeCheck({ aliases }: { aliases: Record<string, string> }) {
  const t = useI18nStore((s) => s.t) as T
  const { known } = useFleetDaemons()
  const runtime = useHostStore((s) => s.runtime)
  const [lines, setLines] = useState<Record<string, Line>>({})
  const [running, setRunning] = useState(false)
  const seq = useRef(0)
  const latest = useRef({ known, aliases, runtime })
  latest.current = { known, aliases, runtime }

  const run = useCallback(async () => {
    const my = ++seq.current
    const { known: hosts, aliases: al, runtime: rt } = latest.current
    const next: Record<string, Line> = {}
    const asking: string[] = []
    for (const id of hosts) {
      if (rt[id]?.status !== 'connected') next[id] = { kind: 'text', key: 'fleet_hosts.check.offline', params: { host: labelOf(id) } }
      else if (!al[id]) next[id] = { kind: 'text', key: 'fleet_hosts.check.no_alias', params: { host: labelOf(id) } }
      else { next[id] = { kind: 'running' }; asking.push(id) }
    }
    setLines(next)
    setRunning(asking.length > 0)
    await Promise.all(asking.map(async (id) => {
      const r = await runProbeCheck(id, al[id])
      if (seq.current !== my) return
      const line: Line = r.kind === 'answer' ? { kind: 'view', view: checkMarks(r.answer) }
        : r.kind === 'unsupported' ? { kind: 'text', key: 'fleet_hosts.check.unsupported', params: { host: labelOf(id) } }
        : { kind: 'text', key: 'fleet_hosts.check.error', params: { host: labelOf(id), text: r.text } }
      setLines((p) => ({ ...p, [id]: line }))
    }))
    if (seq.current === my) setRunning(false)
  }, [])

  const connected = known.map((id) => runtime[id]?.status === 'connected')
  const key = JSON.stringify([known, aliases, connected])
  useEffect(() => {
    void run()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- `key` covers the daemon list, whether each is connected, and the saved aliases
  }, [key])

  return (
    <div data-testid="probe-check" className="mt-6">
      <div className="flex items-center justify-between mb-2">
        <h3 className="text-sm font-semibold">{t('fleet_hosts.check.title')}</h3>
        <button type="button" data-testid="probe-check-rerun" disabled={running} onClick={() => void run()}
          className="px-3 py-1 rounded text-xs bg-surface-tertiary text-text-secondary cursor-pointer disabled:opacity-50">
          {t(running ? 'fleet_hosts.check.running' : 'fleet_hosts.check.rerun')}
        </button>
      </div>
      <ul className="border border-border-subtle rounded-lg divide-y divide-border-subtle">
        {known.map((id) => {
          const l = lines[id]
          return (
            <li key={id} data-testid={`probe-check-${id}`} className="px-3 py-2 text-xs flex flex-wrap items-center gap-x-4 gap-y-1">
              {l?.kind === 'view' ? (
                <>
                  <span className="w-24 shrink-0 truncate text-text-primary">{labelOf(id)}</span>
                  <Mark id={id} which="ssh" label={t('fleet_hosts.check.ssh')} m={l.view.ssh} t={t} />
                  <Mark id={id} which="measure" label={t('fleet_hosts.check.measure')} m={l.view.measure} t={t} />
                  {l.view.note && <span className="text-text-muted">{t(l.view.note)}</span>}
                </>
              ) : l?.kind === 'text' ? (
                <span className="text-status-warning-text">{t(l.key, l.params)}</span>
              ) : (
                <span className="text-text-muted">{labelOf(id)} · {t('fleet_hosts.check.running')}</span>
              )}
            </li>
          )
        })}
      </ul>
    </div>
  )
}
