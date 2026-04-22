import { useCallback } from 'react'
import { FilePlus, FileText } from '@phosphor-icons/react'
import { getFsBackend } from '../../lib/fs-backend'
import { scanPaneTree } from '../../lib/pane-tree'
import { useI18nStore } from '../../stores/useI18nStore'
import { useTabStore } from '../../stores/useTabStore'
import type { PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function EditorNewTabSection({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)

  const nextUntitledName = useCallback(async (suggestedExtension: '.txt' | '.md') => {
    const source: FileSource = { type: 'inapp' }
    const backend = getFsBackend(source)
    if (!backend) return null

    const usedFilenames = new Set<string>()

    for (const tab of Object.values(useTabStore.getState().tabs)) {
      scanPaneTree(tab.layout, (pane) => {
        if (pane.content.kind !== 'editor' || pane.content.source.type !== 'inapp') return
        if (pane.content.untitled) {
          const suffix = pane.content.untitled.hasBeenRenamed ? '' : pane.content.untitled.suggestedExtension
          usedFilenames.add(`${pane.content.untitled.name}${suffix}`)
          return
        }
        const path = pane.content.filePath
        if (path.startsWith('/buffer/')) {
          usedFilenames.add(path.slice('/buffer/'.length))
        }
      })
    }

    const entries = await backend.list('/buffer')
    for (const entry of entries) {
      if (!entry.isDir) usedFilenames.add(entry.name)
    }

    let n = 0
    let name = 'Untitled'
    while (usedFilenames.has(`${name}${suggestedExtension}`)) {
      n += 1
      name = `Untitled-${n}`
    }

    return name
  }, [])

  const createFile = useCallback(async (suggestedExtension: '.txt' | '.md') => {
    const source: FileSource = { type: 'inapp' }

    const name = await nextUntitledName(suggestedExtension)
    if (!name) {
      console.error('[editor] InApp backend not available')
      return
    }

    onSelect({
      kind: 'editor',
      source,
      filePath: `untitled:${name}`,
      untitled: {
        name,
        suggestedExtension,
        hasBeenRenamed: false,
      },
    } as PaneContent)
  }, [nextUntitledName, onSelect])

  return (
    <div className="flex gap-2">
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('.txt')}
      >
        <FilePlus size={16} />
        {t('editor.new_file')}
      </button>
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('.md')}
      >
        <FileText size={16} />
        {t('editor.new_markdown')}
      </button>
    </div>
  )
}
