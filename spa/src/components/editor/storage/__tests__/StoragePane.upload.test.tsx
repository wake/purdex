// Upload into Storage: the picker path and the native OS-file drop, plus the
// banners a partial failure / an over-cap file / a quota error surface.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { Mock } from 'vitest'
import { render, screen, fireEvent, waitFor, act, createEvent } from '@testing-library/react'
import { StoragePane } from '../StoragePane'
import { SOFT_MAX_UPLOAD_BYTES } from '../storage-actions'
import { openInAppFile } from '../../../../lib/open-in-app-file'
import { mockBackend, tSpy } from './storage-pane-mocks'
import {
  makePane,
  pathAwareList,
  resetStoragePaneMocks,
  restoreStoragePaneGlobals
} from './storage-pane-harness'

// `vi.mock` is hoisted per file, so every suite re-registers the same set; the
// factory bodies themselves live once in `./storage-pane-mocks`.
vi.mock('@dnd-kit/core', async () => (await import('./storage-pane-mocks')).dndKitMock())
vi.mock('../../../../lib/fs-backend', async () => (await import('./storage-pane-mocks')).fsBackendMock())
vi.mock('../../../../lib/open-in-app-file', async () => (await import('./storage-pane-mocks')).openInAppFileMock())
vi.mock('../../../../lib/download-file', async () => (await import('./storage-pane-mocks')).downloadFileMock())
vi.mock('../../../../features/workspace/store', async () => (await import('./storage-pane-mocks')).workspaceStoreMock())
vi.mock('../../../../stores/useI18nStore', async () => (await import('./storage-pane-mocks')).i18nStoreMock())
vi.mock('../../../../stores/useTabStore', async () => (await import('./storage-pane-mocks')).tabStoreMock())
vi.mock('../../../RenamePopover', async () => (await import('./storage-pane-mocks')).renamePopoverMock())

beforeEach(resetStoragePaneMocks)
afterEach(restoreStoragePaneGlobals)

