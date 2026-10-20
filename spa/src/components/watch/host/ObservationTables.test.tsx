// spa/src/components/watch/host/ObservationTables.test.tsx — the 行程 tab's table of undeclared heavy commands (monitoring tabs spec M45; plan MON-8c): the columns, the words when the table is empty or cannot be read.
import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen, within, cleanup } from '@testing-library/react'
import { UndeclaredTable } from './ObservationTables'
import { clock } from '../../../lib/pool/words'
import { useI18nStore } from '../../../stores/useI18nStore'
import type { UndeclaredGroup, UndeclaredPage } from '../../../lib/pool/observations-api'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const LAST = Date.UTC(2026, 9, 20, 3, 4, 5)
const group = (over: Partial<UndeclaredGroup> = {}): UndeclaredGroup => ({ signature: 'go test', count: 3, cpuSeconds: 120.5, maxMemMb: 1536, lastAt: LAST, lastSession: 'ab12', lastVia: 'session', lastCommand: 'go test ./...', ...over })
const page = (over: Partial<UndeclaredPage> = {}): UndeclaredPage => ({ host: 'mlab', known: true, truncated: false, groups: [group(), group({ signature: 'codex exec', lastVia: 'codex', lastSession: 'orphan', cpuSeconds: 4, maxMemMb: 50, count: 1 })], ...over })
const mount = (p: Partial<React.ComponentProps<typeof UndeclaredTable>> = {}) =>
  render(<UndeclaredTable availability="ok" page={page()} load="ok" stateWord="" {...p} />)

beforeEach(() => cleanup())

describe('UndeclaredTable', () => {
  it('a row a group: signature, count, CPU seconds, peak memory, and the last one with its time and session', () => {
    mount()
    const row = screen.getByTestId('undeclared-row-go test')
    expect(row).toHaveTextContent('go test')
    expect(within(row).getByTestId('undeclared-count')).toHaveTextContent('3')
    expect(within(row).getByTestId('undeclared-cpu')).toHaveTextContent('120.5')
    expect(within(row).getByTestId('undeclared-mem')).toHaveTextContent('1,536 MB')
    expect(within(row).getByTestId('undeclared-last')).toHaveTextContent(clock(LAST))
    expect(within(row).getByTestId('undeclared-last')).toHaveTextContent('ab12')
    expect(within(row).getByTestId('undeclared-last').getAttribute('title')).toBe('go test ./...')
  })

  it('a codex one is marked, and a session that is the orphan reads as no owner', () => {
    mount()
    const row = screen.getByTestId('undeclared-row-codex exec')
    expect(row).toHaveTextContent(t('watch.host.obs.via_codex'))
    expect(row).toHaveTextContent(t('watch.host.owner.orphan'))
  })

  it('names what the table is, and says when only the biggest groups are here', () => {
    mount({ page: page({ truncated: true }) })
    expect(screen.getByTestId('undeclared')).toHaveTextContent(t('watch.host.obs.undeclared_title'))
    expect(screen.getByTestId('undeclared-truncated')).toHaveTextContent(t('watch.host.obs.truncated'))
  })

  it('no groups: nothing in 7 days', () => {
    mount({ page: page({ groups: [] }) })
    expect(screen.getByTestId('undeclared')).toHaveTextContent(t('watch.host.obs.undeclared_none'))
  })

  it('says why it cannot be read', () => {
    const cases: Array<[Partial<React.ComponentProps<typeof UndeclaredTable>>, string]> = [
      [{ load: 'forbidden', page: null }, t('watch.host.obs.forbidden')],
      [{ page: page({ known: false, groups: [] }) }, t('watch.host.obs.too_old')],
      [{ availability: 'no_route', page: null, load: 'idle' }, t('watch.host.obs.no_route')],
      [{ availability: 'host_down', page: null, load: 'idle', stateWord: 'Host is offline' }, t('watch.host.obs.host_down', { state: 'Host is offline' })],
      [{ load: 'error', page: null }, t('watch.host.obs.error')],
    ]
    for (const [over, text] of cases) {
      cleanup()
      mount(over)
      expect(screen.getByTestId('undeclared-note')).toHaveTextContent(text)
      expect(screen.queryByTestId('undeclared-row-go test')).toBeNull()
    }
  })

  it('a failed refresh keeps the last page and says so', () => {
    mount({ load: 'error' })
    expect(screen.getByTestId('undeclared-row-go test')).toBeInTheDocument()
    expect(screen.getByTestId('undeclared-note')).toHaveTextContent(t('watch.host.obs.error'))
  })

  it('reading for the first time says it is reading', () => {
    mount({ page: null, load: 'loading' })
    expect(screen.getByTestId('undeclared-note')).toHaveTextContent(t('watch.host.obs.loading'))
  })
})
