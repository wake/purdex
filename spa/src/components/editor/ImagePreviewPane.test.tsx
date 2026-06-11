import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { ImagePreviewPane } from './ImagePreviewPane'
import type { Pane } from '../../types/tab'
import type { FileSource } from '../../types/fs'
import { getFsBackend } from '../../lib/fs-backend'

const read = vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3]))

vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: vi.fn(() => ({ read })),
}))

// Track defineProperty installs so we can restore between tests.
let imgComplete = true
let imgNaturalW = 0
let imgNaturalH = 0
let boxW = 0
let boxH = 0

function makePane(filePath: string, source: FileSource = { type: 'inapp' }): Pane {
  return {
    id: 'p1',
    content: { kind: 'image-preview', source, filePath },
  }
}

beforeEach(() => {
  read.mockClear()
  vi.mocked(getFsBackend).mockReturnValue({ read } as unknown as ReturnType<typeof getFsBackend>)

  // Polyfill ResizeObserver (jsdom lacks it).
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver

  // Stub object URL APIs.
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:mock')
  globalThis.URL.revokeObjectURL = vi.fn()

  // Stub image natural size + complete via prototype.
  Object.defineProperty(HTMLImageElement.prototype, 'complete', {
    configurable: true,
    get: () => imgComplete,
  })
  Object.defineProperty(HTMLImageElement.prototype, 'naturalWidth', {
    configurable: true,
    get: () => imgNaturalW,
  })
  Object.defineProperty(HTMLImageElement.prototype, 'naturalHeight', {
    configurable: true,
    get: () => imgNaturalH,
  })

  // Stub container box size via prototype.
  Object.defineProperty(HTMLElement.prototype, 'clientWidth', {
    configurable: true,
    get: () => boxW,
  })
  Object.defineProperty(HTMLElement.prototype, 'clientHeight', {
    configurable: true,
    get: () => boxH,
  })
})

afterEach(() => {
  cleanup()
})

async function renderAndMeasure(filePath: string) {
  render(<ImagePreviewPane pane={makePane(filePath)} isActive={true} />)
  const img = await screen.findByRole('img')
  // Trigger the native load listener now that complete/naturalWidth are set.
  fireEvent.load(img)
  return img as HTMLImageElement
}

