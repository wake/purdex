// The kind chip's colour comes from theme tokens (spec §11: the prototype's seven kinds, each its own colour; 待確認 the warning colour).
import { describe, it, expect, beforeEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { KindChip } from './KindChip'
import { ENTRY_KINDS, type EntryKind } from '../../lib/journal/types'
import { useI18nStore } from '../../stores/useI18nStore'

beforeEach(() => { cleanup(); useI18nStore.getState().setLocale('en') })

const TOKEN: Record<EntryKind, string> = {
  exec: 'journal-kind-exec', exec_done: 'journal-kind-exec-done', task_new: 'journal-kind-task-new', task_done: 'journal-kind-task-done',
  comm: 'journal-kind-comm', request: 'journal-kind-request', confirm: 'status-warning-text',
}

describe('KindChip', () => {
  it.each(ENTRY_KINDS)('%s is coloured by its own theme token, with a soft fill of the same colour', (kind) => {
    render(<KindChip kind={kind} />)
    const cls = screen.getByTestId('journal-kind').className
    expect(cls).toContain(`text-${TOKEN[kind]}`)
    // 待確認 keeps the warning *fill* under warning *text*: on the light theme the fill is a pale yellow, so the text stays readable.
    const fill = kind === 'confirm' ? 'status-warning' : TOKEN[kind]
    expect(cls).toMatch(new RegExp(`bg-${fill}/\\d+`))
    expect(cls).toMatch(new RegExp(`border-${fill}/\\d+`))
  })

  it('no two of the seven kinds share a text colour (M: collapse the kinds onto the four stock tokens)', () => {
    const seen = new Set<string>()
    for (const kind of ENTRY_KINDS) {
      cleanup()
      render(<KindChip kind={kind} />)
      seen.add(screen.getByTestId('journal-kind').className.split(' ').find((c) => c.startsWith('text-') && !c.startsWith('text-[')) ?? '')
    }
    expect(seen.size).toBe(ENTRY_KINDS.length)
  })

  it('has no colour code anywhere in its classes or style', () => {
    for (const kind of ENTRY_KINDS) {
      cleanup()
      render(<KindChip kind={kind} />)
      const el = screen.getByTestId('journal-kind')
      expect(el.className).not.toMatch(/#[0-9a-f]{3,8}/i)
      expect(el.getAttribute('style')).toBeNull()
    }
  })
})
