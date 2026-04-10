import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerModule,
  unregisterModule,
  getModule,
  getModules,
  getPaneRenderer,
  getViewDefinition,
  getViewsByRegion,
  getModulesWithWorkspaceConfig,
  getModulesWithGlobalConfig,
  getModulesWithCommands,
  clearModuleRegistry,
} from './module-registry'
import type { ModuleDefinition } from './module-registry'

// eslint-disable-next-line @typescript-eslint/no-explicit-any
const DummyComponent = (() => null) as React.FC<any>

const sessionModule: ModuleDefinition = {
  id: 'session',
  name: 'Session',
  pane: { kind: 'tmux-session', component: DummyComponent },
  views: [{
    id: 'session-list',
    label: 'Sessions',
    icon: DummyComponent,
    scope: 'system',
    defaultRegion: 'primary-sidebar',
    component: DummyComponent,
  }],
}

const filesModule: ModuleDefinition = {
  id: 'files',
  name: 'Files',
  views: [{
    id: 'file-tree',
    label: 'Files',
    icon: DummyComponent,
    scope: 'workspace',
    defaultRegion: 'primary-panel',
    component: DummyComponent,
  }],
}

beforeEach(() => {
  clearModuleRegistry()
})

describe('module-registry', () => {
  describe('registerModule / getModule', () => {
    it('registers and retrieves a module', () => {
      registerModule(sessionModule)
      expect(getModule('session')).toEqual(sessionModule)
    })

    it('returns undefined for unregistered module', () => {
      expect(getModule('nonexistent')).toBeUndefined()
    })

    it('overwrites existing module with same id', () => {
      registerModule(sessionModule)
      const updated = { ...sessionModule, name: 'Updated' }
      registerModule(updated)
      expect(getModule('session')?.name).toBe('Updated')
    })
  })

  describe('unregisterModule', () => {
    it('removes a registered module', () => {
      registerModule(sessionModule)
      unregisterModule('session')
      expect(getModule('session')).toBeUndefined()
    })

    it('is a no-op for unregistered module', () => {
      unregisterModule('nonexistent') // should not throw
    })
  })

  describe('getModules', () => {
    it('returns all registered modules', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      expect(getModules()).toHaveLength(2)
    })

    it('returns empty array when none registered', () => {
      expect(getModules()).toEqual([])
    })
  })

  describe('getPaneRenderer', () => {
    it('returns component for registered pane kind', () => {
      registerModule(sessionModule)
      const renderer = getPaneRenderer('tmux-session')
      expect(renderer).toBeDefined()
      expect(renderer?.component).toBe(DummyComponent)
    })

    it('returns undefined for module without pane', () => {
      registerModule(filesModule)
      expect(getPaneRenderer('files')).toBeUndefined()
    })

    it('returns undefined for unknown kind', () => {
      expect(getPaneRenderer('unknown')).toBeUndefined()
    })
  })

  describe('getViewDefinition', () => {
    it('returns view by id', () => {
      registerModule(sessionModule)
      const view = getViewDefinition('session-list')
      expect(view?.label).toBe('Sessions')
    })

    it('returns undefined for unknown view id', () => {
      expect(getViewDefinition('unknown')).toBeUndefined()
    })
  })

  describe('getViewsByRegion', () => {
    it('returns views matching region', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      const sidebarViews = getViewsByRegion('primary-sidebar')
      expect(sidebarViews).toHaveLength(1)
      expect(sidebarViews[0].id).toBe('session-list')
    })

    it('filters by scope when provided', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      const systemViews = getViewsByRegion('primary-sidebar', 'system')
      expect(systemViews).toHaveLength(1)
      const wsViews = getViewsByRegion('primary-sidebar', 'workspace')
      expect(wsViews).toHaveLength(0)
    })

    it('returns empty array for region with no views', () => {
      expect(getViewsByRegion('secondary-sidebar')).toEqual([])
    })
  })

  describe('clearModuleRegistry', () => {
    it('removes all modules', () => {
      registerModule(sessionModule)
      registerModule(filesModule)
      clearModuleRegistry()
      expect(getModules()).toEqual([])
      expect(getPaneRenderer('tmux-session')).toBeUndefined()
    })
  })
})

describe('workspaceConfig / globalConfig', () => {
  it('getModulesWithWorkspaceConfig returns modules that declared workspaceConfig', () => {
    registerModule({
      id: 'files',
      name: 'Files',
      workspaceConfig: [{ key: 'projectPath', type: 'string', label: '專案路徑' }],
    })
    registerModule({ id: 'browser', name: 'Browser' })

    const result = getModulesWithWorkspaceConfig()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('files')
    expect(result[0].workspaceConfig![0].key).toBe('projectPath')
  })

  it('getModulesWithGlobalConfig returns modules that declared globalConfig', () => {
    registerModule({
      id: 'theme-mod',
      name: 'Theme Module',
      globalConfig: [{ key: 'darkMode', type: 'boolean', label: 'Dark Mode', defaultValue: true }],
    })
    registerModule({ id: 'other', name: 'Other' })

    const result = getModulesWithGlobalConfig()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('theme-mod')
  })
})

describe('module-registry commands', () => {
  it('getModulesWithCommands returns modules that have commands', () => {
    registerModule({ id: 'no-cmds', name: 'No Commands' })
    registerModule({
      id: 'has-cmds',
      name: 'Has Commands',
      commands: [{ id: 'test', name: 'Test', command: 'echo test' }],
    })
    const result = getModulesWithCommands()
    expect(result).toHaveLength(1)
    expect(result[0].id).toBe('has-cmds')
  })

  it('supports function commands', () => {
    registerModule({
      id: 'dynamic',
      name: 'Dynamic',
      commands: [{ id: 'dyn', name: 'Dynamic', command: (ctx) => `cd ${ctx.moduleConfig?.path ?? '~'}` }],
    })
    const result = getModulesWithCommands()
    expect(result).toHaveLength(1)
    const cmd = result[0].commands![0]
    expect(typeof cmd.command).toBe('function')
  })
})
