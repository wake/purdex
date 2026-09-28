// AttachmentThumbs — replayed image attachments on a user line (phase E, E4).
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { render, screen, waitFor, act } from '@testing-library/react'
import AttachmentThumbs, { PendingAttachmentThumbs } from './AttachmentThumbs'
import { AttachmentSourceContext } from './attachment-source'
import { useNexHostStore } from '../../stores/useNexHostStore'
import { NexApiError } from '../../lib/nex/types'
import * as api from '../../lib/nex/nex-api'

vi.mock('../../lib/nex/nex-api', () => ({ fetchAttachment: vi.fn() }))

const H = 'h', E = 'exc_1'
const ROUTE = { method: 'GET', path: '/api/nex/v1/executions/{id}/attachments/{sha256}' }
const sha = (c: string) => c.repeat(64)
const meta = (c: string, media_type = 'image/png') => ({ media_type, bytes: 10, sha256: sha(c) })

function seed(route: typeof ROUTE | null, phase = 'ready') {
  const send = route
    ? { delivery: ['delivered'], max_text_bytes: 1, max_request_bytes: 1, attachments: { image: { media_types: [], max_bytes: 1, max_count: 1, max_total_bytes: 1, providers: [], fetch: route } } }
    : { delivery: ['delivered'], max_text_bytes: 1 }
  useNexHostStore.setState({
    byHost: { [H]: { phase, capabilities: phase === 'ready' ? { send } : null, info: null, error: null, fetchedAt: 0, generation: 1, fingerprint: 'x' } } as never,
  })
}
function deferredBlob() {
  let resolve!: (b: Blob) => void
  let reject!: (e: unknown) => void
  const p = new Promise<Blob>((res, rej) => { resolve = res; reject = rej })
  return { p, resolve, reject }
}
const inPane = (ui: React.ReactNode) => render(<AttachmentSourceContext.Provider value={{ hostId: H, executionId: E }}>{ui}</AttachmentSourceContext.Provider>)
const thumbs = () => screen.queryAllByTestId('attachment-thumb')

let createUrl: ReturnType<typeof vi.fn>, revokeUrl: ReturnType<typeof vi.fn>
const origCreate = URL.createObjectURL, origRevoke = URL.revokeObjectURL
beforeEach(() => {
  vi.mocked(api.fetchAttachment).mockReset()
  let n = 0
  createUrl = vi.fn((_blob: Blob) => `blob:t${++n}`)
  revokeUrl = vi.fn()
  Object.assign(URL, { createObjectURL: createUrl, revokeObjectURL: revokeUrl })
  seed(ROUTE)
})
afterEach(() => Object.assign(URL, { createObjectURL: origCreate, revokeObjectURL: origRevoke }))

