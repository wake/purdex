// spa/src/components/team/Steppers.names.test.tsx — #3446: the − / + of a QuotaStepper are told apart by name. Two steppers of one screen (the auto relay and the member pool of a lead, and every row or
// team beside it) used to be called just 「增加」 / 「減少」, which a screen reader or voice control cannot tell apart. The names say the action, the field and whose quota it is; the number box has the same name
// minus the action. The assertions are on accessible names (getByRole), several steppers mounted at once.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import { QuotaStepper } from './Steppers'
import { useI18nStore } from '../../stores/useI18nStore'
import { useRelayQuotaStore } from '../../lib/team/relay-quota'
import { useHostStore } from '../../stores/useHostStore'
import type { SessionQuota } from '../../lib/team/types'

vi.mock('../../lib/team/relay-quota-writer', () => ({ setQuota: vi.fn() }))

const row = (id: string, over: Partial<SessionQuota> = {}): SessionQuota => ({
  session_id: id, root_session_id: `root-${id}`, title: `T-${id}`, address: `mlab/${id}-xx`, is_lead: true, self_left: 4, member_pool_left: 2, rev: 1, ...over,
})

/** The − and + buttons (the number's own display is a button too, named after the field and the value). */
const stepButtons = () => screen.getAllByRole('button').filter((b) => b.textContent === '−' || b.textContent === '+')

/** Two leads, each with both steppers: four steppers, eight buttons, four number boxes, all on one screen. */
function mountTwoLeads() {
  render(
    <div>
      <QuotaStepper hostId="h" row={row('a')} name="Alma" field="self_left" label={useI18nStore.getState().t('unattended.quota.self')} />
      <QuotaStepper hostId="h" row={row('a')} name="Alma" field="member_pool_left" label={useI18nStore.getState().t('unattended.quota.pool')} />
      <QuotaStepper hostId="h" row={row('b')} name="Bram" field="self_left" label={useI18nStore.getState().t('unattended.quota.self')} />
      <QuotaStepper hostId="h" row={row('b')} name="Bram" field="member_pool_left" label={useI18nStore.getState().t('unattended.quota.pool')} />
    </div>,
  )
}

const hostCfg = (id: string, name: string, order: number) => ({ id, name, ip: '1', port: 1, token: 't', order })

beforeEach(() => {
  cleanup()
  useRelayQuotaStore.getState().reset()
  useHostStore.setState({ hosts: {}, hostOrder: [], runtime: {} } as never)
})
afterEach(() => { cleanup() })

describe('QuotaStepper accessible names', () => {
  it('zh-TW: each button says the action, the field and whose it is, and no two are the same', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoLeads()
    const names = stepButtons().map((b) => b.getAttribute('aria-label'))
    expect(names).toEqual([
      '減少自動接力額度（Alma）', '增加自動接力額度（Alma）',
      '減少member額度（Alma）', '增加member額度（Alma）',
      '減少自動接力額度（Bram）', '增加自動接力額度（Bram）',
      '減少member額度（Bram）', '增加member額度（Bram）',
    ])
    expect(new Set(names).size).toBe(names.length)
  })

  it('en: the same', () => {
    useI18nStore.getState().setLocale('en')
    mountTwoLeads()
    const names = stepButtons().map((b) => b.getAttribute('aria-label'))
    expect(new Set(names).size).toBe(8)
    expect(names[0]).toBe('Decrease Auto relay quota (Alma)')
    expect(names[1]).toBe('Increase Auto relay quota (Alma)')
    expect(names[7]).toBe('Increase Members quota (Bram)')
  })

  it('a button is found by its accessible name, one at a time (getByRole, not an index into a list of 「增加」)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoLeads()
    expect(screen.getByRole('button', { name: '增加member額度（Bram）' })).toHaveTextContent('+')
    expect(screen.getByRole('button', { name: '減少自動接力額度（Alma）' })).toHaveTextContent('−')
    expect(screen.queryByRole('button', { name: '增加' })).toBeNull() // the bare name is gone
    expect(screen.queryByRole('button', { name: '減少' })).toBeNull()
  })

  it('a custom locale saved before this change, with the old bare 「Decrease」 / 「Increase」 for the old keys, does not take the names away (the buttons use their own keys; a missing one falls back to the built-in English)', () => {
    const id = useI18nStore.getState().importLocale({ name: 'Old custom', translations: { 'unattended.quota.minus': 'Decrease', 'unattended.quota.plus': 'Increase', 'unattended.quota.self': 'Auto relay', 'unattended.quota.pool': 'Members' } })
    useI18nStore.getState().setLocale(id)
    mountTwoLeads()
    const names = stepButtons().map((b) => b.getAttribute('aria-label'))
    expect(names).toContain('Decrease Auto relay quota (Alma)')
    expect(names).toContain('Increase Members quota (Bram)')
    expect(new Set(names).size).toBe(8)
    expect(names).not.toContain('Decrease')
    expect(names).not.toContain('Increase')
  })

  it('two hosts that show the same row name: the host is in the name, so the buttons are still told apart (the heading above is no part of a button\'s name)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', 0), h2: hostCfg('h2', 'air26', 1) }, hostOrder: ['h1', 'h2'] } as never)
    render(
      <div>
        <QuotaStepper hostId="h1" row={row('a')} name="Alma" field="self_left" label="自動接力" />
        <QuotaStepper hostId="h2" row={row('a2')} name="Alma" field="self_left" label="自動接力" />
      </div>,
    )
    const names = stepButtons().map((b) => b.getAttribute('aria-label'))
    expect(names).toEqual(['減少自動接力額度（Alma · mlab）', '增加自動接力額度（Alma · mlab）', '減少自動接力額度（Alma · air26）', '增加自動接力額度（Alma · air26）'])
    expect(screen.getByRole('button', { name: '增加自動接力額度（Alma · air26）' })).toBeInTheDocument()
    const boxes = screen.getAllByTestId('quota-value').map((b) => b.getAttribute('aria-label') ?? b.querySelector('[aria-label]')?.getAttribute('aria-label'))
    expect(new Set(boxes).size).toBe(2)
  })

  it('with one host the name does not carry it (nothing to tell apart; the visible heading and rows are enough)', () => {
    useI18nStore.getState().setLocale('zh-TW')
    useHostStore.setState({ hosts: { h1: hostCfg('h1', 'mlab', 0) }, hostOrder: ['h1'] } as never)
    render(<QuotaStepper hostId="h1" row={row('a')} name="Alma" field="self_left" label="自動接力" />)
    expect(stepButtons().map((b) => b.getAttribute('aria-label'))).toEqual(['減少自動接力額度（Alma）', '增加自動接力額度（Alma）'])
  })

  it('the number box has the same name without the action, so it is told apart too', () => {
    useI18nStore.getState().setLocale('zh-TW')
    mountTwoLeads()
    const boxes = screen.getAllByTestId('quota-value')
    expect(boxes).toHaveLength(4)
    const names = boxes.map((b) => b.getAttribute('aria-label') ?? b.querySelector('[aria-label]')?.getAttribute('aria-label'))
    expect(new Set(names).size).toBe(4)
    expect(names).toEqual(['自動接力額度（Alma）: 4', 'member額度（Alma）: 2', '自動接力額度（Bram）: 4', 'member額度（Bram）: 2'])
  })
})
