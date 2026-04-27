import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerModule,
  unregisterModule,
  type ModuleDefinition,
} from '../module-registry'
import {
  clearAllForHmr,
  getRegisteredOpeners,
  type FileOpener,
} from '../file-opener-registry'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { applyModuleFileOpeners } from './module-file-openers'

const mkOpener = (id: string): FileOpener => ({
  id,
  label: id,
  icon: 'File',
  match: () => true,
  priority: 'default',
  createContent: () => ({ kind: 'editor' } as never),
})

const mkModule = (id: string, openers: FileOpener[], disableable = false): ModuleDefinition => ({
  id,
  name: id,
  disableable,
  fileOpeners: openers,
})

beforeEach(() => {
  clearAllForHmr()
  unregisterModule('m1')
  unregisterModule('m2')
  // Reset enable overrides between tests so prior `setEnabled(false)` does
  // not leak into the next case.
  useModuleEnabledStore.setState({ enabled: {} })
})

describe('applyModuleFileOpeners', () => {
  it('registers fileOpeners for non-disableable modules', () => {
    registerModule(mkModule('m1', [mkOpener('a')]))
    applyModuleFileOpeners()
    const ids = getRegisteredOpeners().map((o) => o.id).sort()
    expect(ids).toEqual(['a'])
    expect(getRegisteredOpeners()[0].ownerModuleId).toBe('m1')
  })

  it('skips fileOpeners for disabled disableable modules', () => {
    registerModule(mkModule('m1', [mkOpener('a')], true))
    useModuleEnabledStore.getState().setEnabled('m1', false)
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })

  it('registers fileOpeners for disableable modules that are enabled (default)', () => {
    registerModule(mkModule('m1', [mkOpener('a')], true))
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['a'])
  })

  it('idempotent: repeated apply does not duplicate openers', () => {
    registerModule(mkModule('m1', [mkOpener('a')]))
    applyModuleFileOpeners()
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toHaveLength(1)
  })

  it('only owner-scoped removal: re-apply after a module changes leaves other owners untouched', () => {
    registerModule(mkModule('m1', [mkOpener('a')]))
    registerModule(mkModule('m2', [mkOpener('b')]))
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id).sort()).toEqual(['a', 'b'])

    // Simulate `m1` being disabled and re-applied; `m2` should keep its opener.
    unregisterModule('m1')
    registerModule(mkModule('m1', [mkOpener('a')], true))
    useModuleEnabledStore.getState().setEnabled('m1', false)
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['b'])
  })

  it('skips modules without fileOpeners without removing existing entries', () => {
    registerModule(mkModule('m1', [mkOpener('a')]))
    registerModule({ id: 'm2', name: 'm2' })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['a'])
  })
})
