import type { ReactNode } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { openWatchArea } from '../../features/workspace/lib/open-watch-area'
import type { PoolHostRow } from '../../lib/pool/use-pool-host-rows'
import type { PoolFacts } from '../../lib/pool/types'
import type { PoolReadings } from '../../lib/pool/use-pool-host-rows'
import { ProbeBadge } from './probe-rows'
import { PoolStatusDot } from './PoolStatusDot'
import { statusWord } from './pool-status-word'

/**
 * A resource pool host's own page (plan WWD-7, 7a-3b): the header (badge, name, light, status word; wwd version, proto, platform,
 * OS), 「現在」, 「Worker」 (read only: the one place to change it is the host tab's 設定, linked), 「標籤」 by group, 「工具鏈」 and,
 * for a host without a daemon, `children` under 「樣式與 SSH 備援」 (today's style editor and connection check). Nothing is
 * written here.
 */
export function PoolHostPage({ row, children }: { row: PoolHostRow; children?: ReactNode }) {
  const t = useI18nStore((s) => s.t)
  const unknown = t('pool.card.unknown')
  const h = row.host
  const facts = h?.facts ?? null
  const sub = h ? t('pool_hosts.page.sub', { version: h.version, proto: h.proto, platform: h.platform, arch: h.arch, os: facts?.os_version ?? '' }) : null
  const w = row.worker

  const openSettings = () => { if (h) openWatchArea({ poolHost: { hostId: h.host_id }, sub: 'settings' }) }

  return (
    <div data-testid="pool-page" data-pool-id={row.id}>
      <div className="flex items-center gap-3 mb-4">
        <ProbeBadge row={row.look as never} box={28} />
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 data-testid="pool-page-name" className="text-base font-semibold truncate">{row.name}</h2>
            <PoolStatusDot dot={row.dot} />
            <span data-testid="pool-page-status" className="text-xs text-text-secondary">{statusWord(t, row)}</span>
          </div>
          {sub && <div data-testid="pool-page-sub" className="text-xs text-text-muted">{sub}</div>}
        </div>
      </div>

      <Section title={t('pool_hosts.page.now')} testId="pool-page-now">
        {row.readings ? <Readings r={row.readings} /> : <span className="text-text-muted">{unknown}</span>}
      </Section>

      <Section title={t('pool.card.worker')} testId="pool-page-worker">
        <div className="flex items-center gap-3">
          <span>{!w ? unknown : w.enabled ? t('pool.card.worker_on', { hr: w.running.heavy, hs: w.slots.heavy, lr: w.running.light, ls: w.slots.light }) : t('pool.card.worker_off')}</span>
          <button type="button" data-testid="pool-page-worker-settings" onClick={openSettings} disabled={!h} className="text-xs text-accent cursor-pointer hover:underline">{t('pool_hosts.page.worker_settings')}</button>
        </div>
        <div className="text-xs text-text-muted mt-1">{t('pool.card.headroom')}: {row.headroom ? t('pool.card.headroom_value', { h: row.headroom.heavy, l: row.headroom.light }) : unknown}</div>
      </Section>

      <Section title={t('pool_hosts.page.labels')} testId="pool-page-labels">
        {!h ? <span className="text-text-muted">{unknown}</span> : <Labels labels={h.labels} />}
      </Section>

      <Section title={t('pool_hosts.page.tools')} testId="pool-page-tools">
        {!facts ? <span className="text-text-muted">{unknown}</span> : <Tools tools={facts.tools} />}
      </Section>

      {children && <Section title={t('pool_hosts.page.style')} testId="pool-page-style">{children}</Section>}
    </div>
  )
}

function Section({ title, testId, children }: { title: string; testId: string; children: ReactNode }) {
  return (
    <section data-testid={testId} className="mb-5">
      <h3 className="text-sm font-semibold mb-2">{title}</h3>
      <div className="text-sm">{children}</div>
    </section>
  )
}

/** CPU, memory, memory pressure and the fullest disk, the way the host card words them (`fleet.card.*`). */
function Readings({ r }: { r: PoolReadings }) {
  const t = useI18nStore((s) => s.t)
  return (
    <ul className="space-y-0.5">
      <li data-testid="pool-page-cpu">{t('fleet.cpu', { v: r.cpu })}</li>
      {r.mem !== null && <li data-testid="pool-page-mem">{t('fleet.mem', { v: r.mem })}</li>}
      <li data-testid="pool-page-pressure">{t('fleet.card.pressure')}: {t(`fleet.card.level.${r.level}`)}</li>
      {r.disk !== null && <li data-testid="pool-page-disk">{t('fleet.disk', { v: r.disk })}{r.diskFree !== null ? t('fleet.card.disk_free', { v: r.diskFree }) : ''}</li>}
    </ul>
  )
}

/** The labels a host carries, grouped: platform (os, arch), repo, and anything else (the daemon may send kinds this App does not know). */
function Labels({ labels }: { labels: string[] }) {
  const t = useI18nStore((s) => s.t)
  if (labels.length === 0) return <span className="text-text-muted">{t('pool_hosts.page.labels_none')}</span>
  const platform = labels.filter((l) => l.startsWith('os:') || l.startsWith('arch:'))
  const repo = labels.filter((l) => l.startsWith('repo:'))
  const other = labels.filter((l) => !platform.includes(l) && !repo.includes(l))
  const groups: Array<[string, string, string[]]> = [['platform', t('pool_hosts.page.labels_platform'), platform], ['repo', t('pool_hosts.page.labels_repo'), repo], ['other', t('pool_hosts.page.labels_other'), other]]
  return (
    <div className="space-y-1">
      {groups.filter(([, , ls]) => ls.length > 0).map(([key, title, ls]) => (
        <div key={key} data-testid={`pool-labels-${key}`} className="flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-text-muted w-16 shrink-0">{title}</span>
          {ls.map((l) => <span key={l} className="px-1.5 py-0.5 rounded bg-surface-tertiary text-xs">{l}</span>)}
        </div>
      ))}
    </div>
  )
}

function Tools({ tools }: { tools: PoolFacts['tools'] }) {
  const t = useI18nStore((s) => s.t)
  const entries = Object.entries(tools)
  if (entries.length === 0) return <span className="text-text-muted">{t('pool_hosts.page.labels_none')}</span>
  return (
    <ul className="space-y-0.5">
      {entries.map(([name, v]) => (
        <li key={name} data-testid={`pool-tool-${name}`} className="flex gap-3">
          <span className="w-16 shrink-0 text-text-secondary">{name}</span>
          <span className={v === null ? 'text-text-muted' : 'tabular-nums'}>{v === null ? t('pool_hosts.page.tool_missing') : Array.isArray(v) ? v.join(', ') : v}</span>
        </li>
      ))}
    </ul>
  )
}
