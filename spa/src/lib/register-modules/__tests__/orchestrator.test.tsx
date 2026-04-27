import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { registerBuiltinModules } from '../index'
import {
  clearAllForHmr,
  getDefaultOpener,
  getRegisteredOpeners,
} from '../../file-opener-registry'
import { clearModuleRegistry, getModule } from '../../module-registry'
import { clearContributions } from '../../settings-contribution-registry'

beforeEach(() => {
  clearContributions()
  clearModuleRegistry()
  clearAllForHmr()
  registerBuiltinModules()
})

afterEach(() => {
  clearContributions()
  clearModuleRegistry()
  clearAllForHmr()
})

describe('registerBuiltinModules orchestrator', () => {
  it('registers the editor module definition', () => {
    const editor = getModule('editor')
    expect(editor).toBeDefined()
    expect(editor?.disableable).toBe(true)
  })

  it('registers Editor file openers in the file-opener registry', () => {
    const ids = getRegisteredOpeners().map((o) => o.id).sort()
    expect(ids).toEqual(['image-preview', 'monaco-editor', 'pdf-viewer'])
  })

  it('all editor file openers are owned by the editor module', () => {
    const owners = new Set(getRegisteredOpeners().map((o) => o.ownerModuleId))
    expect(owners).toEqual(new Set(['editor']))
  })

  it('returns monaco-editor as the default opener for plain text files', () => {
    const txt = { name: 'a.txt', path: '/a.txt', extension: 'txt', size: 1, isDirectory: false }
    expect(getDefaultOpener(txt)?.id).toBe('monaco-editor')
  })

  it('returns image-preview as the default opener for png files', () => {
    const png = { name: 'a.png', path: '/a.png', extension: 'png', size: 1, isDirectory: false }
    expect(getDefaultOpener(png)?.id).toBe('image-preview')
  })
})
