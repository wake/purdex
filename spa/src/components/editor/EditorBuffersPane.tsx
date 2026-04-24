import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FilePlus, PencilSimple, Stack, Trash, FolderOpen } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'
import { scanPaneTree } from '../../lib/pane-tree'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import type { FileEntry } from '../../types/fs'
import type { PaneContent, Tab } from '../../types/tab'
import { RenamePopover } from '../RenamePopover'

/**
 * EditorBuffersPane — management UI for `/buffer/*` entries (spec §4.5).
 *
 * Flat (no subfolders) CRUD with smart-open on double-click / Open button
 * (§4.6: active tab first → tabOrder scan → new tab). Deletion closes any
 * open editor pane pointing at the target path BEFORE calling
 * `backend.delete` (§4.9.5; `closePane` is a no-op on locked tabs — the
 * pane stays and shows EditorPane's "file not found" banner, intentional).
 */
export function EditorBuffersPane(_: PaneRendererProps) {
  const t = useI18nStore((s) => s.t)

  const [files, setFiles] = useState<FileEntry[]>([])
  const [selected, setSelected] = useState<Set<string>>(() => new Set())
  const [refreshKey, setRefreshKey] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [renameTarget, setRenameTarget] = useState<string | null>(null)
  const renameAnchorRef = useRef<HTMLButtonElement | null>(null)

  // Load files whenever `refreshKey` changes or on mount.
  useEffect(() => {
    let stale = false
    setLoading(true)
    setError(null)
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) {
      setFiles([])
      setError('InApp backend unavailable')
      setLoading(false)
      return
    }
    backend
      .list('/buffer')
      .then((entries) => {
        if (stale) return
        const filtered = entries
          .filter((e) => !e.isDir)
          .slice()
          .sort((a, b) => a.name.localeCompare(b.name))
        setFiles(filtered)
      })
      .catch((err: unknown) => {
        if (stale) return
        setFiles([])
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!stale) setLoading(false)
      })
    return () => {
      stale = true
    }
  }, [refreshKey])

  const refresh = useCallback(() => setRefreshKey((k) => k + 1), [])

  const toggleSelect = useCallback((name: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }, [])

  const selectedArray = useMemo(() => Array.from(selected), [selected])
  const singleSelected = selectedArray.length === 1 ? selectedArray[0] : null

  // --- Actions ---

  const handleNew = useCallback(async () => {
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    const path = `/buffer/Untitled-${Date.now()}.md`
    try {
      await backend.write(path, new Uint8Array(0))
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    }
  }, [refresh])

  const handleOpenRename = useCallback(() => {
    if (!singleSelected) return
    setRenameTarget(singleSelected)
  }, [singleSelected])

  const handleRenameConfirm = useCallback(
    async (newName: string) => {
      if (!renameTarget) return
      const backend = getFsBackend({ type: 'inapp' })
      if (!backend) return
      try {
        await backend.rename(`/buffer/${renameTarget}`, `/buffer/${newName}`)
        setRenameTarget(null)
        setSelected(new Set())
        refresh()
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    },
    [renameTarget, refresh],
  )

  const validateRename = useCallback(
    (trimmed: string, current: string): string | undefined => {
      if (!trimmed) return undefined
      if (trimmed === current) return undefined
      if (trimmed.includes('/')) return t('editor.buffers.rename_slash_error')
      return undefined
    },
    [t],
  )

  const handleDelete = useCallback(async () => {
    if (selectedArray.length === 0) return
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    if (selectedArray.length > 1) {
      const ok = window.confirm(t('editor.buffers.confirm_delete', { count: selectedArray.length }))
      if (!ok) return
    }
    const targets = selectedArray.map((n) => `/buffer/${n}`)
    setLoading(true)
    try {
      // Step 1: snapshot every editor pane pointing at a targeted path and
      // close it BEFORE deleting the underlying file. closePane is a no-op
      // for locked tabs (useTabStore.ts:195) — intentional per spec §4.9.5.
      const { tabs } = useTabStore.getState()
      const panesToClose: Array<[string, string]> = []
      for (const [tabId, tab] of Object.entries(tabs) as Array<[string, Tab]>) {
        scanPaneTree(tab.layout, (pane) => {
          const c = pane.content
          if (
            c.kind === 'editor' &&
            c.source.type === 'inapp' &&
            targets.includes(c.filePath)
          ) {
            panesToClose.push([tabId, pane.id])
          }
        })
      }
      for (const [tabId, paneId] of panesToClose) {
        useTabStore.getState().closePane(tabId, paneId)
      }
      // Step 2: actually delete the files.
      for (const path of targets) {
        await backend.delete(path)
      }
      setSelected(new Set())
      refresh()
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setLoading(false)
    }
  }, [selectedArray, t, refresh])

  const handleOpen = useCallback(() => {
    if (!singleSelected) return
    const path = `/buffer/${singleSelected}`
    const newContent: PaneContent = {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: path,
    }
    const { tabs, tabOrder, activeTabId, setPaneContent, setActiveTab, addTab } =
      useTabStore.getState()

    function firstEditorPaneId(tab: Tab | undefined): string | null {
      if (!tab) return null
      let found: string | null = null
      scanPaneTree(tab.layout, (p) => {
        if (found) return
        if (p.content.kind === 'editor') found = p.id
      })
      return found
    }

    // Rule 1: active tab.
    if (activeTabId && tabs[activeTabId]) {
      const pid = firstEditorPaneId(tabs[activeTabId])
      if (pid) {
        setPaneContent(activeTabId, pid, newContent)
        setActiveTab(activeTabId)
        return
      }
    }
    // Rule 2: tabOrder scan.
    for (const tid of tabOrder) {
      if (tid === activeTabId) continue
      const pid = firstEditorPaneId(tabs[tid])
      if (pid) {
        setPaneContent(tid, pid, newContent)
        setActiveTab(tid)
        return
      }
    }
    // Rule 3: new tab.
    const tab = createTab(newContent)
    addTab(tab)
    setActiveTab(tab.id)
  }, [singleSelected])

  // --- Render ---

  const hasAny = files.length > 0
  const canRename = selectedArray.length === 1
  const canDelete = selectedArray.length >= 1
  const canOpen = selectedArray.length === 1

  return (
    <div className="flex flex-col h-full overflow-hidden">
      {/* Toolbar */}
      <div className="flex items-center gap-2 px-3 py-2 border-b border-border-subtle">
        <Stack size={16} className="text-text-muted" />
        <h2 className="text-sm font-medium text-text-primary">{t('editor.buffers.tab_title')}</h2>
        <div className="flex-1" />
        <button
          data-testid="toolbar-new"
          onClick={handleNew}
          disabled={loading}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.new')}
        >
          <FilePlus size={14} />
          {t('editor.buffers.new')}
        </button>
        <button
          data-testid="toolbar-rename"
          ref={renameAnchorRef}
          onClick={handleOpenRename}
          disabled={!canRename || loading}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.rename')}
        >
          <PencilSimple size={14} />
          {t('editor.buffers.rename')}
        </button>
        <button
          data-testid="toolbar-delete"
          onClick={handleDelete}
          disabled={!canDelete || loading}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.delete')}
        >
          <Trash size={14} />
          {t('editor.buffers.delete')}
        </button>
        <button
          data-testid="toolbar-open"
          onClick={handleOpen}
          disabled={!canOpen || loading}
          className="flex items-center gap-1 px-2 py-1 rounded-md text-xs text-text-secondary hover:bg-surface-hover disabled:opacity-50"
          title={t('editor.buffers.open')}
        >
          <FolderOpen size={14} />
          {t('editor.buffers.open')}
        </button>
      </div>

      {/* Body */}
      <div className="flex-1 overflow-y-auto">
        {error && (
          <div className="p-4 text-xs text-red-400">{error}</div>
        )}
        {!error && !hasAny && (
          <div className="p-8 flex flex-col items-center justify-center text-text-muted">
            <Stack size={32} className="mb-2 opacity-50" />
            <p className="text-sm">{t('editor.buffers.empty')}</p>
          </div>
        )}
        {hasAny && (
          <ul className="divide-y divide-border-subtle">
            {files.map((f) => {
              const isSelected = selected.has(f.name)
              return (
                <li key={f.name}>
                  <button
                    data-testid="buffer-row"
                    data-name={f.name}
                    aria-selected={isSelected}
                    onClick={() => toggleSelect(f.name)}
                    onDoubleClick={() => {
                      setSelected(new Set([f.name]))
                      // Defer so state commits before handleOpen reads it.
                      queueMicrotask(() => handleOpen())
                    }}
                    className={
                      'w-full flex items-center justify-between px-3 py-2 text-left text-xs transition-colors ' +
                      (isSelected
                        ? 'bg-surface-selected text-text-primary'
                        : 'text-text-secondary hover:bg-surface-hover')
                    }
                  >
                    <span className="truncate">{f.name}</span>
                    <span className="text-text-muted tabular-nums">{f.size} B</span>
                  </button>
                </li>
              )
            })}
          </ul>
        )}
      </div>

      {renameTarget && (
        <RenamePopover
          anchorRect={
            renameAnchorRef.current?.getBoundingClientRect() ??
            ({
              left: 0,
              top: 0,
              right: 0,
              bottom: 0,
              width: 0,
              height: 0,
              x: 0,
              y: 0,
              toJSON: () => ({}),
            } as DOMRect)
          }
          currentName={renameTarget}
          onConfirm={handleRenameConfirm}
          onCancel={() => setRenameTarget(null)}
          validateName={validateRename}
        />
      )}
    </div>
  )
}
