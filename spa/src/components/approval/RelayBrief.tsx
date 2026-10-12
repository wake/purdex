// spa/src/components/approval/RelayBrief.tsx — the pieces UP-5a adds to a relay card: the session's title beside its id,
// the 重新申請 mark, and the work report (what it did, what it plans). Pure display; the host passes ids and strings.
import { useI18nStore } from '../../stores/useI18nStore'
import { clipForDisplay } from '../../lib/team/types'
import { useRelayBrief } from '../../hooks/useRelayBrief'

/** The title the session wrote for itself, cleaned for display; renders nothing when blank. Optionally followed by the 重新申請 mark. */
export function RelayTitle({ title, reask }: { title: string; reask?: boolean }) {
  const t = useI18nStore((s) => s.t)
  const shown = clipForDisplay(title, 80).trim()
  if (shown === '' && !reask) return null
  return (
    <>
      {shown !== '' && <span data-testid="approval-title" dir="auto" className="ml-2 break-all font-sans text-text-secondary">{shown}</span>}
      {reask === true && <span data-testid="approval-reask" className="ml-2 rounded bg-surface-hover px-1 font-sans text-[10px] text-text-secondary">{t('approval.dialog.reask')}</span>}
    </>
  )
}

/** Renders nothing until there is something to say: no spinner, no placeholder, no error. */
export function RelayBrief({ hostId, sessionId }: { hostId: string; sessionId: string }) {
  const t = useI18nStore((s) => s.t)
  const { entries, todos } = useRelayBrief(hostId, sessionId)
  if (entries.length === 0 && todos.length === 0) return null
  return (
    <div data-testid="approval-brief" className="mt-3 max-h-40 space-y-2 overflow-y-auto text-xs">
      {entries.length > 0 && (
        <section data-testid="approval-brief-entries">
          <h3 className="text-text-muted">{t('approval.dialog.brief_done')}</h3>
          <ul className="mt-0.5 space-y-0.5 text-text-primary">
            {entries.map((e, i) => <li key={i} data-testid="approval-brief-entry" dir="auto" className="break-words">{clipForDisplay(e, 200)}</li>)}
          </ul>
        </section>
      )}
      {todos.length > 0 && (
        <section data-testid="approval-brief-todos">
          <h3 className="text-text-muted">{t('approval.dialog.brief_next')}</h3>
          <ul className="mt-0.5 space-y-0.5 text-text-primary">
            {todos.map((e, i) => <li key={i} data-testid="approval-brief-todo" dir="auto" className="break-words">{clipForDisplay(e, 200)}</li>)}
          </ul>
        </section>
      )}
    </div>
  )
}
