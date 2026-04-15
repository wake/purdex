import { describe, it, expect } from 'vitest'
import { nextWorkspaceName } from './workspace-naming'

describe('nextWorkspaceName', () => {
  it('returns "Workspace 1" when no workspaces exist', () => {
    expect(nextWorkspaceName([])).toBe('Workspace 1')
  })

  it('returns next sequential name', () => {
    expect(nextWorkspaceName(['Workspace 1'])).toBe('Workspace 2')
  })

  it('fills gap in numbering', () => {
    expect(nextWorkspaceName(['Workspace 1', 'Workspace 3'])).toBe('Workspace 2')
  })

  it('starts from 1 even if only higher numbers exist', () => {
    expect(nextWorkspaceName(['Workspace 2'])).toBe('Workspace 1')
  })

  it('ignores non-pattern names', () => {
    expect(nextWorkspaceName(['Dev', 'Workspace 2'])).toBe('Workspace 1')
  })
})
