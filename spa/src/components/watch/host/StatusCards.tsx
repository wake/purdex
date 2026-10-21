// spa/src/components/watch/host/StatusCards.tsx — the six cards on the top of the host page's 狀態 tab (monitoring tabs spec M44, third-wave decision 8; plan MON-7b-2): 判斷, Worker 與主人, 連線, 暫停, 版本與身分, 標籤・工具・fingerprint.
// The data is `lib/pool/host-status.ts`'s; this draws it. A host that is not ok has no reading, so the first, second and fourth cards say that (with the state's word); the others are drawn from the pool's row.
// The only thing read here is the pauses' detail (`GET …/exceptions`), and only when the facts list a pause: it is re-read when the list of names changes, and "reading" shows until the answer is in.
import { useEffect, useMemo, useState, type ReactNode } from 'react'
import { Gauge, PauseCircle, PlayCircle, Plugs, StackSimple, Wrench } from '@phosphor-icons/react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { useHostStore } from '../../../stores/useHostStore'
import { useNexHostStore } from '../../../stores/useNexHostStore'
import { hostLabel, useHostLookResolver } from '../../../lib/host-look'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import { usePoolStore } from '../../../lib/pool/store'
import { useAlertsStore } from '../../../lib/pool/alerts-store'
import { controlSource, fetchPauses } from '../../../lib/pool/control-api'
import { connectionOf, identityOf, judgeOf, pausesOf, tagsOf, workerCardOf, type Judge, type LoadPart } from '../../../lib/pool/host-status'
import { clock, driverKey, livenessWordOf, reasonWord } from '../../../lib/pool/words'
import type { PoolExceptionDetail, PoolFacts, PoolHost } from '../../../lib/pool/types'

type T = (key: string, opts?: Record<string, string | number>) => string
const pad = (n: number): string => String(n).padStart(2, '0')
const dayTime = (ms: number): string => { const d = new Date(ms); return `${d.getMonth() + 1}/${d.getDate()} ${pad(d.getHours())}:${pad(d.getMinutes())}` }
const levelWord = (t: T, level: number | null): string => t(`pool.level.${level ?? 0}`)

function Card({ id, icon, title, children }: { id: string; icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <section data-testid={`status-card-${id}`} className="min-w-0 rounded-lg border border-border-default bg-surface-secondary p-3">
      <h3 className="mb-2 flex items-center gap-1.5 text-xs font-medium text-text-secondary">{icon}{title}</h3>
      <div className="flex flex-col gap-1 text-xs text-text-primary">{children}</div>
    </section>
  )
}

function Line({ label, testId, children }: { label: string; testId?: string; children: ReactNode }) {
  return (
    <div className="flex gap-2" data-testid={testId}>
      <span className="w-20 shrink-0 text-text-muted">{label}</span>
      <span className="min-w-0 break-words">{children}</span>
    </div>
  )
}

const NoReading = ({ t, host }: { t: T; host: MonitorHost }) => <p className="text-text-muted">{t('watch.host.status.no_reading', { state: livenessWordOf(t, host.state.state) })}</p>

// ---- 判斷 ---------------------------------------------------------------------------------------------------------------------------------------------------------

/** Why a class is at what it is: the daemon's reasons; for a 0 with none named, "unknown"; a daemon that sent no reasons gets nothing (no reason is invented). */
function slotWords(t: T, n: number, codes: string[] | null): string | null {
  if (codes === null) return null
  if (codes.length > 0) return codes.map((c) => reasonWord(c, t)).join('、')
  return n === 0 ? reasonWord('unknown', t) : null
}

