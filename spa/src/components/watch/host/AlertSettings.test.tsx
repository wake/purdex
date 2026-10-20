// spa/src/components/watch/host/AlertSettings.test.tsx — the 設定 tab's 告警 section, read only (MON-9a-2a; spec M49, P9, W8). The thresholds are wwd's defaults written in the App, shown as disabled inputs; the two
// "unreachable" switches have no API yet, so they are neither on nor off.
import { describe, it, expect, beforeEach, afterEach } from 'vitest'
import { render, screen, cleanup, within, act } from '@testing-library/react'
import { AlertSettings, ALERT_RULES } from './AlertSettings'
import { usePoolStore } from '../../../lib/pool/store'
import { parseFacts, parseHostsAnswer } from '../../../lib/pool/parse'
import type { PoolHost } from '../../../lib/pool/types'
import type { MonitorHost } from '../../../lib/pool/use-monitor-hosts'
import darwinJson from '../../../lib/pool/__fixtures__/facts_darwin.json'
import hostsJson from '../../../lib/pool/__fixtures__/pool_hosts_response.json'
import { useI18nStore } from '../../../stores/useI18nStore'

const t = (k: string, p?: Record<string, string | number>) => useI18nStore.getState().t(k, p)
const facts = parseFacts(darwinJson)!
const poolHost = (alias: string, psi: boolean): PoolHost => {
  const base = parseHostsAnswer(hostsJson)![0]
  return { ...base, host_id: `id-${alias}`, alias, reachable: true, labels: [], facts: { ...facts, alias, mem: { ...facts.mem, psi: psi ? { some10: 1, full10: 0, some60: 2 } : null } } }
}
const monitorHost = (alias: string) => ({ hostId: `id-${alias}`, alias, daemonHostId: null } as MonitorHost)
const mount = (alias: string, psi: boolean) => {
  act(() => { usePoolStore.setState({ hosts: [poolHost(alias, psi)], sourceHostId: 'cfg-a' }) })
  render(<AlertSettings host={monitorHost(alias)} />)
}

beforeEach(() => { cleanup(); usePoolStore.getState().reset() })
afterEach(() => cleanup())

describe('the threshold table', () => {
  it('says what it is: the defaults, written in wwd, shown until each host can be read (S9)', () => {
    mount('mlab', false)
    expect(screen.getByTestId('alert-defaults-note')).toHaveTextContent(t('watch.alert.defaults_note'))
  })

  it('has a row for each rule that applies, with its level, and every input is disabled', () => {
    mount('mlab', false)
    const rules = ALERT_RULES.filter((r) => !r.needsPsi)
    expect(rules.length).toBeGreaterThanOrEqual(6)
    for (const r of rules) {
      const row = screen.getByTestId(`alert-rule-${r.id}`)
      expect(row).toHaveTextContent(t(`watch.alert.level.${r.level}`))
      const input = within(row).getByRole('textbox')
      expect(input).toBeDisabled()
      expect(input).toHaveValue(t(`watch.alert.th.${r.id}`))
    }
  })

  it('the three watch-level rules (pressure_watch, pressure_danger, swap_growth) are not listed', () => {
    mount('mlab', false)
    for (const id of ['pressure_watch', 'pressure_danger', 'swap_growth']) expect(screen.queryByTestId(`alert-rule-${id}`)).toBeNull()
  })

  it('the mserv service rule is on a host that reports PSI and on no other', () => {
    mount('mlab', false)
    expect(screen.queryByTestId('alert-rule-mserv_service')).toBeNull()
    cleanup()
    mount('mserv', true)
    expect(screen.getByTestId('alert-rule-mserv_service')).toBeInTheDocument()
  })
})

describe('the two unreachable alerts (W8)', () => {
  it.each(['wwd', 'host'] as const)('%s: disabled, not drawn as on or off, with the reason', (which) => {
    mount('mlab', false)
    const row = screen.getByTestId(`alert-unreach-${which}`)
    expect(within(row).queryByRole('switch')).toBeNull()
    expect(row.querySelector('[aria-checked]')).toBeNull()
    expect(row.querySelector('[role="switch"]')).toBeNull()
    expect(screen.getByTestId(`alert-unreach-${which}-state`)).toHaveTextContent(t('watch.alert.unreach_pending'))
    expect(screen.getByTestId(`alert-unreach-${which}-state`)).toHaveAttribute('aria-disabled', 'true')
  })
})

describe('the observation thresholds', () => {
  it('are disabled inputs marked as the defaults', () => {
    mount('mlab', false)
    for (const id of ['observe_cpu', 'observe_mem']) {
      const input = within(screen.getByTestId(`alert-rule-${id}`)).getByRole('textbox')
      expect(input).toBeDisabled()
      expect(input).toHaveValue(t(`watch.alert.th.${id}`))
    }
  })
})
