// Conversation entity spec §10.3: PreludeSection hands each run of one
// attribution to its own PreludeSegment. The production DOM carries no
// attribution, so this file mocks PreludeSegment to show each run's stint and
// the poses it draws (kept apart so the hoisted mock cannot reach other suites).
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { useEffect, useState } from 'react'
import PreludeSection from './PreludeSection'
import type { PreludeSegmentProps } from './PreludeSegment'
import { segmentPoses } from './test-segment-poses'
import { derivePrelude } from '../../../lib/nex/prelude'
import { sanitizePreludePage } from '../../../lib/nex/prelude-wire'
import real from '../../../lib/nex/__fixtures__/prelude-06GGS8J1YKZCPF4BRXZTX764F4.json'

const events: string[] = []
vi.mock('./PreludeSegment', () => ({
  default: function MockSegment(p: PreludeSegmentProps) {
    const poses = segmentPoses(p)
    // Named once, at mount: a run's first pos moves when an older page joins it.
    const [name] = useState(() => `${p.stintId ?? 'plain'}@${poses[0]}`)
    useEffect(() => {
      events.push(`mount ${name}`)
      return () => { events.push(`unmount ${name}`) }
    }, [name])
    return <div data-testid="prelude-run" data-stint={p.stintId ?? 'plain'} data-poses={poses.join(' ')} />
  },
}))

beforeEach(() => {
  events.length = 0
  vi.stubGlobal('IntersectionObserver', class { observe() {} disconnect() {} })
})
afterEach(() => vi.unstubAllGlobals())

const items = sanitizePreludePage(real)!.items
const from = (pos: string) => items.slice(items.findIndex((i) => i.pos === pos))
const runs = () => screen.queryAllByTestId('prelude-run').map((r) => [r.getAttribute('data-stint'), r.getAttribute('data-poses')])
const base = { hostId: 'h', keyPrefix: 'exc', onLoadOlder: () => {}, onRetry: () => {}, error: null, status: 'ok' as const, done: false }
// Worker A: the capture's own sdk segment and its two lines.
const A = new Map(['394248.0', '394248.1', '411382.1'].map((p) => [p, 'exc_A'] as const))
const poses = (a: string, b: string) => {
  const all = derivePrelude(items).entries.map((e) => e.pos)
  return all.slice(all.indexOf(a), all.indexOf(b) + 1).join(' ')
}

describe.each<['room' | 'chat']>([['room'], ['chat']])('PreludeSection segments in %s mode', (mode) => {
  it('one segment per run, split exactly where the attribution changes', () => {
    render(<PreludeSection {...base} mode={mode} pages={1} view={derivePrelude(items)} attribution={A} />)
    expect(runs()).toEqual([
      ['plain', poses('22485.0', '393190.1')],
      ['exc_A', '394248.0 394248.1 411382.1'],
      ['plain', poses('415815.0', '454104.1')],
    ])
    // Between the sentinel and the handoff, in drawing order.
    const kids = [...screen.getByTestId('worker-prelude').children].map((c) => c.getAttribute('data-testid'))
    expect(kids).toEqual(['prelude-sentinel', 'prelude-run', 'prelude-run', 'prelude-run', 'prelude-handoff'])
  })

  it('no attribution (none listed, or the list unavailable): one plain segment', () => {
    const { unmount } = render(<PreludeSection {...base} mode={mode} pages={1} view={derivePrelude(items)} />)
    expect(runs()).toEqual([['plain', poses('22485.0', '454104.1')]])
    unmount()
    render(<PreludeSection {...base} mode={mode} pages={1} view={derivePrelude(items)} attribution={new Map()} />)
    expect(runs()).toEqual([['plain', poses('22485.0', '454104.1')]])
  })

  it('an older page joining the front remounts no segment already drawn (keyed by its last pos)', () => {
    const { rerender } = render(<PreludeSection {...base} mode={mode} pages={1} view={derivePrelude(from('415815.0'))} />)
    expect(events).toEqual(['mount plain@415815.0'])
    events.length = 0
    // Unattributed: the one plain run only grows at the front.
    rerender(<PreludeSection {...base} mode={mode} pages={2} view={derivePrelude(from('394248.0'))} />)
    expect(events).toEqual([])
    expect(runs()).toEqual([['plain', poses('394248.0', '454104.1')]])
    // The oldest page arrives with the stint list: the A run and the plain run before it are new; the last run stays.
    rerender(<PreludeSection {...base} mode={mode} pages={3} view={derivePrelude(items)} attribution={A} />)
    expect(events).toEqual(['mount plain@22485.0', 'mount exc_A@394248.0'])
  })
})
