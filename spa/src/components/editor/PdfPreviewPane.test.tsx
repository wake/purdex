import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { PdfPreviewPane } from './PdfPreviewPane'
import type { Pane } from '../../types/tab'
import type { FileSource } from '../../types/fs'
import { getFsBackend } from '../../lib/fs-backend'

const read = vi.fn().mockResolvedValue(new Uint8Array([1, 2, 3]))
const backendInstance = { read }

vi.mock('../../lib/fs-backend', () => ({
  getFsBackend: vi.fn(() => backendInstance),
}))

function makePane(filePath: string, source: FileSource = { type: 'inapp' }): Pane {
  return { id: 'p1', content: { kind: 'pdf-preview', source, filePath } }
}

beforeEach(() => {
  read.mockClear()
  vi.mocked(getFsBackend).mockReturnValue(backendInstance as unknown as ReturnType<typeof getFsBackend>)
  globalThis.URL.createObjectURL = vi.fn(() => 'blob:mock')
  globalThis.URL.revokeObjectURL = vi.fn()
})

afterEach(() => {
  cleanup()
})

describe('PdfPreviewPane', () => {
  it('fills its mount with an explicit height (h-full) so the iframe height chain works under a non-flex parent', async () => {
    // Same root cause as ImagePreviewPane: the per-tab mount wrapper in
    // TabContent is position:absolute (a plain block, not a flex container), so
    // a `flex-1` root collapses to content height and the iframe's `h-full` has
    // no definite height to resolve against. The pane must claim height via
    // `h-full w-full` like EditorPane. Guards against regressing to `flex-1`.
    render(<PdfPreviewPane pane={makePane('/doc.pdf')} isActive={true} />)
    const iframe = await screen.findByTitle('doc.pdf')
    const root = iframe.parentElement?.parentElement as HTMLElement
    expect(root.className).toContain('h-full')
    expect(root.className).not.toContain('flex-1')
  })
})
