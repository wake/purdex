import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { render } from '@testing-library/react'
import { registerBuiltinModules, resetFileOpenerRegistryForHmr } from '../index'
import { getDefaultOpener, getRegisteredOpeners } from '../../file-opener-registry'
import { getModule, getViewDefinition, resolvePaneRenderer } from '../../module-registry'
import { useLayoutStore } from '../../../stores/useLayoutStore'
import { useTabStore } from '../../../stores/useTabStore'
import { createTab } from '../../../types/tab'
import { getPrimaryPane } from '../../pane-tree'
import { ExecutionsView } from '../../../components/executions/ExecutionsView'
import ExecutionView from '../../../components/execution/ExecutionView'

vi.mock('../../../components/execution/ExecutionView', () => ({ default: vi.fn(() => null) }))
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

  // P-C.3b task 4: the execution pane wrapper hands ExecutionView the pane's
  // address (tab id via reverse lookup, pane id) and its `from`.
  it('the execution pane wrapper passes tabId (looked up from the pane id), paneId and from to ExecutionView', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
    const tab = createTab({ kind: 'execution', executionId: 'exc_1', host: 'h1', from })
    useTabStore.getState().addTab(tab)
    const pane = getPrimaryPane(tab.layout)
    const resolution = resolvePaneRenderer('execution')
    expect(resolution.kind).toBe('render')
    if (resolution.kind !== 'render') return
    const Component = resolution.component
    render(<Component pane={pane} isActive />)
    expect(vi.mocked(ExecutionView)).toHaveBeenCalled()
    expect(vi.mocked(ExecutionView).mock.calls[0][0]).toEqual({
      hostId: 'h1', executionId: 'exc_1', isActive: true, tabId: tab.id, paneId: pane.id, from,
    })
  })

  it('the execution pane wrapper offers no `from` (no take-back) when the pane belongs to no tab', () => {
    useTabStore.setState({ tabs: {}, tabOrder: [], activeTabId: null })
    vi.mocked(ExecutionView).mockClear()
    const from = { sessionCode: 'zk16vd', tmuxInstance: 'inst-1', cachedName: 'purdex' }
    const tab = createTab({ kind: 'execution', executionId: 'exc_1', host: 'h1', from })
    const pane = getPrimaryPane(tab.layout) // tab never added to the store
    const resolution = resolvePaneRenderer('execution')
    if (resolution.kind !== 'render') throw new Error('execution pane not registered')
    const Component = resolution.component
    render(<Component pane={pane} isActive />)
    expect(vi.mocked(ExecutionView).mock.calls[0][0]).toMatchObject({ tabId: '', paneId: pane.id, from: undefined })
  })
})
