import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { FilePlus, PencilSimple, Stack, Trash, FolderOpen } from '@phosphor-icons/react'
import type { PaneRendererProps } from '../../lib/module-registry'
import { getFsBackend } from '../../lib/fs-backend'
import { scanPaneTree } from '../../lib/pane-tree'
import { bufferKey } from '../../lib/editor-buffer-key'
import { useEditorStore } from '../../stores/useEditorStore'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import { createTab } from '../../types/tab'
import type { FileEntry } from '../../types/fs'
import type { FileSource } from '../../types/fs'
import type { Pane, PaneContent, Tab } from '../../types/tab'
import { RenamePopover } from '../RenamePopover'
import { createMetadata } from '../../lib/editor-language'

// v1.5 G1 fix — mirror EditorPane's rename three-step sync (backend →
// tab-layout → editor-store). Without this, renaming via the buffers
// pane leaves the editor store keyed by the old path: a subsequent Save
// would write to the stale filename and a re-open would resurrect a
// ghost buffer. Reused only inside `handleRenameConfirm`, but extracted
// so the three calls stay visually grouped.
//
// v1.5 R6-MED follow-up — the `renameBuffer` metadata argument must also
// refresh language + languageSource when crossing file extensions; see
// `EditorPane.handleRenameSubmit` lines 376-380 for the canonical pattern
// (preserve manual overrides, otherwise recompute from the new path).
async function performBufferRename(fromPath: string, targetPath: string) {
  const backend = getFsBackend({ type: 'inapp' })
  if (!backend) throw new Error('InApp backend unavailable')
  await backend.rename(fromPath, targetPath)
  const source: FileSource = { type: 'inapp' }
  useTabStore.getState().renameEditorPanes(source, fromPath, targetPath)
  const oldKey = bufferKey(source, fromPath)
  const newKey = bufferKey(source, targetPath)
  const currentBuffer = useEditorStore.getState().buffers[oldKey]
  const nextMetadata = currentBuffer?.languageSource === 'manual'
    ? { language: currentBuffer.language, languageSource: 'manual' as const }
    : createMetadata(source, targetPath)
  useEditorStore.getState().renameBuffer(oldKey, newKey, nextMetadata)
}

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
  const [renameError, setRenameError] = useState<string | null>(null)
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
    setRenameError(null)
    setRenameTarget(singleSelected)
  }, [singleSelected])

  const handleRenameConfirm = useCallback(
    async (newName: string) => {
      if (!renameTarget) return
      const backend = getFsBackend({ type: 'inapp' })
      if (!backend) return
      const fromPath = `/buffer/${renameTarget}`
      const targetPath = `/buffer/${newName}`
      // F4 (spec §4.5): `InAppBackend.rename` is a blind overwrite
      // (store.set(to, ...) + store.delete(from)); a collision silently
      // destroys the existing file. Pre-check with `stat` so the rename
      // is aborted before any backend mutation.
      if (targetPath !== fromPath) {
        const exists = await backend
          .stat(targetPath)
          .then(() => true)
          .catch(() => false)
        if (exists) {
          setRenameError(t('editor.buffers.rename_exists_error'))
          return
        }
      }
      try {
        await performBufferRename(fromPath, targetPath)
        setRenameTarget(null)
        setRenameError(null)
        setSelected(new Set())
        refresh()
      } catch (err) {
        setError(err instanceof Error ? err.message : String(err))
      }
    },
    [renameTarget, refresh, t],
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

    const targets = selectedArray.map((n) => `/buffer/${n}`)

    // Snapshot every editor pane whose content points at one of the
    // targeted paths. This is shared by the locked/dirty pre-check and
    // the later close loop.
    const { tabs } = useTabStore.getState()
    const openPanes: Array<[string, Pane]> = []
    for (const [tabId, tab] of Object.entries(tabs) as Array<[string, Tab]>) {
      scanPaneTree(tab.layout, (pane) => {
        const c = pane.content
        if (
          c.kind === 'editor' &&
          c.source.type === 'inapp' &&
          targets.includes(c.filePath)
        ) {
          openPanes.push([tabId, pane])
        }
      })
    }

    // F2 (spec §4.9.5): refuse delete outright if any affected pane
    // lives in a locked tab. The user must unlock or close the tab
    // before any backend mutation. closePane is ALSO a no-op for
    // locked single-pane tabs, so without this guard the backend
    // delete would silently resurrect after save.
    const lockedHit = openPanes.some(([tabId]) => tabs[tabId]?.locked)
    if (lockedHit) {
      setError(t('editor.buffers.delete_locked_refused'))
      return
    }

    // F5 (spec §4.9.5): dirty-specific confirm wins over generic.
    const dirtyHits = openPanes.filter(([, pane]) => {
      const content = pane.content
      if (content.kind !== 'editor') return false
      const key = bufferKey(content.source, content.filePath)
      return useEditorStore.getState().buffers[key]?.isDirty === true
    })

    // Confirm based on strongest applicable message.
    if (dirtyHits.length > 0) {
      const ok = window.confirm(
        t('editor.buffers.delete_dirty_confirm', { count: dirtyHits.length }),
      )
      if (!ok) return
    } else if (selectedArray.length === 1) {
      // F6 (spec §4.9.5): single-delete confirms too.
      const ok = window.confirm(t('editor.buffers.delete_one_confirm'))
      if (!ok) return
    } else {
      const ok = window.confirm(
        t('editor.buffers.confirm_delete', { count: selectedArray.length }),
      )
      if (!ok) return
    }

    setLoading(true)
    setError(null)
    try {
      // Step 1: close every affected pane BEFORE deleting the underlying
      // file. All panes are guaranteed unlocked (pre-check guard above).
      // v1.5 G2 fix — also drop the editor-store paneState + buffer.
      // `useTabStore.closePane` only tears down the tab-layout pane; if
      // the hosting tab is inactive (EditorPane already unmounted /
      // never mounted), the editor store keeps the stale entry and the
      // post-delete backend write resurrects the buffer on next mount.
      // `expectedBufferKey` guards against a concurrent pane rebind.
      for (const [tabId, pane] of openPanes) {
        useTabStore.getState().closePane(tabId, pane.id)
        if (pane.content.kind === 'editor') {
          const key = bufferKey(pane.content.source, pane.content.filePath)
          useEditorStore.getState().closePane(pane.id, key)
        }
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

  // v1.5 G3 fix — explicit-arg helper so double-click passes the
  // clicked row's name directly instead of racing the `setSelected`
  // state commit. Previously `handleOpen` read `singleSelected` from a
  // stale closure inside `queueMicrotask`, which captured `null` (or
  // the wrong row) and silently no-op'd. Toolbar Open and row double-
  // click now share one entry point.
  const openBufferByName = useCallback((name: string) => {
    const path = `/buffer/${name}`
    const newContent: PaneContent = {
      kind: 'editor',
      source: { type: 'inapp' },
      filePath: path,
    }
    const { tabs, tabOrder, activeTabId, setPaneContent, setActiveTab, addTab } =
      useTabStore.getState()

    // F3 (spec §4.6 v1.4): eligibility is (a) kind 'editor', (b) source
    // 'inapp' (daemon/local editor panes are foreign storage — never
    // overwrite them), AND (c) the currently-bound buffer is clean.
    // Dirty panes are skipped so a management-pane open doesn't silently
    // destroy unsaved work. Falls through to "new tab" if no pane is
    // eligible.
    function firstEligibleEditorPaneId(tab: Tab | undefined): string | null {
      if (!tab) return null
      let found: string | null = null
      scanPaneTree(tab.layout, (p) => {
        if (found) return
        const c = p.content
        if (c.kind !== 'editor') return
        if (c.source.type !== 'inapp') return
        const key = bufferKey(c.source, c.filePath)
        if (useEditorStore.getState().buffers[key]?.isDirty === true) return
        found = p.id
      })
      return found
    }

    // Rule 1: active tab.
    if (activeTabId && tabs[activeTabId]) {
      const pid = firstEligibleEditorPaneId(tabs[activeTabId])
      if (pid) {
        setPaneContent(activeTabId, pid, newContent)
        setActiveTab(activeTabId)
        return
      }
    }
    // Rule 2: tabOrder scan.
    for (const tid of tabOrder) {
      if (tid === activeTabId) continue
      const pid = firstEligibleEditorPaneId(tabs[tid])
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
  }, [])

  const handleOpen = useCallback(() => {
    if (!singleSelected) return
    openBufferByName(singleSelected)
  }, [singleSelected, openBufferByName])

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
                    onDoubleClick={() => openBufferByName(f.name)}
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
          onCancel={() => {
            setRenameTarget(null)
            setRenameError(null)
          }}
          error={renameError ?? undefined}
          onClearError={() => setRenameError(null)}
          validateName={validateRename}
        />
      )}
    </div>
  )
}
