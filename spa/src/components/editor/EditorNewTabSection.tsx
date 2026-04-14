import { useCallback } from 'react'
import { FilePlus, FileText } from '@phosphor-icons/react'
import { generateId } from '../../lib/id'
import { getFsBackend } from '../../lib/fs-backend'
import type { PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function EditorNewTabSection({ onSelect }: Props) {
  const createFile = useCallback(async (ext: string) => {
    const id = generateId()
    const filePath = `/buffer/${id}.${ext}`
    const source: FileSource = { type: 'inapp' }

    const backend = getFsBackend(source)
    if (!backend) {
      console.error('[editor] InApp backend not available')
      return
    }
    try {
      await backend.write(filePath, new TextEncoder().encode(''))
    } catch (err) {
      console.error('[editor] Failed to create file:', err)
      return
    }

    onSelect({ kind: 'editor', source, filePath } as PaneContent)
  }, [onSelect])

  return (
    <div className="flex gap-2">
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('txt')}
      >
        <FilePlus size={16} />
        New File
      </button>
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('md')}
      >
        <FileText size={16} />
        New Markdown
      </button>
    </div>
  )
}
