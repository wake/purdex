// The form's 96 px preview follows Avatar's rule by the persona's real code: a draft with no code yet is "no persona" (the neutral disc), however the initial is chosen.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { PersonaForm } from './PersonaForm'
import { personaColor } from '../../../lib/names/persona-color'
import { makePersona } from '../../../lib/names/__fixtures__/personas'

afterEach(cleanup)
const noop = () => {}
const form = (code: string, name: string) => (
  <PersonaForm draft={makePersona(code, { name })} isNew issues={[]} saving={false} onChange={noop} onSave={noop} onToggleRetired={noop} />
)

describe('PersonaForm avatar preview', () => {
  it('a draft with no code yet: the neutral disc with a "?" (not a persona colour made from a placeholder)', () => {
    render(form('', ''))
    const el = screen.getAllByRole('img').find((e) => e.textContent === '?')!
    expect(el.className).toContain('bg-surface-secondary')
    expect(el.style.backgroundColor).toBe('')
  })

  it('a draft with a code: the persona\'s colour', () => {
    render(form('hiro', 'Hiro'))
    const el = screen.getByRole('img', { name: 'Hiro' })
    expect(el).toHaveStyle({ backgroundColor: personaColor('hiro') })
  })
})
