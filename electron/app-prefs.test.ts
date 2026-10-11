import { describe, expect, it } from 'vitest'
import {
  closeSync, existsSync, fsyncSync, mkdtempSync, openSync, readdirSync, readFileSync, renameSync, rmSync, unlinkSync,
  writeFileSync, writeSync,
} from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  AWAY_MINUTES_OPTIONS, DEFAULT_APP_PREFS, ensureInstallId, loadAppPrefs, parseAppPrefs, saveAppPrefs, serializeAppPrefs,
  type AppPrefs, type PrefsFs,
} from './app-prefs'

const P = (over: Partial<AppPrefs> = {}): AppPrefs => ({ ...DEFAULT_APP_PREFS, ...over })
const UUID = '123e4567-e89b-42d3-a456-426614174000'

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
    expect(parseAppPrefs('{"showTray":"false"}')).toEqual(P({ showTray: true }))
    expect(parseAppPrefs('{"showTray":0}')).toEqual(P({ showTray: true }))
    expect(parseAppPrefs('{"showTray":null}')).toEqual(P({ showTray: true }))
  })
  it('accepts booleans only', () => {
    expect(parseAppPrefs('{"showTray":false}')).toEqual(P({ showTray: false }))
    expect(parseAppPrefs('{"showTray":true}')).toEqual(P({ showTray: true }))
  })
  it('ignores unknown keys and fills missing ones', () => {
    expect(parseAppPrefs('{"other":1}')).toEqual(DEFAULT_APP_PREFS)
    expect(parseAppPrefs('{"showTray":false,"other":1}')).toEqual(P({ showTray: false }))
  })
  it('does not return the shared default object', () => {
    expect(parseAppPrefs('')).not.toBe(DEFAULT_APP_PREFS)
  })
})