function LoadBar({ part, t }: { part: LoadPart; t: T }) {
  const faint = part.id === 'disk' && !part.counted
  return (
    <div data-testid={`load-part-${part.id}`} data-driver={String(part.driver)} data-counted={String(part.counted)} className={`flex items-center gap-2 ${faint ? 'opacity-40' : ''}`}>
      <span className={`w-12 shrink-0 ${part.driver ? 'font-semibold text-text-primary' : 'text-text-muted'}`}>{t(`pool.driver.${part.id}`)}</span>
      <span className="h-1.5 flex-1 rounded bg-surface-tertiary"><i className={`block h-full rounded ${part.driver ? 'bg-accent' : 'bg-text-muted'}`} style={{ width: `${Math.min(Math.max(part.score, 0), 100)}%` }} /></span>
      <b className="w-7 text-right font-mono text-[11px]">{Math.round(part.score)}</b>
      {part.id === 'disk' && <em className="text-[10px] not-italic text-text-muted">{t('watch.host.status.judge.disk_from')}</em>}
    </div>
  )
}

function JudgeCard({ judge, t }: { judge: Judge; t: T }) {
  const heavy = slotWords(t, judge.heavy.n, judge.heavy.codes)
  const light = slotWords(t, judge.light.n, judge.light.codes)
  const driver = judge.load ? (driverKey(judge.load.driver) ? t(driverKey(judge.load.driver) as string) : judge.load.driver) : null
  return (<>
    <Line label={t('watch.host.status.judge.zone')} testId="judge-zone">
      <span>{t(`fleet.zone.${judge.zone}`)}</span>{' '}
      <span className="text-text-muted">{t('watch.host.status.judge.from', { level: levelWord(t, judge.level), rate: judge.pageRate === null ? '—' : Math.round(judge.pageRate) })}</span>
    </Line>
    <Line label={t('watch.host.status.judge.heavy')} testId="judge-heavy">{judge.heavy.n}{heavy !== null && <span className="ml-1 text-text-muted">{heavy}</span>}</Line>
    <Line label={t('watch.host.status.judge.light')} testId="judge-light">{judge.light.n}{light !== null && <span className="ml-1 text-text-muted">{light}</span>}</Line>
    <Line label={t('watch.host.status.judge.sessions_label')}>{t('watch.host.status.judge.sessions', { n: judge.sessionsRoom })}</Line>
    {judge.load && <Line label={t('watch.host.status.judge.load')} testId="judge-load">{Math.round(judge.load.score)}<span className="ml-1 text-text-muted">{t('watch.host.status.judge.driver', { driver: driver ?? '' })}</span></Line>}
    <div className="mt-1 flex flex-col gap-1">{judge.parts.map((p) => <LoadBar key={p.id} part={p} t={t} />)}</div>
  </>)
}

// ---- Worker 與主人 -------------------------------------------------------------------------------------------------------------------------------------------

function WorkerCard({ facts, t }: { facts: PoolFacts; t: T }) {
  const w = workerCardOf(facts)
  const owner = w.owner === null
    ? t('watch.host.status.worker.owner_none')
    : w.owner.active ? t('pool.worker.owner_active', { reason: w.owner.reason }) : t('pool.worker.owner_idle', { min: Math.floor(w.owner.idle_s / 60) })
  return (<>
    <Line label={t('watch.host.status.worker.enabled')}>{w.enabled ? t('watch.host.status.worker.on') : <span className="text-text-muted">{t('watch.host.status.worker.off')}</span>}</Line>
    <Line label={t('watch.host.status.worker.slots_label')}>{t('watch.host.status.worker.slots', { heavy: w.slots.heavy, light: w.slots.light })}</Line>
    <Line label={t('watch.host.status.worker.rule')}>{w.ownerRule === '' ? <span className="text-text-muted">{t('watch.host.status.worker.rule_none')}</span> : w.ownerRule}</Line>
    <Line label={t('watch.host.status.worker.owner')}>{owner}</Line>
  </>)
}

// ---- 連線 ---------------------------------------------------------------------------------------------------------------------------------------------------------

