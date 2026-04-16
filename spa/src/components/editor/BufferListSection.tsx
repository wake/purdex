import { useEffect, useState, useCallback } from 'react'
import { Trash } from '@phosphor-icons/react'
import { getFsBackend } from '../../lib/fs-backend'
import type { FileEntry } from '../../types/fs'

export function BufferListSection() {
  const [files, setFiles] = useState<FileEntry[]>([])
  const [refreshKey, setRefreshKey] = useState(0)

  const refresh = useCallback(() => setRefreshKey((k) => k + 1), [])

  useEffect(() => {
    let stale = false
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    backend.list('/buffer')
      .then((entries) => { if (!stale) setFiles(entries.filter((e) => !e.isDir)) })
      .catch(() => { if (!stale) setFiles([]) })
    return () => { stale = true }
  }, [refreshKey])

  const handleDelete = async (name: string) => {
    const backend = getFsBackend({ type: 'inapp' })
    if (!backend) return
    try {
      await backend.delete(`/buffer/${name}`)
    } catch (err) {
      console.error('[editor] Failed to delete buffer:', err)
    }
    refresh()
  }

  if (files.length === 0) {
    return <p className="text-xs text-text-muted">No in-app files</p>
  }

  return (
    <div className="space-y-1">
      {files.map((f) => (
        <div key={f.name} className="flex items-center justify-between py-1 px-2 rounded hover:bg-surface-hover">
          <span className="text-xs text-text-primary truncate">{f.name}</span>
          <button
            onClick={() => handleDelete(f.name)}
            className="p-1 rounded hover:bg-surface-hover text-text-muted hover:text-red-400 transition-colors"
            title="Delete"
          >
            <Trash size={12} />
          </button>
        </div>
      ))}
    </div>
  )
}
