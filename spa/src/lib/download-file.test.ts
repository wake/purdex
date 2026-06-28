import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { triggerDownload } from './download-file'

// jsdom implements neither URL.createObjectURL nor revokeObjectURL, so we stub
// them and assert the full anchor-download dance: create an object URL for the
// blob, build an <a download> pointing at it, click it, then revoke the URL.
describe('triggerDownload — anchor object-URL download (lifted from SyncSection)', () => {
  let createObjectURL: ReturnType<typeof vi.fn>
  let revokeObjectURL: ReturnType<typeof vi.fn>
  let clickSpy: ReturnType<typeof vi.spyOn>

  beforeEach(() => {
    createObjectURL = vi.fn(() => 'blob:mock-url')
    revokeObjectURL = vi.fn()
    // URL is a global; stub the two static methods jsdom lacks.
    ;(URL as unknown as { createObjectURL: unknown }).createObjectURL = createObjectURL
    ;(URL as unknown as { revokeObjectURL: unknown }).revokeObjectURL = revokeObjectURL
    // Spy on anchor click so the download never actually navigates in jsdom.
    clickSpy = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it('creates an object URL, builds an <a download>, clicks it, then revokes', () => {
    const createElementSpy = vi.spyOn(document, 'createElement')
    const blob = new Blob(['hello'], { type: 'text/plain' })

    triggerDownload(blob, 'report.txt')

    // Object URL created for exactly this blob.
    expect(createObjectURL).toHaveBeenCalledTimes(1)
    expect(createObjectURL).toHaveBeenCalledWith(blob)

    // An anchor was created carrying the URL + download filename, then clicked.
    const anchor = createElementSpy.mock.results.find(
      (r) => r.value instanceof HTMLAnchorElement,
    )?.value as HTMLAnchorElement
    expect(anchor).toBeInstanceOf(HTMLAnchorElement)
    expect(anchor.href).toContain('blob:mock-url')
    expect(anchor.download).toBe('report.txt')
    expect(clickSpy).toHaveBeenCalledTimes(1)

    // The URL is revoked after the click (no leak).
    expect(revokeObjectURL).toHaveBeenCalledTimes(1)
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:mock-url')
  })
})
