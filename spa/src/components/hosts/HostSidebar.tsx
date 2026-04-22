import { Plus, CaretDown, CaretRight, Circle, LockSimple, Spinner, Warning } from '@phosphor-icons/react'
import { useState } from 'react'
import { listContributions } from '../../lib/settings-contribution-registry'
import { useHostStore, type HostRuntime } from '../../stores/useHostStore'
import { useI18nStore } from '../../stores/useI18nStore'

interface Props {
  selectedHostId: string
  selectedSubPage: string
  onSelect: (hostId: string, subPage: string) => void
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

  // Read sub-pages from the contribution registry (order guaranteed by registry sort).
  // Do NOT memoize — ctx may change scope-narrow inputs React cannot track in deps.
  const subPages = listContributions('host')

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
          const isExpanded = expanded[hostId] || hostId === selectedHostId
          // ctx carries runtime[hostId] so disabled(ctx) predicates can react
          // to live host runtime changes without a separate side-read.
          const hostCtx = { scope: 'host' as const, hostId, runtime: runtime[hostId] }
          // R2 defender D2 + R3 standard P1 — when expanding a different host:
          //   1. If the current `selectedSubPage` is still selectable for the
          //      target host, preserve it (UX: user's working sub-page stays
          //      across host switches when valid).
          //   2. Otherwise pick the target host's first selectable sub-page
          //      using its runtime — avoids the blank-and-redirect window
          //      that D2 was originally meant to eliminate.
          const targetSubPageForHost = (): string => {
            const current = subPages.find((page) => page.localId === selectedSubPage)
            if (current && current.disabled?.(hostCtx) !== true) return selectedSubPage
            const candidate = subPages.find((page) => page.disabled?.(hostCtx) !== true)
            return candidate?.localId ?? selectedSubPage
          }
          return (
            <div key={hostId} className="mb-1">
              <button
                onClick={() => {
                  toggleExpand(hostId)
                  if (!isExpanded) {
                    const targetSubPage =
                      hostId === selectedHostId ? selectedSubPage : targetSubPageForHost()
                    onSelect(hostId, targetSubPage)
                  }
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
                  {subPages.map((page) => {
                    // F7: disabled contributions show as a disabled row but are NOT
                    // filtered out. Clicking a disabled row is a no-op.
                    const isDisabled = page.disabled ? page.disabled(hostCtx) === true : false
                    const isActive = selectedHostId === hostId && selectedSubPage === page.localId
                    const disabledTitle =
                      isDisabled && page.disabledReasonKey
                        ? t(page.disabledReasonKey)
                        : undefined
                    return (
                      <button
                        key={page.localId}
                        data-disabled-ctx={isDisabled ? 'true' : undefined}
                        title={disabledTitle}
                        onClick={() => {
                          if (!isDisabled) onSelect(hostId, page.localId)
                        }}
                        className={`w-full text-left px-2 py-1 rounded text-xs ${
                          isDisabled
                            ? 'text-text-muted cursor-not-allowed'
                            : isActive
                              ? 'text-accent font-semibold bg-accent/10 cursor-pointer'
                              : 'text-text-muted hover:text-text-secondary cursor-pointer'
                        }`}
                      >
                        {t(page.labelKey)}
                      </button>
                    )
                  })}
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
