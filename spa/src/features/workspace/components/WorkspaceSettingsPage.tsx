import { useState, useCallback, useEffect, useMemo, useRef } from 'react'
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
import { ModuleConfigSection } from '../../../components/settings/ModuleConfigSection'
import { WorkspaceSettingsDraftProvider, type WorkspaceSettingsDraftActions } from './WorkspaceSettingsDraftContext'

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
  const [dirtyFieldIds, setDirtyFieldIds] = useState<Set<string>>(() => new Set())
  const draftActionsRef = useRef(new Map<string, WorkspaceSettingsDraftActions>())

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

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setNameInput(ws?.name ?? '')
  }, [ws?.name])

  const handleNameSave = useCallback(() => {
    const trimmed = nameInput.trim()
    if (trimmed && trimmed !== ws?.name) {
      renameWorkspace(workspaceId, trimmed)
    } else {
      setNameInput(ws?.name ?? '')
    }
  }, [nameInput, ws?.name, workspaceId, renameWorkspace])

  const handleNameCancel = useCallback(() => {
    setNameInput(ws?.name ?? '')
  }, [ws?.name])

  const handleNameKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.nativeEvent.isComposing) {
      handleNameSave()
    }
  }, [handleNameSave])


  const handleIconSelect = useCallback((icon: string) => {
    setWorkspaceIcon(workspaceId, icon)
  }, [workspaceId, setWorkspaceIcon])

  const setDraftDirty = useCallback((id: string, dirty: boolean) => {
    setDirtyFieldIds((prev) => {
      const next = new Set(prev)
      if (dirty) next.add(id)
      else next.delete(id)
      return next
    })
  }, [])

  const registerDraftActions = useCallback((id: string, actions: WorkspaceSettingsDraftActions) => {
    draftActionsRef.current.set(id, actions)
    return () => {
      draftActionsRef.current.delete(id)
      setDraftDirty(id, false)
    }
  }, [setDraftDirty])

  const draftContext = useMemo(() => ({
    setDirty: setDraftDirty,
    register: registerDraftActions,
  }), [setDraftDirty, registerDraftActions])

  const nameIsDirty = nameInput !== (ws?.name ?? '')
  const hasDirtyChanges = nameIsDirty || dirtyFieldIds.size > 0

  const handleSaveAll = useCallback(() => {
    if (nameIsDirty) handleNameSave()
    for (const actions of draftActionsRef.current.values()) actions.save()
  }, [nameIsDirty, handleNameSave])

  const handleCancelAll = useCallback(() => {
    handleNameCancel()
    for (const actions of draftActionsRef.current.values()) actions.cancel()
  }, [handleNameCancel])

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
    <WorkspaceSettingsDraftProvider value={draftContext}>
      <div className="h-full overflow-y-auto">
        <div className="max-w-4xl mx-auto px-8 py-10 pb-28">
          <div className="mb-10">
            <h1 className="text-3xl font-semibold text-text-primary mb-3">Workspace Settings</h1>
            <p className="text-sm text-text-secondary">Manage this workspace's identity and scoped settings.</p>
          </div>

          <section className="mb-10">
            <h2 className="text-xl font-semibold text-text-primary mb-5">Details</h2>
            <div className="divide-y divide-border-subtle border-y border-border-subtle">
              <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(280px,420px)] gap-4 md:gap-8 py-6 items-start">
                <div>
                  <h3 className="text-sm font-semibold text-text-primary mb-1">Icon</h3>
                  <p className="text-sm text-text-secondary">Choose the icon shown in workspace navigation.</p>
                </div>
                <div>
                  <div className="flex items-center gap-4 mb-4">
                    <div className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl bg-white/12 text-text-primary">
                      <WorkspaceIcon icon={ws.icon} name={ws.name} size={32} weight={ws.iconWeight} />
                    </div>
                    <span className="text-xs text-text-muted">Icon changes still apply immediately.</span>
                  </div>
                  <WorkspaceIconPicker
                    currentIcon={ws.icon}
                    onSelect={handleIconSelect}
                    onCancel={() => {}}
                    inline
                    currentWeight={ws.iconWeight}
                    onWeightChange={(w) => setWorkspaceIconWeight(workspaceId, w)}
                  />
                </div>
              </div>

              <div className="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(280px,420px)] gap-4 md:gap-8 py-6 items-start">
                <div>
                  <h3 className="text-sm font-semibold text-text-primary mb-1">Name</h3>
                  <p className="text-sm text-text-secondary">The label used for this workspace.</p>
                </div>
                <input
                  value={nameInput}
                  onChange={(e) => setNameInput(e.target.value)}
                  onKeyDown={handleNameKeyDown}
                  maxLength={64}
                  className="w-full px-3 py-2 rounded-md border border-border-default bg-surface-primary text-sm text-text-primary focus:border-accent focus:outline-none"
                />
              </div>
            </div>
          </section>

          <section className="mb-10">
            <h2 className="text-xl font-semibold text-text-primary mb-5">Workspace</h2>
            <ModuleConfigSection scope={{ workspaceId }} />
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
                className="mb-10"
              >
                <h2
                  className={`text-xl font-semibold mb-5 flex items-center gap-2 ${
                    isDisabled ? 'text-text-muted' : 'text-text-primary'
                  }`}
                >
                  <span>{t(c.labelKey) ?? c.labelKey}</span>
                  {moduleOwned && (
                    <PuzzlePiece
                      size={14}
                      weight="fill"
                      className="flex-shrink-0 rotate-[30deg] text-text-muted"
                      aria-hidden
                    />
                  )}
                </h2>
                {!isDisabled && <Body ctx={ctx} />}
              </section>
            )
          })}

          {/* Danger Zone */}
          <section className="border-t border-border-subtle pt-6 mt-10">
            <h2 className="text-xl font-semibold text-red-400 mb-3">Danger Zone</h2>
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

        {hasDirtyChanges && (
          <div className="sticky bottom-0 z-20 border-t border-border-subtle bg-surface-primary/95 px-8 py-3 shadow-[0_-8px_24px_rgba(0,0,0,0.18)] backdrop-blur">
            <div className="max-w-4xl mx-auto flex items-center justify-between gap-4">
              <span className="text-sm text-text-secondary">Unsaved changes</span>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={handleCancelAll}
                  className="px-3 py-2 rounded-md border border-border-subtle text-sm text-text-secondary hover:bg-surface-muted cursor-pointer"
                >
                  {t('common.cancel') ?? 'Cancel'}
                </button>
                <button
                  type="button"
                  onClick={handleSaveAll}
                  className="px-4 py-2 rounded-md bg-accent text-white text-sm font-medium hover:bg-accent/90 cursor-pointer disabled:opacity-40 disabled:cursor-not-allowed"
                  disabled={nameInput.trim() === ''}
                >
                  {t('common.save') ?? 'Save'}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>
    </WorkspaceSettingsDraftProvider>
  )
}