describe('serializeAppPrefs', () => {
  it('round-trips through parseAppPrefs', () => {
    const p = P({ showTray: false, presenceAwayMinutes: 15, installId: UUID })
    expect(parseAppPrefs(serializeAppPrefs(p))).toEqual(p)
  })
  it('emits pretty JSON with a trailing newline', () => {
    const s = serializeAppPrefs(P())
    expect(s).toBe('{\n  "showTray": true,\n  "presenceAwayMinutes": 5,\n  "installId": null\n}\n')
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
      saveAppPrefs(path, P({ showTray: false }))
      expect(existsSync(path)).toBe(true)
      expect(readFileSync(path, 'utf8')).toBe(serializeAppPrefs(P({ showTray: false })))
      expect(loadAppPrefs(path)).toEqual(P({ showTray: false }))
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('save overwrites an existing file', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      saveAppPrefs(path, P({ showTray: false }))
      saveAppPrefs(path, P({ showTray: true }))
      expect(loadAppPrefs(path)).toEqual(P({ showTray: true }))
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})

describe('presenceAwayMinutes', () => {
  it('defaults to 5', () => {
    expect(DEFAULT_APP_PREFS.presenceAwayMinutes).toBe(5)
    expect(parseAppPrefs('{}').presenceAwayMinutes).toBe(5)
  })
  it('accepts exactly the offered values', () => {
    expect([...AWAY_MINUTES_OPTIONS]).toEqual([1, 2, 3, 5, 10, 15, 30, 60])
    for (const n of AWAY_MINUTES_OPTIONS) {
      expect(parseAppPrefs(`{"presenceAwayMinutes":${n}}`).presenceAwayMinutes).toBe(n)
    }
  })
  it('anything else falls back to the default', () => {
    for (const bad of ['0', '4', '61', '-5', '5.5', '"5"', 'null', 'true', '[5]', '1e400']) {
      expect(parseAppPrefs(`{"presenceAwayMinutes":${bad}}`).presenceAwayMinutes).toBe(5)
    }
  })
})

describe('installId', () => {
  const mkdir = () => mkdtempSync(join(tmpdir(), 'pdx-app-prefs-'))
  it('is null until generated and a valid stored value is kept', () => {
    expect(parseAppPrefs('{}').installId).toBeNull()
    expect(parseAppPrefs(`{"installId":"${UUID}"}`).installId).toBe(UUID)
  })
  it('an invalid stored value reads as null', () => {
    for (const bad of ['""', '"x"', '123', 'null', '"123e4567-e89b-42d3-a456-42661417400g"', '"../../etc"', `"${UUID}\n"`]) {
      expect(parseAppPrefs(`{"installId":${bad}}`).installId).toBeNull()
    }
  })
  it('ensureInstallId generates once, persists, and returns the same id afterwards', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      saveAppPrefs(path, P({ showTray: false, presenceAwayMinutes: 10 }))
      const gen = () => UUID
      const id = ensureInstallId(path, gen)
      expect(id).toBe(UUID)
      expect(loadAppPrefs(path)).toEqual(P({ showTray: false, presenceAwayMinutes: 10, installId: UUID }))
      expect(ensureInstallId(path, () => { throw new Error('must not generate again') })).toBe(UUID)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('ensureInstallId replaces an invalid stored value', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      writeFileSync(path, '{"installId":"bad"}')
      expect(ensureInstallId(path, () => UUID)).toBe(UUID)
      expect(loadAppPrefs(path).installId).toBe(UUID)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('ensureInstallId uses crypto.randomUUID by default', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      const id = ensureInstallId(path)
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/)
      expect(ensureInstallId(path)).toBe(id)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
  it('only the known keys are ever serialised (no token-like field)', () => {
    const dir = mkdir()
    try {
      const path = join(dir, 'app-prefs.json')
      writeFileSync(path, '{"token":"secret","hosts":[{"token":"s"}],"showTray":true}')
      ensureInstallId(path, () => UUID)
      const text = readFileSync(path, 'utf8')
      expect(text).not.toContain('secret')
      expect(Object.keys(JSON.parse(text)).sort()).toEqual(['installId', 'presenceAwayMinutes', 'showTray'])
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})

describe('saveAppPrefs atomicity', () => {
  const real = { openSync, writeSync, fsyncSync, closeSync, renameSync, unlinkSync }
  const failing = (which: keyof typeof real): PrefsFs => ({
    ...real,
    [which]: () => { throw new Error('disk full') },
  })
  for (const which of ['writeSync', 'fsyncSync', 'renameSync'] as const) {
    it(`a failing ${which} leaves the previous file intact and no temp file`, () => {
      const dir = mkdtempSync(join(tmpdir(), 'app-prefs-atomic-'))
      try {
        const path = join(dir, 'app-prefs.json')
        saveAppPrefs(path, P({ showTray: false, installId: UUID }))
        const before = readFileSync(path, 'utf8')
        expect(() => saveAppPrefs(path, P({ showTray: true, installId: UUID }), failing(which))).toThrow('disk full')
        expect(readFileSync(path, 'utf8')).toBe(before)
        expect(loadAppPrefs(path).installId).toBe(UUID)
        expect(loadAppPrefs(path).showTray).toBe(false)
        expect(readdirSync(dir)).toEqual(['app-prefs.json'])
      } finally {
        rmSync(dir, { recursive: true, force: true })
      }
    })
  }
  it('a successful write leaves no temp files behind', () => {
    const dir = mkdtempSync(join(tmpdir(), 'app-prefs-atomic-'))
    try {
      const path = join(dir, 'app-prefs.json')
      saveAppPrefs(path, P({ installId: UUID }))
      saveAppPrefs(path, P({ installId: UUID, showTray: false }))
      expect(readdirSync(dir)).toEqual(['app-prefs.json'])
      expect(loadAppPrefs(path).showTray).toBe(false)
    } finally {
      rmSync(dir, { recursive: true, force: true })
    }
  })
})
