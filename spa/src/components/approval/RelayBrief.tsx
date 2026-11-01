// spa/src/components/approval/RelayBrief.tsx — the pieces UP-5a adds to a relay card: the session's title beside its id,
// the 重新申請 mark, and (plan T14b) the newest recap of the session about to hand over. Display only; the host passes ids and strings.
import { useEffect } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { selectDialogueEntry, selectDialogueSupport, useDialogueStore } from '../../stores/useDialogueStore'
import { clipForDisplay } from '../../lib/team/types'

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

/**
 * The newest recap of the session that asks to hand over: its headline, what it is on now and its next step. Renders nothing until there is something to say: no spinner,
 * no placeholder, no error; a host without `dialogue.v1`, a session with no recap and a failed read all leave the card as it is.
 */
export function RelayRecap({ hostId, sessionId }: { hostId: string; sessionId: string }) {
  const t = useI18nStore((s) => s.t)
  const usable = useDialogueStore((s) => hostId !== '' && sessionId !== '' && selectDialogueSupport(hostId, s))
  const recap = useDialogueStore((s) => selectDialogueEntry(s, hostId, sessionId)?.recaps?.recaps[0])
  useEffect(() => {
    if (usable) void useDialogueStore.getState().loadRecaps(hostId, sessionId, 1)
  }, [usable, hostId, sessionId])
  if (!usable || recap === undefined) return null
  const fields: Array<[string, string, string]> = [['now', t('recap.sec.now'), recap.now], ['next', t('recap.sec.next'), recap.next]]
  return (
    <div data-testid="approval-brief" className="mt-3 max-h-40 space-y-2 overflow-y-auto text-xs">
      {recap.headline !== '' && <p data-testid="approval-brief-headline" dir="auto" className="break-words font-medium text-text-primary">{clipForDisplay(recap.headline, 200)}</p>}
      {fields.map(([id, label, value]) => value.trim() !== '' && (
        <section key={id} data-testid={`approval-brief-${id}`}>
          <h3 className="text-text-muted">{label}</h3>
          <p dir="auto" className="mt-0.5 break-words text-text-primary">{clipForDisplay(value, 200)}</p>
        </section>
      ))}
    </div>
  )
}

