// The panel's one-line cell draws iconDot's light on the icon's corner (compact), so it takes no slot of its own.
import { describe, it, expect, beforeEach } from 'vitest'
import { render } from '@testing-library/react'
import { TeamSeatIcon } from './TeamSeatIcon'
import { useAgentStore } from '../../stores/useAgentStore'
import { useUISettingsStore } from '../../stores/useUISettingsStore'
import { compositeKey } from '../../lib/composite-key'

const ck = compositeKey('h1', 's1')

describe('TeamSeatIcon compact', () => {
  beforeEach(() => {
    useAgentStore.setState({ statuses: { [ck]: 'running' }, agentTypes: { [ck]: 'cc' } } as never)
    useUISettingsStore.setState({ tabIndicatorStyle: 'iconDot' } as never)
  })

  it('iconDot draws a slot beside the icon; compact is one 16px box with the light overlaid', () => {
    const full = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} />)
    expect(full.container.firstElementChild?.className).not.toContain('w-4')
    full.unmount()
    const compact = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact />)
    expect((compact.container.firstElementChild as HTMLElement).className).toContain('w-4')
    expect(compact.container.querySelector('[data-testid="tab-status-indicator"]')).not.toBeNull()
  })

  it('other styles are untouched by compact', () => {
    useUISettingsStore.setState({ tabIndicatorStyle: 'dot' } as never)
    const a = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} />)
    const before = a.container.innerHTML
    a.unmount()
    const b = render(<TeamSeatIcon hostId="h1" sessionCode="s1" size={12} compact />)
    expect(b.container.innerHTML).toBe(before)
  })
})
