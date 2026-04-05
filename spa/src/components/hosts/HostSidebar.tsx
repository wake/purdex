import { Plus, CaretDown, CaretRight, Circle, LockSimple, Spinner, Warning } from '@phosphor-icons/react'
import { useState } from 'react'
import { useHostStore, type HostRuntime } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'
import type { HostSubPage } from '../HostPage'

const SUB_PAGES: { id: HostSubPage; labelKey: string }[] = [
  { id: 'overview', labelKey: 'hosts.overview' },
  { id: 'sessions', labelKey: 'hosts.sessions' },
  { id: 'hooks', labelKey: 'hosts.hooks' },
  { id: 'uploads', labelKey: 'hosts.uploads' },
]

interface Props {
  selectedHostId: string
  selectedSubPage: HostSubPage
  onSelect: (hostId: string, subPage: HostSubPage) => void
  onAddHost?: () => void
}

function StatusIcon({ runtime }: { runtime?: HostRuntime }) {
  if (!runtime) return <Circle size={8} weight="fill" className="text-text-muted" />
  if (runtime.status === 'connected' && runtime.tmuxState === 'unavailable')
    return <Warning size={12} weight="fill" className="text-yellow-400" />
  if (runtime.status === 'connected') return <Circle size={8} weight="fill" className="text-green-400" />
  if (runtime.status === 'reconnecting') return <Spinner size={10} className="text-yellow-400 animate-spin" />
  if (runtime.status === 'auth-error')
    return <LockSimple size={12} weight="fill" className="text-red-400 animate-pulse" />
  return <Circle size={8} weight="fill" className="text-red-400" />
}

export function HostSidebar({ selectedHostId, selectedSubPage, onSelect, onAddHost }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const [expanded, setExpanded] = useState<Record<string, boolean>>(() => ({
    [selectedHostId]: true,
  }))

  const toggleExpand = (hostId: string) => {
    setExpanded((prev) => ({ ...prev, [hostId]: !prev[hostId] }))
  }

  return (
    <div className="w-48 bg-surface-tertiary border-r border-border-subtle flex flex-col py-2">
      <div className="px-3 mb-2">
        <span className="text-xs uppercase text-text-secondary">{t('hosts.title')}</span>
      </div>
      <div className="flex-1 overflow-y-auto px-1">
        {hostOrder.map((hostId) => {
          const host = hosts[hostId]
          if (!host) return null
          const isExpanded = expanded[hostId] ?? false
          return (
            <div key={hostId} className="mb-1">
              <button
                onClick={() => {
                  toggleExpand(hostId)
                  if (!isExpanded) onSelect(hostId, 'overview')
                }}
                className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-1.5 ${
                  selectedHostId === hostId
                    ? 'bg-accent text-white'
                    : 'text-text-secondary hover:bg-surface-secondary/50'
                }`}
              >
                {isExpanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
                <StatusIcon runtime={runtime[hostId]} />
                <span
                  className={`truncate flex-1 ${runtime[hostId]?.status === 'auth-error' ? 'text-red-400' : ''}`}
                >
                  {host.name}
                </span>
              </button>
              {isExpanded && (
                <div className="ml-4 border-l-2 border-border-subtle pl-2 mt-1">
                  {SUB_PAGES.map((page) => (
                    <button
                      key={page.id}
                      onClick={() => onSelect(hostId, page.id)}
                      className={`w-full text-left px-2 py-1 rounded text-xs cursor-pointer ${
                        selectedHostId === hostId && selectedSubPage === page.id
                          ? 'text-accent font-semibold bg-accent/10'
                          : 'text-text-muted hover:text-text-secondary'
                      }`}
                    >
                      {t(page.labelKey)}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
      <div className="px-1 pt-2 border-t border-border-subtle">
        <button
          onClick={onAddHost}
          className="w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 text-text-muted hover:text-text-secondary border border-dashed border-border-subtle hover:border-border-default"
        >
          <Plus size={14} />
          <span>{t('hosts.add')}</span>
        </button>
      </div>
    </div>
  )
}
