import { useCallback, useEffect, useState } from 'react'
import { FolderSimple, File, CaretRight, CaretDown } from '@phosphor-icons/react'
import { useHostStore } from '../stores/useHostStore'
import type { ViewProps } from '../lib/module-registry'

interface FileEntry {
  name: string
  isDir: boolean
  size: number
}

interface DirState {
  entries: FileEntry[]
  expanded: boolean
  loading: boolean
}

export function FileTreeView({ isActive }: ViewProps) {
  void isActive
  const activeHostId = useHostStore((s) => s.activeHostId ?? s.hostOrder[0] ?? '')
  const baseUrl = useHostStore((s) => (activeHostId ? s.getDaemonBase(activeHostId) : ''))

  const [rootPath, setRootPath] = useState<string>('')
  const [rootEntries, setRootEntries] = useState<FileEntry[]>([])
  const [expandedDirs, setExpandedDirs] = useState<Record<string, DirState>>({})
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const fetchDir = useCallback(async (path?: string): Promise<{ path: string; entries: FileEntry[] }> => {
    const url = path
      ? `${baseUrl}/api/files?path=${encodeURIComponent(path)}`
      : `${baseUrl}/api/files`
    const authHeaders = useHostStore.getState().getAuthHeaders(activeHostId)
    const res = await fetch(url, { headers: authHeaders })
    if (!res.ok) throw new Error(`Failed to fetch: ${res.statusText}`)
    return res.json()
  }, [baseUrl, activeHostId])

  useEffect(() => {
    if (!baseUrl) return
    // eslint-disable-next-line react-hooks/set-state-in-effect -- sync reset before async fetch
    setLoading(true)
    setError(null)
    fetchDir()
      .then((data) => {
        setRootPath(data.path)
        setRootEntries(data.entries)
      })
      .catch((err) => setError(err.message))
      .finally(() => setLoading(false))
  }, [baseUrl, fetchDir])

  const toggleDir = useCallback(async (fullPath: string) => {
    const existing = expandedDirs[fullPath]
    if (existing?.expanded) {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { ...prev[fullPath], expanded: false } }))
      return
    }
    if (existing?.entries.length) {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { ...prev[fullPath], expanded: true } }))
      return
    }
    setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: [], expanded: true, loading: true } }))
    try {
      const data = await fetchDir(fullPath)
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: data.entries, expanded: true, loading: false } }))
    } catch {
      setExpandedDirs((prev) => ({ ...prev, [fullPath]: { entries: [], expanded: false, loading: false } }))
    }
  }, [expandedDirs, fetchDir])

  if (!baseUrl) return <div className="p-3 text-xs text-text-muted">No host connected</div>
  if (loading) return <div className="p-3 text-xs text-text-muted">Loading...</div>
  if (error) return <div className="p-3 text-xs text-red-400">Error: {error}</div>

  const renderEntries = (entries: FileEntry[], parentPath: string, depth: number) => (
    <div>
      {entries.map((entry) => {
        const fullPath = parentPath === '/' ? `/${entry.name}` : `${parentPath}/${entry.name}`
        const dirState = expandedDirs[fullPath]
        const isExpanded = dirState?.expanded ?? false
        return (
          <div key={entry.name}>
            <button
              data-testid={`file-entry-${entry.name}`}
              className="w-full flex items-center gap-1 px-2 py-0.5 text-xs text-text-primary hover:bg-surface-hover transition-colors"
              style={{ paddingLeft: 8 + depth * 16 }}
              onClick={() => entry.isDir && toggleDir(fullPath)}
            >
              {entry.isDir ? (
                <>
                  {isExpanded ? <CaretDown size={12} /> : <CaretRight size={12} />}
                  <FolderSimple size={14} className="text-text-muted shrink-0" />
                </>
              ) : (
                <>
                  <span className="w-3" />
                  <File size={14} className="text-text-muted shrink-0" />
                </>
              )}
              <span className="truncate">{entry.name}</span>
            </button>
            {entry.isDir && isExpanded && dirState?.entries && renderEntries(dirState.entries, fullPath, depth + 1)}
          </div>
        )
      })}
    </div>
  )

  return (
    <div className="flex-1 overflow-auto text-xs">
      <div className="px-2 py-1 text-text-muted font-medium truncate border-b border-border-subtle">{rootPath}</div>
      {renderEntries(rootEntries, rootPath, 0)}
    </div>
  )
}
