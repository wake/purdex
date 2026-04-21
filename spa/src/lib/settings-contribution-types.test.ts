import type React from 'react'
import { describe, expectTypeOf, it } from 'vitest'
import type { ModuleDefinition } from './module-registry'
import type {
  SettingsContributionDeclaration,
  SettingsContextFor,
} from './settings-contribution-types'

const PurdexScopedComponent: React.ComponentType<{ ctx: { scope: 'purdex' } }> = () => null

const HostScopedComponent: React.ComponentType<{ ctx: { scope: 'host'; hostId: string } }> = () => null

const WorkspaceScopedComponent: React.ComponentType<{ ctx: { scope: 'workspace'; workspaceId: string } }> = () => null

describe('SettingsContributionDeclaration typing', () => {
  it('accepts a host-scoped component that requires hostId in ctx', () => {
    const declaration: SettingsContributionDeclaration<'host'> = {
      localId: 'host-settings',
      scope: 'host',
      order: 0,
      labelKey: 'host.settings',
      component: HostScopedComponent,
    }

    expectTypeOf(declaration.component).toEqualTypeOf<
      React.ComponentType<{ ctx: SettingsContextFor<'host'> }>
    >()
  })
})

const invalidHostDeclaration: SettingsContributionDeclaration<'host'> = {
  localId: 'wrong-host-settings',
  scope: 'host',
  order: 0,
  labelKey: 'host.settings',
  // @ts-expect-error -- host-scoped declarations must accept host context, not workspace context
  component: WorkspaceScopedComponent,
}

void invalidHostDeclaration

// Regression tests for Finding A: ModuleDefinition.settings must preserve
// per-item scope↔ctx relationship, not flatten SettingsContributionDeclaration<S>
// into a single instantiation against the full SettingsScope union.

const validMixedScopeModule: ModuleDefinition = {
  id: 'test-module',
  name: 'Test',
  settings: [
    {
      localId: 'purdex-section',
      scope: 'purdex',
      order: 0,
      labelKey: 'test.purdex',
      component: PurdexScopedComponent,
    },
    {
      localId: 'host-section',
      scope: 'host',
      order: 1,
      labelKey: 'test.host',
      component: HostScopedComponent,
    },
    {
      localId: 'workspace-section',
      scope: 'workspace',
      order: 2,
      labelKey: 'test.workspace',
      component: WorkspaceScopedComponent,
    },
  ],
}
void validMixedScopeModule

const hostScopeWithWrongComponentModule: ModuleDefinition = {
  id: 'test-module',
  name: 'Test',
  settings: [
    // @ts-expect-error -- scope: 'host' requires a component accepting { scope: 'host'; hostId: string }
    {
      localId: 'bad-host',
      scope: 'host',
      order: 0,
      labelKey: 'test.bad-host',
      component: WorkspaceScopedComponent,
    },
  ],
}
void hostScopeWithWrongComponentModule

const workspaceScopeWithWrongComponentModule: ModuleDefinition = {
  id: 'test-module',
  name: 'Test',
  settings: [
    // @ts-expect-error -- scope: 'workspace' requires a component accepting { scope: 'workspace'; workspaceId: string }
    {
      localId: 'bad-workspace',
      scope: 'workspace',
      order: 0,
      labelKey: 'test.bad-workspace',
      component: HostScopedComponent,
    },
  ],
}
void workspaceScopeWithWrongComponentModule

const purdexScopeWithWrongComponentModule: ModuleDefinition = {
  id: 'test-module',
  name: 'Test',
  settings: [
    // @ts-expect-error -- scope: 'purdex' requires a component accepting { scope: 'purdex' }
    {
      localId: 'bad-purdex',
      scope: 'purdex',
      order: 0,
      labelKey: 'test.bad-purdex',
      component: HostScopedComponent,
    },
  ],
}
void purdexScopeWithWrongComponentModule
