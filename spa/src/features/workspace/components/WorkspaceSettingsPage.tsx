import { useState, useCallback, useMemo } from 'react'
import { PuzzlePiece, Trash } from '@phosphor-icons/react'
import { useWorkspaceStore } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { getPaneLabel } from '../../../lib/pane-labels'
import { closeTab } from '../../../lib/tab-lifecycle'
import { listContributions } from '../../../lib/settings-contribution-registry'
import {
  isModuleOwnedContribution,
  type SettingsContextFor,
  type SettingsContribution,
} from '../../../lib/settings-contribution-types'
import { WorkspaceIcon } from './WorkspaceIcon'

import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { WorkspaceDeleteDialog } from './WorkspaceDeleteDialog'

interface Props {
  workspaceId: string
}

export function WorkspaceSettingsPage({ workspaceId }: Props) {
  const t = useI18nStore((s) => s.t)
  const ws = useWorkspaceStore((s) => s.workspaces.find((w) => w.id === workspaceId))
  const renameWorkspace = useWorkspaceStore((s) => s.renameWorkspace)

  const setWorkspaceIcon = useWorkspaceStore((s) => s.setWorkspaceIcon)
  const setWorkspaceIconWeight = useWorkspaceStore((s) => s.setWorkspaceIconWeight)
  const tabs = useTabStore((s) => s.tabs)

  const [nameInput, setNameInput] = useState(ws?.name ?? '')
  const [showDelete, setShowDelete] = useState(false)

  // Workspace-scoped ctx per spec §5.3 rule 4 (ctx only produced by the shell).
  // Rebuilt whenever workspaceId changes so `disabled(ctx)` and child
  // components see a fresh, matching entity id.
  const ctx = useMemo<SettingsContextFor<'workspace'>>(
    () => ({ scope: 'workspace' as const, workspaceId }),
    [workspaceId],
  )

  // F1: do NOT memoize the filtered/sorted list against ctx — that caches
  // `disabled(ctx)` once per workspaceId, which freezes rows whose `disabled`
  // closure reads reactive state (Zustand store, capability flag). N is tiny
  // and contributions are already sorted by the registry, so re-read per
  // render. `listContributions` returns a fresh array on each call.
  //
  // F2: keep disabled rows in the list — the render pass below mirrors
  // PR-2's SettingsSidebar pattern (disabled header visible with
  // `data-disabled-ctx="true"` + `title=disabledReasonKey`, body skipped)
  // instead of hiding the contribution entirely.
  const workspaceContributions: SettingsContribution<'workspace'>[] =
    listContributions('workspace')

  const handleNameBlur = useCallback(() => {
    const trimmed = nameInput.trim()
    if (trimmed && trimmed !== ws?.name) {
      renameWorkspace(workspaceId, trimmed)
    } else {
      setNameInput(ws?.name ?? '')
    }
  }, [nameInput, ws?.name, workspaceId, renameWorkspace])

  const handleNameKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
  }, [])

  const handleIconSelect = useCallback((icon: string) => {
    setWorkspaceIcon(workspaceId, icon)
  }, [workspaceId, setWorkspaceIcon])

  if (!ws) {
    return (
      <div className="flex items-center justify-center h-full text-text-secondary">
        Workspace not found
      </div>
    )
  }

  const tabItems = ws.tabs
    .map((tabId) => {
      const tab = tabs[tabId]
      if (!tab) return null
      const content = getPrimaryPane(tab.layout).content
      if (content.kind === 'settings') return null
      const label = getPaneLabel(content, { getByCode: () => undefined }, { getById: () => undefined }, t)
      return { id: tabId, label }
    })
    .filter(Boolean) as { id: string; label: string }[]

  return (
    <div className="h-full overflow-y-auto">
      <div className="max-w-lg mx-auto px-6 py-10">
        {/* Header: Icon + Name */}
        <div className="flex flex-col items-center gap-3 mb-8">
          <div
            className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl bg-white/12 text-text-primary"
          >
            <WorkspaceIcon icon={ws.icon} name={ws.name} size={32} weight={ws.iconWeight} />
          </div>
          <input
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            onBlur={handleNameBlur}
            onKeyDown={handleNameKeyDown}
            maxLength={64}
            className="text-center text-lg font-semibold bg-transparent text-text-primary border-b border-transparent hover:border-border-default focus:border-accent focus:outline-none px-2 py-1 transition-colors"
          />
        </div>

        {/* Icon */}
        <section className="mb-8">
          <h3 className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">
            {t('workspace.change_icon') ?? 'Icon'}
          </h3>
          <WorkspaceIconPicker
            currentIcon={ws.icon}
            onSelect={handleIconSelect}
            onCancel={() => {}}
            inline
            currentWeight={ws.iconWeight}
            onWeightChange={(w) => setWorkspaceIconWeight(workspaceId, w)}
          />
        </section>

        {/* Registry-driven workspace-scoped contributions.
            F2: mirror PR-2's SettingsSidebar disabled-row pattern —
            show the header greyed with a title tooltip carrying the
            i18n'd `disabledReasonKey`, and skip mounting the body.
            F4: key by `${workspaceId}:${c.id}` so swapping to a different
            workspace forces per-section remount; contributions seeding
            local state from `ctx.workspaceId` cannot leak across
            workspaces. */}
        {workspaceContributions.map((c) => {
          const isDisabled = c.disabled ? c.disabled(ctx) === true : false
          const title = isDisabled && c.disabledReasonKey
            ? (t(c.disabledReasonKey) ?? c.disabledReasonKey)
            : undefined
          const moduleOwned = isModuleOwnedContribution(c)
          const Body = c.component
          return (
            <section
              key={`${workspaceId}:${c.id}`}
              data-section={c.localId}
              data-disabled-ctx={isDisabled ? 'true' : undefined}
              title={title}
              className="mb-8"
            >
              <h3
                className={`text-xs font-semibold uppercase tracking-wider mb-3 flex items-center gap-2 ${
                  isDisabled ? 'text-text-muted' : 'text-text-secondary'
                }`}
              >
                <span>{t(c.labelKey) ?? c.labelKey}</span>
                {moduleOwned && (
                  <PuzzlePiece
                    size={12}
                    weight="bold"
                    className="flex-shrink-0 text-text-muted"
                    aria-hidden
                  />
                )}
              </h3>
              {!isDisabled && <Body ctx={ctx} />}
            </section>
          )
        })}

        {/* Danger Zone */}
        <section className="border-t border-border-subtle pt-6 mt-8">
          <h3 className="text-xs font-semibold text-red-400 uppercase tracking-wider mb-3">
            Danger Zone
          </h3>
          <button
            data-testid="delete-workspace-btn"
            onClick={() => setShowDelete(true)}
            className="flex items-center gap-2 px-4 py-2 rounded-md border border-red-500/30 text-red-400 text-sm hover:bg-red-500/10 cursor-pointer transition-colors"
          >
            <Trash size={16} />
            {t('workspace.delete') ?? 'Delete Workspace'}
          </button>
          {showDelete && (
            <WorkspaceDeleteDialog
              workspaceName={ws.name}
              tabs={tabItems}
              onConfirm={(closedTabIds) => {
                closedTabIds.forEach((id) => {
                  closeTab(id)
                })
                useWorkspaceStore.getState().removeWorkspace(workspaceId)
                const hasPreservedTabs = closedTabIds.length < tabItems.length
                if (hasPreservedTabs) {
                  useWorkspaceStore.getState().setActiveWorkspace(null)
                } else {
                  const { activeWorkspaceId: newWsId, workspaces: remaining } = useWorkspaceStore.getState()
                  const newWs = remaining.find((w) => w.id === newWsId)
                  const nextTab = newWs?.activeTabId ?? newWs?.tabs[0]
                  if (nextTab) useTabStore.getState().setActiveTab(nextTab)
                }
                setShowDelete(false)
              }}
              onCancel={() => setShowDelete(false)}
            />
          )}
        </section>
      </div>
    </div>
  )
}
