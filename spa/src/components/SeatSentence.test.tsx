import { describe, it, expect, beforeEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { SeatSentence, SEAT_MARK } from './SeatSentence'
import { usePersonaStore } from '../stores/usePersonaStore'
import { useI18nStore } from '../stores/useI18nStore'
import { makeCollection, makePersona } from '../lib/names/__fixtures__/personas'

function seedKenji(avatar = '') {
  const c = makeCollection(['kenji'])
  c.items[0] = makePersona('kenji', { name: 'Kenji', avatar_data: avatar })
  usePersonaStore.setState({ byHost: { h1: { status: 'ready', collection: c } } })
}

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('zh-TW'); usePersonaStore.setState({ byHost: {} }) })

describe('SeatSentence', () => {
  it('puts the face and the name where the mark is, keeps the text around it, and keeps the address as the tooltip', () => {
    seedKenji('data:image/png;base64,AA==')
    const { container } = render(<SeatSentence template={`來自 ${SEAT_MARK} · 12:00`} address="mlab/purdex-1f-kenji" fallback="mlab/purdex-1f-kenji" hostId="h1" />)
    expect(container).toHaveTextContent('來自 Kenji · 12:00')
    const seat = screen.getByTestId('seat-inline')
    expect(seat).toHaveAttribute('title', 'mlab/purdex-1f-kenji')
    expect(seat.querySelector('img')).not.toBeNull()
    expect(container.textContent).not.toContain('purdex-1f') // the address is only in the tooltip
  })

  it('draws the initials on the host colour when the persona has no picture', () => {
    seedKenji('')
    render(<SeatSentence template={SEAT_MARK} address="purdex-1f-kenji" fallback="x" hostId="h1" />)
    expect(screen.getByTestId('seat-inline').querySelector('img')).toBeNull()
    expect(screen.getByTestId('seat-inline').textContent).toBe('KKenji') // the initial, then the name
  })

  it('a name that is no persona reads as the text it always was', () => {
    seedKenji()
    const { container } = render(<SeatSentence template={`來自 ${SEAT_MARK} · 12:00`} address="mlab/purdex-1f-ghost" fallback="mlab/purdex-1f-ghost" hostId="h1" />)
    expect(container.textContent).toBe('來自 mlab/purdex-1f-ghost · 12:00')
    expect(screen.queryByTestId('seat-inline')).toBeNull()
  })

  it('a template with no mark is shown as it is', () => {
    const { container } = render(<SeatSentence template="背景任務回報" address="a" fallback="a" hostId={null} />)
    expect(container.textContent).toBe('背景任務回報')
  })
})
