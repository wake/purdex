import { useCallback } from 'react'
import { FilePlus, FileText } from '@phosphor-icons/react'
import { getInAppBackend } from '../../lib/fs-backend-inapp'
import { useI18nStore } from '../../stores/useI18nStore'
import type { PaneContent } from '../../types/tab'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function EditorNewTabSection({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const createFile = useCallback(async (ext: string) => {
    const backend = getInAppBackend()
    if (!backend) {
      console.error('[editor] InApp backend not available')
      return
    }
    try {
      const created = await backend.createUntitledFile(ext)
      onSelect({
        kind: 'editor',
        source: { type: 'inapp' },
        docId: created.docId,
        filePath: created.path,
      })
    } catch (err) {
      console.error('[editor] Failed to create file:', err)
    }
  }, [onSelect])

  return (
    <div className="flex gap-2">
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('txt')}
      >
        <FilePlus size={16} />
        {t('editor.new_file')}
      </button>
      <button
        className="flex items-center gap-2 px-4 py-2 rounded-lg border border-border-subtle bg-surface-secondary hover:bg-surface-hover text-text-primary text-sm transition-colors"
        onClick={() => createFile('md')}
      >
        <FileText size={16} />
        {t('editor.new_markdown')}
      </button>
    </div>
  )
}
