import { describe, it, expect, beforeEach } from 'vitest'
import { useModuleConfigStore } from './useModuleConfigStore'

beforeEach(() => {
  useModuleConfigStore.setState({ globalConfig: {} })
})

describe('useModuleConfigStore', () => {
  it('sets and reads global module config', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'maxDepth', 5)
    expect(useModuleConfigStore.getState().globalConfig.files?.maxDepth).toBe(5)
  })

  it('preserves existing keys when setting new ones', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'keyA', 'valA')
    useModuleConfigStore.getState().setGlobalModuleConfig('files', 'keyB', 'valB')
    const cfg = useModuleConfigStore.getState().globalConfig.files!
    expect(cfg.keyA).toBe('valA')
    expect(cfg.keyB).toBe('valB')
  })

  it('getGlobalModuleConfig returns value or undefined', () => {
    useModuleConfigStore.getState().setGlobalModuleConfig('m', 'k', 42)
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('m', 'k')).toBe(42)
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('m', 'missing')).toBeUndefined()
    expect(useModuleConfigStore.getState().getGlobalModuleConfig('nope', 'k')).toBeUndefined()
  })
})
