import { describe, it, expect } from 'vitest'
import {
  STORAGE_ROOT,
  join,
  parentOf,
  basename,
  relativeToRoot,
  isUnderRoot,
} from './storage-paths'

describe('STORAGE_ROOT', () => {
  it('is /buffer', () => {
    expect(STORAGE_ROOT).toBe('/buffer')
  })
})

describe('join', () => {
  it('returns the root unchanged', () => {
    expect(join(STORAGE_ROOT)).toBe('/buffer')
  })

  it('joins a single child segment under the root', () => {
    expect(join(STORAGE_ROOT, 'a.md')).toBe('/buffer/a.md')
  })

  it('joins nested segments', () => {
    expect(join(STORAGE_ROOT, 'a', 'b', 'x.md')).toBe('/buffer/a/b/x.md')
  })

  it('keeps a leading slash from the first segment', () => {
    expect(join('/buffer', 'a')).toBe('/buffer/a')
  })

  it('drops a leading slash when the first segment has none', () => {
    expect(join('a', 'b')).toBe('a/b')
  })

  it('collapses double slashes from trailing-slash segments', () => {
    expect(join('/buffer/', 'a.md')).toBe('/buffer/a.md')
    expect(join('/buffer/', '/a.md')).toBe('/buffer/a.md')
  })

  it('ignores empty segments', () => {
    expect(join('/buffer', '', 'a.md')).toBe('/buffer/a.md')
    expect(join('/buffer', 'a/', 'b')).toBe('/buffer/a/b')
  })
})

describe('parentOf', () => {
  it('returns the parent directory of a nested file', () => {
    expect(parentOf('/buffer/a/b/x.md')).toBe('/buffer/a/b')
  })

  it('returns the root for a root-level file', () => {
    expect(parentOf('/buffer/x.md')).toBe('/buffer')
  })

  it('returns / for the root itself', () => {
    expect(parentOf('/buffer')).toBe('/')
  })

  it('tolerates a trailing slash', () => {
    expect(parentOf('/buffer/a/b/')).toBe('/buffer/a')
  })
})

describe('basename', () => {
  it('returns the last segment of a nested path', () => {
    expect(basename('/buffer/a/b/x.md')).toBe('x.md')
  })

  it('returns the last segment of a root-level file', () => {
    expect(basename('/buffer/x.md')).toBe('x.md')
  })

  it('returns the root folder name for the root path', () => {
    expect(basename('/buffer')).toBe('buffer')
  })

  it('returns empty string for the filesystem root', () => {
    expect(basename('/')).toBe('')
  })

  it('tolerates a trailing slash', () => {
    expect(basename('/buffer/a/b/')).toBe('b')
  })
})

describe('relativeToRoot', () => {
  it('strips the storage root prefix from a nested path', () => {
    expect(relativeToRoot('/buffer/a/b/x.md')).toBe('a/b/x.md')
  })

  it('strips the storage root prefix from a root-level file', () => {
    expect(relativeToRoot('/buffer/x.md')).toBe('x.md')
  })

  it('returns empty string for the root itself', () => {
    expect(relativeToRoot('/buffer')).toBe('')
    expect(relativeToRoot('/buffer/')).toBe('')
  })

  it('returns the path unchanged when not under root', () => {
    expect(relativeToRoot('/other/x.md')).toBe('/other/x.md')
  })
})

describe('isUnderRoot', () => {
  it('is true for a nested file under the root', () => {
    expect(isUnderRoot('/buffer/a.md')).toBe(true)
    expect(isUnderRoot('/buffer/a/b/x.md')).toBe(true)
  })

  it('is true for the root itself', () => {
    expect(isUnderRoot('/buffer')).toBe(true)
  })

  it('is false for an unrelated path', () => {
    expect(isUnderRoot('/other/x.md')).toBe(false)
    expect(isUnderRoot('/buffers/x.md')).toBe(false)
  })
})
