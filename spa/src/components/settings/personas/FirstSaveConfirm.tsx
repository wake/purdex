// spa/src/components/settings/personas/FirstSaveConfirm.tsx — asked before the first persona is saved on a source that has
// none. From then on the daemon names every live conversation after a persona, one notice each, and the lead's address
// changes (the old one stays an alias). The user should know that before it happens, not after.
import { useEffect, useRef, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../../lib/host-look'
import { usePeerStore } from '../../../stores/usePeerStore'
import { liveConversationCount } from '../../../lib/names/live-conversations'
import { fetchRenamePreview } from '../../../lib/names/rename-preview'
import type { Persona } from '../../../lib/names/persona-types'

interface Props {
  hostId: string
  /** The whole collection that is about to be saved: what the daemon counts against. */
  personas: Persona[]
  onConfirm: () => void
  onCancel: () => void
}

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer'

export function FirstSaveConfirm({ hostId, personas, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  // The cached count can be old (the list is not refreshed on a timer), so no number is shown until the host has been read
  // again for this question; until then, and when the read fails, the text names none. The daemon's own count is asked for
  // alongside: when it answers, that is the number; when it cannot (an older daemon, not ready, offline), the floor
  // worked out from the inventory is used as before.
  const [read, setRead] = useState<{ hostId: string; exact: number | null } | null>(null)
  // The set is the one that was shown when the question was asked; it does not change while the question is open.
  const asked = useRef(personas)
  useEffect(() => {
    const ctl = new AbortController()
    let live = true
    void Promise.all([
      usePeerStore.getState().refresh(hostId),
      fetchRenamePreview(hostId, asked.current, ctl.signal),
    ]).then(([, exact]) => { if (live) setRead({ hostId, exact }) })
    return () => { live = false; ctl.abort() }
  }, [hostId])
  const settled = read?.hostId === hostId
  const entry = usePeerStore((s) => s.byHost[hostId])
  // The inventory lists session rows, while the daemon renames every live conversation, including ones that have none,
  // so without the daemon's count the number is a floor ("at least"), and a count of 0 is not shown at all.
  const exact = read !== null && read.hostId === hostId ? read.exact : null
  const floor = settled && entry ? liveConversationCount(hostId) : null
  const shown = exact ?? floor
  const count = shown !== null && shown > 0 ? shown : null
  const host = hostLabel(hostId, hostLookOf(hostId))
  return (
    <div className="rounded-md border border-border-default p-3" data-testid="persona-first-save">
      <p className="text-xs text-text-primary" data-testid="persona-first-save-text">
        {count === null
          ? t('settings.personas.firstSave.noCount', { host })
          : t(exact !== null ? 'settings.personas.firstSave.exactCount' : 'settings.personas.firstSave.withCount', { host, count })}
      </p>
      <div className="mt-2 flex justify-end gap-2">
        <button type="button" className={BTN} data-testid="persona-first-save-cancel" onClick={onCancel}>
          {t('settings.personas.firstSave.cancel')}
        </button>
        <button type="button" className={BTN} data-testid="persona-first-save-confirm" onClick={onConfirm}>
          {t('settings.personas.firstSave.confirm')}
        </button>
      </div>
    </div>
  )
}
