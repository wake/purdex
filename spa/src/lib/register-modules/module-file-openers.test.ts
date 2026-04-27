import { describe, it, expect, beforeEach } from 'vitest'
import {
  registerModule,
  unregisterModule,
  type ModuleDefinition,
} from '../module-registry'
import {
  clearAllForHmr,
  getRegisteredOpeners,
  registerFileOpener,
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

  it('clears any pre-existing owner entries when a module declares no fileOpeners', () => {
    // The reconciler must be authoritative: if a module's current declaration
    // has no fileOpeners, any stale owner-scoped openers (from a previous
    // declaration that did, or from a now-removed inline path) must be wiped.
    registerModule({ id: 'm1', name: 'm1' })
    registerFileOpener({ ...mkOpener('stale'), ownerModuleId: 'm1' })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toHaveLength(0)
  })

  it('clears stale owner entries when a module replaces its declaration with one that has no fileOpeners', () => {
    // Pins the regression: HMR or a runtime mutation that swaps a module
    // definition from `fileOpeners: [opener]` to one without `fileOpeners`
    // must not leave the original opener registered for that owner.
    registerModule(mkModule('m1', [mkOpener('a')]))
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['a'])

    registerModule({ id: 'm1', name: 'm1' })  // same id, no fileOpeners
    applyModuleFileOpeners()
    expect(getRegisteredOpeners()).toEqual([])
  })

  it('does not touch entries owned by ids unknown to the module registry', () => {
    // A non-module owner (e.g. plugin host registering directly) is outside
    // the reconciler's responsibility.
    registerFileOpener({ ...mkOpener('plugin-x'), ownerModuleId: 'plugin-host' })
    applyModuleFileOpeners()
    expect(getRegisteredOpeners().map((o) => o.id)).toEqual(['plugin-x'])
  })
})
