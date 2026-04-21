import type React from 'react'
import { describe, expectTypeOf, it } from 'vitest'
import type {
  SettingsContributionDeclaration,
  SettingsContextFor,
} from './settings-contribution-types'

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

// @ts-expect-error -- host-scoped declarations must accept host context, not workspace context
const invalidHostDeclaration: SettingsContributionDeclaration<'host'> = {
  localId: 'wrong-host-settings',
  scope: 'host',
  order: 0,
  labelKey: 'host.settings',
  component: WorkspaceScopedComponent,
}

void invalidHostDeclaration
