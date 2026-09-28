// spa/src/hooks/useWorkerUploads.ts — the attachment chips of one worker pane
// (worker-pane theme spec §9.1). Lives in ExecutionView, not WorkerInput:
// the input is re-keyed on the restored draft and remounts after a failed
// send, and the drop target is the whole pane. Files upload one at a time, in
// the order they were added; a chip removed before its turn is never sent,
// one removed mid-upload stays removed (the file on disk is left alone).
import { useCallback, useEffect, useRef, useState } from 'react'
import { uploadWorkerFile } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import type { Chip } from '../lib/nex/worker-upload'

export interface WorkerUploads {
  chips: Chip[]
  add(files: File[]): void
  remove(key: string): void
  /** Remove the given chips, or every chip when no keys are passed. */
  clear(keys?: readonly string[]): void
}

let seq = 0

export function useWorkerUploads(hostId: string, executionId: string): WorkerUploads {
  const [chips, setChips] = useState<Chip[]>([])
  // Mirrors for work outside render: which chips still exist (a removed one
  // is skipped / ignored) and their thumbnails (revoked on remove / unmount).
  const live = useRef(new Set<string>())
  const previews = useRef(new Map<string, string>())
  const queue = useRef<Promise<void>>(Promise.resolve())
  const mounted = useRef(true)

  useEffect(() => {
    mounted.current = true
    const urls = previews.current
    return () => {
      mounted.current = false
      for (const url of urls.values()) URL.revokeObjectURL(url)
      urls.clear()
    }
  }, [])

  const drop = useCallback((keys: Iterable<string>) => {
    for (const k of keys) {
      live.current.delete(k)
      const url = previews.current.get(k)
      if (url) { URL.revokeObjectURL(url); previews.current.delete(k) }
    }
  }, [])

  const add = useCallback((files: File[]) => {
    if (files.length === 0) return
    const added: Array<{ chip: Chip; file: File }> = files.map((file) => {
      const key = `up${++seq}`
      const chip: Chip = { key, kind: 'path', name: file.name, status: 'uploading' }
      if (file.type.startsWith('image/')) {
        chip.previewUrl = URL.createObjectURL(file)
        previews.current.set(key, chip.previewUrl)
      }
      live.current.add(key)
      return { chip, file }
    })
    setChips((prev) => [...prev, ...added.map((a) => a.chip)])
    const patch = (key: string, p: Partial<Chip>) => {
      if (!mounted.current || !live.current.has(key)) return
      setChips((prev) => prev.map((c) => (c.key === key ? { ...c, ...p } : c)))
    }
    for (const { chip, file } of added) {
      queue.current = queue.current.then(async () => {
        if (!mounted.current || !live.current.has(chip.key)) return
        try {
          const r = await uploadWorkerFile(hostId, executionId, file)
          patch(chip.key, { status: 'done', path: r.path })
        } catch (e) {
          patch(chip.key, { status: 'failed', error: e instanceof NexApiError ? e.code : undefined })
        }
      })
    }
  }, [hostId, executionId])

  const remove = useCallback((key: string) => {
    drop([key])
    setChips((prev) => prev.filter((c) => c.key !== key))
  }, [drop])

  const clear = useCallback((keys?: readonly string[]) => {
    if (keys) {
      const gone = new Set(keys)
      drop(gone)
      setChips((prev) => prev.filter((c) => !gone.has(c.key)))
    } else {
      drop([...live.current])
      setChips([])
    }
  }, [drop])

  return { chips, add, remove, clear }
}
