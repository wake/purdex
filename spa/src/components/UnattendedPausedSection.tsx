// spa/src/components/UnattendedPausedSection.tsx — 「被擋下」 at the top of the unattended panel (UP-5b): one row per paused session
// of every reachable host that lists `team.pause.v1`: `<host>：<title or ref>`, the light text, since when, and 「解除」.
import { useI18nStore } from '../stores/useI18nStore'
import { usePausedStore } from '../stores/usePausedStore'
import { hostLabel, hostLookOf } from '../lib/host-look'
import { clipForDisplay } from '../lib/team/types'
import { sinceText } from '../lib/team/time-text'
import { pausedLightText } from '../lib/team/paused-text'
import { PauseReleaseButton } from './PauseReleaseButton'

export function UnattendedPausedSection({ hostIds }: { hostIds: readonly string[] }) {
  const t = useI18nStore((s) => s.t)
  const byHost = usePausedStore((s) => s.byHost)
  const rows = hostIds.flatMap((hostId) => (byHost[hostId]?.sessions ?? []).map((p) => ({ hostId, p })))
  if (rows.length === 0) return null
  rows.sort((x, y) => y.p.since - x.p.since)
  return (
    <section data-testid="paused-section" className="flex flex-col gap-1">
      <div data-testid="paused-title" className="font-medium text-text-primary">{t('unattended.paused.title')}</div>
      <ul className="flex flex-col gap-1.5">
        {rows.map(({ hostId, p }) => (
          <li key={`${hostId}:${p.session_id}`} data-testid="paused-row" className="flex flex-col gap-0.5 text-text-primary">
            <div className="flex items-center gap-2">
              <span className="min-w-0 flex-1 truncate">
                {t('unattended.paused.row', { host: hostLabel(hostId, hostLookOf(hostId)), session: clipForDisplay(p.title.trim() !== '' ? p.title : p.ref, 40) })}
              </span>
              <PauseReleaseButton hostId={hostId} sessionId={p.session_id} />
            </div>
            <div data-testid="paused-light" className="text-text-muted">{pausedLightText(t, p.causes, p.bounced)} · {sinceText(p.since)}</div>
            {p.role === 'lead' && <div data-testid="paused-lead-hint" className="text-text-muted">{t('paused.release.lead_hint')}</div>}
          </li>
        ))}
      </ul>
    </section>
  )
}
