import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { ImagePreviewPane } from './ImagePreviewPane'
import type { Pane } from '../../types/tab'

const read = vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3]))

vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: () => ({ read }),
}))

// Track defineProperty installs so we can restore between tests.
let imgComplete = true
let imgNaturalW = 0
let imgNaturalH = 0
let boxW = 0
let boxH = 0

function makePane(filePath: string): Pane {
  return {
    id: 'p1',
    content: { kind: 'image-preview', source: { type: 'inapp' }, filePath },
  }
}

beforeEach(() => {
  read.mockClear()

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
})