describe('StoragePane — upload via picker + native OS-file drop (T1c-1)', () => {
  /** A `createUnique` that records bytes and materializes the path in `paths`. */
  function wireUploadBackend() {
    const paths = new Map<string, { isDir: boolean; size: number }>()
    mockBackend.list = pathAwareList(paths)
    mockBackend.createUnique = vi.fn(async (dir: string, base: string, ext: string) => {
      const fileName = ext === '' ? base : `${base}.${ext}`
      const path = `${dir}/${fileName}`
      paths.set(path, { isDir: false, size: 1 })
      return path
    })
    return paths
  }

  async function uploadViaPicker(files: File[]) {
    const input = screen.getByTestId('upload-input') as HTMLInputElement
    await act(async () => {
      fireEvent.change(input, { target: { files } })
    })
  }

  it('T1-1: uploading an image via the picker stores its bytes (png ext), shows it in the tree, and opening routes through openInAppFile', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([new File([new Uint8Array([1, 2, 3])], 'photo.png', { type: 'image/png' })])

    await waitFor(() => expect(mockBackend.createUnique).toHaveBeenCalled())
    const [dir, base, ext, bytes] = (mockBackend.createUnique as Mock).mock.calls[0]
    expect(dir).toBe('/buffer')
    expect(base).toBe('photo')
    expect(ext).toBe('png')
    expect(Array.from(bytes as Uint8Array)).toEqual([1, 2, 3])

    const row = await screen.findByTestId('buffer-row')
    expect(row.getAttribute('data-path')).toBe('/buffer/photo.png')
    fireEvent.doubleClick(row)
    await waitFor(() => expect(openInAppFile).toHaveBeenCalledWith('/buffer/photo.png', 'ws1'))
  })

  it('T1-1b: uploading a .pdf stores it and opening routes through openInAppFile (→ pdf-preview)', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([new File([new Uint8Array([8])], 'doc.pdf', { type: 'application/pdf' })])

    await waitFor(() => expect(mockBackend.createUnique).toHaveBeenCalled())
    expect((mockBackend.createUnique as Mock).mock.calls[0][2]).toBe('pdf')
    const row = await screen.findByTestId('buffer-row')
    expect(row.getAttribute('data-path')).toBe('/buffer/doc.pdf')
    fireEvent.doubleClick(row)
    await waitFor(() => expect(openInAppFile).toHaveBeenCalledWith('/buffer/doc.pdf', 'ws1'))
  })

  it('T1-2: uploading a .docx stores it and opening routes through openInAppFile (→ download)', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([new File([new Uint8Array([5])], 'sheet.docx')])

    await waitFor(() => expect(mockBackend.createUnique).toHaveBeenCalled())
    expect((mockBackend.createUnique as Mock).mock.calls[0][2]).toBe('docx')
    const row = await screen.findByTestId('buffer-row')
    expect(row.getAttribute('data-path')).toBe('/buffer/sheet.docx')
    fireEvent.doubleClick(row)
    await waitFor(() => expect(openInAppFile).toHaveBeenCalledWith('/buffer/sheet.docx', 'ws1'))
  })

  it('T1-4: a native drop carrying dataTransfer.files ingests them', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    const region = screen.getByTestId('storage-tree-region')
    await act(async () => {
      fireEvent.drop(region, {
        dataTransfer: { files: [new File([new Uint8Array([9])], 'dropped.txt')], types: ['Files'] },
      })
    })

    await waitFor(() => expect(mockBackend.createUnique).toHaveBeenCalled())
    const [dir, base, ext] = (mockBackend.createUnique as Mock).mock.calls[0]
    expect(dir).toBe('/buffer')
    expect(base).toBe('dropped')
    expect(ext).toBe('txt')
    const row = await screen.findByTestId('buffer-row')
    expect(row.getAttribute('data-path')).toBe('/buffer/dropped.txt')
  })

  it('T1-4: a native drop with NO files is ignored (dnd-kit node move untouched)', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    const region = screen.getByTestId('storage-tree-region')
    fireEvent.drop(region, { dataTransfer: { files: [], types: [] } })
    // Let any stray microtask settle.
    await act(async () => {
      await Promise.resolve()
    })
    expect(mockBackend.createUnique).not.toHaveBeenCalled()
  })

  it('C3: dropping an OS FOLDER (types:[Files] but files:[]) is preventDefaulted and not ingested', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    const region = screen.getByTestId('storage-tree-region')
    // An OS folder drag reports the `Files` type but yields an EMPTY files list.
    // The handler must claim it (preventDefault) so it never falls back to the
    // browser default — yet must NOT try to ingest a zero-file drop.
    const dropEvent = createEvent.drop(region, {
      dataTransfer: { files: [], types: ['Files'] },
    })
    await act(async () => {
      fireEvent(region, dropEvent)
    })
    expect(dropEvent.defaultPrevented).toBe(true)
    expect(mockBackend.createUnique).not.toHaveBeenCalled()
  })

  it('T1-6: a partial failure surfaces an inline banner naming the first failed file', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>()
    mockBackend.list = pathAwareList(paths)
    mockBackend.createUnique = vi.fn(async (dir: string, base: string, ext: string) => {
      if (base === 'bad') throw new Error('boom')
      const fileName = ext === '' ? base : `${base}.${ext}`
      const path = `${dir}/${fileName}`
      paths.set(path, { isDir: false, size: 1 })
      return path
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([
      new File([new Uint8Array([1])], 'ok.txt'),
      new File([new Uint8Array([2])], 'bad.bin'),
    ])

    // Banner reflects partial success. C6: assert the i18n call carries the
    // right interpolation params (uploaded/failed counts + the first failed
    // name), not just the key.
    await waitFor(() => expect(screen.getByText('editor.buffers.upload_partial')).toBeTruthy())
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.upload_partial', {
      uploaded: 1,
      failed: 1,
      name: 'bad.bin',
    })
    const calls = (mockBackend.createUnique as Mock).mock.calls.map((c) => c[1])
    expect(calls).toEqual(['ok', 'bad'])
  })

  // --- T1c-4: size cap warning + quota error banner -------------------------

  /** A File whose `size` is forced past the cap WITHOUT allocating the bytes. */
  function oversizedFile(name: string, size: number): File {
    const file = new File([new Uint8Array([0])], name)
    Object.defineProperty(file, 'size', { value: size })
    return file
  }

  it('T4-1: an over-cap file shows the amber too-large WARNING and never writes', async () => {
    wireUploadBackend()
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([oversizedFile('huge.bin', SOFT_MAX_UPLOAD_BYTES + 1)])

    const warning = await screen.findByTestId('storage-warning')
    expect(warning.textContent).toBe('editor.buffers.upload_too_large')
    expect(warning.className).toContain('text-amber-400')
    // C6: the warning interpolates the file name + the cap in MB (25 = 25 MiB).
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.upload_too_large', {
      name: 'huge.bin',
      cap: 25,
    })
    expect(mockBackend.createUnique).not.toHaveBeenCalled()
  })

  it('T4-2: a quota write failure shows the red quota ERROR banner (not silent)', async () => {
    const paths = new Map<string, { isDir: boolean; size: number }>()
    mockBackend.list = pathAwareList(paths)
    mockBackend.createUnique = vi.fn(async () => {
      throw new DOMException('full', 'QuotaExceededError')
    })
    render(<StoragePane pane={makePane()} isActive />)
    await screen.findByText('editor.buffers.empty')

    await uploadViaPicker([new File([new Uint8Array([1])], 'x.txt')])

    const banner = await screen.findByText('editor.buffers.upload_quota')
    expect(banner.className).toContain('text-red-400')
    // C6: the quota error names the file that could not be saved.
    expect(tSpy).toHaveBeenCalledWith('editor.buffers.upload_quota', { name: 'x.txt' })
    expect(screen.queryByTestId('storage-warning')).toBeNull()
  })
})
