// spa/src/components/settings/WorkerSettingsPage.tsx — Settings → Worker: a page of tabs over the tab registry
// (`lib/worker-settings-tabs`). Host-scoped tabs get a host picker over the shown hosts.
import { useMemo, useState } from 'react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useHostStore } from '../../stores/useHostStore'
import { useHostLookResolver } from '../../lib/host-look'
import { useShownRefFilter } from '../../lib/shown-hosts'
import { getWorkerSettingsTabs } from '../../lib/worker-settings-tabs'
import { SegmentControl } from './SegmentControl'

export function WorkerSettingsPage(_props: object) {
  const t = useI18nStore((s) => s.t)
  const tabs = getWorkerSettingsTabs()
  const [activeId, setActiveId] = useState<string | null>(null)
  const [pickedHost, setPickedHost] = useState<string | null>(null)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const isShown = useShownRefFilter()
  const lookOf = useHostLookResolver()
  const shownHosts = useMemo(
    () => hostOrder.filter((id) => hosts[id] && isShown(id)).map((id) => ({ value: id, label: lookOf(id).name ?? id })),
    [hosts, hostOrder, isShown, lookOf],
  )

  const active = tabs.find((x) => x.id === activeId) ?? tabs[0]
  if (!active) return null
  const hostId = shownHosts.some((h) => h.value === pickedHost) ? (pickedHost as string) : shownHosts[0]?.value
  const Body = active.component

  return (
    <div className="flex flex-col gap-3">
      <div role="tablist" className="flex gap-4 border-b border-border-default">
        {tabs.map((tab) => {
          const on = tab.id === active.id
          return (
            <button
              key={tab.id}
              type="button"
              role="tab"
              aria-selected={on}
              data-testid={`worker-settings-tab-${tab.id}`}
              onClick={() => setActiveId(tab.id)}
              className={`px-1 py-1.5 text-xs cursor-pointer border-b-2 -mb-px ${
                on ? 'border-accent text-text-primary' : 'border-transparent text-text-muted hover:text-text-primary'
              }`}
            >
              {t(tab.labelKey)}
            </button>
          )
        })}
      </div>
      {active.hostScoped ? (
        shownHosts.length === 0 ? (
          <div data-testid="worker-settings-no-hosts" className="text-xs text-text-muted">{t('settings.worker.no_hosts')}</div>
        ) : (
          <>
            <div data-testid="worker-settings-host-picker">
              <SegmentControl options={shownHosts} value={hostId as string} onChange={setPickedHost} />
            </div>
            <Body key={hostId} hostId={hostId} />
          </>
        )
      ) : (
        <Body />
      )}
    </div>
  )
}
