import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { registerBuiltinModules, resetFileOpenerRegistryForHmr } from '../index'
import { getDefaultOpener, getRegisteredOpeners } from '../../file-opener-registry'
import { getModule, getViewDefinition } from '../../module-registry'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { ExecutionsView } from '../../../components/executions/ExecutionsView'
import {
  clearAllBuiltinModuleRegistries,
  resetAndRegisterBuiltinModules,
  resetModuleEnabledStore,
} from '../../__tests__/test-bootstrap-harness'

beforeEach(() => {
  resetAndRegisterBuiltinModules()
})

afterEach(() => {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
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

  it('resetFileOpenerRegistryForHmr clears every registered opener', () => {
    expect(getRegisteredOpeners().length).toBeGreaterThan(0)
    resetFileOpenerRegistryForHmr()
    expect(getRegisteredOpeners()).toEqual([])
  })

  it('registers the Executions sidebar view on the execution module', () => {
    const view = getViewDefinition('executions')
    expect(view).toBeDefined()
    expect(view?.label).toBe('Executions')
    expect(view?.scope).toBe('system')
    expect(view?.component).toBe(ExecutionsView)
    expect(getModule('execution')?.views?.map((v) => v.id)).toEqual(['executions'])
  })

  it('registration does not mutate any region\'s configured views', () => {
    useLayoutStore.setState({ regions: {
      ...useLayoutStore.getInitialState().regions,
      'primary-sidebar': { views: ['file-tree-workspace'], activeViewId: 'file-tree-workspace', width: 240, mode: 'pinned' },
    } })
    const before = structuredClone(useLayoutStore.getState().regions)
    clearAllBuiltinModuleRegistries()
    registerBuiltinModules()
    expect(getViewDefinition('executions')).toBeDefined()
    expect(useLayoutStore.getState().regions).toEqual(before)
    for (const region of Object.values(useLayoutStore.getState().regions)) {
      expect(region.views).not.toContain('executions')
    }
  })
})
