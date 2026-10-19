import { Plus, CaretDown, CaretRight, Circle, DeviceMobile, DownloadSimple, LockSimple, ShareNetwork, Spinner, Warning } from '@phosphor-icons/react'
import { ModuleOwnedPuzzleIcon } from '../settings/ModuleOwnedPuzzleIcon'
import { useState } from 'react'
import { listContributions } from '../../lib/settings-contribution-registry'
import { isModuleOwnedContribution } from '../../lib/settings-contribution-types'
import { useHostStore, type HostRuntime } from '../../stores/useHostStore'
import { useHostLookResolver } from '../../lib/host-look'
import { useShownRefFilter } from '../../lib/shown-hosts'
import { useI18nStore } from '../../stores/useI18nStore'
import { useFleetDaemons } from '../../lib/fleet/use-fleet-daemons'
import { ProbeBadge, ProbeStatusDot } from './probe-rows'
import { useProbeRows } from './use-probe-rows'
import { usePoolHostRows } from '../../lib/pool/use-pool-host-rows'
import { PoolHostGroup } from './PoolHostGroup'
import { MAX_PROBE_HOSTS, useProbeEditor } from './useProbeEditor'

interface Props {
  selectedHostId: string
  selectedSubPage: string
  onSelect: (hostId: string, subPage: string) => void
  onAddHost?: () => void
  onShareHosts?: () => void
  onReceiveHosts?: () => void
  onPairPhone?: () => void
  onShowPairedPhones?: () => void
  /** The paired-phones page is showing (it is not a host sub-page): no host sub-page is highlighted. */
  pairedPhonesActive?: boolean
  /** The probe host whose own page is showing (its id; selecting one calls `onSelect('probe:<id>', '')`). */
  selectedProbeId?: string | null
  /** The pool host (a daemon host) whose page is showing; selecting one calls `onSelect('pool:<id>', '')`. */
  selectedPoolId?: string | null
  /** The 「觀察主機」 heading's `+`: start a new probe host. */
  onAddProbe?: () => void
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

export function HostSidebar({ selectedHostId, selectedSubPage, onSelect, onAddHost, onShareHosts, onReceiveHosts, onPairPhone, onShowPairedPhones, pairedPhonesActive, selectedProbeId, selectedPoolId, onAddProbe }: Props) {
  const t = useI18nStore((s) => s.t)
  const hosts = useHostStore((s) => s.hosts)
  const hostOrder = useHostStore((s) => s.hostOrder)
  const runtime = useHostStore((s) => s.runtime)
  const lookOf = useHostLookResolver()
  const probeRows = useProbeRows()
  // The `+` is off for the same reasons as the overview's 「新增觀察主機」 (a daemon unreachable, the list unread, the limit).
  const { writable, blocked } = useProbeEditor()
  const addProbeDisabled = !writable || probeRows.length >= MAX_PROBE_HOSTS
  const { known: fleetDaemons } = useFleetDaemons()
  // Once the resource pool is the source the group lists its hosts; before that (or with no daemon announcing it) it is the probe group as it was.
  const pool = usePoolHostRows()
  const [probesOpen, setProbesOpen] = useState(true)
  // Every host is listed in every workbench; a hidden one only looks muted (H2d-2, user rule 1).
  const isShown = useShownRefFilter()
  const [expanded, setExpanded] = useState<Record<string, boolean>>(() => ({
    [selectedHostId]: true,
  }))

  // A host that becomes selected (fallback / external change) opens again, even if it was collapsed by hand.
  const [lastSelectedHostId, setLastSelectedHostId] = useState(selectedHostId)
  if (lastSelectedHostId !== selectedHostId) {
    setLastSelectedHostId(selectedHostId)
    setExpanded((prev) => ({ ...prev, [selectedHostId]: true }))
  }

  // Read sub-pages from the contribution registry (order guaranteed by registry sort).
  // Do NOT memoize — ctx may change scope-narrow inputs React cannot track in deps.
  const subPages = listContributions('host')

  const toggleExpand = (hostId: string) => {
    setExpanded((prev) => ({ ...prev, [hostId]: !(prev[hostId] ?? hostId === selectedHostId) }))
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
          const isExpanded = expanded[hostId] ?? hostId === selectedHostId
          const hidden = !isShown(hostId)
          const selected = selectedHostId === hostId
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
                data-host-hidden={hidden ? 'true' : undefined}
                className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-1.5 ${
                  selected
                    ? `bg-accent text-white${hidden ? ' opacity-70' : ''}`
                    : 'text-text-secondary hover:bg-surface-secondary/50'
                }`}
              >
                {isExpanded ? <CaretDown size={10} /> : <CaretRight size={10} />}
                <StatusIcon runtime={runtime[hostId]} />
                <span
                  className={`truncate flex-1 ${
                    runtime[hostId]?.status === 'auth-error' ? 'text-red-400' : hidden && !selected ? 'text-text-muted' : ''
                  }`}
                >
                  {lookOf(hostId).name}
                </span>
                {hidden && (
                  <span
                    data-testid="host-hidden-tag"
                    className={`shrink-0 px-1 rounded text-[10px] leading-4 border ${
                      selected ? 'border-white/50 text-white' : 'border-border-default text-text-muted'
                    }`}
                  >
                    {t('hosts.shown.hidden_badge')}
                  </span>
                )}
              </button>
              {isExpanded && (
                <div className="ml-4 border-l-2 border-border-subtle pl-2 mt-1">
                  {subPages.map((page) => {
                    // F7: disabled contributions show as a disabled row but are NOT
                    // filtered out. Clicking a disabled row is a no-op.
                    const isDisabled = page.disabled ? page.disabled(hostCtx) === true : false
                    const isActive = !pairedPhonesActive && selectedHostId === hostId && selectedSubPage === page.localId
                    const disabledTitle =
                      isDisabled && page.disabledReasonKey
                        ? t(page.disabledReasonKey)
                        : undefined
                    const moduleOwned = isModuleOwnedContribution(page)
                    return (
                      <button
                        key={page.localId}
                        data-disabled-ctx={isDisabled ? 'true' : undefined}
                        title={disabledTitle}
                        onClick={() => {
                          if (!isDisabled) onSelect(hostId, page.localId)
                        }}
                        className={`w-full text-left px-2 py-1 rounded text-xs flex items-center gap-2 ${
                          isDisabled
                            ? 'text-text-muted cursor-not-allowed'
                            : isActive
                              ? 'text-accent font-semibold bg-accent/10 cursor-pointer'
                              : 'text-text-secondary hover:text-text-primary cursor-pointer'
                        }`}
                      >
                        <span className="flex-1 truncate">{t(page.labelKey)}</span>
                        {moduleOwned && (
                          <ModuleOwnedPuzzleIcon size={10} tone="secondary" />
                        )}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          )
        })}
        {pool.active && pool.rows.length > 0 && (
          <PoolHostGroup
            rows={pool.rows}
            selectedId={selectedPoolId ?? selectedProbeId ?? null}
            // A probe host opens its own page (the pool host page with the style editor); a daemon host opens the pool host page alone.
            onOpen={(r) => onSelect(r.kind === 'probe' ? `probe:${r.id}` : `pool:${r.id}`, '')}
          />
        )}
        {!pool.active && (probeRows.length > 0 || fleetDaemons.length > 0) && (
          <div data-testid="probe-group" className="mt-2">
            <div className="flex items-center px-1">
              <button type="button" data-testid="probe-group-toggle" aria-expanded={probesOpen} onClick={() => setProbesOpen((o) => !o)}
                className="flex-1 flex items-center gap-1.5 px-1 py-1 text-xs uppercase text-text-secondary cursor-pointer text-left">
                {probesOpen ? <CaretDown size={10} /> : <CaretRight size={10} />}
                <span>{t('fleet_hosts.group')}</span>
              </button>
              {onAddProbe && fleetDaemons.length > 0 && (
                <button type="button" data-testid="probe-group-add" title={addProbeDisabled && blocked ? blocked : t('fleet_hosts.probe.add')} aria-label={t('fleet_hosts.probe.add')} disabled={addProbeDisabled} onClick={onAddProbe}
                  className="p-1 rounded text-text-muted hover:text-text-secondary cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"><Plus size={12} /></button>
              )}
            </div>
            {probesOpen && probeRows.map((row) => {
              const selected = selectedProbeId === row.id
              return (
                <button key={row.id} type="button" data-testid={`probe-item-${row.id}`} aria-current={selected ? 'true' : undefined}
                  onClick={() => onSelect(`probe:${row.id}`, '')}
                  className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-1.5 ${
                    selected ? 'bg-accent text-white' : 'text-text-secondary hover:bg-surface-secondary/50'
                  }`}>
                  <ProbeBadge row={row} />
                  <span className="truncate flex-1">{row.name}</span>
                  <ProbeStatusDot status={row.status} />
                </button>
              )
            })}
          </div>
        )}
      </div>
      <div className="px-1 pt-2 border-t border-border-subtle">
        <button
          onClick={onAddHost}
          className="w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 text-text-muted hover:text-text-secondary border border-dashed border-border-subtle hover:border-border-default"
        >
          <Plus size={14} />
          <span>{t('hosts.add')}</span>
        </button>
        {onShareHosts && (
          <button
            onClick={onShareHosts}
            className="w-full text-left px-2 py-1.5 mt-1 rounded text-sm cursor-pointer flex items-center gap-2 text-text-muted hover:text-text-secondary"
          >
            <ShareNetwork size={14} />
            <span>{t('hosts.transfer.share_button')}</span>
          </button>
        )}
        {onReceiveHosts && (
          <button
            onClick={onReceiveHosts}
            className="w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 text-text-muted hover:text-text-secondary"
          >
            <DownloadSimple size={14} />
            <span>{t('hosts.transfer.receive_button')}</span>
          </button>
        )}
        {onPairPhone && (
          <button
            onClick={onPairPhone}
            className="w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 text-text-muted hover:text-text-secondary"
          >
            <DeviceMobile size={14} />
            <span>{t('hosts.pair.button')}</span>
          </button>
        )}
        {onShowPairedPhones && (
          <button
            onClick={onShowPairedPhones}
            aria-current={pairedPhonesActive ? 'page' : undefined}
            className={`w-full text-left px-2 py-1.5 rounded text-sm cursor-pointer flex items-center gap-2 ${
              pairedPhonesActive ? 'text-accent font-semibold bg-accent/10' : 'text-text-muted hover:text-text-secondary'
            }`}
          >
            <DeviceMobile size={14} />
            <span>{t('hosts.pairedPhones.title')}</span>
          </button>
        )}
      </div>
    </div>
  )
}
