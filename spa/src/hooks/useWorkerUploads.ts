// spa/src/hooks/useWorkerUploads.ts — the attachment chips of one worker pane
// (worker-pane theme spec §9.1). Lives in ExecutionView, not WorkerInput:
// the input is re-keyed on the restored draft and remounts after a failed
// send, and the drop target is the whole pane. Files upload one at a time, in
// the order they were added; a chip removed before its turn is never sent,
// one removed mid-upload stays removed (the file on disk is left alone).
//
// Phase E (spec §9.2): with an image capability for this execution's provider
// (`images.caps`, fail-closed null otherwise), each add re-plans the images
// that are still local plus the new ones against the current draft text
// (`planAttachments`). An image planned native is never uploaded — its chip
// is `kind: 'image'`, `done`, with no path, and its File is kept here for the
// send to encode. One planned path uploads as in phase D, noted
// `image_as_path` (only when a capability exists: an older daemon keeps phase
// D exactly). A chip uploaded as path stays path.
import { useCallback, useEffect, useRef, useState } from 'react'
import { uploadWorkerFile } from '../lib/nex/nex-api'
import { NexApiError } from '../lib/nex/types'
import { planAttachments, type Chip, type ImageAttachmentPlanCaps } from '../lib/nex/worker-upload'

export interface WorkerUploads {
  chips: Chip[]
  add(files: File[]): void
  remove(key: string): void
  /** Remove the given chips, or every chip when no keys are passed. */
  clear(keys?: readonly string[]): void
  /** The local Files of native image chips, in chip order; only `keys` when given. */
  nativeFiles(keys?: readonly string[]): { key: string; file: File }[]
  /** Turn native image chips into path uploads (noted `image_as_path`), e.g. when the capability went away. */
  demote(keys: readonly string[]): void
  /** Fail a chip with a code (see `uploadErrorKey`), e.g. a native image the daemon refused. */
  markFailed(key: string, error?: string): void
  /** Whether the chip still exists (not removed or cleared) — for work that outlived a render. */
  isLive(key: string): boolean
  /**
   * Chips for files already on the daemon host (a recalled message's `[file: path]` lines): `done`
   * path chips, no upload. Returns nothing; they join the current chips.
   */
  addPaths(paths: readonly string[]): void
  /**
   * Input-history navigation: move the current chips out of sight (kept alive — uploads go on, previews
   * stay) so a recalled message's chips can take their place. `unpark` drops what is shown and brings
   * the parked chips back; `discardParked` drops them for good (the recalled message was sent).
   */
  park(): void
  unpark(): void
  discardParked(): void
}

export interface WorkerUploadImages {
  /** `selectImageAttachments` for this execution's host and provider. */
  caps: ImageAttachmentPlanCaps
  /** The draft text right now, which the request-size budget counts. */
  getText: () => string
}

let seq = 0