function ConnCard({ host, t }: { host: MonitorHost; t: T }) {
  const byHost = useAlertsStore((s) => s.byHost)
  const poolHosts = usePoolStore((s) => s.hosts)
  const known = poolHosts.map((h) => ({ hostId: h.host_id, alias: h.alias }))
  const hosts = known.some((h) => h.hostId === host.hostId) ? known : [...known, { hostId: host.hostId, alias: host.alias }]
  const conn = connectionOf(host, byHost, hosts)
  if (conn.kind === 'local') return <><Line label={t('watch.host.status.conn.now')}>{t('pool.liveness.ok')}</Line><p className="text-text-muted">{t('watch.host.status.conn.local')}</p></>
  return (<>
    <Line label={t('watch.host.status.conn.now')}>{livenessWordOf(t, conn.state)}</Line>
    {conn.since !== null && <Line label={t('watch.host.status.conn.since')}>{dayTime(conn.since)}</Line>}
    {conn.checkedAt !== null && <Line label={t('watch.host.status.conn.checked')}>{clock(conn.checkedAt)}</Line>}
    <Line label={t('watch.host.status.conn.outages')} testId="conn-outages">
      {conn.outages.rows.length === 0
        ? <span className="text-text-muted">{t('watch.host.status.conn.outages_none')}</span>
        : <>
          {conn.outages.rows.map((r) => <div key={r.id}>{dayTime(r.at)}–{r.endAt === null ? t('watch.host.status.conn.until_now') : clock(r.endAt)} {r.title}</div>)}
          {conn.outages.more > 0 && <div className="text-text-muted">{t('watch.host.status.conn.more', { n: conn.outages.more })}</div>}
        </>}
    </Line>
  </>)
}

// ---- 暫停 ---------------------------------------------------------------------------------------------------------------------------------------------------------

function PauseCard({ host, facts, t }: { host: MonitorHost; facts: PoolFacts; t: T }) {
  const poolSource = usePoolStore((s) => s.sourceHostId)
  useNexHostStore((s) => s.byHost)
  useHostStore((s) => s.runtime)
  // `controlSource` prefers the host the pool came from when it can control, and otherwise the first that can.
  const readFrom = controlSource(poolSource)
  const names = facts.exceptions.map((e) => e.name).join('\n')
  const key = `${readFrom}|${host.alias}|${names}`
  // What was read belongs to the host it was read from, the host it is about and the names it was read for (so it is dropped when nothing can control any more); a new set of names is read again, and "reading" shows until it is in (a read that failed or a host that has none is null: not known).
  const [read, setRead] = useState<{ key: string; details: PoolExceptionDetail[] | null } | null>(null)
  useEffect(() => {
    if (names === '' || readFrom === null) return
    let live = true
    void fetchPauses(readFrom, host.alias).then((details) => { if (live) setRead({ key, details }) })
    return () => { live = false }
  }, [readFrom, host.alias, names, key])
  const loading = names !== '' && readFrom !== null && read?.key !== key
  const pauses = useMemo(() => pausesOf(facts.exceptions, read?.key === key ? read.details : null, facts.at), [facts.exceptions, facts.at, read, key])
  if (loading) return <p data-testid="pause-loading" className="text-text-muted">{t('watch.host.status.pause.loading')}</p>
  if (pauses.length === 0) return <p className="text-text-muted">{t('watch.host.status.pause.none')}</p>
  return (<>
    {pauses.map((p) => (
      <div key={p.name} data-testid={`pause-${p.name}`} className="flex flex-col">
        <span className="font-medium">{p.name}</span>
        <span className="text-text-muted">
          {p.holds === null ? t('watch.host.status.pause.holds_unknown') : t('watch.host.status.pause.holds', { holds: p.holds.map((h) => t(`pool.card.class.${h}`)).join('＋') })}
          {'・'}{t('watch.host.status.pause.since', { time: clock(p.since) })}
          {p.until !== null && `・${t('watch.host.status.pause.until', { time: clock(p.until) })}`}
          {p.pid !== null && `・${t('watch.host.status.pause.pid', { pid: p.pid })}`}
        </span>
      </div>
    ))}
  </>)
}

// ---- 版本與身分 ----------------------------------------------------------------------------------------------------------------------------------------------