describe('ImagePreviewPane', () => {
  it('renders fit mode with zoom-in cursor when image is oversized', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const img = await renderAndMeasure('/big.png')
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))
    expect(img.className).toContain('object-contain')
    expect(img.className).toContain('max-w-full')
  })

  it('toggles to actual size with zoom-out cursor on click', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const img = await renderAndMeasure('/big.png')
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))

    fireEvent.click(img)

    await waitFor(() => expect(img.className).toContain('cursor-zoom-out'))
    expect(img.className).toContain('max-w-none')
    expect(img.className).not.toContain('object-contain')
  })

  it('toggles back to fit on a second click', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const img = await renderAndMeasure('/big.png')
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))

    fireEvent.click(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-out'))

    fireEvent.click(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))
    expect(img.className).toContain('object-contain')
  })

  it('shows no zoom cursor and click is a no-op for a small image', async () => {
    imgComplete = true
    imgNaturalW = 100
    imgNaturalH = 100
    boxW = 500
    boxH = 500

    const img = await renderAndMeasure('/small.png')
    await waitFor(() => expect(img).toBeTruthy())
    expect(img.className).not.toContain('cursor-zoom-in')
    expect(img.className).not.toContain('cursor-zoom-out')

    fireEvent.click(img)
    expect(img.className).toContain('object-contain')
    expect(img.className).not.toContain('max-w-none')
  })

  it('wraps the image in a scrollable container with overflow-auto and min-h-0', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const img = await renderAndMeasure('/big.png')
    const container = img.parentElement as HTMLElement
    expect(container.className).toContain('overflow-auto')
    expect(container.className).toContain('min-h-0')
  })

  it('measures oversized synchronously for a cached/HMR-complete image without a load event (AC17b)', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    render(<ImagePreviewPane pane={makePane('/big.png')} isActive={true} />)
    // Wait for the async backend.read -> objectUrl -> img mount chain.
    const img = (await screen.findByRole('img')) as HTMLImageElement
    // Deliberately do NOT fire a load event — only the mount effect's synchronous
    // measureNatural() should drive the oversized determination.
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))
    expect(img.className).toContain('object-contain')
    expect(img.className).toContain('max-w-full')
  })

  it('resets to fit mode when filePath changes', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const { rerender } = render(
      <ImagePreviewPane pane={makePane('/big.png')} isActive={true} />,
    )
    let img = await screen.findByRole('img')
    fireEvent.load(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))

    fireEvent.click(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-out'))

    // Switch to a different file — should reset to fit.
    rerender(<ImagePreviewPane pane={makePane('/other.png')} isActive={true} />)
    img = await screen.findByRole('img')
    fireEvent.load(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))
    expect(img.className).not.toContain('cursor-zoom-out')
  })

  // F5: same filePath but a different source must also reset zoom/natural.
  it('resets to fit mode when only the source changes for the same filePath (F5)', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    const { rerender } = render(
      <ImagePreviewPane
        pane={makePane('/same.png', { type: 'daemon', hostId: 'h1' })}
        isActive={true}
      />,
    )
    let img = await screen.findByRole('img')
    fireEvent.load(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))

    // Toggle to actual size.
    fireEvent.click(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-out'))

    // Same filePath, different daemon host → should reset back to fit.
    rerender(
      <ImagePreviewPane
        pane={makePane('/same.png', { type: 'daemon', hostId: 'h2' })}
        isActive={true}
      />,
    )
    img = await screen.findByRole('img')
    fireEvent.load(img)
    await waitFor(() => expect(img.className).toContain('cursor-zoom-in'))
    expect(img.className).not.toContain('cursor-zoom-out')
  })

  // F4(a): switching files must not leave the previous image's objectUrl on
  // screen during the new read — it should fall back to Loading immediately.
  it('clears the previous image during the next read when switching files (F4)', async () => {
    imgComplete = true
    imgNaturalW = 2000
    imgNaturalH = 2000
    boxW = 500
    boxH = 500

    // First file loads successfully.
    const { rerender } = render(
      <ImagePreviewPane pane={makePane('/a.png')} isActive={true} />,
    )
    await screen.findByRole('img')

    // Hold the next read pending so we can observe the transition state.
    let resolveB: (data: Uint8Array) => void = () => {}
    read.mockImplementationOnce(
      () => new Promise<Uint8Array>((resolve) => { resolveB = resolve }),
    )

    rerender(<ImagePreviewPane pane={makePane('/b.png')} isActive={true} />)

    // While B is still loading, the old image must be gone (Loading shown).
    await waitFor(() => expect(screen.queryByRole('img')).toBeNull())
    expect(screen.getByText('Loading...')).toBeInTheDocument()

    // Resolve B → image reappears.
    resolveB(new Uint8Array([4, 5, 6]))
    await screen.findByRole('img')
  })

  // F4(b): a prior read failure shows an error banner; switching to a file that
  // reads successfully must clear that sticky error.
  it('clears a prior error banner when switching to a file that loads (F4)', async () => {
    imgComplete = true
    imgNaturalW = 100
    imgNaturalH = 100
    boxW = 500
    boxH = 500

    // First read rejects → error banner.
    read.mockRejectedValueOnce(new Error('read failed'))
    const { rerender } = render(
      <ImagePreviewPane pane={makePane('/bad.png')} isActive={true} />,
    )
    await waitFor(() => expect(screen.getByText('read failed')).toBeInTheDocument())

    // Switch to a file that reads fine → error cleared, image shown.
    rerender(<ImagePreviewPane pane={makePane('/good.png')} isActive={true} />)
    await screen.findByRole('img')
    expect(screen.queryByText('read failed')).toBeNull()
  })

  // H1c: revokeObjectURL must be called on unmount (and when switching files)
  // so blob URLs do not leak.
  it('revokes the object URL on unmount (H1c)', async () => {
    imgComplete = true
    imgNaturalW = 100
    imgNaturalH = 100
    boxW = 500
    boxH = 500

    const { unmount } = render(
      <ImagePreviewPane pane={makePane('/leak.png')} isActive={true} />,
    )
    await screen.findByRole('img')

    unmount()

    expect(globalThis.URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock')
  })

  it('revokes the previous object URL when switching files (H1c)', async () => {
    imgComplete = true
    imgNaturalW = 100
    imgNaturalH = 100
    boxW = 500
    boxH = 500

    const { rerender } = render(
      <ImagePreviewPane pane={makePane('/first.png')} isActive={true} />,
    )
    await screen.findByRole('img')

    rerender(<ImagePreviewPane pane={makePane('/second.png')} isActive={true} />)
    await screen.findByRole('img')

    expect(globalThis.URL.revokeObjectURL).toHaveBeenCalledWith('blob:mock')
  })

  // P3: a backend unavailable on first render but available afterwards (same
  // source/filePath, so identity is unchanged) must still trigger a re-read
  // rather than sticking on "No FS backend". Guards the read effect's `backend`
  // dependency (keying off identity alone would miss this).
  it('re-reads when the backend becomes available after first render (P3)', async () => {
    imgComplete = true
    imgNaturalW = 100
    imgNaturalH = 100
    boxW = 500
    boxH = 500

    // First render: backend not yet registered.
    vi.mocked(getFsBackend).mockReturnValueOnce(undefined)
    const { rerender } = render(
      <ImagePreviewPane pane={makePane('/late.png')} isActive={true} />,
    )
    expect(screen.getByText('No FS backend')).toBeInTheDocument()
    expect(read).not.toHaveBeenCalled()

    // Backend becomes available; identity unchanged, only the `backend` dep
    // flips — which must still drive a re-read.
    rerender(<ImagePreviewPane pane={makePane('/late.png')} isActive={true} />)
    await screen.findByRole('img')
    expect(read).toHaveBeenCalledWith('/late.png')
  })
})
