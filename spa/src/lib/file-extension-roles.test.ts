import { describe, it, expect } from 'vitest'
import {
  IMAGE_EXTS,
  PDF_EXTS,
  DOWNLOAD_EXTS,
  roleForExtension,
} from './file-extension-roles'

describe('roleForExtension', () => {
  it('classifies image extensions as image', () => {
    for (const ext of ['png', 'jpg', 'jpeg', 'gif', 'webp', 'svg', 'ico']) {
      expect(roleForExtension(ext)).toBe('image')
    }
  })

  it('classifies pdf as pdf', () => {
    expect(roleForExtension('pdf')).toBe('pdf')
  })

  it('classifies office/archive/binary extensions as download', () => {
    for (const ext of [
      'doc',
      'docx',
      'xls',
      'xlsx',
      'ppt',
      'pptx',
      'zip',
      'rar',
      '7z',
      'tar',
      'gz',
      'bin',
      'exe',
      'dmg',
    ]) {
      expect(roleForExtension(ext)).toBe('download')
    }
  })

  it('classifies text/code/unknown extensions as text', () => {
    for (const ext of ['md', 'ts', 'txt', 'js', 'json', 'css', 'html', 'go', 'py', '']) {
      expect(roleForExtension(ext)).toBe('text')
    }
    // unknown extension falls through to text
    expect(roleForExtension('xyzzy')).toBe('text')
  })

  it('is case-insensitive', () => {
    expect(roleForExtension('PNG')).toBe('image')
    expect(roleForExtension('PDF')).toBe('pdf')
    expect(roleForExtension('DOCX')).toBe('download')
    expect(roleForExtension('MD')).toBe('text')
  })

  it('DOWNLOAD_EXTS does not include any text/code extensions', () => {
    for (const ext of [
      'md',
      'ts',
      'tsx',
      'js',
      'jsx',
      'txt',
      'json',
      'css',
      'scss',
      'html',
      'xml',
      'yml',
      'yaml',
      'go',
      'py',
      'rs',
      'sh',
      'log',
      'csv',
    ]) {
      expect(DOWNLOAD_EXTS.has(ext)).toBe(false)
    }
  })

  it('the three sets are mutually exclusive', () => {
    for (const ext of IMAGE_EXTS) {
      expect(PDF_EXTS.has(ext)).toBe(false)
      expect(DOWNLOAD_EXTS.has(ext)).toBe(false)
    }
    for (const ext of PDF_EXTS) {
      expect(DOWNLOAD_EXTS.has(ext)).toBe(false)
    }
  })
})