function IdentCard({ host, pool, facts, t }: { host: MonitorHost; pool: PoolHost; facts: PoolFacts | null; t: T }) {
  const lookOf = useHostLookResolver()
  const id = identityOf(pool, facts)
  const daemon = host.daemonHostId === null ? t('watch.host.status.ident.daemon_no') : t('watch.host.status.ident.daemon_yes', { name: hostLabel(host.daemonHostId, lookOf(host.daemonHostId)) })
  return (<>
    <Line label="wwd">{id.version}・proto {id.proto}{id.factsV !== null && `・facts v${id.factsV}`}</Line>
    <Line label={t('watch.host.status.ident.platform')}>{id.platform} {id.arch}{id.osVersion !== null && `・${id.osVersion}`}</Line>
    <Line label="host_id"><span className="font-mono">{id.hostId}</span></Line>
    <Line label="Purdex daemon" testId="ident-daemon">{daemon}</Line>
  </>)
}

// ---- 標籤、工具與 fingerprint ---------------------------------------------------------------------------------------------------------------------------------

const GROUP_CLASS = { task: 'border-status-success text-status-success-text', repo: 'border-accent text-accent', other: 'border-border-default text-text-secondary' } as const
const Chip = ({ children, className = '', ...rest }: { children: ReactNode; className?: string; 'data-testid'?: string; 'data-group'?: string }) => (
  <span {...rest} className={`rounded border px-1.5 py-px text-[11px] ${className}`}>{children}</span>
)

function TagsCard({ pool, facts, t }: { pool: PoolHost; facts: PoolFacts | null; t: T }) {
  const tags = tagsOf(pool, facts)
  return (<>
    <div className="flex flex-wrap gap-1">{tags.labels.map((l) => <Chip key={l.text} data-group={l.group} className={GROUP_CLASS[l.group]}>{l.text}</Chip>)}</div>
    <div className="flex flex-wrap gap-1">
      {tags.tools.map((x) => (
        <Chip key={x.name} data-testid={`tool-${x.name}`} className={x.value === null ? 'border-border-subtle text-text-muted' : 'border-border-default'}>
          {x.name} {x.value === null ? t('watch.host.status.tags.none') : x.value}
        </Chip>
      ))}
    </div>
    <div className="flex flex-wrap gap-1">{tags.fingerprint.map((x) => <Chip key={x.name} className="border-border-default text-text-secondary">{x.name} {x.value}</Chip>)}</div>
  </>)
}

// ---- The six ------------------------------------------------------------------------------------------------------------------------------------------------------

export function StatusCards({ host }: { host: MonitorHost }) {
  const t = useI18nStore((s) => s.t)
  const st = host.state
  const facts = st.state === 'ok' ? st.facts : null
  const pool = st.pool
  const judge = useMemo(() => (facts ? judgeOf(facts, pool) : null), [facts, pool])
  const ICON = 14
  return (
    <div data-testid="status-cards" className="grid grid-cols-2 gap-2 lg:grid-cols-3">
      <Card id="judge" icon={<Gauge size={ICON} />} title={t('watch.host.status.title.judge')}>{judge ? <JudgeCard judge={judge} t={t} /> : <NoReading t={t} host={host} />}</Card>
      <Card id="worker" icon={<PlayCircle size={ICON} />} title={t('watch.host.status.title.worker')}>{facts ? <WorkerCard facts={facts} t={t} /> : <NoReading t={t} host={host} />}</Card>
      <Card id="conn" icon={<Plugs size={ICON} />} title={t('watch.host.status.title.conn')}><ConnCard host={host} t={t} /></Card>
      <Card id="pause" icon={<PauseCircle size={ICON} />} title={t('watch.host.status.title.pause')}>{facts ? <PauseCard host={host} facts={facts} t={t} /> : <NoReading t={t} host={host} />}</Card>
      <Card id="ident" icon={<Wrench size={ICON} />} title={t('watch.host.status.title.ident')}><IdentCard host={host} pool={pool} facts={facts} t={t} /></Card>
      <Card id="tags" icon={<StackSimple size={ICON} />} title={t('watch.host.status.title.tags')}><TagsCard pool={pool} facts={facts} t={t} /></Card>
    </div>
  )
}
