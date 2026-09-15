import { describe, expect, it } from 'vitest'
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { DEFAULT_APP_PREFS, loadAppPrefs, parseAppPrefs, saveAppPrefs, serializeAppPrefs } from './app-prefs'

describe('parseAppPrefs', () => {
  it('returns defaults for empty / null / undefined input', () => {
    expect(parseAppPrefs('')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('   \n')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs(null)).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs(undefined)).toEqual(DEFAULT_APP_PREFS)
  })
  it('returns defaults for malformed JSON', () => {
    expect(parseAppPrefs('{ showTray: ')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('not json')).toEqual(DEFAULT_APP_PREFS)
  })
  it('returns defaults when the document is not an object', () => {
    expect(parseAppPrefs('null')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('[]')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('"str"')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('42')).toEqual(DEFAULT_APP_PREFS)
  })
  it('falls back to the default for a field with the wrong type', () => {
    expect(parseAppPrefs('{"showTray":"false"}')).toEqual({ showTray: true })
    expect(parseAppPrefs('{"showTray":0}')).toEqual({ showTray: true })
    expect(parseAppPrefs('{"showTray":null}')).toEqual({ showTray: true })
  })
  it('accepts booleans only', () => {
    expect(parseAppPrefs('{"showTray":false}')).toEqual({ showTray: false })
    expect(parseAppPrefs('{"showTray":true}')).toEqual({ showTray: true })
  })
  it('ignores unknown keys and fills missing ones', () => {
    expect(parseAppPrefs('{"other":1}')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('{"showTray":false,"other":1}')).toEqual({ showTray: false })
  })
  it('does not return the shared default object', () => {
    expect(parseAppPrefs('')).not.toBe(DEFAULT_APP_PREFS)
  })
})

describe('serializeAppPrefs', () => {
  it('round-trips through parseAppPrefs', () => {
    const p = { showTray: false }
    expect(parseAppPrefs(serializeAppPrefs(p))).toEqual(p)
  })
  it('emits pretty JSON with a trailing newline', () => {
    const s = serializeAppPrefs({ showTray: true })
    expect(s).toBe('{\n  "showTray": true\n}\n')
  })
})

describe('loadAppPrefs / saveAppPrefs', () => {
  const mkdir = () => mkdtempSync(join(tmpdir(), 'pdx-app-prefs-'))

  it('returns defaults when the file does not exist', () => {
    const dir = mkdir()
    try {
      expect(loadAppPrefs(join(dir, 'missing', 'app-prefs.json'))).toEqual(DEFAULT_APP_PREFS)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('returns defaults when the file is unreadable garbage', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      writeFileSync(path, '{{{')
      expect(loadAppPrefs(path)).toEqual(DEFAULT_APP_PREFS)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('returns defaults when the path is a directory', () => {
    const dir = mkdir()
    try {
      expect(loadAppPrefs(dir)).toEqual(DEFAULT_APP_PREFS)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('save creates parent directories and load reads it back', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'nested', 'deeper', 'app-prefs.json')
      saveAppPrefs(path, { showTray: false })
      expect(existsSync(path)).toBe(true)
      expect(readFileSync(path, 'utf8')).toBe(serializeAppPrefs({ showTray: false }))
      expect(loadAppPrefs(path)).toEqual({ showTray: false })
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('save overwrites an existing file', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      saveAppPrefs(path, { showTray: false })
      saveAppPrefs(path, { showTray: true })
      expect(loadAppPrefs(path)).toEqual({ showTray: true })
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})
