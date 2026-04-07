import { useState, useCallback } from 'react'
import { Trash } from '@phosphor-icons/react'
import { useWorkspaceStore } from '../store'
import { useTabStore } from '../../../stores/useTabStore'
import { useI18nStore } from '../../../stores/useI18nStore'
import { getPrimaryPane } from '../../../lib/pane-tree'
import { getPaneLabel } from '../../../lib/pane-labels'
import { WorkspaceIcon } from './WorkspaceIcon'
import { workspaceColorStyle } from '../lib/workspace-colors'
import { ColorGrid } from './WorkspaceColorPicker'
import { WorkspaceIconPicker } from './WorkspaceIconPicker'
import { WorkspaceDeleteDialog } from './WorkspaceDeleteDialog'

interface Props {
  workspaceId: string
}

export function WorkspaceSettingsPage({ workspaceId }: Props) {
  const t = useI18nStore((s) => s.t)
  const ws = useWorkspaceStore((s) => s.workspaces.find((w) => w.id === workspaceId))
  const renameWorkspace = useWorkspaceStore((s) => s.renameWorkspace)
  const setWorkspaceColor = useWorkspaceStore((s) => s.setWorkspaceColor)
  const setWorkspaceIcon = useWorkspaceStore((s) => s.setWorkspaceIcon)
  const tabs = useTabStore((s) => s.tabs)

  const [nameInput, setNameInput] = useState(ws?.name ?? '')
  const [showDelete, setShowDelete] = useState(false)

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
            className="w-16 h-16 rounded-xl flex items-center justify-center text-2xl"
            style={{ backgroundColor: workspaceColorStyle(ws.color).bg, color: workspaceColorStyle(ws.color).fg }}
          >
            <WorkspaceIcon icon={ws.icon} name={ws.name} size={32} />
          </div>
          <input
            value={nameInput}
            onChange={(e) => setNameInput(e.target.value)}
            onBlur={handleNameBlur}
            onKeyDown={handleNameKeyDown}
            className="text-center text-lg font-semibold bg-transparent text-text-primary border-b border-transparent hover:border-border-default focus:border-accent focus:outline-none px-2 py-1 transition-colors"
          />
        </div>

        {/* Color */}
        <section className="mb-8">
          <h3 className="text-xs font-semibold text-text-secondary uppercase tracking-wider mb-3">
            {t('workspace.change_color') ?? 'Color'}
          </h3>
          <ColorGrid currentColor={ws.color} onSelect={(color) => setWorkspaceColor(workspaceId, color)} />
        </section>

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
          />
        </section>

        {/* Danger Zone */}
        <section className="border-t border-border-subtle pt-6">
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
                closedTabIds.forEach((id) => useTabStore.getState().closeTab(id))
                useWorkspaceStore.getState().removeWorkspace(workspaceId)
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
