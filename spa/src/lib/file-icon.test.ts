import { describe, it, expect } from 'vitest'
import { fileIconForPath } from './file-icon'
import { ICON_MAP } from '../components/tab-icon-map'

describe('fileIconForPath', () => {
  it('maps representative extensions to their icon names', () => {
    const cases: Array<[string, string]> = [
      ['/buffer/readme.md', 'FileMd'],
      ['/buffer/a/b/main.ts', 'FileTs'],
      ['/buffer/App.tsx', 'FileTsx'],
      ['/buffer/index.js', 'FileJs'],
      ['/buffer/widget.jsx', 'FileJsx'],
      ['/buffer/comp.vue', 'FileVue'],
      ['/buffer/script.py', 'FilePy'],
      ['/buffer/lib.rs', 'FileRs'],
      ['/buffer/styles.css', 'FileCss'],
      ['/buffer/page.html', 'FileHtml'],
      ['/buffer/data.json', 'FileCode'],
      ['/buffer/rows.csv', 'FileCsv'],
      ['/buffer/doc.pdf', 'FilePdf'],
      ['/buffer/logo.png', 'FilePng'],
      ['/buffer/photo.jpg', 'FileJpg'],
      ['/buffer/photo.jpeg', 'FileJpg'],
      ['/buffer/vector.svg', 'FileSvg'],
      ['/buffer/anim.gif', 'FileImage'],
      ['/buffer/pic.webp', 'FileImage'],
      ['/buffer/favicon.ico', 'FileImage'],
      ['/buffer/report.doc', 'FileDoc'],
      ['/buffer/report.docx', 'FileDoc'],
      ['/buffer/sheet.xls', 'FileXls'],
      ['/buffer/sheet.xlsx', 'FileXls'],
      ['/buffer/deck.ppt', 'FilePpt'],
      ['/buffer/deck.pptx', 'FilePpt'],
      ['/buffer/bundle.zip', 'FileZip'],
      ['/buffer/query.sql', 'FileSql'],
    ]
    for (const [path, expected] of cases) {
      expect(fileIconForPath(path), path).toBe(expected)
    }
  })

  it('is case-insensitive on the extension', () => {
    expect(fileIconForPath('/buffer/README.MD')).toBe('FileMd')
    expect(fileIconForPath('/buffer/Photo.JPG')).toBe('FileJpg')
    expect(fileIconForPath('/buffer/Main.TS')).toBe('FileTs')
  })

  it('falls back to File for unknown or missing extensions', () => {
    expect(fileIconForPath('/buffer/mystery.qzx')).toBe('File')
    expect(fileIconForPath('/buffer/LICENSE')).toBe('File')
    expect(fileIconForPath('/buffer/Makefile')).toBe('File')
    expect(fileIconForPath('/buffer/.gitignore')).toBe('File')
  })

  it('returns Folder / FolderOpen for directories', () => {
    expect(fileIconForPath('/buffer/a', { isDir: true })).toBe('Folder')
    expect(fileIconForPath('/buffer/a', { isDir: true, expanded: false })).toBe('Folder')
    expect(fileIconForPath('/buffer/a', { isDir: true, expanded: true })).toBe('FolderOpen')
  })

  it('ignores extension for directories even if the name looks like a file', () => {
    expect(fileIconForPath('/buffer/assets.css', { isDir: true })).toBe('Folder')
    expect(fileIconForPath('/buffer/assets.css', { isDir: true, expanded: true })).toBe('FolderOpen')
  })

  it('every returned icon name is resolvable in the shared ICON_MAP', () => {
    const probePaths: Array<[string, { isDir?: boolean; expanded?: boolean } | undefined]> = [
      ['/buffer/readme.md', undefined],
      ['/buffer/main.ts', undefined],
      ['/buffer/App.tsx', undefined],
      ['/buffer/index.js', undefined],
      ['/buffer/widget.jsx', undefined],
      ['/buffer/comp.vue', undefined],
      ['/buffer/script.py', undefined],
      ['/buffer/lib.rs', undefined],
      ['/buffer/styles.css', undefined],
      ['/buffer/page.html', undefined],
      ['/buffer/data.json', undefined],
      ['/buffer/rows.csv', undefined],
      ['/buffer/doc.pdf', undefined],
      ['/buffer/logo.png', undefined],
      ['/buffer/photo.jpg', undefined],
      ['/buffer/vector.svg', undefined],
      ['/buffer/anim.gif', undefined],
      ['/buffer/report.doc', undefined],
      ['/buffer/sheet.xls', undefined],
      ['/buffer/deck.ppt', undefined],
      ['/buffer/bundle.zip', undefined],
      ['/buffer/query.sql', undefined],
      ['/buffer/song.mp3', undefined],
      ['/buffer/clip.mp4', undefined],
      ['/buffer/archive.tar', undefined],
      ['/buffer/notes.txt', undefined],
      ['/buffer/main.c', undefined],
      ['/buffer/main.cpp', undefined],
      ['/buffer/unknown.qzx', undefined],
      ['/buffer/LICENSE', undefined],
      ['/buffer/dir', { isDir: true }],
      ['/buffer/dir', { isDir: true, expanded: true }],
    ]
    for (const [path, opts] of probePaths) {
      const name = fileIconForPath(path, opts)
      expect(ICON_MAP[name], `${path} -> ${name}`).toBeDefined()
    }
  })
})