export function useWorkerUploads(hostId: string, executionId: string, images?: WorkerUploadImages): WorkerUploads {
  const [chips, setChips] = useState<Chip[]>([])
  const [parked, setParked] = useState<Chip[]>([])
  // Keys of parked chips: still live (their uploads finish), but not the shown chips.
  const parkedKeys = useRef(new Set<string>())
  // Mirrors for work outside render: which chips still exist (a removed one
  // is skipped / ignored), their thumbnails (revoked on remove / unmount) and
  // the Files of native image chips (insertion order = chip order).
  const live = useRef(new Set<string>())
  const previews = useRef(new Map<string, string>())
  const natives = useRef(new Map<string, File>())
  const queue = useRef<Promise<void>>(Promise.resolve())
  const mounted = useRef(true)
  const imagesRef = useRef(images)
  imagesRef.current = images

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
      parkedKeys.current.delete(k)
      natives.current.delete(k)
      const url = previews.current.get(k)
      if (url) { URL.revokeObjectURL(url); previews.current.delete(k) }
    }
  }, [])

  const patch = useCallback((key: string, p: Partial<Chip>) => {
    if (!mounted.current || !live.current.has(key)) return
    setChips((prev) => prev.map((c) => (c.key === key ? { ...c, ...p } : c)))
    setParked((prev) => (prev.some((c) => c.key === key) ? prev.map((c) => (c.key === key ? { ...c, ...p } : c)) : prev))
  }, [])

  const enqueueUpload = useCallback((key: string, file: File) => {
    queue.current = queue.current.then(async () => {
      if (!mounted.current || !live.current.has(key)) return
      try {
        const r = await uploadWorkerFile(hostId, executionId, file)
        patch(key, { status: 'done', path: r.path })
      } catch (e) {
        patch(key, { status: 'failed', error: e instanceof NexApiError ? e.code : undefined })
      }
    })
  }, [hostId, executionId, patch])

  const demote = useCallback((keys: readonly string[]) => {
    for (const key of keys) {
      const file = natives.current.get(key)
      if (!file) continue
      natives.current.delete(key)
      patch(key, { kind: 'path', status: 'uploading', note: 'image_as_path' })
      enqueueUpload(key, file)
    }
  }, [patch, enqueueUpload])

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
    const img = imagesRef.current
    let demoted: string[] = []
    if (img?.caps) {
      const candidates = [
        ...[...natives.current].map(([key, file]) => ({ key, size: file.size, type: file.type })),
        ...added.filter((a) => a.file.type.startsWith('image/')).map((a) => ({ key: a.chip.key, size: a.file.size, type: a.file.type })),
      ]
      const plan = planAttachments(candidates, img.caps, img.getText())
      const native = new Set(plan.native)
      const path = new Set(plan.path)
      demoted = plan.path.filter((k) => natives.current.has(k))
      for (const a of added) {
        if (native.has(a.chip.key)) {
          a.chip.kind = 'image'
          a.chip.status = 'done'
          natives.current.set(a.chip.key, a.file)
        } else if (path.has(a.chip.key)) {
          a.chip.note = 'image_as_path'
        }
      }
    }
    setChips((prev) => [...prev, ...added.map((a) => a.chip)])
    demote(demoted)
    for (const { chip, file } of added) {
      if (chip.kind === 'path') enqueueUpload(chip.key, file)
    }
  }, [demote, enqueueUpload])

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
      drop([...live.current].filter((k) => !parkedKeys.current.has(k)))
      setChips([])
    }
  }, [drop])

  const nativeFiles = useCallback((keys?: readonly string[]) => {
    const want = keys ? new Set(keys) : null
    return [...natives.current].filter(([key]) => !want || want.has(key)).map(([key, file]) => ({ key, file }))
  }, [])

  // A failed chip is out of play until removed: its File leaves `natives`, so
  // a later add neither re-plans it (taking a slot or budget) nor demotes it
  // into a silent path upload. The thumbnail stays with the chip.
  const markFailed = useCallback((key: string, error?: string) => {
    natives.current.delete(key)
    patch(key, { status: 'failed', error })
  }, [patch])

  const isLive = useCallback((key: string) => live.current.has(key), [])

  const addPaths = useCallback((paths: readonly string[]) => {
    if (paths.length === 0) return
    const added: Chip[] = paths.map((path) => {
      const key = `up${++seq}`
      live.current.add(key)
      return { key, kind: 'path', name: path.split('/').filter(Boolean).pop() ?? path, status: 'done', path }
    })
    setChips((prev) => [...prev, ...added])
  }, [])

  const park = useCallback(() => {
    // A second park without an unpark in between would lose the first set; navigation never does that.
    for (const c of chips) parkedKeys.current.add(c.key)
    setParked((prev) => [...prev, ...chips])
    setChips([])
  }, [chips])

  const unpark = useCallback(() => {
    drop([...live.current].filter((k) => !parkedKeys.current.has(k)))
    parkedKeys.current.clear()
    setChips(parked)
    setParked([])
  }, [drop, parked])

  const discardParked = useCallback(() => {
    drop([...parkedKeys.current])
    setParked([])
  }, [drop])

  return { chips, add, remove, clear, nativeFiles, demote, markFailed, isLive, addPaths, park, unpark, discardParked }
}
