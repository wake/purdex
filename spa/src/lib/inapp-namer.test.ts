import { describe, it, expect, vi, beforeEach } from 'vitest'
import { createUniqueInAppFile } from './inapp-namer'

const getFsBackendMock = vi.hoisted(() => vi.fn())

vi.mock('./fs-backend', () => ({
  getFsBackend: getFsBackendMock,
  // Capability guard (codex H1): mirror the real typeof narrow so the mocked
  // backend (or undefined) flows through createUniqueInAppFile unchanged.
  supportsCreateUnique: (b: { createUnique?: unknown } | undefined) =>
    typeof b?.createUnique === 'function',
}))

describe('createUniqueInAppFile (single namer over the InApp backend)', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('delegates to backend.createUnique(dir, "Untitled", ext) and returns the path', async () => {
    const createUnique = vi.fn(async (dir: string, _base: string, ext: string) => `${dir}/Untitled.${ext}`)
    getFsBackendMock.mockReturnValue({ createUnique })

    const path = await createUniqueInAppFile('/buffer', 'md')

    expect(createUnique).toHaveBeenCalledWith('/buffer', 'Untitled', 'md')
    expect(path).toBe('/buffer/Untitled.md')
  })

  it('forwards a bare txt extension and a nested target dir', async () => {
    const createUnique = vi.fn(async (dir: string, _base: string, ext: string) => `${dir}/Untitled.${ext}`)
    getFsBackendMock.mockReturnValue({ createUnique })

    const path = await createUniqueInAppFile('/buffer/sub', 'txt')

    expect(createUnique).toHaveBeenCalledWith('/buffer/sub', 'Untitled', 'txt')
    expect(path).toBe('/buffer/sub/Untitled.txt')
  })

  it('throws when the InApp backend is unavailable (surfaced, not silent)', async () => {
    getFsBackendMock.mockReturnValue(undefined)
    await expect(createUniqueInAppFile('/buffer', 'md')).rejects.toThrow()
  })
})
