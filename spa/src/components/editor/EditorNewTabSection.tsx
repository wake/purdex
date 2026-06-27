import { useCallback } from 'react'
import { FilePlus, FileText } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { createUniqueInAppFile } from '../../lib/inapp-namer'
import { STORAGE_ROOT } from '../../lib/storage-paths'
import type { PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

interface Props {
  onSelect: (content: PaneContent) => void
}

export function EditorNewTabSection({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)

  // T1b-2: eager reservation. Both buttons map their label to a bare extension
  // (`'txt' | 'md'`, no leading dot — the namer forms the path) and reserve a
  // real `/buffer/Untitled[-N].<ext>` file via the unified atomic namer, then
  // open that real path. The old lazy in-memory `untitled:` buffer producer is
  // gone; `untitled:` is no longer minted here (its runtime load/rename/save
  // contract in EditorPane stays intact for any already-persisted tabs).
  const createFile = useCallback(async (ext: 'txt' | 'md') => {
    const source: FileSource = { type: 'inapp' }
    let filePath: string
    try {
      filePath = await createUniqueInAppFile(STORAGE_ROOT, ext)
    } catch (err) {
      console.error('[editor] failed to reserve a new file', err)
      return
    }
    onSelect({ kind: 'editor', source, filePath })
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
