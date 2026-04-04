import { describe, it, expect, beforeEach } from 'vitest'
import { registerPaneRenderer, getPaneRenderer, clearPaneRegistry } from './pane-registry'

beforeEach(() => {
  clearPaneRegistry()
})

describe('pane-registry', () => {
  it('registers and retrieves a renderer', () => {
    const component = (() => null) as React.FC<unknown>
    registerPaneRenderer('tmux-session', { component })
    expect(getPaneRenderer('tmux-session')).toEqual({ component })
  })

  it('returns undefined for unregistered kind', () => {
    expect(getPaneRenderer('unknown')).toBeUndefined()
  })

  it('clearPaneRegistry removes all entries', () => {
    registerPaneRenderer('tmux-session', { component: (() => null) as React.FC<unknown> })
    clearPaneRegistry()
    expect(getPaneRenderer('tmux-session')).toBeUndefined()
  })

  it('overwrites existing registration', () => {
    const comp1 = (() => null) as React.FC<unknown>
    const comp2 = (() => 'v2') as unknown as React.FC<unknown>
    registerPaneRenderer('tmux-session', { component: comp1 })
    registerPaneRenderer('tmux-session', { component: comp2 })
    expect(getPaneRenderer('tmux-session')?.component).toBe(comp2)
  })
})
