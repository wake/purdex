// spa/src/components/journal/PersonMark.test.tsx — the highlighted person's name (J-2c, spec §2 item 23). The name comes from the host's
// answer (`person_name`), never from the code.
import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { PersonMark } from './PersonMark'

afterEach(() => cleanup())
const marks = () => screen.queryAllByTestId('journal-person').map((n) => n.textContent)

describe('PersonMark', () => {
  it('wraps every occurrence of the name and only that', () => {
    render(<PersonMark text="Kenji → Wake: please ask Wake" name="Wake" />)
    expect(marks()).toEqual(['Wake', 'Wake'])
  })
  it('two hosts, two names: each highlights its own (M: the name is hard-coded as Wake)', () => {
    const { unmount } = render(<PersonMark text="Kenji → Wake, Mia" name="Mia" />)
    expect(marks()).toEqual(['Mia'])
    unmount()
    render(<PersonMark text="Kenji → Wake, Mia" name="Wake" />)
    expect(marks()).toEqual(['Wake'])
  })
  it('a Unicode name works', () => {
    render(<PersonMark text="小明 → 阿華：小明請回覆" name="小明" />)
    expect(marks()).toEqual(['小明', '小明'])
  })
  it('no name: plain text, nothing highlighted, nothing lost', () => {
    const { container } = render(<PersonMark text="Kenji → Wake" name="" />)
    expect(marks()).toEqual([])
    expect(container.textContent).toBe('Kenji → Wake')
  })
  it('the highlight uses theme tokens only', () => {
    render(<PersonMark text="Wake" name="Wake" />)
    const el = screen.getByTestId('journal-person')
    expect(el.className).toContain('text-journal-warning-text')
    expect(el.className).toContain('bg-status-warning/')
    expect(el.getAttribute('style')).toBeNull()
  })
})
