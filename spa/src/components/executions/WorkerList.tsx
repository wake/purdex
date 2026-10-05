// spa/src/components/executions/WorkerList.tsx — the activity bar's worker list (shell cleanup spec §4.3, rule B.1):
// one `ExecutionsView` section per host shown in this workbench, in `hostOrder`. A host whose Nexen phase is
// `disabled` gets no section; every other phase (loading / unknown / ready / unavailable) keeps one, so a host that is
// still connecting or has failed stays visible. With no visible section, one empty line says so. The caller scrolls.
import { useEffect, useMemo } from 'react'
import { useShownRefFilter } from '../../lib/shown-hosts'
import { useHostStore } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { ExecutionsView } from './ExecutionsView'

function HostSection({ hostId }: { hostId: string }) {
  const phase = useNexHostStore((s) => s.byHost[hostId]?.phase)
  // A disabled host renders no ExecutionsView (whose hook would ensure it), so the section ensures it itself: its phase
  // stays current, and a host whose Nexen is enabled later gets its section back.
  useEffect(() => {
    void useNexHostStore.getState().ensure(hostId)
  }, [hostId])
  if (phase === 'disabled') return null
  return <ExecutionsView hostId={hostId} isActive />
}

export function WorkerList() {
  const t = useI18nStore((s) => s.t)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const isShown = useShownRefFilter()
  const shownIds = useMemo(() => hostOrder.filter(isShown), [hostOrder, isShown])
  // From the phases, not the DOM: a host with no phase yet counts as visible (its section shows it loading).
  const anyVisible = useNexHostStore((s) => shownIds.some((id) => s.byHost[id]?.phase !== 'disabled'))

  return (
    <div data-testid="worker-list" className="flex flex-col">
      {shownIds.map((id) => <HostSection key={id} hostId={id} />)}
      {!anyVisible && (
        <p data-testid="worker-list-none" className="px-3 py-2 text-xs text-text-muted">{t('workers.list.none')}</p>
      )}
    </div>
  )
}
