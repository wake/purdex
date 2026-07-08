import { useCallback, useMemo, useState } from 'react'
import { FilePlus, FileText, Image as ImageIcon, FilePdf } from '@phosphor-icons/react'
import { useI18nStore } from '../../stores/useI18nStore'
import { useRecentFilesStore, type RecentFileKind } from '../../stores/useRecentFilesStore'
import { useHostStore } from '../../stores/useHostStore'
import { openRecentEntry } from '../../lib/recent-files/open-recent-entry'
import { createUniqueInAppFile } from '../../lib/inapp-namer'
import { STORAGE_ROOT } from '../../lib/storage-paths'
import type { PaneContent } from '../../types/tab'
import type { FileSource } from '../../types/fs'

interface Props {
  onSelect: (content: PaneContent) => void
}

type FilterKey = 'all' | RecentFileKind

const FILTERS: { key: FilterKey; label: string }[] = [
  { key: 'all', label: 'editor.recent.filter.all' },
  { key: 'editor', label: 'editor.recent.filter.text' },
  { key: 'image-preview', label: 'editor.recent.filter.image' },
  { key: 'pdf-preview', label: 'editor.recent.filter.pdf' },
]

function KindIcon({ kind }: { kind: RecentFileKind }) {
  if (kind === 'image-preview') return <ImageIcon size={16} />
  if (kind === 'pdf-preview') return <FilePdf size={16} />
  return <FileText size={16} />
}

export function EditorNewTabSection({ onSelect }: Props) {
  const t = useI18nStore((s) => s.t)
  const files = useRecentFilesStore((s) => s.files)
  const hosts = useHostStore((s) => s.hosts)
  const [filter, setFilter] = useState<FilterKey>('all')

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

  const visible = useMemo(
    () => (filter === 'all' ? files : files.filter((f) => f.kind === filter)),
    [files, filter],
  )

  return (
    <div className="flex flex-col gap-4">
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

      {files.length > 0 && (
        <div className="flex flex-col gap-2">
          <h4 className="text-xs font-medium text-text-secondary px-1">{t('editor.recent.title')}</h4>
          <div className="flex gap-1">
            {FILTERS.map((f) => (
              <button
                key={f.key}
                onClick={() => setFilter(f.key)}
                className={`px-2 py-0.5 rounded text-xs transition-colors ${
                  filter === f.key
                    ? 'bg-surface-active text-text-primary'
                    : 'text-text-secondary hover:bg-surface-hover'
                }`}
              >
                {t(f.label)}
              </button>
            ))}
          </div>
          <ul className="flex flex-col">
            {visible.map((entry) => {
              const badge =
                entry.source.type === 'daemon'
                  ? hosts[entry.source.hostId]?.name ?? entry.source.hostId
                  : null
              return (
                <li key={`${entry.source.type}:${entry.source.type === 'daemon' ? entry.source.hostId : ''}:${entry.path}`}>
                  <button
                    onClick={() => void openRecentEntry(entry, onSelect)}
                    className="w-full flex items-center gap-2 px-2 py-1.5 rounded hover:bg-surface-hover text-left transition-colors"
                  >
                    <span className="text-text-muted flex-shrink-0"><KindIcon kind={entry.kind} /></span>
                    <span className="text-sm text-text-primary truncate" title={entry.path}>{entry.name}</span>
                    <span className="text-xs text-text-muted truncate flex-1" title={entry.path}>{entry.path}</span>
                    {badge && (
                      <span
                        data-testid="recent-host-badge"
                        className="text-[10px] px-1.5 py-0.5 rounded bg-surface-secondary text-text-secondary flex-shrink-0"
                      >
                        {badge}
                      </span>
                    )}
                  </button>
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
