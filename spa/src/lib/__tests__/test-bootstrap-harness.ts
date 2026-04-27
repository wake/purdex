import { clearContributions } from '../settings-contribution-registry'
import { clearModuleRegistry } from '../module-registry'
import { clearAllForHmr } from '../file-opener-registry'
import {
  clearSettingsSectionRegistry,
  clearLegacyPending as clearLegacySettingsPending,
} from '../settings-section-registry'
import { clearNewTabRegistry } from '../new-tab-registry'
import { clearInterfaceSubsectionRegistry } from '../interface-subsection-registry'
import { clearHostBuiltinSources } from '../host-builtin-sections'
import { clearFsBackendRegistry } from '../fs-backend'
import { useModuleEnabledStore } from '../../stores/useModuleEnabledStore'
import { registerBuiltinModules } from '../register-modules'

/**
 * Reset every global registry that `registerBuiltinModules()` mutates so the
 * next bootstrap starts from a guaranteed-clean state. Append-only registries
 * (e.g. `registerNewTabProvider`) leak silently across tests when callers
 * forget one of these — codex review noted this hole in the original
 * editor-disable-flow setup.
 *
 * The module-enabled store is intentionally left alone: it's a test input,
 * not a registry. Tests that need a specific enable state must call
 * `useModuleEnabledStore.setState(...)` themselves; tests that want a fully
 * pristine environment should also call `resetModuleEnabledStore()` below.
 *
 * Always call this from `beforeEach` (and after each repeat-bootstrap inside
 * a test) before invoking `registerBuiltinModules()` again.
 */
export function clearAllBuiltinModuleRegistries(): void {
  clearContributions()
  clearLegacySettingsPending()
  clearModuleRegistry()
  clearAllForHmr()
  clearSettingsSectionRegistry()
  clearNewTabRegistry()
  clearInterfaceSubsectionRegistry()
  clearHostBuiltinSources()
  clearFsBackendRegistry()
}

export function resetModuleEnabledStore(): void {
  useModuleEnabledStore.setState({ enabled: {}, baseline: null })
}

/**
 * Convenience helper for tests that want a fully clean environment plus the
 * built-in modules wired up. Resets the module-enabled store as well; tests
 * that need a specific override should call setEnabled(...) afterwards.
 */
export function resetAndRegisterBuiltinModules(): void {
  clearAllBuiltinModuleRegistries()
  resetModuleEnabledStore()
  registerBuiltinModules()
}