describe('AttachmentThumbs', () => {
  it('draws one thumbnail per attachment, fetched through the capability route, as an image that opens in a new tab', async () => {
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['x'], { type: 'image/png' }))
    inPane(<AttachmentThumbs items={[meta('a'), meta('b', 'image/jpeg')]} />)
    expect(thumbs()).toHaveLength(2)
    await waitFor(() => expect(thumbs().map((t) => t.dataset.state)).toEqual(['ready', 'ready']))
    expect(api.fetchAttachment).toHaveBeenCalledWith(H, E, sha('a'), ROUTE, expect.any(AbortSignal))
    expect(api.fetchAttachment).toHaveBeenCalledWith(H, E, sha('b'), ROUTE, expect.any(AbortSignal))
    const imgs = screen.getAllByRole('img')
    expect(imgs.map((i) => i.getAttribute('src')).sort()).toEqual(['blob:t1', 'blob:t2'])
    const link = thumbs()[0] as HTMLAnchorElement
    expect(link.target).toBe('_blank')
    expect(link.rel).toContain('noopener')
    expect(link.getAttribute('href')).toBe(imgs[0].getAttribute('src'))
  })

  it('a mislabeled Content-Type is rewrapped when the declared media_type is a trusted image type (fix round 1)', async () => {
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['<script>'], { type: 'text/html' }))
    inPane(<AttachmentThumbs items={[meta('a', 'image/png')]} />)
    await waitFor(() => expect(thumbs()[0].dataset.state).toBe('ready'))
    expect(createUrl).toHaveBeenCalledTimes(1)
    expect(createUrl.mock.calls[0][0].type).toBe('image/png')
  })

  it('a mislabeled Content-Type with an untrusted declared media_type is a load error, never opened as a blob (fix round 1)', async () => {
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['<script>'], { type: 'text/html' }))
    inPane(<AttachmentThumbs items={[meta('a', 'text/html')]} />)
    await waitFor(() => expect(thumbs()[0].dataset.state).toBe('error'))
    expect(createUrl).not.toHaveBeenCalled()
    expect(screen.queryByRole('link')).toBeNull()
  })

  it('a 404 (attachment_not_found) shows a broken-image placeholder naming the media type (Review Focus 5)', async () => {
    vi.mocked(api.fetchAttachment).mockRejectedValue(new NexApiError(404, 'attachment_not_found', 'gone'))
    inPane(<AttachmentThumbs items={[meta('a', 'image/webp')]} />)
    expect(thumbs()[0].dataset.state).toBe('loading')
    expect(thumbs()[0]).toHaveTextContent('image/webp')
    await waitFor(() => expect(thumbs()[0].dataset.state).toBe('error'))
    expect(thumbs()[0]).toHaveTextContent('image/webp')
    expect(thumbs()[0]).toHaveAccessibleName('Image unavailable (image/webp)')
    expect(screen.queryByRole('link')).toBeNull()
    expect(createUrl).not.toHaveBeenCalled()
  })

  it('revokes its object URLs on unmount, and never creates one for a fetch that settles after it', async () => {
    const late = deferredBlob()
    vi.mocked(api.fetchAttachment).mockResolvedValueOnce(new Blob(['x'])).mockReturnValueOnce(late.p)
    const { unmount } = inPane(<AttachmentThumbs items={[meta('a'), meta('b')]} />)
    await waitFor(() => expect(thumbs()[0].dataset.state).toBe('ready'))
    unmount()
    expect(revokeUrl.mock.calls).toEqual([['blob:t1']])
    await act(async () => { late.resolve(new Blob(['y'])) })
    expect(createUrl).toHaveBeenCalledTimes(1)
  })

  it('fetches at most four at a time', async () => {
    const pending = Array.from({ length: 6 }, deferredBlob)
    pending.forEach((d) => vi.mocked(api.fetchAttachment).mockReturnValueOnce(d.p))
    const { unmount } = inPane(<AttachmentThumbs items={['a', 'b', 'c', 'd', 'e', 'f'].map((c) => meta(c))} />)
    await act(async () => {})
    expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
    await act(async () => { pending[0].resolve(new Blob(['x'])) })
    expect(api.fetchAttachment).toHaveBeenCalledTimes(5)
    await act(async () => { pending[1].reject(new Error('x')) })
    expect(api.fetchAttachment).toHaveBeenCalledTimes(6)
    unmount()
    await act(async () => { pending.slice(2).forEach((d) => d.resolve(new Blob(['z']))) })
  })

  it('unmounting a thumbnail with an in-flight fetch aborts it (its signal fires) and frees its concurrency slot immediately (A1)', async () => {
    const signals: AbortSignal[] = []
    vi.mocked(api.fetchAttachment).mockImplementation((_h, _e, _sha, _route, signal) => {
      if (signal) signals.push(signal)
      return new Promise<Blob>((_resolve, reject) => {
        signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
      })
    })
    const held = inPane(<AttachmentThumbs items={['a', 'b', 'c', 'd'].map((c) => meta(c))} />)
    await act(async () => {})
    expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
    expect(signals).toHaveLength(4)
    expect(signals.every((s) => !s.aborted)).toBe(true)

    // A fifth thumbnail queues behind the four held slots — it gets none
    // until one frees up.
    const fifth = inPane(<AttachmentThumbs items={[meta('e')]} />)
    await act(async () => {})
    expect(api.fetchAttachment).toHaveBeenCalledTimes(4)

    // Unmounting the four in-flight thumbnails must abort their fetches
    // (not just stop caring about the result) so the slots they hold don't
    // stay pinned until the network eventually settles on its own.
    held.unmount()
    expect(signals.every((s) => s.aborted)).toBe(true)
    await act(async () => {})
    expect(api.fetchAttachment).toHaveBeenCalledTimes(5)
    fifth.unmount()
  })

  it('a hung fetch times out after 30s, releasing its concurrency slot and settling the thumbnail as an error placeholder (A1)', async () => {
    vi.useFakeTimers()
    try {
      vi.mocked(api.fetchAttachment).mockImplementation((_h, _e, _sha, _route, signal) =>
        new Promise<Blob>((_resolve, reject) => {
          signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        }))
      const held = inPane(<AttachmentThumbs items={['a', 'b', 'c', 'd'].map((c) => meta(c))} />)
      await act(async () => {})
      expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
      expect(thumbs().every((t) => t.dataset.state === 'loading')).toBe(true)

      await act(async () => { await vi.advanceTimersByTimeAsync(30_000) })
      expect(thumbs().every((t) => t.dataset.state === 'error')).toBe(true)

      // The timeout released every held slot — a freshly mounted thumbnail
      // gets one immediately instead of queueing behind four dead requests.
      vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['x'], { type: 'image/png' }))
      const fresh = inPane(<AttachmentThumbs items={[meta('e')]} />)
      await act(async () => {})
      expect(api.fetchAttachment).toHaveBeenCalledTimes(5)
      held.unmount(); fresh.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  it("a queued thumbnail's 30s deadline starts only once its own fetch begins, not at mount (A1 fix round 2)", async () => {
    vi.useFakeTimers()
    try {
      let calls = 0
      vi.mocked(api.fetchAttachment).mockImplementation((_h, _e, _sha, _route, signal) => new Promise<Blob>((resolve, reject) => {
        const idx = calls++
        signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')))
        // The 5th call (and later) actually completes, shortly after it
        // starts — but only if it wasn't already aborted the instant it
        // began (which is what the bug would do to a thumbnail dequeued
        // right as the four ahead of it expire).
        if (idx >= 4) setTimeout(() => resolve(new Blob(['x'], { type: 'image/png' })), 100)
        // idx < 4: hangs forever except for abort.
      }))
      const held = inPane(<AttachmentThumbs items={['a', 'b', 'c', 'd'].map((c) => meta(c))} />)
      await act(async () => {})
      expect(api.fetchAttachment).toHaveBeenCalledTimes(4)

      // A 5th queues behind the four held slots.
      const fifth = inPane(<AttachmentThumbs items={[meta('e')]} />)
      await act(async () => {})
      expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
      expect(thumbs()[4].dataset.state).toBe('loading')

      // A 6th, queued behind the 5th, is unmounted while still waiting —
      // it must never fetch, deadline or not.
      const sixth = inPane(<AttachmentThumbs items={[meta('f')]} />)
      await act(async () => {})
      sixth.unmount()

      // 29s pass with the 5th still queued: no deadline has touched it yet.
      await act(async () => { await vi.advanceTimersByTimeAsync(29_000) })
      expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
      expect(thumbs()[4].dataset.state).toBe('loading')

      // At 30s the first four expire and free their slots; the 5th is
      // dequeued and starts its own fetch — this call must not already be
      // carrying an aborted signal.
      await act(async () => { await vi.advanceTimersByTimeAsync(1_000) })
      expect(thumbs().slice(0, 4).every((t) => t.dataset.state === 'error')).toBe(true)
      expect(api.fetchAttachment).toHaveBeenCalledTimes(5)

      // The 5th's own fetch (started at t=30s) settles 100ms after it
      // began, well inside a fresh 30s window — it must succeed, not be
      // aborted by a stale mount-time deadline.
      await act(async () => { await vi.advanceTimersByTimeAsync(100) })
      expect(thumbs()[4].dataset.state).toBe('ready')

      expect(vi.mocked(api.fetchAttachment).mock.calls.map((c) => c[2])).not.toContain(sha('f'))
      held.unmount(); fifth.unmount()
    } finally {
      vi.useRealTimers()
    }
  })

  it('an unmounted thumbnail still waiting for a slot is skipped and passes the slot on', async () => {
    const pending = Array.from({ length: 4 }, deferredBlob)
    pending.forEach((d) => vi.mocked(api.fetchAttachment).mockReturnValueOnce(d.p))
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['x']))
    const first = inPane(<AttachmentThumbs items={['a', 'b', 'c', 'd'].map((c) => meta(c))} />)
    const queued = inPane(<AttachmentThumbs items={[meta('e')]} />)
    const later = inPane(<AttachmentThumbs items={[meta('f')]} />)
    await act(async () => {})
    expect(api.fetchAttachment).toHaveBeenCalledTimes(4)
    queued.unmount()
    await act(async () => { pending[0].resolve(new Blob(['x'])) })
    expect(vi.mocked(api.fetchAttachment).mock.calls.map((c) => c[2])).not.toContain(sha('e'))
    expect(vi.mocked(api.fetchAttachment).mock.calls.map((c) => c[2])).toContain(sha('f'))
    first.unmount(); later.unmount()
    await act(async () => { pending.slice(1).forEach((d) => d.resolve(new Blob(['z']))) })
  })

  it('re-seeding content-equal capabilities (a capability refresh) does not refetch or revoke; a genuinely different route does (fix round 1)', async () => {
    vi.mocked(api.fetchAttachment).mockResolvedValue(new Blob(['x'], { type: 'image/png' }))
    inPane(<AttachmentThumbs items={[meta('a')]} />)
    await waitFor(() => expect(thumbs()[0].dataset.state).toBe('ready'))
    expect(api.fetchAttachment).toHaveBeenCalledTimes(1)

    // A brand-new capabilities object, same route content — what a 60s TTL
    // capability refresh's `commitLoaded` installs.
    act(() => seed({ ...ROUTE }))
    expect(api.fetchAttachment).toHaveBeenCalledTimes(1)
    expect(revokeUrl).not.toHaveBeenCalled()
    expect(thumbs()[0].dataset.state).toBe('ready')

    // A genuinely different route: the thumbnail refetches, revoking the
    // stale blob URL first.
    const OTHER_ROUTE = { method: 'GET', path: '/elsewhere/{sha256}' }
    act(() => seed(OTHER_ROUTE))
    expect(revokeUrl).toHaveBeenCalledWith('blob:t1')
    await waitFor(() => expect(api.fetchAttachment).toHaveBeenCalledTimes(2))
    expect(api.fetchAttachment).toHaveBeenCalledWith(H, E, sha('a'), OTHER_ROUTE, expect.any(AbortSignal))
  })

  it('no fetch route: loading while the host is not ready yet, the placeholder once it is ready without one', async () => {
    seed(null, 'loading')
    inPane(<AttachmentThumbs items={[meta('a')]} />)
    expect(thumbs()[0].dataset.state).toBe('loading')
    act(() => seed(null))
    expect(thumbs()[0].dataset.state).toBe('error')
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })

  it('outside an execution pane (no source) every thumbnail is the placeholder', () => {
    render(<AttachmentThumbs items={[meta('a')]} />)
    expect(thumbs()[0].dataset.state).toBe('error')
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })
})

describe('PendingAttachmentThumbs', () => {
  it('draws the local previews as they are and never revokes them', () => {
    const { unmount } = render(<PendingAttachmentThumbs items={[{ previewUrl: 'blob:local', media_type: 'image/png' }]} />)
    expect(screen.getByRole('img')).toHaveAttribute('src', 'blob:local')
    unmount()
    expect(revokeUrl).not.toHaveBeenCalled()
    expect(api.fetchAttachment).not.toHaveBeenCalled()
  })
})
