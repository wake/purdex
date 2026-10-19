// spa/src/components/settings/personas/FirstSaveConfirm.tsx — asked before the first persona is saved on a source that has
// none. From then on the daemon names every live conversation after a persona, one notice each, and the lead's address
// changes (the old one stays an alias). The user should know that before it happens, not after.
import { useEffect, useState } from 'react'
import { useI18nStore } from '../../../stores/useI18nStore'
import { hostLabel, hostLookOf } from '../../../lib/host-look'
import { usePeerStore } from '../../../stores/usePeerStore'
import { liveConversationCount } from '../../../lib/names/live-conversations'

interface Props {
  hostId: string
  onConfirm: () => void
  onCancel: () => void
}

const BTN =
  'shrink-0 rounded-md border border-border-default px-2.5 py-1 text-xs text-text-secondary hover:text-text-primary hover:border-border-active cursor-pointer'

export function FirstSaveConfirm({ hostId, onConfirm, onCancel }: Props) {
  const t = useI18nStore((s) => s.t)
  // The cached count can be old (the list is not refreshed on a timer), so no number is shown until the host has been read
  // again for this question; until then, and when the read fails, the text names none.
  const [readHost, setReadHost] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    void usePeerStore.getState().refresh(hostId).then(() => { if (live) setReadHost(hostId) })
    return () => { live = false }
  }, [hostId])
  const settled = readHost === hostId
  const entry = usePeerStore((s) => s.byHost[hostId])
  // The inventory lists session rows, while the daemon renames every live conversation, including ones that have none,
  // so the number is a floor ("at least"), and a count of 0 is not shown at all (#3520 tracks an exact count from the
  // daemon).
  const known = settled && entry ? liveConversationCount(hostId) : null
  const count = known !== null && known > 0 ? known : null
  const host = hostLabel(hostId, hostLookOf(hostId))
  return (
    <div className="rounded-md border border-border-default p-3" data-testid="persona-first-save">
      <p className="text-xs text-text-primary" data-testid="persona-first-save-text">
        {count === null
          ? t('settings.personas.firstSave.noCount', { host })
          : t('settings.personas.firstSave.withCount', { host, count })}
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
