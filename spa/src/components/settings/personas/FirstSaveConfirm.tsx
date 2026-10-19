// spa/src/components/settings/personas/FirstSaveConfirm.tsx — asked before the first persona is saved on a source that has
// none. From then on the daemon names every live conversation after a persona, one notice each, and the lead's address
// changes (the old one stays an alias). The user should know that before it happens, not after.
import { useEffect } from 'react'
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
  // Read again so the number is the host's current one; the subscription below redraws when it lands.
  useEffect(() => { void usePeerStore.getState().refresh(hostId) }, [hostId])
  const entry = usePeerStore((s) => s.byHost[hostId])
  // A count of 0 is not shown as a number: the inventory lists session rows, and the daemon also renames conversations
  // that have none, so "0 conversations" would promise more than is known.
  const known = entry ? liveConversationCount(hostId) : null
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
